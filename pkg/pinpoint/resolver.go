// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Masterminds/semver/v3"
	"github.com/carabiner-dev/github"
)

// ErrNoReleases is returned by a resolver when a repository has no releases
// or tags to pin an action to.
var ErrNoReleases = errors.New("repository has no releases or tags")

// Release captures a released version of an action repository.
type Release struct {
	// Tag is the name of the release tag, for example v1.3.0.
	Tag string

	// Commit is the full hash of the commit the tag points to.
	Commit string
}

// Resolver looks up the versions of the repositories hosting actions. It is
// the piece that talks to the forge, implementations are expected to cache
// their responses as the same repository is commonly referenced many times.
type Resolver interface {
	// LatestRelease returns the newest release of an owner/name repository.
	// It returns ErrNoReleases when the repository has nothing to pin to.
	LatestRelease(ctx context.Context, repository string) (*Release, error)

	// ResolveVersion returns the commit a version of a repository points to.
	// The version is a tag or a branch, an empty version resolves to the tip
	// of the default branch. The returned release carries the most precise
	// tag naming that commit, falling back to the version as received.
	ResolveVersion(ctx context.Context, repository, version string) (*Release, error)
}

// SourceResolver maps a repository to the project it was forked from. It is
// what lets an attestation name the repository the code comes from instead
// of the fork it was scanned in.
type SourceResolver interface {
	// SourceRepository returns the owner/name of the repository a fork was
	// created from, or the repository itself when it is not a fork.
	SourceRepository(ctx context.Context, repository string) (string, error)
}

// apiCaller is the slice of the GitHub client the resolver needs. It keeps
// the resolver testable without hitting the API.
type apiCaller interface {
	Call(context.Context, string, string, io.Reader) (*http.Response, error)
}

// GitHubResolver resolves action versions using the GitHub API. Responses are
// cached, resolving the same version twice only calls the API once.
type GitHubResolver struct {
	client apiCaller

	mtx      sync.Mutex
	releases map[string]*Release
	tags     map[string][]repositoryTag
	sources  map[string]string
}

// repositoryTag is a tag as returned by the GitHub API.
type repositoryTag struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// NewGitHubResolver creates a resolver that talks to the GitHub API. The
// client reads its token from the GITHUB_TOKEN environment variable, calls
// are made anonymously when it is not set.
func NewGitHubResolver() (*GitHubResolver, error) {
	client, err := github.NewClient()
	if err != nil {
		return nil, fmt.Errorf("creating GitHub client: %w", err)
	}
	return NewGitHubResolverWithClient(client), nil
}

// NewGitHubResolverWithClient creates a resolver using a preconfigured API
// client.
func NewGitHubResolverWithClient(client apiCaller) *GitHubResolver {
	return &GitHubResolver{
		client:   client,
		releases: map[string]*Release{},
		tags:     map[string][]repositoryTag{},
		sources:  map[string]string{},
	}
}

// SourceRepository returns the repository a fork was created from, the root
// of the fork network when the fork was itself forked. Repositories that are
// not forks resolve to themselves, with the name the forge has for them now:
// a repository that was renamed resolves to its current owner/name.
func (r *GitHubResolver) SourceRepository(ctx context.Context, repository string) (string, error) {
	if repository == "" {
		return "", errors.New("no repository specified")
	}

	r.mtx.Lock()
	cached, ok := r.sources[repository]
	r.mtx.Unlock()
	if ok {
		return cached, nil
	}

	var repo struct {
		FullName string `json:"full_name"`
		Fork     bool   `json:"fork"`
		// Source is the root of the fork network, parent the repository
		// this one was forked from directly.
		Source *struct {
			FullName string `json:"full_name"`
		} `json:"source"`
		Parent *struct {
			FullName string `json:"full_name"`
		} `json:"parent"`
	}
	if err := r.get(ctx, "repos/"+repository, &repo); err != nil {
		return "", err
	}

	source := repo.FullName
	if source == "" {
		source = repository
	}
	if repo.Fork {
		switch {
		case repo.Source != nil && repo.Source.FullName != "":
			source = repo.Source.FullName
		case repo.Parent != nil && repo.Parent.FullName != "":
			source = repo.Parent.FullName
		}
	}

	r.mtx.Lock()
	r.sources[repository] = source
	r.mtx.Unlock()

	return source, nil
}

// LatestRelease returns the newest release of a repository. Repositories that
// don't publish releases are resolved to their highest semver tag.
func (r *GitHubResolver) LatestRelease(ctx context.Context, repository string) (*Release, error) {
	if repository == "" {
		return nil, errors.New("no repository specified")
	}

	if cached, ok := r.cached(repository, ""); ok {
		return cached, nil
	}

	tag, err := r.latestTag(ctx, repository)
	if err != nil {
		return nil, err
	}

	commit, err := r.commitFor(ctx, repository, tag)
	if err != nil {
		return nil, err
	}

	return r.cache(repository, "", &Release{Tag: tag, Commit: commit}), nil
}

// ResolveVersion returns the commit a tag or branch of a repository points
// to. The release is named after the most precise tag pointing at the same
// commit, so pinning an entry tracking a major version records the patch
// release it resolved to.
func (r *GitHubResolver) ResolveVersion(ctx context.Context, repository, version string) (*Release, error) {
	if repository == "" {
		return nil, errors.New("no repository specified")
	}

	if cached, ok := r.cached(repository, version); ok {
		return cached, nil
	}

	// An entry with no version tracks the default branch of the repository.
	ref := version
	if ref == "" {
		ref = "HEAD"
	}

	commit, err := r.commitFor(ctx, repository, ref)
	if err != nil {
		return nil, err
	}

	return r.cache(repository, version, &Release{
		Tag:    r.tagFor(ctx, repository, commit, version),
		Commit: commit,
	}), nil
}

// cached returns a previously resolved version of a repository.
func (r *GitHubResolver) cached(repository, version string) (*Release, bool) {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	release, ok := r.releases[repository+"@"+version]
	return release, ok
}

// cache stores a resolved version of a repository and returns it.
func (r *GitHubResolver) cache(repository, version string, release *Release) *Release {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	r.releases[repository+"@"+version] = release
	return release
}

// latestTag returns the tag of the latest published release of a repository,
// falling back to its highest semver tag when it publishes no releases.
func (r *GitHubResolver) latestTag(ctx context.Context, repository string) (string, error) {
	var release struct {
		TagName string `json:"tag_name"`
	}

	err := r.get(ctx, fmt.Sprintf("repos/%s/releases/latest", repository), &release)
	switch {
	case err == nil && release.TagName != "":
		return release.TagName, nil
	case err != nil && !errors.Is(err, errNotFound):
		return "", err
	}

	// No published releases, look at the repository tags.
	tags, err := r.repositoryTags(ctx, repository)
	if err != nil {
		return "", err
	}

	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		names = append(names, tag.Name)
	}

	tag := highestSemver(names)
	if tag == "" {
		return "", fmt.Errorf("resolving %s: %w", repository, ErrNoReleases)
	}
	return tag, nil
}

// tagFor returns the tag that best names a commit of a repository. When more
// than one tag points at it, the most specific one wins: an entry tracking
// v4 is recorded as the v4.2.1 it resolved to. The fallback is returned when
// no tag matches, which is the common case for branches.
func (r *GitHubResolver) tagFor(ctx context.Context, repository, commit, fallback string) string {
	tags, err := r.repositoryTags(ctx, repository)
	if err != nil {
		return fallback
	}

	best := ""
	for _, tag := range tags {
		if tag.Commit.SHA != commit {
			continue
		}
		if best == "" || moreSpecific(tag.Name, best) {
			best = tag.Name
		}
	}
	if best == "" {
		return fallback
	}
	return best
}

// repositoryTags returns the tags of a repository, caching the response as
// pinning a repository commonly needs them more than once.
func (r *GitHubResolver) repositoryTags(ctx context.Context, repository string) ([]repositoryTag, error) {
	r.mtx.Lock()
	cached, ok := r.tags[repository]
	r.mtx.Unlock()
	if ok {
		return cached, nil
	}

	var tags []repositoryTag
	if err := r.get(ctx, fmt.Sprintf("repos/%s/tags?per_page=100", repository), &tags); err != nil {
		return nil, err
	}

	r.mtx.Lock()
	r.tags[repository] = tags
	r.mtx.Unlock()

	return tags, nil
}

// moreSpecific compares two tags naming the same commit. A version with more
// components wins (v4.2.1 over v4), everything else is decided by semver.
func moreSpecific(candidate, current string) bool {
	candidateVersion, err := semver.NewVersion(strings.TrimSpace(candidate))
	if err != nil {
		return false
	}
	currentVersion, err := semver.NewVersion(strings.TrimSpace(current))
	if err != nil {
		return true
	}

	if parts, other := versionParts(candidate), versionParts(current); parts != other {
		return parts > other
	}
	return candidateVersion.GreaterThan(currentVersion)
}

// versionParts counts the dot separated components of a version string.
func versionParts(version string) int {
	return len(strings.Split(strings.SplitN(strings.TrimPrefix(version, "v"), "-", 2)[0], "."))
}

// commitFor resolves a tag or branch to the hash of the commit it points to.
func (r *GitHubResolver) commitFor(ctx context.Context, repository, ref string) (string, error) {
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := r.get(ctx, fmt.Sprintf("repos/%s/commits/%s", repository, ref), &commit); err != nil {
		return "", err
	}
	if commit.SHA == "" {
		return "", fmt.Errorf("no commit found for %s of %s", ref, repository)
	}
	return commit.SHA, nil
}

// errNotFound signals a 404 response from the API.
var errNotFound = errors.New("not found")

// get calls an API endpoint and parses the JSON response into v.
func (r *GitHubResolver) get(ctx context.Context, path string, v any) error {
	resp, err := r.client.Call(ctx, http.MethodGet, path, nil)
	if err != nil {
		return fmt.Errorf("calling the GitHub API: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // nothing to do if closing fails

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return fmt.Errorf("%s: %w", path, errNotFound)
	case resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf(
			"the GitHub API returned %s, set GITHUB_TOKEN to raise the rate limit", resp.Status,
		)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("the GitHub API returned %s reading %s", resp.Status, path)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading the API response: %w", err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parsing the API response: %w", err)
	}
	return nil
}

// highestSemver returns the highest semver tag of a list, ignoring the ones
// that are not versions. Prereleases are only considered when the repository
// has no stable versions, pinning an action to a release candidate when a
// released version exists would be a surprising thing to do.
func highestSemver(tags []string) string {
	type candidate struct {
		name    string
		version *semver.Version
	}

	var stable, prerelease []candidate
	for _, tag := range tags {
		version, err := semver.NewVersion(strings.TrimSpace(tag))
		if err != nil {
			continue
		}
		if version.Prerelease() == "" {
			stable = append(stable, candidate{name: tag, version: version})
			continue
		}
		prerelease = append(prerelease, candidate{name: tag, version: version})
	}

	candidates := stable
	if len(candidates) == 0 {
		candidates = prerelease
	}
	if len(candidates) == 0 {
		return ""
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].version.GreaterThan(candidates[j].version)
	})
	return candidates[0].name
}

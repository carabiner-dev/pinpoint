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
}

// apiCaller is the slice of the GitHub client the resolver needs. It keeps
// the resolver testable without hitting the API.
type apiCaller interface {
	Call(context.Context, string, string, io.Reader) (*http.Response, error)
}

// GitHubResolver resolves action versions using the GitHub API. Responses are
// cached, resolving the same repository twice only calls the API once.
type GitHubResolver struct {
	client apiCaller

	mtx   sync.Mutex
	cache map[string]*Release
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
		client: client,
		cache:  map[string]*Release{},
	}
}

// LatestRelease returns the newest release of a repository. Repositories that
// don't publish releases are resolved to their highest semver tag.
func (r *GitHubResolver) LatestRelease(ctx context.Context, repository string) (*Release, error) {
	if repository == "" {
		return nil, errors.New("no repository specified")
	}

	r.mtx.Lock()
	cached, ok := r.cache[repository]
	r.mtx.Unlock()
	if ok {
		return cached, nil
	}

	tag, err := r.latestTag(ctx, repository)
	if err != nil {
		return nil, err
	}

	commit, err := r.tagCommit(ctx, repository, tag)
	if err != nil {
		return nil, err
	}

	release := &Release{Tag: tag, Commit: commit}

	r.mtx.Lock()
	r.cache[repository] = release
	r.mtx.Unlock()

	return release, nil
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
	var tags []struct {
		Name string `json:"name"`
	}
	if err := r.get(ctx, fmt.Sprintf("repos/%s/tags?per_page=100", repository), &tags); err != nil {
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

// tagCommit resolves a tag to the hash of the commit it points to.
func (r *GitHubResolver) tagCommit(ctx context.Context, repository, tag string) (string, error) {
	var commit struct {
		SHA string `json:"sha"`
	}
	if err := r.get(ctx, fmt.Sprintf("repos/%s/commits/%s", repository, tag), &commit); err != nil {
		return "", err
	}
	if commit.SHA == "" {
		return "", fmt.Errorf("no commit found for %s of %s", tag, repository)
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

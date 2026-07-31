// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"context"
	"crypto/sha1" //nolint:gosec // matching the hashes git and unpack publish
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/google/uuid"
	"github.com/protobom/protobom/pkg/sbom"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// BuildSBOM scans the GitHub Actions workflows and action definitions found
// under path and models the external action references of the repository as
// a protobom document. The graph has the repository commit at the root, each
// distinct action version hanging from it as a development tool, and the
// workflow files defining the references as the containers of the actions:
//
//	commit --devTool--> action --contained_by--> workflow file
//
// The resolver names the version each reference is using: the tag its commit
// corresponds to for pinned entries, the release its version resolves to for
// the rest. Lookups are required, a reference that cannot be resolved fails
// the build so that an emitted SBOM always carries verified versions.
func BuildSBOM(ctx context.Context, resolver Resolver, path string) (*sbom.Document, error) {
	if resolver == nil {
		return nil, errors.New("building an SBOM requires a resolver")
	}

	repo, err := describeRepository(path)
	if err != nil {
		return nil, err
	}

	report, err := NewScanner(WithActions(true)).Scan(path)
	if err != nil {
		return nil, err
	}

	external := groupReferences(report.References)
	releases, err := resolveReleases(ctx, resolver, external)
	if err != nil {
		return nil, err
	}

	nl := sbom.NewNodeList()
	root := repo.node()
	nl.AddRootNode(root)

	files := map[string]*sbom.Node{}
	for _, group := range external {
		action := actionNode(group.ref, releases[group.ref.Uses])
		if err := nl.RelateNodeAtID(action, root.Id, sbom.Edge_devTool); err != nil {
			return nil, fmt.Errorf("adding action node: %w", err)
		}

		for _, workflow := range group.workflows {
			file, ok := files[workflow]
			if !ok {
				file, err = fileNode(path, repo.root, workflow)
				if err != nil {
					return nil, err
				}
				files[workflow] = file
			}
			if err := nl.RelateNodeAtID(file, action.Id, sbom.Edge_contained_by); err != nil {
				return nil, fmt.Errorf("adding workflow node: %w", err)
			}
		}
	}

	doc := sbom.NewDocument()
	doc.Metadata.Id = uuid.NewString()
	doc.Metadata.Name = repo.name
	doc.Metadata.Date = timestamppb.Now()
	doc.Metadata.Tools = append(doc.Metadata.Tools, &sbom.Tool{Name: "pinpoint"})
	doc.NodeList = nl
	return doc, nil
}

// group is a distinct action version and the workflow files referencing it.
type group struct {
	ref       *Reference
	workflows []string
}

// groupReferences collapses the external references into one group per
// distinct `uses:` value, keeping the order they were scanned in. Local
// references are dropped, they ride along the repository.
func groupReferences(refs []Reference) []*group {
	var groups []*group
	index := map[string]*group{}

	for i := range refs {
		ref := &refs[i]
		if ref.Kind == KindLocal {
			continue
		}

		g, ok := index[ref.Uses]
		if !ok {
			g = &group{ref: ref}
			index[ref.Uses] = g
			groups = append(groups, g)
		}
		if !slices.Contains(g.workflows, ref.Workflow) {
			g.workflows = append(g.workflows, ref.Workflow)
		}
	}
	return groups
}

// resolveReleases looks up the version every repository hosted reference is
// using, keyed by the `uses:` value. Pinned entries resolve their hash to the
// tag naming it, unpinned ones resolve their version to a release. Container
// images have no repository to ask about and are left out.
func resolveReleases(ctx context.Context, resolver Resolver, groups []*group) (map[string]Release, error) {
	releases := make(map[string]Release, len(groups))
	errs := make([]error, len(groups))

	var mtx sync.Mutex
	var wg sync.WaitGroup
	workers := make(chan struct{}, defaultConcurrency)

	for i, g := range groups {
		repository := g.ref.Repository()
		if repository == "" {
			continue
		}

		wg.Add(1)
		go func(i int, ref *Reference) {
			defer wg.Done()

			workers <- struct{}{}
			defer func() { <-workers }()

			release, err := resolver.ResolveVersion(ctx, ref.Repository(), ref.Version())
			if err != nil {
				errs[i] = fmt.Errorf("resolving %s: %w", ref.Uses, err)
				return
			}

			mtx.Lock()
			releases[ref.Uses] = *release
			mtx.Unlock()
		}(i, g.ref)
	}
	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return releases, nil
}

// actionNode models an action version as an SBOM package: identified by a
// purl in the convention GitHub uses in its SBOMs, versioned when the entry
// resolved to a tag, and carrying a VCS locator back to the repository
// hosting the action.
func actionNode(ref *Reference, release Release) *sbom.Node {
	path, version, _ := strings.Cut(ref.Uses, "@")

	node := &sbom.Node{
		Id:      uuid.NewString(),
		Type:    sbom.Node_PACKAGE,
		Name:    path,
		Version: actionVersion(ref, release),
	}

	if ref.Kind == KindContainer {
		node.Name, node.Version = containerNameVersion(ref.Uses)
		node.Identifiers = map[int32]string{
			int32(sbom.SoftwareIdentifierType_PURL): containerPurl(ref.Uses),
		}
		return node
	}

	node.Identifiers = map[int32]string{
		int32(sbom.SoftwareIdentifierType_PURL): actionPurl(ref),
	}

	if repository := ref.Repository(); repository != "" {
		node.UrlHome = "https://" + githubHost + "/" + repository
		node.ExternalReferences = []*sbom.ExternalReference{{
			Type: sbom.ExternalReference_VCS,
			Url:  vcsLocator(repository, path, version),
		}}
	}
	return node
}

// actionVersion returns the version an action node is at: the tag the entry
// resolved to. Pinned entries whose hash matches no tag get no version, the
// hash in the purl is all we can say about them.
func actionVersion(ref *Reference, release Release) string {
	if ref.IsPinned() && release.Tag == ref.Version() {
		return ""
	}
	return release.Tag
}

// actionPurl returns the package URL of an action reference in the shape
// GitHub uses in its SBOMs: the full action path with the raw reference (a
// hash when pinned, the tracked version otherwise) as the version.
func actionPurl(ref *Reference) string {
	path, version, ok := strings.Cut(ref.Uses, "@")
	purl := "pkg:githubactions/" + path
	if ok && version != "" {
		purl += "@" + version
	}
	return purl
}

// vcsLocator returns the VCS locator of an action at the reference it is
// used at, for example git+https://github.com/actions/checkout@v5. Actions
// defined in a subdirectory of their repository carry it as the fragment.
func vcsLocator(repository, path, version string) string {
	locator := "git+https://" + githubHost + "/" + repository
	if version != "" {
		locator += "@" + version
	}
	if subpath := strings.TrimPrefix(path, repository); subpath != "" {
		locator += "#" + strings.TrimPrefix(subpath, "/")
	}
	return locator
}

// containerNameVersion returns the image path and tag of a docker://
// reference, the pieces that name and version its node.
func containerNameVersion(uses string) (name, version string) {
	image, tag, _ := parseImage(uses)
	return image, tag
}

// containerPurl returns the package URL of an action run from a container
// image, following the pkg:oci conventions: the digest as the version when
// the image is pinned, with the repository and tag as qualifiers.
func containerPurl(uses string) string {
	image, tag, digest := parseImage(uses)

	name := image
	if at := strings.LastIndex(image, "/"); at >= 0 {
		name = image[at+1:]
	}

	purl := "pkg:oci/" + strings.ToLower(name)
	if digest != "" {
		purl += "@" + strings.ReplaceAll(digest, ":", "%3A")
	}

	// Qualifiers are sorted by key, as the purl spec asks.
	var qualifiers []string
	if image != name {
		qualifiers = append(qualifiers, "repository_url="+strings.ToLower(image))
	}
	if tag != "" {
		qualifiers = append(qualifiers, "tag="+tag)
	}
	if len(qualifiers) > 0 {
		purl += "?" + strings.Join(qualifiers, "&")
	}
	return purl
}

// parseImage splits a docker:// reference into the image path, its tag and
// its digest, any of which can be missing.
func parseImage(uses string) (image, tag, digest string) {
	image = strings.TrimPrefix(uses, "docker://")
	image, digest, _ = strings.Cut(image, "@")

	if at := strings.LastIndex(image, ":"); at > strings.LastIndex(image, "/") {
		image, tag = image[:at], image[at+1:]
	}
	return image, tag, digest
}

// fileNode models a workflow file as an SBOM file node: named by its path
// relative to the repository root and identified by its hashes.
func fileNode(scanPath, repoRoot, workflow string) (*sbom.Node, error) {
	location := filepath.Join(scanPath, workflow)

	name, err := repositoryPath(repoRoot, location)
	if err != nil {
		return nil, err
	}

	node := sbom.NewNode()
	node.Id = uuid.NewString()
	node.Type = sbom.Node_FILE
	node.Name = name
	node.FileName = name
	node.PrimaryPurpose = append(node.PrimaryPurpose, sbom.Purpose_SOURCE)

	data, err := os.ReadFile(location)
	if err != nil {
		return nil, fmt.Errorf("reading workflow: %w", err)
	}

	sum1 := sha1.Sum(data) //nolint:gosec // matching the hashes git publishes
	node.AddHash(sbom.HashAlgorithm_SHA1, hex.EncodeToString(sum1[:]))
	sum256 := sha256.Sum256(data)
	node.AddHash(sbom.HashAlgorithm_SHA256, hex.EncodeToString(sum256[:]))

	return node, nil
}

// repositoryPath returns the slash separated path of a file relative to the
// repository root.
func repositoryPath(repoRoot, location string) (string, error) {
	absolute, err := filepath.Abs(location)
	if err != nil {
		return "", fmt.Errorf("locating workflow: %w", err)
	}
	relative, err := filepath.Rel(repoRoot, absolute)
	if err != nil {
		return "", fmt.Errorf("locating workflow in the repository: %w", err)
	}
	return filepath.ToSlash(relative), nil
}

// repository describes the repository an SBOM is built for: what the root
// node of the graph is made of.
type repository struct {
	// name locates the repository as the origin remote has it, for example
	// github.com/carabiner-dev/pinpoint. Repositories without an origin are
	// named after their directory.
	name string

	// version is derived from the latest tag, in the same shape unpack
	// gives its root nodes: the tag itself at the tag, tag-N+hash past it,
	// empty when the repository has no tags.
	version string

	// commit is the hash of the commit at HEAD.
	commit string

	// root is the absolute path of the repository worktree.
	root string
}

// node models the repository as the root of the SBOM graph, in the same
// shape unpack gives its root nodes: a package carrying the commit as the
// hash of a VCS external reference.
func (r *repository) node() *sbom.Node {
	return &sbom.Node{
		Id:      uuid.NewString(),
		Type:    sbom.Node_PACKAGE,
		Name:    r.name,
		Version: r.version,
		ExternalReferences: []*sbom.ExternalReference{{
			Type: sbom.ExternalReference_VCS,
			Hashes: map[int32]string{
				int32(sbom.HashAlgorithm_SHA1): r.commit,
			},
		}},
	}
}

// describeRepository collects what the root node says about the repository
// holding the scanned workflows: an SBOM subject is the repository at its
// current commit, so not being in one is an error.
func describeRepository(path string) (*repository, error) {
	repo, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, fmt.Errorf("an SBOM describes the repository at a commit, opening it: %w", err)
	}

	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("reading repository HEAD: %w", err)
	}

	worktree, err := repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("finding the repository root: %w", err)
	}
	root, err := filepath.Abs(worktree.Filesystem.Root())
	if err != nil {
		return nil, fmt.Errorf("finding the repository root: %w", err)
	}

	name, err := originLocation(repo)
	if err != nil {
		name = filepath.Base(root)
	}

	return &repository{
		name:    name,
		version: repositoryVersion(repo, head.Hash()),
		commit:  head.Hash().String(),
		root:    root,
	}, nil
}

// repositoryVersion derives a version for the repository from its tags, the
// same way unpack does: the latest tag when HEAD sits on it, the tag with
// the distance and a commit preview past it (v1.2.3-17+abcd1234), nothing
// when the repository has no tags or the tags cannot be walked.
func repositoryVersion(repo *git.Repository, head plumbing.Hash) string {
	tag, tagCommit, err := latestRepositoryTag(repo)
	if err != nil || tag == "" {
		return ""
	}

	distance, err := commitsBetween(repo, head, tagCommit.Hash)
	if err != nil {
		return ""
	}
	if distance == 0 {
		return tag
	}

	separator := "-"
	if strings.Contains(tag, "-") {
		separator = "."
	}
	return fmt.Sprintf("%s%s%d+%s", tag, separator, distance, head.String()[:8])
}

// latestRepositoryTag returns the tag whose commit is the newest, by commit
// date, and the commit it points to. Repositories without tags return an
// empty tag.
func latestRepositoryTag(repo *git.Repository) (string, *object.Commit, error) {
	tags, err := repo.Tags()
	if err != nil {
		return "", nil, err
	}

	var latest *object.Commit
	var name string
	err = tags.ForEach(func(tag *plumbing.Reference) error {
		hash, err := repo.ResolveRevision(plumbing.Revision(tag.Name().String()))
		if err != nil {
			return err
		}
		commit, err := repo.CommitObject(*hash)
		if err != nil {
			return err
		}

		if latest == nil || commit.Committer.When.After(latest.Committer.When) {
			latest = commit
			name = tag.Name().Short()
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	return name, latest, nil
}

// errStopIteration ends a commit walk early once the target is found.
var errStopIteration = errors.New("stop iterating")

// commitsBetween counts the commits separating HEAD from a tag, walking the
// history backwards until the tagged commit is found.
func commitsBetween(repo *git.Repository, head, tagged plumbing.Hash) (int, error) {
	log, err := repo.Log(&git.LogOptions{From: head})
	if err != nil {
		return 0, err
	}

	count := 0
	found := false
	err = log.ForEach(func(commit *object.Commit) error {
		if commit.Hash == tagged {
			found = true
			return errStopIteration
		}
		count++
		return nil
	})
	if err != nil && !errors.Is(err, errStopIteration) {
		return 0, err
	}
	if !found {
		return 0, errors.New("tagged commit not reachable from HEAD")
	}
	return count, nil
}

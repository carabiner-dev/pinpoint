// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/protobom/protobom/pkg/sbom"
)

// testRepository builds a git repository holding a workflow file, tagged at
// its single commit and with a GitHub origin remote.
func testRepository(t *testing.T) (dir, commit string) {
	t.Helper()
	dir = t.TempDir()

	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("creating test repository: %v", err)
	}

	workflows := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(workflows, 0o750); err != nil {
		t.Fatalf("creating workflows directory: %v", err)
	}
	for _, name := range []string{"ci.yaml", "release.yaml"} {
		if err := os.WriteFile(filepath.Join(workflows, name), []byte("name: "+name+"\n"), 0o600); err != nil {
			t.Fatalf("writing workflow: %v", err)
		}
	}

	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatalf("opening worktree: %v", err)
	}
	if err := worktree.AddGlob("."); err != nil {
		t.Fatalf("staging files: %v", err)
	}

	head, err := worktree.Commit("initial", &git.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()},
	})
	if err != nil {
		t.Fatalf("committing: %v", err)
	}

	if _, err := repo.CreateTag("v1.0.0", head, nil); err != nil {
		t.Fatalf("tagging: %v", err)
	}
	if _, err := repo.CreateRemote(&config.RemoteConfig{
		Name: "origin", URLs: []string{"https://github.com/carabiner-dev/test.git"},
	}); err != nil {
		t.Fatalf("adding remote: %v", err)
	}

	return dir, head.String()
}

func TestBuildSBOM(t *testing.T) {
	t.Parallel()

	checkoutHash := "11bd71901bbe5b1630ceea73d27597364c9af683"
	untaggedHash := "d35c59abb061a4a6fb18e82ac0862c26744d6ab5"
	checkout := "actions/checkout@" + checkoutHash
	untagged := "actions/setup-go@" + untaggedHash

	dir, commit := testRepository(t)
	ci := filepath.Join(".github", "workflows", "ci.yaml")
	release := filepath.Join(".github", "workflows", "release.yaml")

	refs := []Reference{
		{Workflow: ci, Uses: checkout, Kind: KindAction, Line: 4},
		{Workflow: ci, Uses: "actions/cache@v4", Kind: KindAction, Line: 5},
		{Workflow: ci, Uses: "docker://ghcr.io/org/tool:1.2", Kind: KindContainer, Line: 6},
		{Workflow: ci, Uses: "./.github/actions/build", Kind: KindLocal, Line: 7},
		// The version of this pin names no tag: the resolver answers with
		// the hash it received.
		{Workflow: release, Uses: untagged, Kind: KindAction, Line: 4},
		// The same action version in a second file is one node contained
		// by both files.
		{Workflow: release, Uses: checkout, Kind: KindAction, Line: 5},
	}

	resolver := &stubResolver{versions: map[string]Release{
		"actions/checkout@" + checkoutHash: {Tag: "v5.0.0", Commit: checkoutHash},
		"actions/cache@v4":                 {Tag: "v4.4.0", Commit: "3d3c42e5aac5ba805825da76410c181273ba90b1"},
		"actions/setup-go@" + untaggedHash: {Tag: untaggedHash, Commit: untaggedHash},
	}}

	doc, err := BuildSBOM(t.Context(), resolver, dir, refs)
	if err != nil {
		t.Fatalf("BuildSBOM(): %v", err)
	}

	// The root names the repository at its commit the way unpack does: the
	// origin location, the tag it sits on and the commit as a VCS hash.
	roots := doc.NodeList.GetRootNodes()
	if len(roots) != 1 {
		t.Fatalf("expected one root node, got %d", len(roots))
	}
	root := roots[0]
	if root.Name != "github.com/carabiner-dev/test" {
		t.Errorf("root node name = %q", root.Name)
	}
	if root.Version != "v1.0.0" {
		t.Errorf("root node version = %q", root.Version)
	}
	if hash := root.ExternalReferences[0].Hashes[int32(sbom.HashAlgorithm_SHA1)]; hash != commit {
		t.Errorf("root VCS hash = %q, want %q", hash, commit)
	}

	// Four external actions hang from the root as development tools, the
	// local reference is dropped.
	tools := doc.NodeList.GetEdgeByType(root.Id, sbom.Edge_devTool)
	if tools == nil || len(tools.To) != 4 {
		t.Fatalf("expected 4 devTool links from the root, got %+v", tools)
	}

	for _, expected := range []struct {
		name    string
		version string
		purl    string
		locator string
	}{
		{
			name:    "actions/checkout",
			version: "v5.0.0",
			purl:    "pkg:githubactions/actions/checkout@" + checkoutHash,
			locator: "git+https://github.com/actions/checkout@" + checkoutHash,
		},
		{
			name:    "actions/cache",
			version: "v4.4.0",
			purl:    "pkg:githubactions/actions/cache@v4",
			locator: "git+https://github.com/actions/cache@v4",
		},
		{
			name: "actions/setup-go",
			purl: "pkg:githubactions/actions/setup-go@" + untaggedHash,
		},
		{
			name:    "ghcr.io/org/tool",
			version: "1.2",
			purl:    "pkg:oci/tool?repository_url=ghcr.io/org/tool&tag=1.2",
		},
	} {
		nodes := doc.NodeList.GetNodesByName(expected.name)
		if len(nodes) != 1 {
			t.Fatalf("expected one node named %q, got %d", expected.name, len(nodes))
		}
		node := nodes[0]

		if node.Version != expected.version {
			t.Errorf("%s version = %q, want %q", expected.name, node.Version, expected.version)
		}
		if purl := string(node.Purl()); purl != expected.purl {
			t.Errorf("%s purl = %q, want %q", expected.name, purl, expected.purl)
		}
		if expected.locator != "" {
			if url := node.ExternalReferences[0].Url; url != expected.locator {
				t.Errorf("%s VCS locator = %q, want %q", expected.name, url, expected.locator)
			}
		}
	}

	// The checkout node is contained by both workflow files.
	checkoutNode := doc.NodeList.GetNodesByName("actions/checkout")[0]
	contained := doc.NodeList.GetEdgeByType(checkoutNode.Id, sbom.Edge_contained_by)
	if contained == nil || len(contained.To) != 2 {
		t.Fatalf("expected checkout to be contained by 2 files, got %+v", contained)
	}

	// File nodes carry the repository relative path and the file hashes.
	files := doc.NodeList.GetNodesByName(".github/workflows/ci.yaml")
	if len(files) != 1 {
		t.Fatalf("expected one node for the ci workflow, got %d", len(files))
	}
	sum := sha256.Sum256([]byte("name: ci.yaml\n"))
	if hash := files[0].Hashes[int32(sbom.HashAlgorithm_SHA256)]; hash != hex.EncodeToString(sum[:]) {
		t.Errorf("ci workflow sha256 = %q, want %q", hash, hex.EncodeToString(sum[:]))
	}
	if files[0].Type != sbom.Node_FILE {
		t.Errorf("workflow node type = %v, want FILE", files[0].Type)
	}
}

// TestBuildSBOMResolutionFails checks that an SBOM is not emitted when a
// version cannot be verified.
func TestBuildSBOMResolutionFails(t *testing.T) {
	t.Parallel()
	dir, _ := testRepository(t)

	refs := []Reference{
		{Workflow: "ci.yaml", Uses: "actions/checkout@v5", Kind: KindAction, Line: 4},
	}

	if _, err := BuildSBOM(t.Context(), &stubResolver{}, dir, refs); err == nil {
		t.Fatal("expected an error from the unresolvable reference")
	}
}

// TestBuildSBOMNeedsRepository checks that scanning a directory that is not
// a git repository refuses to build.
func TestBuildSBOMNeedsRepository(t *testing.T) {
	t.Parallel()
	if _, err := BuildSBOM(t.Context(), &stubResolver{}, t.TempDir(), nil); err == nil {
		t.Fatal("expected an error outside a repository")
	}
}

func TestContainerPurl(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		uses     string
		expected string
	}{
		{"docker://alpine:3.22", "pkg:oci/alpine?tag=3.22"},
		{"docker://alpine", "pkg:oci/alpine"},
		{
			"docker://ghcr.io/org/tool:1.2",
			"pkg:oci/tool?repository_url=ghcr.io/org/tool&tag=1.2",
		},
		{
			"docker://alpine@sha256:beefdbd8a1da6d2915566fde36db9db0b524eb737fc57cd1367effd16dc0d06d",
			"pkg:oci/alpine@sha256%3Abeefdbd8a1da6d2915566fde36db9db0b524eb737fc57cd1367effd16dc0d06d",
		},
	} {
		if purl := containerPurl(tc.uses); purl != tc.expected {
			t.Errorf("containerPurl(%q) = %q, want %q", tc.uses, purl, tc.expected)
		}
	}
}

func TestVCSLocator(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		repository string
		path       string
		version    string
		expected   string
	}{
		{
			"actions/checkout", "actions/checkout", "v5",
			"git+https://github.com/actions/checkout@v5",
		},
		{
			"github/codeql-action", "github/codeql-action/upload-sarif", "v3",
			"git+https://github.com/github/codeql-action@v3#upload-sarif",
		},
		{
			"example/workflows", "example/workflows/.github/workflows/ci.yml", "main",
			"git+https://github.com/example/workflows@main#.github/workflows/ci.yml",
		},
	} {
		if locator := vcsLocator(tc.repository, tc.path, tc.version); locator != tc.expected {
			t.Errorf("vcsLocator(%q) = %q, want %q", tc.path, locator, tc.expected)
		}
	}
}

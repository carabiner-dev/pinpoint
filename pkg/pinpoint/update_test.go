// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// stubResolver answers from fixed maps, recording the lookups it was asked
// for. Versions are keyed as repository@version.
type stubResolver struct {
	releases map[string]Release
	versions map[string]Release

	mtx   sync.Mutex
	calls []string
}

// lookups returns the sorted list of lookups the resolver was asked for.
// References resolve in parallel, so the order they arrive in is not fixed.
func (s *stubResolver) lookups() []string {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	sorted := append([]string(nil), s.calls...)
	sort.Strings(sorted)
	return sorted
}

func (s *stubResolver) record(lookup string) {
	s.mtx.Lock()
	s.calls = append(s.calls, lookup)
	s.mtx.Unlock()
}

func (s *stubResolver) LatestRelease(_ context.Context, repository string) (*Release, error) {
	s.record(repository)
	release, ok := s.releases[repository]
	if !ok {
		return nil, ErrNoReleases
	}
	return &release, nil
}

func (s *stubResolver) ResolveVersion(_ context.Context, repository, version string) (*Release, error) {
	s.record(repository + "@" + version)
	release, ok := s.versions[repository+"@"+version]
	if !ok {
		return nil, ErrNoReleases
	}
	return &release, nil
}

func TestRewriteUses(t *testing.T) {
	t.Parallel()
	release := Release{Tag: "v5.0.0", Commit: "08c6903cd8c0fde910a37f88322edcfb5dd907a8"}

	for _, tc := range []struct {
		name     string
		line     string
		uses     string
		tag      string
		expected string
		mustErr  bool
	}{
		{
			name:     "step entry",
			line:     "        uses: actions/checkout@v4",
			uses:     "actions/checkout@v4",
			expected: "        uses: actions/checkout@08c6903cd8c0fde910a37f88322edcfb5dd907a8 # v5.0.0",
		},
		{
			name:     "sequence entry",
			line:     "      - uses: actions/checkout@v4",
			uses:     "actions/checkout@v4",
			expected: "      - uses: actions/checkout@08c6903cd8c0fde910a37f88322edcfb5dd907a8 # v5.0.0",
		},
		{
			name:     "existing comment is replaced",
			line:     "        uses: actions/checkout@v4 # v4.0.0",
			uses:     "actions/checkout@v4",
			expected: "        uses: actions/checkout@08c6903cd8c0fde910a37f88322edcfb5dd907a8 # v5.0.0",
		},
		{
			name:     "quotes are preserved",
			line:     `        uses: "actions/checkout@v4"`,
			uses:     "actions/checkout@v4",
			expected: `        uses: "actions/checkout@08c6903cd8c0fde910a37f88322edcfb5dd907a8" # v5.0.0`,
		},
		{
			name:     "unversioned reference",
			line:     "        uses: actions/checkout",
			uses:     "actions/checkout",
			expected: "        uses: actions/checkout@08c6903cd8c0fde910a37f88322edcfb5dd907a8 # v5.0.0",
		},
		{
			name:     "reusable workflow",
			line:     "    uses: example/workflows/.github/workflows/release.yml@main",
			uses:     "example/workflows/.github/workflows/release.yml@main",
			expected: "    uses: example/workflows/.github/workflows/release.yml@08c6903cd8c0fde910a37f88322edcfb5dd907a8 # v5.0.0",
		},
		{
			name:     "version we cannot name gets no comment",
			line:     "        uses: actions/checkout@main",
			uses:     "actions/checkout@main",
			tag:      "-",
			expected: "        uses: actions/checkout@08c6903cd8c0fde910a37f88322edcfb5dd907a8",
		},
		{
			name:    "line is not a uses entry",
			line:    "        run: go test ./...",
			uses:    "actions/checkout@v4",
			mustErr: true,
		},
		{
			name:    "entry changed since the scan",
			line:    "        uses: actions/setup-go@v6",
			uses:    "actions/checkout@v4",
			mustErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			update := Update{
				Reference: Reference{Uses: tc.uses, Kind: KindAction},
				Release:   release,
			}
			// A "-" tag stands for a version we could not name.
			if tc.tag == "-" {
				update.Release.Tag = ""
			}

			got, err := rewriteUses(tc.line, &update)
			if tc.mustErr {
				if err == nil {
					t.Fatalf("expected error rewriting %q, got %q", tc.line, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("rewriting %q: %v", tc.line, err)
			}
			if got != tc.expected {
				t.Errorf("rewriteUses():\n got: %q\nwant: %q", got, tc.expected)
			}
		})
	}
}

// planResolver returns a resolver answering for the references used by the
// planning tests.
func planResolver() *stubResolver {
	return &stubResolver{
		releases: map[string]Release{
			"actions/checkout": {Tag: "v5.0.0", Commit: "08c6903cd8c0fde910a37f88322edcfb5dd907a8"},
			"actions/setup-go": {Tag: "v6.0.0", Commit: "11bd71901bbe5b1630ceea73d27597364c9af683"},
		},
		versions: map[string]Release{
			// The v4 entry resolves to the patch release naming its commit.
			"actions/checkout@v4": {Tag: "v4.2.2", Commit: "ea165f8d65b6e75b540449e92b4886f43607fa02"},
		},
	}
}

func TestPlanUpgrade(t *testing.T) {
	t.Parallel()
	resolver := planResolver()
	updater := &Updater{Resolver: resolver, Upgrade: true}

	refs := []Reference{
		{Uses: "actions/checkout@v4", Kind: KindAction},
		// Already at the latest release commit, nothing to do.
		{Uses: "actions/setup-go@11bd71901bbe5b1630ceea73d27597364c9af683", Kind: KindAction},
		{Uses: "./.github/actions/local", Kind: KindLocal},
		{Uses: "docker://alpine:3.22", Kind: KindContainer},
		{Uses: "unknown/action@v1", Kind: KindAction},
	}

	plan, err := updater.Plan(t.Context(), refs)
	if err != nil {
		t.Fatalf("planning updates: %v", err)
	}

	if len(plan.Updates) != 1 {
		t.Fatalf("expected 1 update, got %+v", plan.Updates)
	}
	if got := plan.Updates[0].String(); got != "actions/checkout@08c6903cd8c0fde910a37f88322edcfb5dd907a8 # v5.0.0" {
		t.Errorf("unexpected update value: %q", got)
	}

	expectedSkips := []SkipReason{SkipUpToDate, SkipLocal, SkipContainer, SkipUnresolved}
	if len(plan.Skipped) != len(expectedSkips) {
		t.Fatalf("expected %d skips, got %+v", len(expectedSkips), plan.Skipped)
	}
	for i, reason := range expectedSkips {
		if plan.Skipped[i].Reason != reason {
			t.Errorf("skip %d = %q, want %q", i, plan.Skipped[i].Reason, reason)
		}
	}
	if !errors.Is(plan.Skipped[3].Err, ErrNoReleases) {
		t.Errorf("unresolved skip lost its error: %v", plan.Skipped[3].Err)
	}

	// Local and container references must not reach the resolver, and an
	// upgrade only asks for the latest release of each repository.
	expectedCalls := []string{"actions/checkout", "actions/setup-go", "unknown/action"}
	if got := resolver.lookups(); !reflect.DeepEqual(got, expectedCalls) {
		t.Errorf("resolver called with %+v, want %+v", got, expectedCalls)
	}
}

func TestPlanPinsCurrentVersion(t *testing.T) {
	t.Parallel()
	resolver := planResolver()
	updater := &Updater{Resolver: resolver}

	refs := []Reference{
		{Uses: "actions/checkout@v4", Kind: KindAction},
		// Pinned entries are left alone unless we are upgrading.
		{Uses: "actions/setup-go@1234567890123456789012345678901234567890", Kind: KindAction},
	}

	plan, err := updater.Plan(t.Context(), refs)
	if err != nil {
		t.Fatalf("planning updates: %v", err)
	}

	if len(plan.Updates) != 1 {
		t.Fatalf("expected 1 update, got %+v", plan.Updates)
	}
	// The reference keeps the version it was using, pinned to its commit.
	if got := plan.Updates[0].String(); got != "actions/checkout@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.2.2" {
		t.Errorf("unexpected update value: %q", got)
	}

	if len(plan.Skipped) != 1 || plan.Skipped[0].Reason != SkipPinned {
		t.Errorf("unexpected skips: %+v", plan.Skipped)
	}

	expectedCalls := []string{"actions/checkout@v4"}
	if got := resolver.lookups(); !reflect.DeepEqual(got, expectedCalls) {
		t.Errorf("resolver called with %+v, want %+v", got, expectedCalls)
	}
}

func TestApply(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workflow := filepath.Join(".github", "workflows", "ci.yaml")
	if err := os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o750); err != nil {
		t.Fatalf("creating workflow directory: %v", err)
	}

	source := strings.Join([]string{
		"jobs:",
		"  build:",
		"    steps:",
		"      - uses: actions/checkout@v4",
		"      - run: go test ./...",
		"      - uses: actions/setup-go@v5 # v5",
		"",
	}, "\n")
	path := filepath.Join(root, workflow)
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatalf("writing workflow: %v", err)
	}

	updater := &Updater{Resolver: &stubResolver{}}
	modified, err := updater.Apply(root, []Update{
		{
			Reference: Reference{Workflow: workflow, Uses: "actions/checkout@v4", Line: 4, Kind: KindAction},
			Release:   Release{Tag: "v5.0.0", Commit: "08c6903cd8c0fde910a37f88322edcfb5dd907a8"},
		},
		{
			Reference: Reference{Workflow: workflow, Uses: "actions/setup-go@v5", Line: 6, Kind: KindAction},
			Release:   Release{Tag: "v6.0.0", Commit: "11bd71901bbe5b1630ceea73d27597364c9af683"},
		},
	})
	if err != nil {
		t.Fatalf("applying updates: %v", err)
	}
	if len(modified) != 1 || modified[0] != workflow {
		t.Fatalf("modified files = %+v, want [%s]", modified, workflow)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading workflow: %v", err)
	}

	expected := strings.Join([]string{
		"jobs:",
		"  build:",
		"    steps:",
		"      - uses: actions/checkout@08c6903cd8c0fde910a37f88322edcfb5dd907a8 # v5.0.0",
		"      - run: go test ./...",
		"      - uses: actions/setup-go@11bd71901bbe5b1630ceea73d27597364c9af683 # v6.0.0",
		"",
	}, "\n")
	if string(data) != expected {
		t.Errorf("updated workflow:\n got:\n%s\nwant:\n%s", string(data), expected)
	}

	// A second pass over the same lines must not corrupt the file: the
	// entries no longer match what the updates expect.
	if _, err := updater.Apply(root, []Update{
		{
			Reference: Reference{Workflow: workflow, Uses: "actions/checkout@v4", Line: 4, Kind: KindAction},
			Release:   Release{Tag: "v5.0.0", Commit: "08c6903cd8c0fde910a37f88322edcfb5dd907a8"},
		},
	}); err == nil {
		t.Error("expected an error re-applying a stale update")
	}
}

func TestApplyStaleLine(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workflow := "action.yml"
	if err := os.WriteFile(filepath.Join(root, workflow), []byte("runs:\n"), 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatalf("writing action: %v", err)
	}

	updater := &Updater{Resolver: &stubResolver{}}
	if _, err := updater.Apply(root, []Update{
		{
			Reference: Reference{Workflow: workflow, Uses: "actions/checkout@v4", Line: 99, Kind: KindAction},
			Release:   Release{Tag: "v5.0.0", Commit: "08c6903cd8c0fde910a37f88322edcfb5dd907a8"},
		},
	}); err == nil {
		t.Error("expected an error updating a line beyond the end of the file")
	}
}

// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubResolver answers from a fixed map, recording the repositories it was
// asked about.
type stubResolver struct {
	releases map[string]Release
	calls    []string
}

func (s *stubResolver) LatestRelease(_ context.Context, repository string) (*Release, error) {
	s.calls = append(s.calls, repository)
	release, ok := s.releases[repository]
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

func TestPlan(t *testing.T) {
	t.Parallel()
	resolver := &stubResolver{
		releases: map[string]Release{
			"actions/checkout": {Tag: "v5.0.0", Commit: "08c6903cd8c0fde910a37f88322edcfb5dd907a8"},
			"actions/setup-go": {Tag: "v6.0.0", Commit: "11bd71901bbe5b1630ceea73d27597364c9af683"},
		},
	}
	updater := &Updater{Resolver: resolver}

	refs := []Reference{
		{Uses: "actions/checkout@v4", Kind: KindAction},
		// Already at the release commit, nothing to do.
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

	// Local and container references must not reach the resolver.
	expectedCalls := []string{"actions/checkout", "actions/setup-go", "unknown/action"}
	if strings.Join(resolver.calls, ",") != strings.Join(expectedCalls, ",") {
		t.Errorf("resolver called with %+v, want %+v", resolver.calls, expectedCalls)
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

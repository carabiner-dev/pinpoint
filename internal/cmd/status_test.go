// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/carabiner-dev/pinpoint/pkg/pinpoint"
)

func TestExternal(t *testing.T) {
	t.Parallel()

	refs := []pinpoint.Reference{
		{Uses: "actions/checkout@v5", Kind: pinpoint.KindAction},
		{Uses: "./.github/actions/build", Kind: pinpoint.KindLocal},
		{Uses: "docker://alpine:3.22", Kind: pinpoint.KindContainer},
		{Uses: "org/repo/.github/workflows/ci.yml@v1", Kind: pinpoint.KindReusableWorkflow},
	}

	got := external(refs)
	if len(got) != 3 {
		t.Fatalf("external() kept %d references, want 3: %+v", len(got), got)
	}
	for _, ref := range got {
		if ref.Kind == pinpoint.KindLocal {
			t.Errorf("external() kept the local reference %q", ref.Uses)
		}
	}
}

func TestWriteStatusTable(t *testing.T) {
	t.Parallel()

	checkout := "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683"
	setupGo := "actions/setup-go@d35c59abb061a4a6fb18e82ac0862c26744d6ab5"

	refs := []pinpoint.Reference{
		{Workflow: "ci.yaml", Uses: checkout, Kind: pinpoint.KindAction, Line: 4},
		{Workflow: "ci.yaml", Uses: setupGo, Comment: "v5.5.0", Kind: pinpoint.KindAction, Line: 5},
		{Workflow: "ci.yaml", Uses: "actions/cache@v4", Kind: pinpoint.KindAction, Line: 6},
		{Workflow: "ci.yaml", Uses: "docker://alpine:3.22", Kind: pinpoint.KindContainer, Line: 7},
	}

	updates := pinpoint.NewUpdates(&pinpoint.Plan{
		Updates: []pinpoint.Update{
			{
				Reference: pinpoint.Reference{Uses: setupGo},
				Release:   pinpoint.Release{Tag: "v6.0.0", Commit: "b7ad1dad31e06c5925ef5d2fc7ad053ef454303e"},
			},
			{
				Reference: pinpoint.Reference{Uses: "actions/cache@v4"},
				Release:   pinpoint.Release{Tag: "v4.4.0", Commit: "3d3c42e5aac5ba805825da76410c181273ba90b1"},
			},
		},
		Skipped: []pinpoint.Skip{
			{
				Reference: pinpoint.Reference{Uses: checkout},
				Reason:    pinpoint.SkipUpToDate,
				Release:   &pinpoint.Release{Tag: "v5.0.0", Commit: "11bd71901bbe5b1630ceea73d27597364c9af683"},
			},
		},
	}, nil)

	var out bytes.Buffer
	if err := writeStatusTable(&out, refs, updates); err != nil {
		t.Fatalf("writeStatusTable(): %v", err)
	}

	for _, expected := range []struct {
		action  string
		version string
		ticks   int
		cross   int
		marks   int
	}{
		// Pinned to the commit of the latest release: two ticks. With no
		// comment naming the version, the hash is previewed instead.
		{action: "actions/checkout@", version: "11bd719…", ticks: 2},
		// Pinned, but a newer release exists. The version is the one the
		// comment trailing the hash names.
		{action: "actions/setup-go@", version: "v5.5.0", ticks: 1, cross: 1},
		// Not pinned and not on the latest release.
		{action: "actions/cache@", version: "v4", cross: 2},
		// Containers are not resolved: no version and no update status.
		{action: "docker://alpine", cross: 1, marks: 2},
	} {
		row := findRow(t, out.String(), expected.action)
		if expected.version != "" && !strings.Contains(row, expected.version) {
			t.Errorf("row %q does not show version %q", row, expected.version)
		}
		if got := strings.Count(row, "✓"); got != expected.ticks {
			t.Errorf("row %q has %d tick(s), want %d", row, got, expected.ticks)
		}
		if got := strings.Count(row, "✗"); got != expected.cross {
			t.Errorf("row %q has %d cross(es), want %d", row, got, expected.cross)
		}
		if got := strings.Count(row, unknownVersion); got != expected.marks {
			t.Errorf("row %q has %d unknown mark(s), want %d", row, got, expected.marks)
		}
	}
}

// TestWriteStatusTableOffline checks that the update column is dropped when
// the versions were not looked up.
func TestWriteStatusTableOffline(t *testing.T) {
	t.Parallel()

	refs := []pinpoint.Reference{
		{Workflow: "ci.yaml", Uses: "actions/checkout@v5", Kind: pinpoint.KindAction, Line: 4},
	}

	var out bytes.Buffer
	if err := writeStatusTable(&out, refs, nil); err != nil {
		t.Fatalf("writeStatusTable(): %v", err)
	}

	if !strings.Contains(out.String(), "Pinned") {
		t.Errorf("output has no Pinned column:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Version") {
		t.Errorf("output has no Version column:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Up to date") {
		t.Errorf("output has an update column with no version data:\n%s", out.String())
	}
	if row := findRow(t, out.String(), "actions/checkout@"); !strings.Contains(row, "✗") {
		t.Errorf("row %q does not mark the reference as unpinned", row)
	}
}

// findRow returns the line of a rendered table naming an action.
func findRow(t *testing.T, table, action string) string {
	t.Helper()
	for line := range strings.Lines(table) {
		if strings.Contains(line, action) {
			return line
		}
	}
	t.Fatalf("no row names %q in:\n%s", action, table)
	return ""
}

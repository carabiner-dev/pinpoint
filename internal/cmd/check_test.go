// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"reflect"
	"testing"

	"github.com/carabiner-dev/pinpoint/pkg/pinpoint"
)

func TestBumps(t *testing.T) {
	t.Parallel()

	checkout := "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683"
	setupGo := "actions/setup-go@d35c59abb061a4a6fb18e82ac0862c26744d6ab5"
	oldCheckout := "actions/checkout@08c6903cd8c0fde910a37f88322edcfb5dd907a8"

	updates := pinpoint.NewUpdates(&pinpoint.Plan{
		Updates: []pinpoint.Update{
			{
				Reference: pinpoint.Reference{Uses: checkout},
				Release:   pinpoint.Release{Tag: "v7.0.1", Commit: "3d3c42e5aac5ba805825da76410c181273ba90b1"},
			},
			{
				Reference: pinpoint.Reference{Uses: setupGo},
				Release:   pinpoint.Release{Tag: "v7.0.0", Commit: "b7ad1dad31e06c5925ef5d2fc7ad053ef454303e"},
			},
			{
				Reference: pinpoint.Reference{Uses: oldCheckout},
				Release:   pinpoint.Release{Tag: "v7.0.1", Commit: "3d3c42e5aac5ba805825da76410c181273ba90b1"},
			},
		},
	})

	ci := ".github/workflows/ci.yaml"
	release := ".github/workflows/release.yaml"
	refs := []pinpoint.Reference{
		// The same version of the same action in one file is a single bump,
		// no matter how many jobs use it. The lines are reported together.
		{Workflow: ci, Job: "build", Uses: checkout, Comment: "v4.2.2", Kind: pinpoint.KindAction, Line: 12},
		{Workflow: ci, Job: "build", Uses: setupGo, Comment: "v5.5.0", Kind: pinpoint.KindAction, Line: 5},
		{Workflow: ci, Job: "test", Uses: checkout, Comment: "v4.2.2", Kind: pinpoint.KindAction, Line: 4},
		// A different file is a different bump...
		{Workflow: release, Job: "release", Uses: checkout, Comment: "v4.2.2", Kind: pinpoint.KindAction, Line: 4},
		// ...and so is the same action pinned to another version.
		{Workflow: release, Job: "release", Uses: oldCheckout, Comment: "v5.0.0", Kind: pinpoint.KindAction, Line: 5},
	}

	expected := []bump{
		{Workflow: ci, Action: "actions/checkout", Using: "v4.2.2", Latest: "v7.0.1", Lines: []int{4, 12}},
		{Workflow: ci, Action: "actions/setup-go", Using: "v5.5.0", Latest: "v7.0.0", Lines: []int{5}},
		{Workflow: release, Action: "actions/checkout", Using: "v4.2.2", Latest: "v7.0.1", Lines: []int{4}},
		{Workflow: release, Action: "actions/checkout", Using: "v5.0.0", Latest: "v7.0.1", Lines: []int{5}},
	}

	got := bumps(refs, updates)
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("bumps():\n got: %+v\nwant: %+v", got, expected)
	}

	if list := got[0].lineList(); list != "4, 12" {
		t.Errorf("lineList() = %q, want %q", list, "4, 12")
	}
}

func TestPinnedVersion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		ref      pinpoint.Reference
		expected string
	}{
		{
			name:     "the comment names the version",
			ref:      pinpoint.Reference{Uses: "actions/checkout@11bd719", Comment: "v4.2.2"},
			expected: "v4.2.2",
		},
		{
			name:     "without a comment the hash is abbreviated",
			ref:      pinpoint.Reference{Uses: "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683"},
			expected: "11bd719",
		},
		{
			name:     "nothing to show",
			ref:      pinpoint.Reference{Uses: "actions/checkout"},
			expected: unknownVersion,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := pinnedVersion(&tc.ref); got != tc.expected {
				t.Errorf("pinnedVersion() = %q, want %q", got, tc.expected)
			}
		})
	}
}

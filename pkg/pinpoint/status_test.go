// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"testing"
)

// statusRepository builds a repository whose workflow uses one pinned, one
// unpinned, one container and one local action reference.
func statusRepository(t *testing.T, checkoutHash string) string {
	t.Helper()
	dir, _ := testRepository(t, map[string]string{
		".github/workflows/ci.yaml": "name: ci\non: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n" +
			"      - uses: actions/checkout@" + checkoutHash + " # v5.0.0\n" +
			"      - uses: actions/cache@v4\n" +
			"      - uses: docker://alpine:3.22\n" +
			"      - uses: ./.github/actions/build\n",
	})
	return dir
}

func TestScanStatus(t *testing.T) {
	t.Parallel()

	checkoutHash := "11bd71901bbe5b1630ceea73d27597364c9af683"
	dir := statusRepository(t, checkoutHash)

	resolver := &stubResolver{
		releases: map[string]Release{
			"actions/checkout": {Tag: "v7.0.1", Commit: "3d3c42e5aac5ba805825da76410c181273ba90b1"},
			"actions/cache":    {Tag: "v4.4.0", Commit: "b7ad1dad31e06c5925ef5d2fc7ad053ef454303e"},
		},
		versions: map[string]Release{
			"actions/cache@v4": {Tag: "v4.4.0", Commit: "b7ad1dad31e06c5925ef5d2fc7ad053ef454303e"},
		},
	}

	status, err := ScanStatus(t.Context(), resolver, dir)
	if err != nil {
		t.Fatalf("ScanStatus(): %v", err)
	}

	if !status.Checked {
		t.Error("status does not report the versions as checked")
	}
	if len(status.Unresolved) != 0 {
		t.Errorf("unexpected unresolved references: %+v", status.Unresolved)
	}
	// The local reference is dropped, the rest keep their scan order.
	if len(status.References) != 3 {
		t.Fatalf("expected 3 references, got %+v", status.References)
	}

	for i, expected := range []ReferenceStatus{
		// Pinned with a comment naming the version, and behind the latest.
		{Version: "v5.0.0", Pinned: true, Checked: true, Outdated: true, Latest: Release{Tag: "v7.0.1", Commit: "3d3c42e5aac5ba805825da76410c181273ba90b1"}},
		// Unpinned, tracking a version older than the latest release.
		{Version: "v4", Pinned: false, Checked: true, Outdated: true, Latest: Release{Tag: "v4.4.0", Commit: "b7ad1dad31e06c5925ef5d2fc7ad053ef454303e"}},
		// Containers are not resolved: no version data at all.
		{Version: "", Pinned: false, Checked: false},
	} {
		got := status.References[i]
		expected.Reference = got.Reference
		if got != expected {
			t.Errorf("reference %d:\n got: %+v\nwant: %+v", i, got, expected)
		}
	}

	if uses := status.References[0].Reference.Uses; uses != "actions/checkout@"+checkoutHash {
		t.Errorf("first reference is %q", uses)
	}
}

// TestScanStatusOffline checks that scanning without a resolver still knows
// which references are pinned.
func TestScanStatusOffline(t *testing.T) {
	t.Parallel()

	dir := statusRepository(t, "11bd71901bbe5b1630ceea73d27597364c9af683")

	status, err := ScanStatus(t.Context(), nil, dir)
	if err != nil {
		t.Fatalf("ScanStatus(): %v", err)
	}

	if status.Checked {
		t.Error("offline status claims the versions were checked")
	}
	if len(status.References) != 3 {
		t.Fatalf("expected 3 references, got %+v", status.References)
	}
	for _, entry := range status.References {
		if entry.Checked {
			t.Errorf("offline reference %q claims to be checked", entry.Reference.Uses)
		}
	}
	if !status.References[0].Pinned {
		t.Error("the pinned reference is not reported as pinned")
	}
	if status.References[1].Pinned {
		t.Error("the unpinned reference is reported as pinned")
	}
}

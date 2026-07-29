// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"strings"
	"testing"
)

func TestCheckUpdates(t *testing.T) {
	t.Parallel()
	refs := []Reference{
		{Uses: "actions/checkout@v4", Kind: KindAction},
		{Uses: "actions/setup-go@1234567890123456789012345678901234567890", Kind: KindAction},
	}

	newResolver := func() *stubResolver {
		return &stubResolver{
			releases: map[string]Release{
				"actions/checkout": {Tag: "v7.0.1", Commit: "3d3c42e5"},
				"actions/setup-go": {Tag: "v7.0.0", Commit: "b7ad1dad"},
			},
			versions: map[string]Release{
				"actions/checkout@v4": {Tag: "v4.4.0", Commit: "11d5960a"},
			},
		}
	}

	t.Run("with latest releases", func(t *testing.T) {
		t.Parallel()
		resolver := newResolver()
		updates, err := CheckUpdates(t.Context(), resolver, refs)
		if err != nil {
			t.Fatalf("checking updates: %v", err)
		}
		if !updates.LatestChecked() {
			t.Error("updates do not report the latest releases as checked")
		}

		status, _ := updates.Status(&refs[0])
		if status.Pin.Tag != "v4.4.0" || status.Latest.Tag != "v7.0.1" {
			t.Errorf("unexpected status: %+v", status)
		}
		if !updates.Outdated(&refs[1]) {
			t.Error("the pinned reference should be reported as outdated")
		}
	})

	t.Run("without latest releases", func(t *testing.T) {
		t.Parallel()
		resolver := newResolver()
		updates, err := CheckUpdates(t.Context(), resolver, refs, WithLatestReleases(false))
		if err != nil {
			t.Fatalf("checking updates: %v", err)
		}
		if updates.LatestChecked() {
			t.Error("updates report releases as checked when they were not")
		}

		// The pin target is still resolved, nothing else is.
		status, ok := updates.Status(&refs[0])
		if !ok || status.Pin.Tag != "v4.4.0" {
			t.Errorf("unexpected status: %+v", status)
		}
		if status.Latest.Tag != "" || status.Outdated {
			t.Errorf("status carries release data: %+v", status)
		}
		if updates.Outdated(&refs[1]) {
			t.Error("nothing can be outdated when releases were not looked up")
		}

		// Only the version in use was asked about.
		for _, call := range resolver.calls {
			if !strings.Contains(call, "@") {
				t.Errorf("resolver was asked for the latest release of %q", call)
			}
		}
	})
}

func TestUpdates(t *testing.T) {
	t.Parallel()

	unpinned := Reference{Uses: "actions/checkout@v4", Kind: KindAction}
	pinned := Reference{Uses: "actions/setup-go@d35c59abb061a4a6fb18e82ac0862c26744d6ab5", Kind: KindAction}
	container := Reference{Uses: "docker://alpine:3.22", Kind: KindContainer}

	updates := NewUpdates(
		// What an updater set to upgrade resolved.
		&Plan{
			Updates: []Update{
				{Reference: unpinned, Release: Release{Tag: "v7.0.1", Commit: "3d3c42e5"}},
				{Reference: pinned, Release: Release{Tag: "v7.0.0", Commit: "b7ad1dad"}},
			},
			Skipped: []Skip{{Reference: container, Reason: SkipContainer}},
		},
		// What an updater that is not upgrading resolved: the pinned entry
		// never reached the forge, it is already a hash.
		&Plan{
			Updates: []Update{
				{Reference: unpinned, Release: Release{Tag: "v4.4.0", Commit: "11d5960a"}},
			},
			Skipped: []Skip{
				{Reference: pinned, Reason: SkipPinned},
				{Reference: container, Reason: SkipContainer},
			},
		},
	)

	// An unpinned entry knows both where pinning would take it and how far
	// behind that leaves it.
	status, ok := updates.Status(&unpinned)
	if !ok {
		t.Fatal("no status for the unpinned reference")
	}
	if status.Pin.Tag != "v4.4.0" || status.Pin.Commit != "11d5960a" {
		t.Errorf("pin target = %+v, want v4.4.0/11d5960a", status.Pin)
	}
	if status.Latest.Tag != "v7.0.1" || !status.Outdated {
		t.Errorf("latest = %+v outdated = %v, want v7.0.1 and true", status.Latest, status.Outdated)
	}

	// A pinned entry has a latest release but nothing to pin it to.
	status, ok = updates.Status(&pinned)
	if !ok {
		t.Fatal("no status for the pinned reference")
	}
	if status.Pin.Tag != "" {
		t.Errorf("pin target = %+v, want none", status.Pin)
	}
	if status.Latest.Tag != "v7.0.0" {
		t.Errorf("latest = %+v, want v7.0.0", status.Latest)
	}

	// References that cannot be resolved are unknown, not zero valued.
	if _, ok := updates.Status(&container); ok {
		t.Error("the container reference should have no status")
	}
	if updates.Outdated(&container) {
		t.Error("an unresolved reference cannot be reported as outdated")
	}

	// A nil pair of plans is usable and empty.
	if _, ok := NewUpdates(nil, nil).Status(&unpinned); ok {
		t.Error("empty updates returned a status")
	}
	if _, ok := (*Updates)(nil).Status(&unpinned); ok {
		t.Error("nil updates returned a status")
	}
}

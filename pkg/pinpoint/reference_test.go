// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import "testing"

func TestReferenceParts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		ref        Reference
		owner      string
		repository string
		version    string
	}{
		{
			name:       "action",
			ref:        Reference{Uses: "actions/setup-go@v6", Kind: KindAction},
			owner:      "actions",
			repository: "actions/setup-go",
			version:    "v6",
		},
		{
			name:       "action in subdirectory",
			ref:        Reference{Uses: "carabiner-dev/actions/unpack/sbom@main", Kind: KindAction},
			owner:      "carabiner-dev",
			repository: "carabiner-dev/actions",
			version:    "main",
		},
		{
			name:       "reusable workflow",
			ref:        Reference{Uses: "example/workflows/.github/workflows/release.yml@main", Kind: KindReusableWorkflow},
			owner:      "example",
			repository: "example/workflows",
			version:    "main",
		},
		{
			name:       "unversioned action",
			ref:        Reference{Uses: "actions/checkout", Kind: KindAction},
			owner:      "actions",
			repository: "actions/checkout",
			version:    "",
		},
		{
			name:       "container",
			ref:        Reference{Uses: "docker://alpine@sha256:beef", Kind: KindContainer},
			owner:      "",
			repository: "",
			version:    "sha256:beef",
		},
		{
			name:       "local",
			ref:        Reference{Uses: "./.github/actions/local-action", Kind: KindLocal},
			owner:      "",
			repository: "",
			version:    "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.ref.Owner(); got != tc.owner {
				t.Errorf("Owner() = %q, want %q", got, tc.owner)
			}
			if got := tc.ref.Repository(); got != tc.repository {
				t.Errorf("Repository() = %q, want %q", got, tc.repository)
			}
			if got := tc.ref.Version(); got != tc.version {
				t.Errorf("Version() = %q, want %q", got, tc.version)
			}
		})
	}
}

func TestIsPinned(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		uses   string
		pinned bool
	}{
		{"actions/checkout@08c6903cd8c0fde910a37f88322edcfb5dd907a8", true},
		{"actions/checkout@08C6903CD8C0FDE910A37F88322EDCFB5DD907A8", true},
		{"actions/checkout@da39a3ee5e6b4b0d3255bfef95601890afd80709da39a3ee5e6b4b0d3255bfef", true},
		{"actions/checkout@v5", false},
		{"actions/checkout@main", false},
		{"actions/checkout@08c6903", false},
		{"actions/checkout", false},
		{"actions/checkout@", false},
		{"./.github/actions/local-action", true},
		{"docker://alpine@sha256:beefdbd8a1da6d2915566fde36db9db0b524eb737fc57cd1367effd16dc0d06d", true},
		{"docker://alpine:3.22", false},
	} {
		t.Run(tc.uses, func(t *testing.T) {
			t.Parallel()
			ref := Reference{Uses: tc.uses}
			if got := ref.IsPinned(); got != tc.pinned {
				t.Errorf("IsPinned(%q) = %v, want %v", tc.uses, got, tc.pinned)
			}
		})
	}
}

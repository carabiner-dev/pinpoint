// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import "testing"

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

// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestScan(t *testing.T) {
	t.Parallel()
	wf := filepath.Join(".github", "workflows", "ci.yaml")

	report, err := NewScanner().Scan(filepath.Join("testdata", "repo"))
	if err != nil {
		t.Fatalf("scanning test repo: %v", err)
	}

	expected := []Reference{
		{Workflow: wf, Job: "build", Step: "Checkout code", Uses: "actions/checkout@08c6903cd8c0fde910a37f88322edcfb5dd907a8", Line: 12},
		{Workflow: wf, Job: "build", Step: "Setup go", Uses: "actions/setup-go@v6", Line: 14},
		{Workflow: wf, Job: "build", Uses: "./.github/actions/local-action", Line: 17},
		{Workflow: wf, Job: "build", Step: "pinned-docker", Uses: "docker://alpine@sha256:beefdbd8a1da6d2915566fde36db9db0b524eb737fc57cd1367effd16dc0d06d", Line: 19},
		{Workflow: wf, Job: "build", Step: "Unpinned docker", Uses: "docker://alpine:3.22", Line: 21},
		{Workflow: wf, Job: "release", Uses: "example/workflows/.github/workflows/release.yml@main", Line: 24},
	}
	if !reflect.DeepEqual(report.References, expected) {
		t.Errorf("unexpected references:\n got: %+v\nwant: %+v", report.References, expected)
	}

	unpinned := report.Unpinned()
	expectedUnpinned := []string{
		"actions/setup-go@v6",
		"docker://alpine:3.22",
		"example/workflows/.github/workflows/release.yml@main",
	}
	if len(unpinned) != len(expectedUnpinned) {
		t.Fatalf("expected %d unpinned references, got %+v", len(expectedUnpinned), unpinned)
	}
	for i, ref := range unpinned {
		if ref.Uses != expectedUnpinned[i] {
			t.Errorf("unpinned[%d] = %q, want %q", i, ref.Uses, expectedUnpinned[i])
		}
	}
}

func TestScanNoWorkflows(t *testing.T) {
	t.Parallel()
	report, err := NewScanner().Scan(t.TempDir())
	if err != nil {
		t.Fatalf("scanning empty directory: %v", err)
	}
	if len(report.References) != 0 {
		t.Errorf("expected empty report, got %+v", report.References)
	}
}

// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The Actions files of the test repository, in the order the scanner walks
// them.
var (
	localAction = filepath.Join(".github", "actions", "local-action", "action.yml")
	starter     = filepath.Join(".github", "workflow-templates", "starter.yaml")
	ciWorkflow  = filepath.Join(".github", "workflows", "ci.yaml")
	rootAction  = "action.yaml"
)

func TestScan(t *testing.T) {
	t.Parallel()

	report, err := NewScanner().Scan(filepath.Join("testdata", "repo"))
	if err != nil {
		t.Fatalf("scanning test repo: %v", err)
	}

	expected := []Reference{
		{Workflow: localAction, Step: "Setup node", Uses: "actions/setup-node@v4", Kind: KindAction, Line: 7},
		{Workflow: starter, Job: "lint", Step: "Lint", Uses: "golangci/golangci-lint-action@v6", Kind: KindAction, Line: 9},
		{Workflow: ciWorkflow, Job: "build", Step: "Checkout code", Uses: "actions/checkout@08c6903cd8c0fde910a37f88322edcfb5dd907a8", Comment: "v5.0.0", Kind: KindAction, Line: 12},
		{Workflow: ciWorkflow, Job: "build", Step: "Setup go", Uses: "actions/setup-go@v6", Kind: KindAction, Line: 14},
		{Workflow: ciWorkflow, Job: "build", Uses: "./.github/actions/local-action", Kind: KindLocal, Line: 17},
		{Workflow: ciWorkflow, Job: "build", Step: "pinned-docker", Uses: "docker://alpine@sha256:beefdbd8a1da6d2915566fde36db9db0b524eb737fc57cd1367effd16dc0d06d", Kind: KindContainer, Line: 19},
		{Workflow: ciWorkflow, Job: "build", Step: "Unpinned docker", Uses: "docker://alpine:3.22", Kind: KindContainer, Line: 21},
		{Workflow: ciWorkflow, Job: "release", Uses: "example/workflows/.github/workflows/release.yml@main", Kind: KindReusableWorkflow, Line: 24},
		{Workflow: rootAction, Step: "Checkout the caller", Uses: "actions/checkout@v5", Kind: KindAction, Line: 7},
	}
	if !reflect.DeepEqual(report.References, expected) {
		t.Errorf("unexpected references:\n got: %+v\nwant: %+v", report.References, expected)
	}

	unpinned := report.Unpinned()
	expectedUnpinned := []string{
		"actions/setup-node@v4",
		"golangci/golangci-lint-action@v6",
		"actions/setup-go@v6",
		"docker://alpine:3.22",
		"example/workflows/.github/workflows/release.yml@main",
		"actions/checkout@v5",
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

// TestScanDiscovery checks which files the scan picks up: the workflows a
// forge loads, those recognized by what they hold, the action definitions
// anywhere in the tree, and none of the YAML a repository keeps for other
// purposes.
func TestScanDiscovery(t *testing.T) {
	t.Parallel()

	report, err := NewScanner().Scan(filepath.Join("testdata", "repo"))
	if err != nil {
		t.Fatalf("scanning test repo: %v", err)
	}

	// starter.yaml lives outside .github/workflows and is only recognized
	// by declaring jobs to run on an event.
	expectedWorkflows := []string{starter, ciWorkflow}
	if !reflect.DeepEqual(report.Workflows, expectedWorkflows) {
		t.Errorf("scanned workflows = %+v, want %+v", report.Workflows, expectedWorkflows)
	}

	// The composite action a repository publishes from its root is as much
	// an action definition as one nested under .github/actions.
	expectedActions := []string{localAction, rootAction}
	if !reflect.DeepEqual(report.Actions, expectedActions) {
		t.Errorf("scanned actions = %+v, want %+v", report.Actions, expectedActions)
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

// TestScanBrokenWorkflow checks that a file a forge would load has to parse,
// while the YAML a repository keeps elsewhere is free not to.
func TestScanBrokenWorkflow(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	broken := []byte("name: [unclosed\n")

	if err := os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o750); err != nil {
		t.Fatalf("creating workflows directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "template.yaml"), broken, 0o600); err != nil {
		t.Fatalf("writing template: %v", err)
	}

	if _, err := NewScanner().Scan(root); err != nil {
		t.Fatalf("scanning a repository with an unparseable template: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, ".github", "workflows", "ci.yaml"), broken, 0o600); err != nil {
		t.Fatalf("writing workflow: %v", err)
	}
	if _, err := NewScanner().Scan(root); err == nil {
		t.Error("expected an error scanning an unparseable workflow")
	}
}

func TestInWorkflowsDir(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		file     string
		expected bool
	}{
		{filepath.Join(".github", "workflows", "ci.yaml"), true},
		{filepath.Join(".github", "workflows", "shared", "ci.yaml"), true},
		{filepath.Join(".forgejo", "workflows", "ci.yaml"), true},
		{filepath.Join(".gitea", "workflows", "ci.yaml"), true},
		{filepath.Join(".github", "workflow-templates", "ci.yaml"), false},
		{filepath.Join("docs", "workflows", "ci.yaml"), false},
		{"ci.yaml", false},
	} {
		if got := inWorkflowsDir(tc.file); got != tc.expected {
			t.Errorf("inWorkflowsDir(%q) = %v, want %v", tc.file, got, tc.expected)
		}
	}
}

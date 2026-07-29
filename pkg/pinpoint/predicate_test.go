// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	gointoto "github.com/in-toto/attestation/go/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestPredicate(t *testing.T) {
	t.Parallel()
	report, err := NewScanner().Scan(filepath.Join("testdata", "repo"))
	if err != nil {
		t.Fatalf("scanning test repo: %v", err)
	}

	predicate := report.Predicate(nil)

	expectedSummary := PredicateSummary{Workflows: 1, References: 6, Pinned: 3, Unpinned: 3}
	if predicate.Summary != expectedSummary {
		t.Errorf("summary = %+v, want %+v", predicate.Summary, expectedSummary)
	}

	expectedWorkflows := []string{filepath.Join(".github", "workflows", "ci.yaml")}
	if !reflect.DeepEqual(predicate.Workflows, expectedWorkflows) {
		t.Errorf("workflows = %+v, want %+v", predicate.Workflows, expectedWorkflows)
	}

	if len(predicate.References) != len(report.References) {
		t.Fatalf("got %d references, want %d", len(predicate.References), len(report.References))
	}

	expectedFirst := PredicateReference{
		Workflow:   filepath.Join(".github", "workflows", "ci.yaml"),
		Line:       12,
		Job:        "build",
		Step:       "Checkout code",
		Uses:       "actions/checkout@08c6903cd8c0fde910a37f88322edcfb5dd907a8",
		Kind:       KindAction,
		Owner:      "actions",
		Repository: "actions/checkout",
		Version:    "08c6903cd8c0fde910a37f88322edcfb5dd907a8",
		Pinned:     true,
	}
	if predicate.References[0] != expectedFirst {
		t.Errorf("first reference = %+v, want %+v", predicate.References[0], expectedFirst)
	}
}

// TestPredicateTool checks that the tool section is a resource descriptor
// that in-toto tooling can read back, version included.
func TestPredicateTool(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal((&Report{}).Predicate(nil))
	if err != nil {
		t.Fatalf("marshaling predicate: %v", err)
	}

	var decoded struct {
		Tool json.RawMessage `json:"tool"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshaling predicate: %v", err)
	}

	descriptor := &gointoto.ResourceDescriptor{}
	if err := protojson.Unmarshal(decoded.Tool, descriptor); err != nil {
		t.Fatalf("the tool section is not a resource descriptor: %v", err)
	}
	if err := descriptor.Validate(); err != nil {
		t.Errorf("tool descriptor does not validate: %v", err)
	}

	if descriptor.GetName() != toolName {
		t.Errorf("tool name = %q, want %q", descriptor.GetName(), toolName)
	}
	if descriptor.GetUri() != toolURI {
		t.Errorf("tool uri = %q, want %q", descriptor.GetUri(), toolURI)
	}

	version := descriptor.GetAnnotations().GetFields()["version"].GetStringValue()
	if version != toolVersion() {
		t.Errorf("tool version = %q, want %q", version, toolVersion())
	}
}

func TestPredicateUpdates(t *testing.T) {
	t.Parallel()
	report := &Report{
		Workflows: []string{".github/workflows/ci.yaml"},
		References: []Reference{
			{Uses: "actions/checkout@v4", Kind: KindAction},
			{Uses: "actions/setup-go@11bd71901bbe5b1630ceea73d27597364c9af683", Kind: KindAction},
			{Uses: "docker://alpine:3.22", Kind: KindContainer},
		},
	}

	// Without a lookup the predicate says so and reports nothing outdated.
	predicate := report.Predicate(nil)
	if predicate.UpdatesChecked {
		t.Error("predicate claims the versions were checked")
	}
	if predicate.Summary.Outdated != 0 {
		t.Errorf("outdated = %d, want 0", predicate.Summary.Outdated)
	}

	updates := NewUpdates(&Plan{
		Updates: []Update{
			{
				Reference: Reference{Uses: "actions/checkout@v4", Kind: KindAction},
				Release:   Release{Tag: "v5.0.0", Commit: "08c6903cd8c0fde910a37f88322edcfb5dd907a8"},
			},
		},
		Skipped: []Skip{
			{
				Reference: Reference{Uses: "actions/setup-go@11bd71901bbe5b1630ceea73d27597364c9af683"},
				Reason:    SkipUpToDate,
				Release:   &Release{Tag: "v6.0.0", Commit: "11bd71901bbe5b1630ceea73d27597364c9af683"},
			},
			{Reference: Reference{Uses: "docker://alpine:3.22"}, Reason: SkipContainer},
		},
	}, nil)

	predicate = report.Predicate(updates)
	if !predicate.UpdatesChecked {
		t.Error("predicate does not report the versions as checked")
	}
	if predicate.Summary.Outdated != 1 {
		t.Errorf("outdated = %d, want 1", predicate.Summary.Outdated)
	}

	expected := []PredicateReference{
		{Uses: "actions/checkout@v4", LatestVersion: "v5.0.0", LatestCommit: "08c6903cd8c0fde910a37f88322edcfb5dd907a8", Outdated: true},
		{Uses: "actions/setup-go@11bd71901bbe5b1630ceea73d27597364c9af683", LatestVersion: "v6.0.0", LatestCommit: "11bd71901bbe5b1630ceea73d27597364c9af683", Outdated: false},
		// Container references have no version to compare against.
		{Uses: "docker://alpine:3.22", LatestVersion: "", LatestCommit: "", Outdated: false},
	}
	for i, want := range expected {
		got := predicate.References[i]
		if got.LatestVersion != want.LatestVersion || got.LatestCommit != want.LatestCommit || got.Outdated != want.Outdated {
			t.Errorf(
				"reference %d: latest=%q/%q outdated=%v, want latest=%q/%q outdated=%v",
				i, got.LatestVersion, got.LatestCommit, got.Outdated,
				want.LatestVersion, want.LatestCommit, want.Outdated,
			)
		}
	}
}

// TestPredicateFieldsAlwaysRendered guards the contract policies rely on:
// every field of a reference is written to the JSON, even when empty, so
// that policy code never needs to guard expressions with has().
func TestPredicateFieldsAlwaysRendered(t *testing.T) {
	t.Parallel()
	report := &Report{
		Workflows:  []string{".github/workflows/ci.yaml"},
		References: []Reference{{Workflow: ".github/workflows/ci.yaml", Job: "build", Uses: "./local", Kind: KindLocal, Line: 3}},
	}

	data, err := json.Marshal(report.Predicate(nil))
	if err != nil {
		t.Fatalf("marshaling predicate: %v", err)
	}

	var decoded struct {
		References []map[string]any `json:"references"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshaling predicate: %v", err)
	}
	if len(decoded.References) != 1 {
		t.Fatalf("got %d references, want 1", len(decoded.References))
	}

	for _, field := range []string{
		"workflow", "line", "job", "step", "uses", "kind", "owner",
		"repository", "version", "pinned", "latest_version", "latest_commit",
		"outdated",
	} {
		if _, ok := decoded.References[0][field]; !ok {
			t.Errorf("reference field %q missing from the predicate JSON", field)
		}
	}
}

// TestEmptyReportPredicate checks that a report with no findings still
// renders the lists as empty arrays instead of nulls.
func TestEmptyReportPredicate(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal((&Report{}).Predicate(nil))
	if err != nil {
		t.Fatalf("marshaling predicate: %v", err)
	}

	var decoded struct {
		Workflows  *[]string         `json:"workflows"`
		References *[]map[string]any `json:"references"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshaling predicate: %v", err)
	}
	if decoded.Workflows == nil || decoded.References == nil {
		t.Fatalf("empty predicate lists rendered as null: %s", string(data))
	}
}

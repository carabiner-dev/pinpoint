// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPredicate(t *testing.T) {
	t.Parallel()
	report, err := NewScanner().Scan(filepath.Join("testdata", "repo"))
	if err != nil {
		t.Fatalf("scanning test repo: %v", err)
	}

	predicate := report.Predicate()

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

// TestPredicateFieldsAlwaysRendered guards the contract policies rely on:
// every field of a reference is written to the JSON, even when empty, so
// that policy code never needs to guard expressions with has().
func TestPredicateFieldsAlwaysRendered(t *testing.T) {
	t.Parallel()
	report := &Report{
		Workflows:  []string{".github/workflows/ci.yaml"},
		References: []Reference{{Workflow: ".github/workflows/ci.yaml", Job: "build", Uses: "./local", Kind: KindLocal, Line: 3}},
	}

	data, err := json.Marshal(report.Predicate())
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
		"repository", "version", "pinned",
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
	data, err := json.Marshal((&Report{}).Predicate())
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

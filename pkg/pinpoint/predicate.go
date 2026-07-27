// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"runtime/debug"
	"time"
)

const (
	// PredicateType is the in-toto predicate type of the pinpoint scan
	// results.
	PredicateType = "https://carabiner.dev/pinpoint/v1"

	// toolURI identifies pinpoint as the producer of the predicate data.
	toolURI = "https://github.com/carabiner-dev/pinpoint"
)

// Predicate captures the result of scanning a repository for action
// references. It carries every reference found, pinned or not, so that
// policies can reason about more than just the pinning status.
type Predicate struct {
	// Tool identifies the scanner that produced the results.
	Tool PredicateTool `json:"tool"`

	// Date is the RFC3339 timestamp of the scan.
	Date string `json:"date"`

	// Workflows lists the workflow files that were scanned.
	Workflows []string `json:"workflows"`

	// Summary has the reference counts of the scan.
	Summary PredicateSummary `json:"summary"`

	// References are all the action references found in the workflows.
	References []PredicateReference `json:"references"`
}

// PredicateTool identifies the scanner that generated a predicate.
type PredicateTool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	URI     string `json:"uri"`
}

// PredicateSummary counts the findings of a scan. It lets policies check the
// overall result without iterating the reference list.
type PredicateSummary struct {
	Workflows  int `json:"workflows"`
	References int `json:"references"`
	Pinned     int `json:"pinned"`
	Unpinned   int `json:"unpinned"`
}

// PredicateReference is an action reference as captured in an attestation.
// Every field is always rendered, even when empty, so that policies can read
// them without having to guard each expression with has().
type PredicateReference struct {
	// Workflow is the path of the workflow file, relative to the repository.
	Workflow string `json:"workflow"`

	// Line is the line in the workflow file defining the reference.
	Line int `json:"line"`

	// Job is the ID of the job using the action.
	Job string `json:"job"`

	// Step is the name or ID of the step using the action. It is empty for
	// job level references such as reusable workflow calls.
	Step string `json:"step"`

	// Uses is the raw value of the `uses:` entry.
	Uses string `json:"uses"`

	// Kind is one of action, reusable_workflow, container or local.
	Kind Kind `json:"kind"`

	// Owner is the user or organization owning the action, empty for
	// container and local references.
	Owner string `json:"owner"`

	// Repository is the owner/name of the repository hosting the action,
	// empty for container and local references.
	Repository string `json:"repository"`

	// Version is the fragment after the @ sign in the reference: a hash, a
	// tag, a branch or an image digest.
	Version string `json:"version"`

	// Pinned is true when the reference points to an immutable version of
	// the action. Local references are always considered pinned as they
	// ride along the repository.
	Pinned bool `json:"pinned"`
}

// Predicate builds the attestation predicate describing the report.
func (r *Report) Predicate() *Predicate {
	p := &Predicate{
		Tool: PredicateTool{
			Name:    "pinpoint",
			Version: toolVersion(),
			URI:     toolURI,
		},
		Date:       time.Now().UTC().Format(time.RFC3339),
		Workflows:  []string{},
		References: []PredicateReference{},
		Summary: PredicateSummary{
			Workflows:  len(r.Workflows),
			References: len(r.References),
		},
	}

	p.Workflows = append(p.Workflows, r.Workflows...)

	for _, ref := range r.References {
		pinned := ref.IsPinned()
		if pinned {
			p.Summary.Pinned++
		} else {
			p.Summary.Unpinned++
		}

		p.References = append(p.References, PredicateReference{
			Workflow:   ref.Workflow,
			Line:       ref.Line,
			Job:        ref.Job,
			Step:       ref.Step,
			Uses:       ref.Uses,
			Kind:       ref.Kind,
			Owner:      ref.Owner(),
			Repository: ref.Repository(),
			Version:    ref.Version(),
			Pinned:     pinned,
		})
	}

	return p
}

// toolVersion returns the version of the pinpoint build generating the
// attestation, as recorded by the go tooling.
func toolVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "unknown"
	}
	return info.Main.Version
}

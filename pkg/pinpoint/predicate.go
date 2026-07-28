// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"fmt"
	"runtime/debug"
	"time"

	gointoto "github.com/in-toto/attestation/go/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
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
	// Tool identifies the scanner that produced the results. The version is
	// carried in the descriptor annotations, the in-toto resource has no
	// field for it.
	Tool ToolDescriptor `json:"tool"`

	// Date is the RFC3339 timestamp of the scan.
	Date string `json:"date"`

	// UpdatesChecked is true when pinpoint looked up the versions available
	// for the references. Policies asserting on outdated actions need to
	// check this flag, the counts are zero when no lookup was made.
	UpdatesChecked bool `json:"updates_checked"`

	// Workflows lists the workflow files that were scanned.
	Workflows []string `json:"workflows"`

	// Summary has the reference counts of the scan.
	Summary PredicateSummary `json:"summary"`

	// References are all the action references found in the workflows.
	References []PredicateReference `json:"references"`
}

// toolName is the name pinpoint identifies itself with in the predicate.
const toolName = "pinpoint"

// ToolDescriptor is the in-toto resource describing the scanner. It is the
// official ResourceDescriptor, wrapped only to render it through protojson:
// the JSON tags of the generated Go type spell some fields in snake case
// while the in-toto specification writes them in camel case.
type ToolDescriptor struct {
	*gointoto.ResourceDescriptor
}

// MarshalJSON renders the descriptor as in-toto defines it.
func (t ToolDescriptor) MarshalJSON() ([]byte, error) {
	data, err := protojson.Marshal(t.ResourceDescriptor)
	if err != nil {
		return nil, fmt.Errorf("marshaling tool descriptor: %w", err)
	}
	return data, nil
}

// UnmarshalJSON reads a descriptor rendered as in-toto defines it.
func (t *ToolDescriptor) UnmarshalJSON(data []byte) error {
	descriptor := &gointoto.ResourceDescriptor{}
	if err := protojson.Unmarshal(data, descriptor); err != nil {
		return fmt.Errorf("unmarshaling tool descriptor: %w", err)
	}
	t.ResourceDescriptor = descriptor
	return nil
}

// toolDescriptor describes the pinpoint build generating an attestation as
// an in-toto resource. The version goes in the annotations, the resource
// descriptor has no field of its own for it.
func toolDescriptor() ToolDescriptor {
	descriptor := &gointoto.ResourceDescriptor{
		Name: toolName,
		Uri:  toolURI,
	}

	annotations, err := structpb.NewStruct(map[string]any{"version": toolVersion()})
	if err != nil {
		// The only value is a string, this cannot fail, but a predicate
		// without the version is better than no predicate at all.
		return ToolDescriptor{descriptor}
	}
	descriptor.Annotations = annotations
	return ToolDescriptor{descriptor}
}

// PredicateSummary counts the findings of a scan. It lets policies check the
// overall result without iterating the reference list.
type PredicateSummary struct {
	Workflows  int `json:"workflows"`
	References int `json:"references"`
	Pinned     int `json:"pinned"`
	Unpinned   int `json:"unpinned"`

	// Outdated counts the references that are not using the latest release
	// of their action. It is zero when the versions were not looked up.
	Outdated int `json:"outdated"`
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

	// LatestVersion is the tag of the newest release of the action. It is
	// empty when the version was not looked up or could not be resolved.
	LatestVersion string `json:"latest_version"`

	// LatestCommit is the commit the latest release points at.
	LatestCommit string `json:"latest_commit"`

	// Outdated is true when the reference is known not to point at the
	// commit of the latest release.
	Outdated bool `json:"outdated"`
}

// Predicate builds the attestation predicate describing the report. The
// versions available for the references are folded in when they were looked
// up, a nil updates set marks the predicate as not carrying them.
func (r *Report) Predicate(updates *Updates) *Predicate {
	p := &Predicate{
		Tool:           toolDescriptor(),
		Date:           time.Now().UTC().Format(time.RFC3339),
		UpdatesChecked: updates != nil,
		Workflows:      []string{},
		References:     []PredicateReference{},
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

		predicateRef := PredicateReference{
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
		}

		if status, ok := updates.Status(&ref); ok {
			predicateRef.LatestVersion = status.Latest.Tag
			predicateRef.LatestCommit = status.Latest.Commit
			predicateRef.Outdated = status.Outdated
			if status.Outdated {
				p.Summary.Outdated++
			}
		}

		p.References = append(p.References, predicateRef)
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

// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"regexp"
	"strings"
)

// hashRegexp matches full length commit hashes (SHA-1 and SHA-256).
var hashRegexp = regexp.MustCompile(`(?i)^([0-9a-f]{40}|[0-9a-f]{64})$`)

// Kind classifies the thing a reference points to.
type Kind string

const (
	// KindAction is an action published in a repository.
	KindAction Kind = "action"

	// KindReusableWorkflow is a call to a reusable workflow.
	KindReusableWorkflow Kind = "reusable_workflow"

	// KindContainer is an action run from a container image (docker://).
	KindContainer Kind = "container"

	// KindLocal is an action or workflow stored in the repository itself.
	KindLocal Kind = "local"
)

// Reference captures a `uses:` action reference found in a workflow.
type Reference struct {
	// Workflow is the path of the workflow file, relative to the scan root.
	Workflow string

	// Job is the ID of the job where the reference was found.
	Job string

	// Step is the name (or ID) of the step using the action. It is empty
	// for job-level references, ie calls to reusable workflows.
	Step string

	// Uses is the raw value of the `uses:` entry.
	Uses string

	// Kind classifies what the reference points to.
	Kind Kind

	// Line is the line of the workflow file where the reference is defined.
	Line int
}

// classify returns the kind of a reference found in a step from its raw
// `uses:` value. Job level references are reusable workflow calls and are
// classified by the parser.
func classify(uses string) Kind {
	switch {
	case strings.HasPrefix(uses, "./"):
		return KindLocal
	case strings.HasPrefix(uses, "docker://"):
		return KindContainer
	default:
		return KindAction
	}
}

// Version returns the fragment identifying the version of the referenced
// action: whatever follows the @ sign in the `uses:` value. It returns an
// empty string when the reference is not versioned.
func (r *Reference) Version() string {
	_, version, ok := strings.Cut(r.Uses, "@")
	if !ok {
		return ""
	}
	return version
}

// Repository returns the owner/name of the repository hosting the referenced
// action or workflow. It returns an empty string for references that don't
// live in a repository (local and container actions).
func (r *Reference) Repository() string {
	switch r.Kind {
	case KindAction, KindReusableWorkflow:
	default:
		return ""
	}

	path, _, _ := strings.Cut(r.Uses, "@")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// Owner returns the user or organization owning the referenced action or
// workflow, an empty string when the reference has no repository.
func (r *Reference) Owner() string {
	owner, _, _ := strings.Cut(r.Repository(), "/")
	return owner
}

// IsPinned returns true when the reference points to an immutable version
// of the action: a full length commit hash or, for docker actions, an image
// digest. Local actions (./path) always return true as they ride along the
// repository and cannot be pinned.
func (r *Reference) IsPinned() bool {
	switch {
	case strings.HasPrefix(r.Uses, "./"):
		return true
	case strings.HasPrefix(r.Uses, "docker://"):
		return strings.Contains(r.Uses, "@sha256:")
	}

	_, version, ok := strings.Cut(r.Uses, "@")
	if !ok {
		return false
	}
	return hashRegexp.MatchString(version)
}

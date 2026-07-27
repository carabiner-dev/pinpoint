// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"regexp"
	"strings"
)

// hashRegexp matches full length commit hashes (SHA-1 and SHA-256).
var hashRegexp = regexp.MustCompile(`(?i)^([0-9a-f]{40}|[0-9a-f]{64})$`)

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

	// Line is the line of the workflow file where the reference is defined.
	Line int
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

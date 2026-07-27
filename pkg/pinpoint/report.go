// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

// Report holds the action references collected when scanning a repository.
type Report struct {
	// Workflows lists the paths of the workflow files that were scanned,
	// relative to the scan root, including those with no references.
	Workflows []string

	// Actions lists the paths of the action definitions that were scanned.
	// It is only populated by scanners created with WithActions.
	Actions []string

	// References are all the action references found in the scanned files.
	References []Reference
}

// Unpinned returns the action references that are not pinned to a hash.
func (r *Report) Unpinned() []Reference {
	var refs []Reference
	for _, ref := range r.References {
		if !ref.IsPinned() {
			refs = append(refs, ref)
		}
	}
	return refs
}

// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

// Report holds the action references collected when scanning a repository.
type Report struct {
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

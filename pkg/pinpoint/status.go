// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import "context"

// ReferenceStatus describes the pinning state of one external action
// reference: the version it is using and how it compares to the latest
// release of the action.
type ReferenceStatus struct {
	// Reference is the entry as found when scanning.
	Reference Reference

	// Version is the version the reference is using as it is best known:
	// the comment naming a pinned hash when there is one, the version
	// fragment of the entry otherwise.
	Version string

	// Pinned is whether the reference points at an immutable version: a
	// commit hash or, for container actions, an image digest.
	Pinned bool

	// Checked reports whether the versions of the action were looked up.
	// Latest and Outdated say nothing when it is false, as for container
	// images and references the forge did not answer for.
	Checked bool

	// Latest is the newest release of the action.
	Latest Release

	// Outdated is true when the reference does not point at the commit of
	// the latest release.
	Outdated bool
}

// Status is the pinning state of the external action references of a
// repository, one entry per reference in the order they were scanned.
type Status struct {
	// References are the external action references found.
	References []ReferenceStatus

	// Checked reports whether the versions of the actions were looked up
	// at all. When it is false no reference carries version data.
	Checked bool

	// Unresolved are the references the forge was asked about and gave no
	// answer for, each with the error that stopped the lookup.
	Unresolved []Skip
}

// ScanStatus scans the GitHub Actions workflows and action definitions found
// under path and describes the pinning state of every external action
// reference: those pointing at other repositories or at container images.
// Local references ride along the repository and are left out.
//
// The resolver looks up the versions of the actions to report the references
// that are outdated. Pass nil to skip the lookups: the statuses then only
// know whether the references are pinned.
func ScanStatus(ctx context.Context, resolver Resolver, path string) (*Status, error) {
	report, err := NewScanner().Scan(path)
	if err != nil {
		return nil, err
	}

	var updates *Updates
	if resolver != nil {
		updates, err = CheckUpdates(ctx, resolver, report.References)
		if err != nil {
			return nil, err
		}
	}

	status := &Status{
		Checked:    updates.LatestChecked(),
		Unresolved: updates.Unresolved(),
	}
	for _, ref := range report.References {
		if ref.Kind == KindLocal {
			continue
		}

		entry := ReferenceStatus{
			Reference: ref,
			Version:   ref.VersionInUse(),
			Pinned:    ref.IsPinned(),
		}
		if update, ok := updates.Status(&ref); ok {
			entry.Checked = true
			entry.Latest = update.Latest
			entry.Outdated = update.Outdated
		}
		status.References = append(status.References, entry)
	}
	return status, nil
}

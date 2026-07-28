// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import "context"

// UpdateStatus is what pinpoint knows about the versions available for an
// action reference.
type UpdateStatus struct {
	// Latest is the newest release of the action.
	Latest Release

	// Outdated is true when the reference does not point at the commit of
	// the latest release.
	Outdated bool
}

// Updates holds the versions available for the references of a report,
// indexed by the value of their `uses:` entry: two entries using the same
// action at the same version share a status.
type Updates struct {
	statuses map[string]UpdateStatus
}

// CheckUpdates looks up the latest release of every reference received and
// returns what is available for each one. References that cannot be resolved
// (local actions, container images, repositories with no releases) are left
// out of the result and reported as unknown.
func CheckUpdates(ctx context.Context, resolver Resolver, refs []Reference) (*Updates, error) {
	updater := &Updater{Resolver: resolver, Upgrade: true}

	plan, err := updater.Plan(ctx, refs)
	if err != nil {
		return nil, err
	}
	return NewUpdates(plan), nil
}

// NewUpdates indexes the versions resolved while planning a set of updates.
// Plans are computed by an updater set to upgrade, any other plan resolves
// the versions in use instead of the latest ones.
func NewUpdates(plan *Plan) *Updates {
	updates := &Updates{statuses: map[string]UpdateStatus{}}
	if plan == nil {
		return updates
	}

	for i := range plan.Updates {
		update := &plan.Updates[i]
		updates.statuses[update.Reference.Uses] = UpdateStatus{
			Latest:   update.Release,
			Outdated: true,
		}
	}

	for i := range plan.Skipped {
		skip := &plan.Skipped[i]
		if skip.Release == nil {
			continue
		}
		updates.statuses[skip.Reference.Uses] = UpdateStatus{
			Latest:   *skip.Release,
			Outdated: false,
		}
	}

	return updates
}

// Status returns what is known about the versions available for a reference.
// The second return value is false when pinpoint could not find out.
func (u *Updates) Status(ref *Reference) (UpdateStatus, bool) {
	if u == nil || ref == nil {
		return UpdateStatus{}, false
	}
	status, ok := u.statuses[ref.Uses]
	return status, ok
}

// Outdated returns true when a reference is known not to point at the latest
// release of its action.
func (u *Updates) Outdated(ref *Reference) bool {
	status, ok := u.Status(ref)
	return ok && status.Outdated
}

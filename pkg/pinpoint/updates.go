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

	// Pin is the version an unpinned reference would be pinned to without
	// an upgrade: the release naming the version the entry already uses.
	// It is empty for the references that are already pinned.
	Pin Release
}

// Updates holds the versions available for the references of a report,
// indexed by the value of their `uses:` entry: two entries using the same
// action at the same version share a status.
type Updates struct {
	statuses      map[string]UpdateStatus
	unresolved    []Skip
	latestChecked bool
}

// checkOptions configures a version lookup.
type checkOptions struct {
	latestReleases bool
}

// CheckOption configures how CheckUpdates looks up versions.
type CheckOption func(*checkOptions)

// WithLatestReleases controls whether the newest release of each action is
// looked up. Turning it off leaves only the versions the unpinned entries
// would be pinned to, at the cost of not knowing what is outdated.
func WithLatestReleases(check bool) CheckOption {
	return func(o *checkOptions) {
		o.latestReleases = check
	}
}

// CheckUpdates looks up the versions available for every reference received:
// the version each unpinned entry would be pinned to and, unless disabled
// with WithLatestReleases, the latest release of each action. References that
// cannot be resolved (local actions, container images, repositories with no
// releases) are left out of the result and reported as unknown.
func CheckUpdates(ctx context.Context, resolver Resolver, refs []Reference, opts ...CheckOption) (*Updates, error) {
	options := checkOptions{latestReleases: true}
	for _, opt := range opts {
		opt(&options)
	}

	var latest *Plan
	if options.latestReleases {
		plan, err := (&Updater{Resolver: resolver, Upgrade: true}).Plan(ctx, refs)
		if err != nil {
			return nil, err
		}
		latest = plan
	}

	// The same references as an updater that is not upgrading sees them.
	// Entries already pinned are skipped without reaching the forge, so
	// this only resolves the versions the unpinned ones are using.
	pin, err := (&Updater{Resolver: resolver}).Plan(ctx, refs)
	if err != nil {
		return nil, err
	}

	return NewUpdates(latest, pin), nil
}

// NewUpdates indexes the versions resolved by a pair of plans. The latest
// plan is the one of an updater set to upgrade, it carries the newest
// release of every action. The pin plan is the one of an updater that is not
// upgrading, it carries the version each unpinned entry would be pinned to.
// A nil plan simply contributes nothing.
func NewUpdates(latest, pin *Plan) *Updates {
	updates := &Updates{
		statuses:      map[string]UpdateStatus{},
		latestChecked: latest != nil,
	}

	if latest != nil {
		for i := range latest.Updates {
			update := &latest.Updates[i]
			updates.statuses[update.Reference.Uses] = UpdateStatus{
				Latest:   update.Release,
				Outdated: true,
			}
		}

		for i := range latest.Skipped {
			skip := &latest.Skipped[i]
			if skip.Reason == SkipUnresolved {
				updates.unresolved = append(updates.unresolved, *skip)
				continue
			}
			if skip.Release == nil {
				continue
			}
			updates.statuses[skip.Reference.Uses] = UpdateStatus{
				Latest:   *skip.Release,
				Outdated: false,
			}
		}
	}

	if pin != nil {
		for i := range pin.Updates {
			update := &pin.Updates[i]
			status := updates.statuses[update.Reference.Uses]
			status.Pin = update.Release
			updates.statuses[update.Reference.Uses] = status
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

// LatestChecked reports whether the newest release of the actions was looked
// up. It is false when only the pin targets were resolved, and nothing can
// be said about references being outdated.
func (u *Updates) LatestChecked() bool {
	return u != nil && u.latestChecked
}

// Unresolved returns the references pinpoint asked the forge about and got
// no answer for, each with the error that stopped it. An expired token or a
// rate limit shows up here.
func (u *Updates) Unresolved() []Skip {
	if u == nil {
		return nil
	}
	return u.unresolved
}

// Outdated returns true when a reference is known not to point at the latest
// release of its action.
func (u *Updates) Outdated(ref *Reference) bool {
	status, ok := u.Status(ref)
	return ok && status.Outdated
}

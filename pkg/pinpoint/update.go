// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// usesLineRegexp captures the pieces of a line defining a `uses:` entry: the
// key and its indentation, the value and any trailing comment.
var usesLineRegexp = regexp.MustCompile(`^(\s*(?:-\s+)?uses:\s*)(\S+)(\s*(?:#.*)?)$`)

// Update is a change to be applied to an action reference to point it at a
// released version of the action.
type Update struct {
	// Reference is the entry as found when scanning.
	Reference Reference

	// Release is the version the reference will be pinned to.
	Release Release
}

// String returns the value the `uses:` entry will be set to, comment
// included.
func (u *Update) String() string {
	path, _, _ := strings.Cut(u.Reference.Uses, "@")
	return path + "@" + u.Release.Commit + versionComment(u.Release.Tag)
}

// versionComment returns the comment recording the version a reference was
// pinned from. Versions we could not name get no comment at all.
func versionComment(tag string) string {
	if tag == "" {
		return ""
	}
	return " # " + tag
}

// SkipReason explains why a reference cannot be updated.
type SkipReason string

const (
	// SkipLocal is a reference to an action in the repository itself.
	SkipLocal SkipReason = "local action, it rides along the repository"

	// SkipContainer is an action run from a container image.
	SkipContainer SkipReason = "container action, not hosted in a repository"

	// SkipNoRepository is a reference we cannot map to a repository.
	SkipNoRepository SkipReason = "unable to determine the source repository"

	// SkipPinned is a reference already pinned to a commit hash.
	SkipPinned SkipReason = "already pinned to a commit hash"

	// SkipUpToDate is a reference already pinned to the latest release.
	SkipUpToDate SkipReason = "already pinned to the latest release"

	// SkipUnresolved is a reference whose version could not be looked up.
	SkipUnresolved SkipReason = "unable to resolve the latest release"
)

// Skip is a reference that was left untouched, and why.
type Skip struct {
	// Reference is the entry that was not updated.
	Reference Reference

	// Reason explains why the reference was skipped.
	Reason SkipReason

	// Release is the version the reference was resolved to, set for the
	// entries skipped because they already point at it.
	Release *Release

	// Err carries the underlying error for references skipped because the
	// resolver could not answer for them.
	Err error
}

// Plan is the set of changes needed to bring a group of references to their
// latest released version. Commands that only report on updates (instead of
// applying them) use the same plan.
type Plan struct {
	// Updates are the references that can be moved to a new version.
	Updates []Update

	// Skipped are the references that were left alone.
	Skipped []Skip
}

// defaultConcurrency is how many references are resolved at the same time.
// The forge is the slow part, but asking it too many things at once is a
// good way to get throttled.
const defaultConcurrency = 8

// Updater computes and applies the updates that pin action references to a
// commit hash.
type Updater struct {
	// Resolver looks up the versions of the actions.
	Resolver Resolver

	// Upgrade makes the updater move the references to the latest release
	// of each action. When false, references are pinned to the commit their
	// current version points at.
	Upgrade bool

	// Concurrency is how many references are resolved in parallel. Zero
	// selects a sensible default.
	Concurrency int
}

// UpdaterOption configures an updater.
type UpdaterOption func(*Updater)

// WithUpgrade makes the updater pin references to the latest release of the
// action instead of to the version they already track.
func WithUpgrade(upgrade bool) UpdaterOption {
	return func(u *Updater) {
		u.Upgrade = upgrade
	}
}

// WithConcurrency sets how many references the updater resolves in parallel.
func WithConcurrency(workers int) UpdaterOption {
	return func(u *Updater) {
		u.Concurrency = workers
	}
}

// concurrency returns the number of references to resolve in parallel.
func (u *Updater) concurrency() int {
	if u.Concurrency > 0 {
		return u.Concurrency
	}
	return defaultConcurrency
}

// NewUpdater creates an updater that resolves versions using the GitHub API.
// Callers with a resolver of their own build the Updater directly.
func NewUpdater(opts ...UpdaterOption) (*Updater, error) {
	resolver, err := NewGitHubResolver()
	if err != nil {
		return nil, err
	}

	updater := &Updater{Resolver: resolver}
	for _, opt := range opts {
		opt(updater)
	}
	return updater, nil
}

// Plan computes the changes needed to pin a list of references to a commit
// hash: the commit of the version each reference tracks or, for updaters set
// to upgrade, the commit of the latest release of the action. References that
// need no change, and those that cannot be pinned at all, are returned as
// skips.
func (u *Updater) Plan(ctx context.Context, refs []Reference) (*Plan, error) {
	if u.Resolver == nil {
		return nil, errors.New("updater has no resolver configured")
	}

	// Every reference is resolved on its own, so they go out in parallel:
	// the forge round trips are the slow part of a scan. Results are
	// collected by index to keep the plan in the order of the references.
	outcomes := make([]outcome, len(refs))
	workers := make(chan struct{}, u.concurrency())

	var wg sync.WaitGroup
	for i := range refs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			workers <- struct{}{}
			defer func() { <-workers }()

			outcomes[i] = u.planReference(ctx, &refs[i])
		}(i)
	}
	wg.Wait()

	plan := &Plan{}
	for i := range outcomes {
		switch {
		case outcomes[i].update != nil:
			plan.Updates = append(plan.Updates, *outcomes[i].update)
		case outcomes[i].skip != nil:
			plan.Skipped = append(plan.Skipped, *outcomes[i].skip)
		}
	}

	return plan, nil
}

// outcome is what planning a single reference produced: it either becomes an
// update or a skip.
type outcome struct {
	update *Update
	skip   *Skip
}

// planReference works out what should happen to a single reference.
func (u *Updater) planReference(ctx context.Context, ref *Reference) outcome {
	skip := func(reason SkipReason, release *Release, err error) outcome {
		return outcome{skip: &Skip{
			Reference: *ref, Reason: reason, Release: release, Err: err,
		}}
	}

	switch ref.Kind {
	case KindLocal:
		return skip(SkipLocal, nil, nil)
	case KindContainer:
		return skip(SkipContainer, nil, nil)
	case KindAction, KindReusableWorkflow:
	}

	repository := ref.Repository()
	if repository == "" {
		return skip(SkipNoRepository, nil, nil)
	}

	// Without an upgrade there is nothing to do to a reference that already
	// points at a commit.
	if !u.Upgrade && ref.IsPinned() {
		return skip(SkipPinned, nil, nil)
	}

	release, err := u.resolve(ctx, ref, repository)
	if err != nil {
		return skip(SkipUnresolved, nil, err)
	}

	if ref.Version() == release.Commit {
		return skip(SkipUpToDate, release, nil)
	}

	return outcome{update: &Update{Reference: *ref, Release: *release}}
}

// resolve returns the version a reference is to be pinned to.
func (u *Updater) resolve(ctx context.Context, ref *Reference, repository string) (*Release, error) {
	if u.Upgrade {
		return u.Resolver.LatestRelease(ctx, repository)
	}
	return u.Resolver.ResolveVersion(ctx, repository, ref.Version())
}

// Apply writes the updates to the files under root and returns the paths of
// the files that changed. Each entry is rewritten in place, preserving the
// rest of the line and the file.
func (u *Updater) Apply(root string, updates []Update) ([]string, error) {
	// Group the updates by the file they belong to so that each one is read
	// and written only once.
	byFile := map[string][]Update{}
	var order []string
	for i := range updates {
		path := updates[i].Reference.Workflow
		if _, ok := byFile[path]; !ok {
			order = append(order, path)
		}
		byFile[path] = append(byFile[path], updates[i])
	}

	var modified []string
	for _, path := range order {
		full, err := resolvePath(root, path)
		if err != nil {
			return modified, err
		}

		changed, err := applyToFile(full, byFile[path])
		if err != nil {
			return modified, fmt.Errorf("updating %s: %w", path, err)
		}
		if changed {
			modified = append(modified, path)
		}
	}
	return modified, nil
}

// resolvePath joins a reference path to the scan root, making sure the result
// stays inside it. References come from scanning the repository, but they are
// data and we are about to write to the path they carry.
func resolvePath(root, path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("refusing to write to absolute path %q", path)
	}

	full := filepath.Clean(filepath.Join(root, path))
	rel, err := filepath.Rel(filepath.Clean(root), full)
	if err != nil {
		return "", fmt.Errorf("resolving %q: %w", path, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("refusing to write to %q, it is outside the scanned directory", path)
	}
	return full, nil
}

// applyToFile rewrites the `uses:` entries of a single file.
func applyToFile(path string, updates []Update) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("reading file: %w", err)
	}

	// Keep the line ending style of the original file.
	newline := "\n"
	if strings.Contains(string(data), "\r\n") {
		newline = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")

	changed := false
	for u := range updates {
		update := &updates[u]
		i := update.Reference.Line - 1
		if i < 0 || i >= len(lines) {
			return false, fmt.Errorf(
				"line %d not found (the file has %d lines)", update.Reference.Line, len(lines),
			)
		}

		rewritten, err := rewriteUses(lines[i], update)
		if err != nil {
			return false, fmt.Errorf("line %d: %w", update.Reference.Line, err)
		}
		if rewritten == lines[i] {
			continue
		}
		lines[i] = rewritten
		changed = true
	}

	if !changed {
		return false, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("reading file mode: %w", err)
	}
	//nolint:gosec // the path is checked by resolvePath to be inside the scan root
	if err := os.WriteFile(path, []byte(strings.Join(lines, newline)), info.Mode().Perm()); err != nil {
		return false, fmt.Errorf("writing file: %w", err)
	}
	return true, nil
}

// rewriteUses returns the `uses:` line pointing at the updated version. The
// indentation and quoting of the entry are preserved and the tag the action
// was pinned to is left in a trailing comment.
func rewriteUses(line string, update *Update) (string, error) {
	parts := usesLineRegexp.FindStringSubmatch(line)
	if parts == nil {
		return "", fmt.Errorf("line does not define a uses entry: %q", strings.TrimSpace(line))
	}

	key, value := parts[1], parts[2]

	// Keep the quotes if the entry is quoted.
	quote := ""
	if len(value) > 1 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		quote = string(value[0])
		value = strings.Trim(value, quote)
	}

	if value != update.Reference.Uses {
		return "", fmt.Errorf(
			"entry changed since it was scanned: found %q, expected %q", value, update.Reference.Uses,
		)
	}

	path, _, _ := strings.Cut(value, "@")
	return fmt.Sprintf(
		"%s%s%s@%s%s%s",
		key, quote, path, update.Release.Commit, quote, versionComment(update.Release.Tag),
	), nil
}

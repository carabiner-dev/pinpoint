// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/carabiner-dev/termtable"
	"github.com/spf13/cobra"

	"github.com/carabiner-dev/pinpoint/options"
	"github.com/carabiner-dev/pinpoint/pkg/pinpoint"
)

func addCheck(parent *cobra.Command) {
	opts := &options.Check{}

	checkCmd := &cobra.Command{
		Use:   "check [path]",
		Short: "list the action references that are not pinned to a hash",
		Long: appName + ` check: list unpinned action references

The check subcommand scans the GitHub Actions workflows found in a
directory (the current one by default) and lists the action references
that are not pinned to a commit hash, along with the version each one
would be pinned to by ` + appName + ` pin.

Pinpoint also looks up the versions of the references that are pinned
and reports those that have a newer release, so that the output covers
both halves of keeping actions under control: pinning them and keeping
them current. Only the unpinned ones fail the command.

Two flags control how much pinpoint asks the forge. With --updates=false
it stops reporting the references that have a newer release, but still
resolves the version each unpinned entry would be pinned to. With
--offline it makes no calls at all: the scan then only knows which
references are pinned and which are not.

The directory to scan can be specified as the first argument. The
command exits with an error if any unpinned references are found.

Using --attest, pinpoint writes the full scan results (both the pinned
and unpinned references) as an unsigned in-toto attestation instead of
the table. The subject of the attestation is the scanned repository at
its current commit. Attestations are data, not verdicts, so the command
exits cleanly when generating one, leaving the pass/fail decision to
whoever evaluates it.

Scans often run in a fork, which is not the project the code belongs to.
Pinpoint asks the forge which repository a fork was created from and
names that one in the subject, noting the fork it read the workflows
from in the subject annotations. With --offline the subject names the
repository as the local origin remote has it.

Versions are read from the GitHub API through the carabiner GitHub
client. Export a token in GITHUB_TOKEN or GH_TOKEN to authenticate the
calls: anonymous calls work for public repositories but are rate limited
to a handful of scans per hour.
`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Path = "."
			if len(args) > 0 {
				opts.Path = args[0]
			}
			if err := opts.Validate(); err != nil {
				return err
			}

			report, err := pinpoint.NewScanner().Scan(opts.Path)
			if err != nil {
				return err
			}

			// One resolver serves the version lookups and the search for
			// the repository a fork was created from.
			var resolver *pinpoint.GitHubResolver
			if !opts.Offline {
				resolver, err = newResolver(cmd)
				if err != nil {
					return err
				}
			}

			var updates *pinpoint.Updates
			if resolver != nil {
				updates, err = checkUpdates(cmd, resolver, report, opts.Updates)
				if err != nil {
					return err
				}
			}

			if opts.Attest {
				return writeAttestation(cmd, report, updates, resolver, opts.Path)
			}

			return writeReport(cmd.OutOrStdout(), report, updates)
		},
	}
	opts.AddFlags(checkCmd)
	parent.AddCommand(checkCmd)
}

// newResolver creates the client pinpoint asks about versions and forks.
// Failing to create it is not fatal, the scan still knows which references
// are pinned, so the error is reported and the command carries on.
func newResolver(cmd *cobra.Command) (*pinpoint.GitHubResolver, error) {
	resolver, err := pinpoint.NewGitHubResolver()
	if err != nil {
		return nil, warnNoUpdates(cmd, err)
	}
	return resolver, nil
}

// checkUpdates looks up the versions available for the references of a
// report. Lookup errors leave the command without version data instead of
// stopping it.
func checkUpdates(
	cmd *cobra.Command, resolver pinpoint.Resolver, report *pinpoint.Report, latest bool,
) (*pinpoint.Updates, error) {
	updates, err := pinpoint.CheckUpdates(
		cmd.Context(), resolver, report.References, pinpoint.WithLatestReleases(latest),
	)
	if err != nil {
		return nil, warnNoUpdates(cmd, err)
	}

	// References that got no answer render as unknown versions. Say why
	// once, a rejected token turns every row into a question mark.
	if unresolved := updates.Unresolved(); len(unresolved) > 0 {
		if _, err := fmt.Fprintf(
			cmd.ErrOrStderr(), "Could not look up %d reference(s): %v\n",
			len(unresolved), firstError(unresolved),
		); err != nil {
			return nil, fmt.Errorf("writing warning: %w", err)
		}
	}

	return updates, nil
}

// firstError returns the error behind the first skip that carries one.
func firstError(skipped []pinpoint.Skip) error {
	for i := range skipped {
		if skipped[i].Err != nil {
			return skipped[i].Err
		}
	}
	return errors.New("the version could not be resolved")
}

// warnNoUpdates tells the user why pinpoint is not reporting the versions
// available for the actions.
func warnNoUpdates(cmd *cobra.Command, reason error) error {
	if _, err := fmt.Fprintf(
		cmd.ErrOrStderr(), "Not checking for newer versions: %v\n", reason,
	); err != nil {
		return fmt.Errorf("writing warning: %w", err)
	}
	return nil
}

// writeReport renders the findings of a scan: the references that are not
// pinned and those pinned to something older than the latest release.
func writeReport(w io.Writer, report *pinpoint.Report, updates *pinpoint.Updates) error {
	unpinned := report.Unpinned()

	var outdated []pinpoint.Reference
	for _, ref := range report.References {
		if ref.IsPinned() && updates.Outdated(&ref) {
			outdated = append(outdated, ref)
		}
	}

	if len(unpinned) == 0 && len(outdated) == 0 {
		message := "All action references are pinned to hashes."
		if updates.LatestChecked() {
			message = "All action references are pinned to hashes and up to date."
		}
		_, err := fmt.Fprintln(w, message)
		return err
	}

	if len(unpinned) > 0 {
		if _, err := fmt.Fprintln(w, "Action references not pinned to a commit hash:"); err != nil {
			return fmt.Errorf("writing findings: %w", err)
		}
		if err := writeTable(w, unpinned, updates); err != nil {
			return err
		}
	}

	if len(outdated) > 0 {
		if len(unpinned) > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return fmt.Errorf("writing findings: %w", err)
			}
		}
		if _, err := fmt.Fprintln(w, "Pinned references with a newer release available:"); err != nil {
			return fmt.Errorf("writing findings: %w", err)
		}
		if err := writeOutdatedTable(w, outdated, updates); err != nil {
			return err
		}
	}

	if len(unpinned) == 0 {
		return nil
	}
	return fmt.Errorf("found %d action reference(s) not pinned to a hash", len(unpinned))
}

// writeAttestation renders the scan results as an in-toto attestation
// vouching for the repository found at path at its current commit. When a
// resolver is available the subject names the repository a fork was created
// from instead of the fork itself.
func writeAttestation(
	cmd *cobra.Command, report *pinpoint.Report,
	updates *pinpoint.Updates, resolver *pinpoint.GitHubResolver, path string,
) error {
	w := cmd.OutOrStdout()

	// Only hand over a resolver we actually have: a nil pointer stored in
	// an interface is not a nil interface, and the subject builder would
	// take it for a usable one.
	var source pinpoint.SourceResolver
	if resolver != nil {
		source = resolver
	}

	subject, err := pinpoint.SubjectFromRepository(cmd.Context(), path, source)
	if err != nil {
		return fmt.Errorf("defining attestation subject: %w", err)
	}

	statement, err := report.Statement(updates, subject)
	if err != nil {
		return err
	}

	data, err := pinpoint.MarshalStatement(statement)
	if err != nil {
		return err
	}

	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("writing attestation: %w", err)
	}
	return nil
}

// unknownVersion is shown when pinpoint could not find out which version an
// action is at, or which one is the newest.
const unknownVersion = "?"

// writeTable renders the unpinned references as a table, with the version
// each entry would be pinned to when it is known.
func writeTable(w io.Writer, refs []pinpoint.Reference, updates *pinpoint.Updates) error {
	t := termtable.NewTable()

	head := t.AddHeader()
	head.AddCell(termtable.WithContent("Workflow"))
	head.AddCell(termtable.WithContent("Line"))
	head.AddCell(termtable.WithContent("Job"))
	head.AddCell(termtable.WithContent("Action"))
	if updates != nil {
		head.AddCell(termtable.WithContent("Pin to"))
	}

	// Keep every finding on a single line, trimming the ones that overflow.
	// The versions column is only created when we have versions to show,
	// asking for it would add an empty column to the table.
	styleFileColumn(t, 0)
	t.Column(1).SetAlign(termtable.AlignRight)
	t.Column(2).Style("white-space: nowrap")
	t.Column(3).Style("white-space: nowrap; flex: 3")
	if updates != nil {
		t.Column(4).Style("white-space: nowrap")
	}

	lines := make([]string, 0, len(refs))
	jobs := make([]string, 0, len(refs))
	targets := make([]string, 0, len(refs))
	for _, ref := range refs {
		line := strconv.Itoa(ref.Line)
		lines = append(lines, line)
		jobs = append(jobs, ref.Job)

		row := t.AddRow()
		row.AddCell(termtable.WithContent(ref.Workflow))
		row.AddCell(termtable.WithContent(line))
		row.AddCell(termtable.WithContent(ref.Job))
		row.AddCell(termtable.WithContent(ref.Uses))

		if updates != nil {
			target := pinTarget(&ref, updates)
			targets = append(targets, target)
			row.AddCell(termtable.WithContent(target))
		}
	}

	// Line numbers, job IDs and versions are short, pin their columns to
	// their widest value so the whole budget goes to the wide ones.
	t.Column(1).SetWidth(narrowWidth("Line", lines...))
	t.Column(2).SetWidth(narrowWidth("Job", jobs...))
	if updates != nil {
		t.Column(4).SetWidth(narrowWidth("Pin to", targets...))
	}

	if _, err := fmt.Fprint(w, t.String()); err != nil {
		return fmt.Errorf("writing findings table: %w", err)
	}
	return nil
}

// maxLinesWidth caps the width of the column listing the lines of a bump so
// that a long list wraps instead of squeezing the rest of the table.
const maxLinesWidth = 14

// bump is an action upgrade available to a workflow: every entry of a file
// using the same version of the same action is reported as one.
type bump struct {
	// Workflow is the path of the file holding the entries.
	Workflow string

	// Action is the repository hosting the action.
	Action string

	// Using is the version the entries are pinned to.
	Using string

	// Latest is the newest release of the action.
	Latest string

	// Lines are the lines of the file defining the entries.
	Lines []int
}

// bumps groups the outdated references by the file they live in and the
// version they are using, so that a workflow using the same old action three
// times is one bump to apply, not three findings.
func bumps(refs []pinpoint.Reference, updates *pinpoint.Updates) []bump {
	var grouped []bump
	index := map[string]int{}

	for _, ref := range refs {
		using := pinnedVersion(&ref)
		key := strings.Join([]string{ref.Workflow, ref.Repository(), using}, "\x00")

		if at, ok := index[key]; ok {
			grouped[at].Lines = append(grouped[at].Lines, ref.Line)
			continue
		}

		index[key] = len(grouped)
		grouped = append(grouped, bump{
			Workflow: ref.Workflow,
			Action:   ref.Repository(),
			Using:    using,
			Latest:   latestVersion(&ref, updates),
			Lines:    []int{ref.Line},
		})
	}

	for i := range grouped {
		sort.Ints(grouped[i].Lines)
	}
	return grouped
}

// lineList renders the lines of a bump as a comma separated list.
func (b *bump) lineList() string {
	numbers := make([]string, 0, len(b.Lines))
	for _, line := range b.Lines {
		numbers = append(numbers, strconv.Itoa(line))
	}
	return strings.Join(numbers, ", ")
}

// writeOutdatedTable renders the references that are pinned to a version
// older than the latest release of the action, one row per action version
// in each workflow.
func writeOutdatedTable(w io.Writer, refs []pinpoint.Reference, updates *pinpoint.Updates) error {
	t := termtable.NewTable()

	head := t.AddHeader()
	head.AddCell(termtable.WithContent("Workflow"))
	head.AddCell(termtable.WithContent("Lines"))
	head.AddCell(termtable.WithContent("Action"))
	head.AddCell(termtable.WithContent("Using"))
	head.AddCell(termtable.WithContent("Latest"))

	styleFileColumn(t, 0)
	// The lines are the one column allowed to wrap: a long list is better
	// read on two lines than trimmed away.
	t.Column(1).SetAlign(termtable.AlignRight)
	t.Column(2).Style("white-space: nowrap; flex: 3")
	t.Column(3).Style("white-space: nowrap")
	t.Column(4).Style("white-space: nowrap")

	grouped := bumps(refs, updates)
	lines := make([]string, 0, len(grouped))
	usings := make([]string, 0, len(grouped))
	latests := make([]string, 0, len(grouped))
	for i := range grouped {
		b := &grouped[i]
		lines = append(lines, b.lineList())
		usings = append(usings, b.Using)
		latests = append(latests, b.Latest)

		row := t.AddRow()
		row.AddCell(termtable.WithContent(b.Workflow))
		row.AddCell(termtable.WithContent(b.lineList()))
		row.AddCell(termtable.WithContent(b.Action))
		row.AddCell(termtable.WithContent(b.Using))
		row.AddCell(termtable.WithContent(b.Latest))
	}

	t.Column(1).SetWidth(min(narrowWidth("Lines", lines...), maxLinesWidth))
	t.Column(3).SetWidth(narrowWidth("Using", usings...))
	t.Column(4).SetWidth(narrowWidth("Latest", latests...))

	if _, err := fmt.Fprint(w, t.String()); err != nil {
		return fmt.Errorf("writing findings table: %w", err)
	}
	return nil
}

// shortHashLength is how much of a commit hash is shown when previewing the
// value an entry would be pinned to.
const shortHashLength = 7

// pinTarget returns the version `pinpoint pin` would pin a reference to: the
// release naming the version the entry is already using, with a preview of
// the hash that would land in the file. Entries pinpoint cannot resolve get a
// mark, they are the ones pin leaves alone.
func pinTarget(ref *pinpoint.Reference, updates *pinpoint.Updates) string {
	status, ok := updates.Status(ref)
	if !ok {
		return unknownVersion
	}
	return release(status.Pin)
}

// latestVersion returns the newest release of the action used by a reference
// and the hash an upgrade would pin it to, or a mark when pinpoint could not
// find out.
func latestVersion(ref *pinpoint.Reference, updates *pinpoint.Updates) string {
	status, ok := updates.Status(ref)
	if !ok {
		return unknownVersion
	}
	return release(status.Latest)
}

// release renders a version as its tag followed by a preview of the commit
// it points at, for example "v4.4.0 (11d5960…)".
func release(release pinpoint.Release) string {
	if release.Tag == "" {
		return unknownVersion
	}
	if release.Commit == "" {
		return release.Tag
	}
	return fmt.Sprintf("%s (%s)", release.Tag, shortHash(release.Commit))
}

// shortHash abbreviates a commit hash, marking that it was cut short.
func shortHash(commit string) string {
	if len(commit) <= shortHashLength {
		return commit
	}
	return commit[:shortHashLength] + "…"
}

// pinnedVersion returns the version a pinned reference is using: the comment
// trailing the entry when there is one, the abbreviated hash otherwise.
func pinnedVersion(ref *pinpoint.Reference) string {
	if ref.Comment != "" {
		return ref.Comment
	}

	version := ref.Version()
	if version == "" {
		return unknownVersion
	}
	return shortHash(version)
}

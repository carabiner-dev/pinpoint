// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"io"
	"strconv"

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
that are not pinned to a commit hash, along with the latest release
available for each one.

Pinpoint also looks up the versions of the references that are pinned
and reports those that have a newer release, so that the output covers
both halves of keeping actions under control: pinning them and keeping
them current. Only the unpinned ones fail the command. Pass
--updates=false to skip the lookups and run offline.

The directory to scan can be specified as the first argument. The
command exits with an error if any unpinned references are found.

Using --attest, pinpoint writes the full scan results (both the pinned
and unpinned references) as an unsigned in-toto attestation instead of
the table. The subject of the attestation is the scanned repository at
its current commit. Attestations are data, not verdicts, so the command
exits cleanly when generating one, leaving the pass/fail decision to
whoever evaluates it.

Versions are read from the GitHub API. Export a token in GITHUB_TOKEN
to raise the rate limit applied to the lookups.
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

			var updates *pinpoint.Updates
			if opts.Updates {
				updates, err = checkUpdates(cmd, report)
				if err != nil {
					return err
				}
			}

			if opts.Attest {
				return writeAttestation(cmd.OutOrStdout(), report, updates, opts.Path)
			}

			return writeReport(cmd.OutOrStdout(), report, updates)
		},
	}
	opts.AddFlags(checkCmd)
	parent.AddCommand(checkCmd)
}

// checkUpdates looks up the versions available for the references of a
// report. Failing to reach the forge is not fatal, pinpoint still knows
// which references are pinned, so the lookup errors are reported and the
// command carries on without the version data.
func checkUpdates(cmd *cobra.Command, report *pinpoint.Report) (*pinpoint.Updates, error) {
	resolver, err := pinpoint.NewGitHubResolver()
	if err != nil {
		return nil, warnNoUpdates(cmd, err)
	}

	updates, err := pinpoint.CheckUpdates(cmd.Context(), resolver, report.References)
	if err != nil {
		return nil, warnNoUpdates(cmd, err)
	}
	return updates, nil
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
		if updates != nil {
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
// vouching for the repository found at path at its current commit.
func writeAttestation(w io.Writer, report *pinpoint.Report, updates *pinpoint.Updates, path string) error {
	subject, err := pinpoint.SubjectFromRepository(path)
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

// writeTable renders the unpinned references as a table, with the latest
// release of each action when it is known.
func writeTable(w io.Writer, refs []pinpoint.Reference, updates *pinpoint.Updates) error {
	t := termtable.NewTable()

	head := t.AddHeader()
	head.AddCell(termtable.WithContent("Workflow"))
	head.AddCell(termtable.WithContent("Line"))
	head.AddCell(termtable.WithContent("Job"))
	head.AddCell(termtable.WithContent("Action"))
	if updates != nil {
		head.AddCell(termtable.WithContent("Latest"))
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
	latests := make([]string, 0, len(refs))
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
			latest := latestVersion(&ref, updates)
			latests = append(latests, latest)
			row.AddCell(termtable.WithContent(latest))
		}
	}

	// Line numbers, job IDs and versions are short, pin their columns to
	// their widest value so the whole budget goes to the wide ones.
	t.Column(1).SetWidth(narrowWidth("Line", lines...))
	t.Column(2).SetWidth(narrowWidth("Job", jobs...))
	if updates != nil {
		t.Column(4).SetWidth(narrowWidth("Latest", latests...))
	}

	if _, err := fmt.Fprint(w, t.String()); err != nil {
		return fmt.Errorf("writing findings table: %w", err)
	}
	return nil
}

// writeOutdatedTable renders the references that are pinned to a version
// older than the latest release of the action.
func writeOutdatedTable(w io.Writer, refs []pinpoint.Reference, updates *pinpoint.Updates) error {
	t := termtable.NewTable()

	head := t.AddHeader()
	head.AddCell(termtable.WithContent("Workflow"))
	head.AddCell(termtable.WithContent("Line"))
	head.AddCell(termtable.WithContent("Action"))
	head.AddCell(termtable.WithContent("Using"))
	head.AddCell(termtable.WithContent("Latest"))

	styleFileColumn(t, 0)
	t.Column(1).SetAlign(termtable.AlignRight)
	t.Column(2).Style("white-space: nowrap; flex: 3")
	t.Column(3).Style("white-space: nowrap")
	t.Column(4).Style("white-space: nowrap")

	lines := make([]string, 0, len(refs))
	usings := make([]string, 0, len(refs))
	latests := make([]string, 0, len(refs))
	for _, ref := range refs {
		line := strconv.Itoa(ref.Line)
		using := pinnedVersion(&ref)
		latest := latestVersion(&ref, updates)
		lines = append(lines, line)
		usings = append(usings, using)
		latests = append(latests, latest)

		row := t.AddRow()
		row.AddCell(termtable.WithContent(ref.Workflow))
		row.AddCell(termtable.WithContent(line))
		row.AddCell(termtable.WithContent(ref.Repository()))
		row.AddCell(termtable.WithContent(using))
		row.AddCell(termtable.WithContent(latest))
	}

	t.Column(1).SetWidth(narrowWidth("Line", lines...))
	t.Column(3).SetWidth(narrowWidth("Using", usings...))
	t.Column(4).SetWidth(narrowWidth("Latest", latests...))

	if _, err := fmt.Fprint(w, t.String()); err != nil {
		return fmt.Errorf("writing findings table: %w", err)
	}
	return nil
}

// latestVersion returns the tag of the newest release of the action used by
// a reference, or a mark when pinpoint could not find out.
func latestVersion(ref *pinpoint.Reference, updates *pinpoint.Updates) string {
	status, ok := updates.Status(ref)
	if !ok || status.Latest.Tag == "" {
		return unknownVersion
	}
	return status.Latest.Tag
}

// pinnedVersion returns the version a pinned reference is using: the comment
// trailing the entry when there is one, the abbreviated hash otherwise.
func pinnedVersion(ref *pinpoint.Reference) string {
	if ref.Comment != "" {
		return ref.Comment
	}

	version := ref.Version()
	if len(version) > 7 {
		return version[:7]
	}
	if version == "" {
		return unknownVersion
	}
	return version
}

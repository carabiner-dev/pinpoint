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
that are not pinned to a commit hash. Findings are printed in a table
listing the workflow, job, step and action reference of each one.

The directory to scan can be specified as the first argument. The
command exits with an error if any unpinned references are found.

Using --attest, pinpoint writes the full scan results (both the pinned
and unpinned references) as an unsigned in-toto attestation instead of
the table. The subject of the attestation is the scanned repository at
its current commit. Attestations are data, not verdicts, so the command
exits cleanly when generating one, leaving the pass/fail decision to
whoever evaluates it.
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

			if opts.Attest {
				return writeAttestation(cmd.OutOrStdout(), report, opts.Path)
			}

			unpinned := report.Unpinned()
			if len(unpinned) == 0 {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), "All action references are pinned to hashes."); err != nil {
					return err
				}
				return nil
			}

			if err := writeTable(cmd.OutOrStdout(), unpinned); err != nil {
				return err
			}
			return fmt.Errorf("found %d action reference(s) not pinned to a hash", len(unpinned))
		},
	}
	opts.AddFlags(checkCmd)
	parent.AddCommand(checkCmd)
}

// writeAttestation renders the scan results as an in-toto attestation
// vouching for the repository found at path at its current commit.
func writeAttestation(w io.Writer, report *pinpoint.Report, path string) error {
	subject, err := pinpoint.SubjectFromRepository(path)
	if err != nil {
		return fmt.Errorf("defining attestation subject: %w", err)
	}

	statement, err := report.Statement(subject)
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

// writeTable renders the unpinned references as a table.
func writeTable(w io.Writer, refs []pinpoint.Reference) error {
	t := termtable.NewTable()

	head := t.AddHeader()
	head.AddCell(termtable.WithContent("Workflow"))
	head.AddCell(termtable.WithContent("Line"))
	head.AddCell(termtable.WithContent("Job"))
	head.AddCell(termtable.WithContent("Action"))

	// Keep every finding on a single line, trimming the ones that overflow.
	styleFileColumn(t, 0)
	t.Column(1).SetAlign(termtable.AlignRight)
	t.Column(2).Style("white-space: nowrap")
	t.Column(3).Style("white-space: nowrap; flex: 3")

	lines := make([]string, 0, len(refs))
	jobs := make([]string, 0, len(refs))
	for _, ref := range refs {
		line := strconv.Itoa(ref.Line)
		lines = append(lines, line)
		jobs = append(jobs, ref.Job)

		row := t.AddRow()
		row.AddCell(termtable.WithContent(ref.Workflow))
		row.AddCell(termtable.WithContent(line))
		row.AddCell(termtable.WithContent(ref.Job))
		row.AddCell(termtable.WithContent(ref.Uses))
	}

	// Line numbers and job IDs are short, pin their columns to their widest
	// value so the whole budget goes to the workflow and action columns.
	t.Column(1).SetWidth(narrowWidth("Line", lines...))
	t.Column(2).SetWidth(narrowWidth("Job", jobs...))

	if _, err := fmt.Fprint(w, t.String()); err != nil {
		return fmt.Errorf("writing findings table: %w", err)
	}
	return nil
}

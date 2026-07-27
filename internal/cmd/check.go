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
	opts := &options.Scan{}

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

// maxJobWidth caps the width of the job column so that a workflow with
// unusually long job IDs does not starve the rest of the table.
const maxJobWidth = 24

// writeTable renders the unpinned references as a table. The workflow paths
// are trimmed from the left when they don't fit the column as the tail (the
// workflow file name) is the part that identifies them.
func writeTable(w io.Writer, refs []pinpoint.Reference) error {
	t := termtable.NewTable()

	head := t.AddHeader()
	head.AddCell(termtable.WithContent("Workflow"))
	head.AddCell(termtable.WithContent("Line"))
	head.AddCell(termtable.WithContent("Job"))
	head.AddCell(termtable.WithContent("Action"))

	// Keep every finding on a single line. The workflow path loses its head
	// when it overflows, the rest of the columns are trimmed at the tail.
	t.Column(0).Style("white-space: nowrap; text-overflow-position: start; flex: 2")
	t.Column(1).SetAlign(termtable.AlignRight)
	t.Column(2).Style("white-space: nowrap")
	t.Column(3).Style("white-space: nowrap; flex: 3")

	lineWidth := termtable.DisplayWidth("Line")
	jobWidth := termtable.DisplayWidth("Job")
	for _, ref := range refs {
		line := strconv.Itoa(ref.Line)
		lineWidth = max(lineWidth, termtable.DisplayWidth(line))
		jobWidth = max(jobWidth, termtable.DisplayWidth(ref.Job))

		row := t.AddRow()
		row.AddCell(termtable.WithContent(ref.Workflow))
		row.AddCell(termtable.WithContent(line))
		row.AddCell(termtable.WithContent(ref.Job))
		row.AddCell(termtable.WithContent(ref.Uses))
	}

	// Line numbers and job IDs are short, pin their columns to their widest
	// value so the whole budget goes to the workflow and action columns.
	t.Column(1).SetWidth(lineWidth)
	t.Column(2).SetWidth(min(jobWidth, maxJobWidth))

	if _, err := fmt.Fprint(w, t.String()); err != nil {
		return fmt.Errorf("writing findings table: %w", err)
	}
	return nil
}

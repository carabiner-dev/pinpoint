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

func addPin(parent *cobra.Command) {
	opts := &options.Pin{}

	pinCmd := &cobra.Command{
		Use:   "pin [path]",
		Short: "pin the action references of the workflows to a commit hash",
		Long: appName + ` pin: pin action references to a commit hash

The pin subcommand scans the GitHub Actions workflows found in a
directory (the current one by default) and rewrites in place the action
references that are not pinned to a commit hash. Each entry is pinned to
the commit the version it uses points at, leaving the version in a
comment next to the reference:

  uses: actions/checkout@08c6903cd8c0fde910a37f88322edcfb5dd907a8 # v5.0.0

The comment records the most precise tag naming the commit, so an entry
tracking a major version (@v5) is pinned with the patch release it
resolved to. Any comment already trailing the entry is replaced, it is
the slot where the version a hash corresponds to is kept by convention.

Using --upgrade, references are pinned to the latest release of each
action instead of to the version they are using, upgrading them in the
same pass.

Using --all, pinpoint looks at every reference instead of only the
unpinned ones. Combined with --upgrade this moves the whole repository
to the newest releases. The scan is also widened to the action
definitions (action.yml files) found in the repository, skipping the
.git, vendor, node_modules and testdata directories.

References that cannot be pinned to a repository commit (local actions
and those run from container images) are left untouched.

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

			report, err := pinpoint.NewScanner(
				pinpoint.WithActions(opts.All),
			).Scan(opts.Path)
			if err != nil {
				return err
			}

			refs := report.Unpinned()
			if opts.All {
				refs = report.References
			}
			if len(refs) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "No action references to pin.")
				return err
			}

			updater, err := pinpoint.NewUpdater(
				pinpoint.WithUpgrade(opts.Upgrade),
			)
			if err != nil {
				return err
			}

			plan, err := updater.Plan(cmd.Context(), refs)
			if err != nil {
				return err
			}

			modified, err := updater.Apply(opts.Path, plan.Updates)
			if err != nil {
				return err
			}

			return writePinResults(cmd.OutOrStdout(), plan, modified)
		},
	}
	opts.AddFlags(pinCmd)
	parent.AddCommand(pinCmd)
}

// writePinResults reports the references that were pinned and those that
// could not be resolved.
func writePinResults(w io.Writer, plan *pinpoint.Plan, modified []string) error {
	if len(plan.Updates) > 0 {
		if err := writeUpdatesTable(w, plan.Updates); err != nil {
			return err
		}
	}

	summary := "No action references were updated.\n"
	if len(plan.Updates) > 0 {
		summary = fmt.Sprintf(
			"Pinned %d action reference(s) in %d file(s).\n", len(plan.Updates), len(modified),
		)
	}
	if _, err := fmt.Fprint(w, summary); err != nil {
		return fmt.Errorf("writing summary: %w", err)
	}

	// Only the references we could not look up are worth calling out one by
	// one, those we know we cannot pin are just counted.
	unpinnable := 0
	for _, skip := range plan.Skipped {
		switch skip.Reason {
		case pinpoint.SkipLocal, pinpoint.SkipContainer:
			unpinnable++
		case pinpoint.SkipUnresolved, pinpoint.SkipNoRepository:
			if _, err := fmt.Fprintf(
				w, "Left %s in %s alone: %s\n",
				skip.Reference.Uses, skip.Reference.Workflow, skip.Reason,
			); err != nil {
				return fmt.Errorf("writing summary: %w", err)
			}
		case pinpoint.SkipPinned, pinpoint.SkipUpToDate:
		}
	}

	if unpinnable > 0 {
		if _, err := fmt.Fprintf(
			w, "%d reference(s) cannot be pinned to a commit (local or container actions).\n",
			unpinnable,
		); err != nil {
			return fmt.Errorf("writing summary: %w", err)
		}
	}

	return nil
}

// writeUpdatesTable renders the applied updates as a table.
func writeUpdatesTable(w io.Writer, updates []pinpoint.Update) error {
	t := termtable.NewTable()

	head := t.AddHeader()
	head.AddCell(termtable.WithContent("File"))
	head.AddCell(termtable.WithContent("Line"))
	head.AddCell(termtable.WithContent("Action"))
	head.AddCell(termtable.WithContent("Version"))

	styleFileColumn(t, 0)
	t.Column(1).SetAlign(termtable.AlignRight)
	t.Column(2).Style("white-space: nowrap; flex: 3")
	t.Column(3).Style("white-space: nowrap")

	lines := make([]string, 0, len(updates))
	versions := make([]string, 0, len(updates))
	for _, update := range updates {
		line := strconv.Itoa(update.Reference.Line)
		lines = append(lines, line)
		versions = append(versions, update.Release.Tag)

		row := t.AddRow()
		row.AddCell(termtable.WithContent(update.Reference.Workflow))
		row.AddCell(termtable.WithContent(line))
		row.AddCell(termtable.WithContent(update.Reference.Uses))
		row.AddCell(termtable.WithContent(update.Release.Tag))
	}

	t.Column(1).SetWidth(narrowWidth("Line", lines...))
	t.Column(3).SetWidth(narrowWidth("Version", versions...))

	if _, err := fmt.Fprint(w, t.String()); err != nil {
		return fmt.Errorf("writing updates table: %w", err)
	}
	return nil
}

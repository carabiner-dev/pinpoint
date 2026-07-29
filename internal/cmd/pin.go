// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

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

Using --update, references are pinned to the latest release of each
action instead of to the version they are using, updating them in the
same pass. It is the flag that acts on what check reports as updatable.

Using --all, pinpoint looks at every reference instead of only the
unpinned ones. Combined with --update this moves the whole repository
to the newest releases. The scan is also widened to the action
definitions (action.yml files) found in the repository, skipping the
.git, vendor, node_modules and testdata directories.

References that cannot be pinned to a repository commit (local actions
and those run from container images) are left untouched.

The changes are listed before anything is written and confirmed with a
question, so a run can be inspected before it touches the workflows.
Answer y to write them, anything else leaves the files alone. Use --yes
to skip the question, which is also what unattended runs need: with no
one to answer, pinpoint stops instead of guessing.

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
				pinpoint.WithUpgrade(opts.Update),
			)
			if err != nil {
				return err
			}

			plan, err := updater.Plan(cmd.Context(), refs)
			if err != nil {
				return err
			}

			// Show what the run would write before writing it, the table
			// doubles as the preview the question refers to.
			if len(plan.Updates) > 0 {
				if err := writeUpdatesTable(cmd.OutOrStdout(), plan.Updates); err != nil {
					return err
				}
			}

			pin, err := confirmPin(cmd, len(plan.Updates), opts.Yes)
			if err != nil {
				return err
			}
			if !pin {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "Nothing was pinned.")
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

// confirmPin asks whether the entries pinpoint resolved should be written to
// the workflows. There is nothing to ask when the run found no changes to
// make or when the caller already said yes with --yes.
func confirmPin(cmd *cobra.Command, updates int, assumeYes bool) (bool, error) {
	if updates == 0 || assumeYes {
		return true, nil
	}

	if _, err := fmt.Fprintf(
		cmd.OutOrStdout(), "Pin %d action(s) to hashes? (y/N) ", updates,
	); err != nil {
		return false, fmt.Errorf("writing question: %w", err)
	}

	answer, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("reading answer: %w", err)
	}

	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer == "" && errors.Is(err, io.EOF) {
		return false, errors.New("nothing to read the answer from, use --yes to pin without asking")
	}

	return answer == "y" || answer == "yes", nil
}

// writePinResults reports the references that were pinned and those that
// could not be resolved.
func writePinResults(w io.Writer, plan *pinpoint.Plan, modified []string) error {
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
	for i := range plan.Skipped {
		skip := &plan.Skipped[i]
		switch skip.Reason {
		case pinpoint.SkipLocal, pinpoint.SkipContainer:
			unpinnable++
		case pinpoint.SkipUnresolved, pinpoint.SkipNoRepository:
			reason := string(skip.Reason)
			if skip.Err != nil {
				reason = fmt.Sprintf("%s (%v)", reason, skip.Err)
			}
			if _, err := fmt.Fprintf(
				w, "Left %s in %s alone: %s\n",
				skip.Reference.Uses, skip.Reference.Workflow, reason,
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
	t := newTable()
	addHeader(t, "File", "Line", "Action", "Version")

	styleFileColumn(t, 0)
	t.Column(1).SetAlign(termtable.AlignRight)
	t.Column(2).Style("white-space: nowrap; flex: 3")
	t.Column(3).Style("white-space: nowrap")

	lines := make([]string, 0, len(updates))
	versions := make([]string, 0, len(updates))
	for i := range updates {
		update := &updates[i]
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

	return printTable(w, t)
}

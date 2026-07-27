// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"

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
that are not pinned to a commit hash. Each finding is printed as:

  workflow > job > action

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
				fmt.Fprintln(cmd.OutOrStdout(), "All action references are pinned to hashes.")
				return nil
			}

			for _, ref := range unpinned {
				fmt.Fprintf(cmd.OutOrStdout(), "%s > %s > %s\n", ref.Workflow, ref.Job, ref.Uses)
			}
			return fmt.Errorf("found %d action reference(s) not pinned to a hash", len(unpinned))
		},
	}
	opts.AddFlags(checkCmd)
	parent.AddCommand(checkCmd)
}

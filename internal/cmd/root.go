// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

// Package cmd implements the pinpoint command line interface.
package cmd

import (
	"github.com/carabiner-dev/command/log"
	"github.com/spf13/cobra"
)

const appName = "pinpoint"

// Execute builds the pinpoint command tree and runs it.
func Execute() error {
	logOpts := &log.Options{}

	rootCmd := &cobra.Command{
		Use:   appName,
		Short: "checks that GitHub Actions are pinned to commit hashes",
		Long: appName + `: a GitHub Actions pinning checker

Pinpoint looks for GitHub Actions workflows in a repository and reports
the reusable actions that are not pinned to commit hashes.
`,
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := logOpts.Validate(); err != nil {
				return err
			}
			ctx, err := logOpts.WithLogger(cmd.Context())
			if err != nil {
				return err
			}
			cmd.SetContext(ctx)
			return nil
		},
	}
	logOpts.AddFlags(rootCmd)
	addCheck(rootCmd)
	addPin(rootCmd)
	addStatus(rootCmd)

	return rootCmd.Execute()
}

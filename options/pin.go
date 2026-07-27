// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package options

import (
	"github.com/carabiner-dev/command"
	"github.com/spf13/cobra"
)

var _ command.OptionsSet = &Pin{}

// Pin groups the options of the subcommand that rewrites the action
// references to pin them to a commit hash.
type Pin struct {
	Scan

	// All makes pinpoint look at every action reference instead of only
	// the unpinned ones, updating those that have a newer release. It also
	// widens the scan to the action definitions in the repository.
	All bool
}

// AddFlags adds the pin flags to a command.
func (po *Pin) AddFlags(cmd *cobra.Command) {
	po.Scan.AddFlags(cmd)
	cmd.PersistentFlags().BoolVar(
		&po.All, "all", false,
		"check every action reference, updating those with a newer release",
	)
}

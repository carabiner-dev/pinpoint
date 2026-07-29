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
	// the unpinned ones. It also widens the scan to the action definitions
	// in the repository.
	All bool

	// Update makes pinpoint pin the references to the latest release of
	// each action instead of to the version they already track. It is the
	// counterpart of the update reporting of the check subcommand.
	Update bool

	// Yes writes the changes without asking for confirmation first.
	Yes bool
}

// AddFlags adds the pin flags to a command.
func (po *Pin) AddFlags(cmd *cobra.Command) {
	po.Scan.AddFlags(cmd)
	cmd.PersistentFlags().BoolVar(
		&po.All, "all", false,
		"check every action reference, not just the unpinned ones",
	)
	cmd.PersistentFlags().BoolVar(
		&po.Update, "update", false,
		"pin to the latest release of each action instead of to the version in use",
	)
	cmd.PersistentFlags().BoolVarP(
		&po.Yes, "yes", "y", false,
		"write the changes without asking for confirmation",
	)
}

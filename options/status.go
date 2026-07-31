// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package options

import (
	"github.com/carabiner-dev/command"
	"github.com/spf13/cobra"
)

var _ command.OptionsSet = &Status{}

// Status groups the options of the subcommand that shows the pinning and
// update status of every action reference.
type Status struct {
	Scan

	// Offline stops pinpoint from calling the forge: no versions are
	// resolved, so only the pinning status is reported.
	Offline bool
}

// AddFlags adds the status flags to a command.
func (st *Status) AddFlags(cmd *cobra.Command) {
	st.Scan.AddFlags(cmd)
	cmd.PersistentFlags().BoolVar(
		&st.Offline, "offline", false,
		"make no calls to the forge, no versions are resolved",
	)
}

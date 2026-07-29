// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package options

import (
	"github.com/carabiner-dev/command"
	"github.com/spf13/cobra"
)

var _ command.OptionsSet = &Check{}

// Check groups the options of the subcommand that reports on the pinning
// status of the action references.
type Check struct {
	Scan

	// Attest makes pinpoint write the scan results as an in-toto
	// attestation instead of the human readable output.
	Attest bool

	// Updates makes pinpoint look up the latest release of the actions to
	// report the references that have a newer version available.
	Updates bool

	// Offline stops pinpoint from calling the forge at all: no versions
	// are resolved and no repository is checked for being a fork.
	Offline bool
}

// AddFlags adds the check flags to a command.
func (co *Check) AddFlags(cmd *cobra.Command) {
	co.Scan.AddFlags(cmd)
	cmd.PersistentFlags().BoolVarP(
		&co.Attest, "attest", "a", false,
		"write the results as an (unsigned) in-toto attestation",
	)
	cmd.PersistentFlags().BoolVar(
		&co.Updates, "updates", true,
		"report the references that have a newer release available",
	)
	cmd.PersistentFlags().BoolVar(
		&co.Offline, "offline", false,
		"make no calls to the forge, no versions are resolved",
	)
}

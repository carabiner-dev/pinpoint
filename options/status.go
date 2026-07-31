// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package options

import (
	"fmt"

	"github.com/carabiner-dev/command"
	"github.com/spf13/cobra"
)

var _ command.OptionsSet = &Status{}

// The SBOM formats the status subcommand can write.
const (
	FormatSPDX      = "spdx"
	FormatCycloneDX = "cyclonedx"
)

// Status groups the options of the subcommand that shows the pinning and
// update status of every action reference.
type Status struct {
	Scan

	// Format switches the output from the table to an SBOM in this format:
	// spdx or cyclonedx. Empty renders the table.
	Format string

	// Offline stops pinpoint from calling the forge: no versions are
	// resolved, so only the pinning status is reported.
	Offline bool
}

// AddFlags adds the status flags to a command.
func (st *Status) AddFlags(cmd *cobra.Command) {
	st.Scan.AddFlags(cmd)
	cmd.PersistentFlags().StringVar(
		&st.Format, "format", "",
		fmt.Sprintf("write the results as an SBOM: %s or %s", FormatSPDX, FormatCycloneDX),
	)
	cmd.PersistentFlags().BoolVar(
		&st.Offline, "offline", false,
		"make no calls to the forge, no versions are resolved",
	)
}

// Validate checks that the status options are usable.
func (st *Status) Validate() error {
	if err := st.Scan.Validate(); err != nil {
		return err
	}

	switch st.Format {
	case "", FormatSPDX, FormatCycloneDX:
	default:
		return fmt.Errorf(
			"unknown SBOM format %q, use %s or %s", st.Format, FormatSPDX, FormatCycloneDX,
		)
	}

	// The SBOM carries versions verified against the forge, there is no
	// offline way to build one.
	if st.Format != "" && st.Offline {
		return fmt.Errorf("SBOMs carry verified versions, --format cannot be combined with --offline")
	}
	return nil
}

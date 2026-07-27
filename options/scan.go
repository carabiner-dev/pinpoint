// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

// Package options defines the reusable option sets of the pinpoint commands.
package options

import (
	"errors"
	"fmt"
	"os"

	"github.com/carabiner-dev/command"
	"github.com/spf13/cobra"
)

var _ command.OptionsSet = &Scan{}

// Scan groups the options that control how pinpoint scans a repository
// for GitHub Actions workflows.
type Scan struct {
	config *command.OptionsSetConfig

	// Path is the directory pinpoint looks in for workflows. It is read
	// from the command arguments, not from a flag.
	Path string

	// Attest makes pinpoint write the scan results as an in-toto
	// attestation instead of the human readable output.
	Attest bool
}

// Config returns the flag configuration of the scanner options.
func (so *Scan) Config() *command.OptionsSetConfig {
	if so.config == nil {
		so.config = &command.OptionsSetConfig{
			Flags: map[string]command.FlagConfig{},
		}
	}
	return so.config
}

// AddFlags adds the scanner flags to a command.
func (so *Scan) AddFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().BoolVarP(
		&so.Attest, "attest", "a", false,
		"write the results as an (unsigned) in-toto attestation",
	)
}

// Validate checks that the scanner options are usable.
func (so *Scan) Validate() error {
	if so.Path == "" {
		return errors.New("scan path not set")
	}
	info, err := os.Stat(so.Path)
	if err != nil {
		return fmt.Errorf("checking scan path: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("scan path %q is not a directory", so.Path)
	}
	return nil
}

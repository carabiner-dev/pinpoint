// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

// Pinpoint checks that the actions referenced in GitHub Actions workflows
// are pinned to commit hashes.
package main

import (
	"os"

	"github.com/carabiner-dev/pinpoint/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}

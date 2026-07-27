// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

// Package pinpoint scans repositories for GitHub Actions workflows and
// collects the action references used in them to check they are pinned
// to commit hashes.
package pinpoint

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Scanner looks for GitHub Actions workflows and extracts the action
// references used in them.
type Scanner struct{}

// NewScanner creates a new workflow scanner.
func NewScanner() *Scanner {
	return &Scanner{}
}

// Scan looks for workflow files in the .github/workflows directory under
// root and returns a report with all the action references found in them.
func (s *Scanner) Scan(root string) (*Report, error) {
	report := &Report{}

	wfDir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(wfDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return report, nil
		}
		return nil, fmt.Errorf("reading workflows directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if ext := filepath.Ext(entry.Name()); ext != ".yml" && ext != ".yaml" {
			continue
		}

		path := filepath.Join(wfDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading workflow file: %w", err)
		}

		refs, err := parseWorkflow(data)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}

		wfPath, err := filepath.Rel(root, path)
		if err != nil {
			wfPath = path
		}
		for i := range refs {
			refs[i].Workflow = wfPath
		}
		report.References = append(report.References, refs...)
	}

	return report, nil
}

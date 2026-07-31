// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

// Package pinpoint scans repositories for GitHub Actions workflows and
// collects the action references used in them to check they are pinned
// to commit hashes.
//
// Tools embedding pinpoint get the same views its command line renders:
// ScanStatus describes the pinning state of every external action reference
// of a repository and BuildSBOM models the repository, its actions and its
// workflow files as a protobom graph. The lower level pieces are available
// too: Scanner collects the references, CheckUpdates looks up the versions
// available for them and Updater pins them in place.
package pinpoint

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// skippedDirs are directories we never walk when looking for action
// definitions.
var skippedDirs = map[string]struct{}{
	".git":         {},
	"node_modules": {},
	"vendor":       {},
	"testdata":     {},
}

// Scanner looks for GitHub Actions workflows and extracts the action
// references used in them.
type Scanner struct {
	// Actions makes the scanner also read the action definitions
	// (action.yml files) found in the repository.
	Actions bool
}

// ScannerOption configures a scanner.
type ScannerOption func(*Scanner)

// WithActions makes the scanner read the `uses:` entries of the action
// definitions found in the repository, not just those of the workflows.
func WithActions(actions bool) ScannerOption {
	return func(s *Scanner) {
		s.Actions = actions
	}
}

// NewScanner creates a new workflow scanner.
func NewScanner(opts ...ScannerOption) *Scanner {
	scanner := &Scanner{}
	for _, opt := range opts {
		opt(scanner)
	}
	return scanner
}

// Scan looks for workflow files in the .github/workflows directory under root
// and returns a report with all the action references found in them. Scanners
// configured with WithActions also read the action definitions in the
// repository.
func (s *Scanner) Scan(root string) (*Report, error) {
	report := &Report{}

	if err := s.scanWorkflows(root, report); err != nil {
		return nil, err
	}

	if s.Actions {
		if err := s.scanActions(root, report); err != nil {
			return nil, err
		}
	}

	return report, nil
}

// scanWorkflows reads the workflows in the .github/workflows directory.
func (s *Scanner) scanWorkflows(root string, report *Report) error {
	wfDir := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(wfDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading workflows directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !isYAML(entry.Name()) {
			continue
		}

		path := filepath.Join(wfDir, entry.Name())
		refs, err := parseFile(path, parseWorkflow)
		if err != nil {
			return err
		}

		wfPath := relativePath(root, path)
		for i := range refs {
			refs[i].Workflow = wfPath
		}
		report.Workflows = append(report.Workflows, wfPath)
		report.References = append(report.References, refs...)
	}

	return nil
}

// scanActions walks the repository looking for action definitions and reads
// the references used in their steps.
func (s *Scanner) scanActions(root string, report *Report) error {
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			if _, skip := skippedDirs[entry.Name()]; skip && path != root {
				return fs.SkipDir
			}
			return nil
		}

		if name := entry.Name(); name != "action.yml" && name != "action.yaml" {
			return nil
		}

		refs, err := parseFile(path, parseAction)
		if err != nil {
			return err
		}

		actionPath := relativePath(root, path)
		for i := range refs {
			refs[i].Workflow = actionPath
		}
		report.Actions = append(report.Actions, actionPath)
		report.References = append(report.References, refs...)
		return nil
	})
	if err != nil {
		return fmt.Errorf("looking for action definitions: %w", err)
	}
	return nil
}

// parseFile reads a YAML file and extracts its references using parse.
func parseFile(path string, parse func([]byte) ([]Reference, error)) ([]Reference, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	refs, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return refs, nil
}

// isYAML returns true when a file name looks like a YAML document.
func isYAML(name string) bool {
	ext := filepath.Ext(name)
	return ext == ".yml" || ext == ".yaml"
}

// relativePath returns path relative to root, falling back to the original
// path when it cannot be made relative.
func relativePath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return strings.TrimPrefix(rel, "./")
}

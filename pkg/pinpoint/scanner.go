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
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// skippedDirs are directories we never walk when looking for workflows and
// action definitions.
var skippedDirs = map[string]struct{}{
	".git":         {},
	"node_modules": {},
	"vendor":       {},
	"testdata":     {},
}

// forgeDirs are the configuration directories a forge reads a repository's
// workflows from. Every YAML file under their workflows subdirectory is a
// workflow, whatever it holds.
var forgeDirs = map[string]struct{}{
	".github":  {},
	".gitea":   {},
	".forgejo": {},
}

// actionFiles are the names an action definition has to have for a forge to
// load it.
var actionFiles = map[string]struct{}{
	"action.yml":  {},
	"action.yaml": {},
}

// documentKind is what a YAML file found in a repository turned out to be.
type documentKind int

const (
	// documentOther is a YAML file that defines neither a workflow nor an
	// action: the bulk of what a repository holds.
	documentOther documentKind = iota

	// documentWorkflow is a workflow, a file defining jobs to run on an
	// event.
	documentWorkflow

	// documentAction is an action definition, the action.yml file saying
	// how an action runs.
	documentAction
)

// Scanner looks for GitHub Actions workflows and action definitions and
// extracts the action references used in them.
type Scanner struct{}

// ScannerOption configures a scanner.
type ScannerOption func(*Scanner)

// NewScanner creates a new workflow scanner.
func NewScanner(opts ...ScannerOption) *Scanner {
	scanner := &Scanner{}
	for _, opt := range opts {
		opt(scanner)
	}
	return scanner
}

// Scan walks the directory at root looking for the files GitHub Actions
// reads and returns a report with all the action references found in them.
//
// Files are recognized by where they sit and by what they hold, so that a
// repository keeping its Actions code outside .github/workflows is covered
// as well as one that does not. Every YAML file under a forge's workflows
// directory is a workflow and every action.yml in the tree is an action
// definition; on top of those, any other YAML file declaring the jobs it
// runs on an event is read as a workflow and any file saying how an action
// runs is read as an action definition. That is what catches the composite
// action a repository publishes from its root, the workflow templates under
// .github/workflow-templates and the workflows of a monorepo's subprojects.
//
// The .git, vendor, node_modules and testdata directories are never walked.
func (s *Scanner) Scan(root string) (*Report, error) {
	report := &Report{}

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

		if !isYAML(entry.Name()) {
			return nil
		}
		return s.scanFile(root, path, report)
	})
	if err != nil {
		return nil, fmt.Errorf("looking for workflows and action definitions: %w", err)
	}

	return report, nil
}

// scanFile reads one YAML file found in the repository and adds the action
// references it defines to the report.
func (s *Scanner) scanFile(root, path string, report *Report) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	file := relativePath(root, path)
	kind := locationKind(file)

	doc, err := parseDocument(data)
	if err != nil {
		// Repositories are full of YAML that is really a template and
		// does not parse. Only the files a forge would load are expected
		// to be valid, the rest are just not Actions files.
		if kind == documentOther {
			return nil
		}
		return fmt.Errorf("parsing %s: %w", path, err)
	}

	if kind == documentOther {
		kind = contentKind(doc)
	}

	switch kind {
	case documentWorkflow:
		report.Workflows = append(report.Workflows, file)
		report.References = append(report.References, readFrom(parseWorkflow(doc), file)...)
	case documentAction:
		report.Actions = append(report.Actions, file)
		report.References = append(report.References, readFrom(parseAction(doc), file)...)
	case documentOther:
	}

	return nil
}

// locationKind returns the kind of the files a forge loads by their location
// alone: the YAML files it picks up from its workflows directory and the
// action definitions, which have to be named action.yml to be usable. Files
// found there count even when they hold nothing we can read, they are what
// the forge runs. Everything else is left to be classified by its contents.
func locationKind(file string) documentKind {
	if _, ok := actionFiles[filepath.Base(file)]; ok {
		return documentAction
	}
	if inWorkflowsDir(file) {
		return documentWorkflow
	}
	return documentOther
}

// inWorkflowsDir reports whether a file lives under the workflows directory
// of a forge configuration directory, at any depth: .github/workflows is
// where GitHub reads workflows from and the drop-in forges keep their own.
func inWorkflowsDir(file string) bool {
	dirs := strings.Split(filepath.ToSlash(filepath.Dir(file)), "/")
	for i := 1; i < len(dirs); i++ {
		if _, ok := forgeDirs[dirs[i-1]]; ok && dirs[i] == "workflows" {
			return true
		}
	}
	return false
}

// contentKind tells what a YAML file sitting outside the places a forge
// loads is by what it holds: a workflow declares the jobs it runs and the
// events triggering them, an action definition says how the action runs.
// Requiring both marks of a workflow keeps the unrelated YAML a repository
// is full of out of the report.
func contentKind(doc *yaml.Node) documentKind {
	jobs := mapValue(doc, "jobs")
	if jobs != nil && jobs.Kind == yaml.MappingNode && mapValue(doc, "on") != nil {
		return documentWorkflow
	}
	if runs := mapValue(doc, "runs"); scalarValue(runs, "using") != "" {
		return documentAction
	}
	return documentOther
}

// readFrom stamps the file a set of references was read from on each one.
func readFrom(refs []Reference, file string) []Reference {
	for i := range refs {
		refs[i].Workflow = file
	}
	return refs
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

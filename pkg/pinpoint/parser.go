// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// parseWorkflow reads the YAML source of a workflow and returns the action
// references found in its jobs, in the order they appear in the file. The
// references are returned without the workflow path, the caller is expected
// to complete them.
func parseWorkflow(data []byte) ([]Reference, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing workflow YAML: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, nil
	}

	jobs := mapValue(doc.Content[0], "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return nil, nil
	}

	var refs []Reference
	for i := 0; i+1 < len(jobs.Content); i += 2 {
		jobID := jobs.Content[i].Value
		job := resolve(jobs.Content[i+1])
		if job == nil || job.Kind != yaml.MappingNode {
			continue
		}

		// Job level uses entry (a reusable workflow call)
		if uses := mapValue(job, "uses"); uses != nil && uses.Kind == yaml.ScalarNode {
			kind := KindReusableWorkflow
			if classify(uses.Value) == KindLocal {
				kind = KindLocal
			}
			refs = append(refs, Reference{
				Job:     jobID,
				Uses:    uses.Value,
				Comment: lineComment(uses),
				Kind:    kind,
				Line:    uses.Line,
			})
		}

		refs = append(refs, parseSteps(mapValue(job, "steps"), jobID)...)
	}
	return refs, nil
}

// parseAction reads the YAML source of an action definition and returns the
// action references used by its steps. Only composite actions run steps, any
// other action kind yields no references.
func parseAction(data []byte) ([]Reference, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing action YAML: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, nil
	}

	runs := mapValue(doc.Content[0], "runs")
	if runs == nil || runs.Kind != yaml.MappingNode {
		return nil, nil
	}

	return parseSteps(mapValue(runs, "steps"), ""), nil
}

// parseSteps returns the references used by the steps in a YAML sequence,
// attributed to jobID.
func parseSteps(steps *yaml.Node, jobID string) []Reference {
	if steps == nil || steps.Kind != yaml.SequenceNode {
		return nil
	}

	var refs []Reference
	for _, step := range steps.Content {
		step = resolve(step)
		if step == nil || step.Kind != yaml.MappingNode {
			continue
		}
		uses := mapValue(step, "uses")
		if uses == nil || uses.Kind != yaml.ScalarNode {
			continue
		}
		name := scalarValue(step, "name")
		if name == "" {
			name = scalarValue(step, "id")
		}
		refs = append(refs, Reference{
			Job:     jobID,
			Step:    name,
			Uses:    uses.Value,
			Comment: lineComment(uses),
			Kind:    classify(uses.Value),
			Line:    uses.Line,
		})
	}
	return refs
}

// lineComment returns the comment trailing a node, stripped of its hash
// sign and surrounding spaces.
func lineComment(node *yaml.Node) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(node.LineComment), "#"))
}

// mapValue returns the value of key in a YAML mapping node, nil if the key
// is not found (or the node is not a mapping).
func mapValue(node *yaml.Node, key string) *yaml.Node {
	node = resolve(node)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return resolve(node.Content[i+1])
		}
	}
	return nil
}

// scalarValue returns the value of key in a YAML mapping node when it is a
// scalar, an empty string otherwise.
func scalarValue(node *yaml.Node, key string) string {
	value := mapValue(node, key)
	if value == nil || value.Kind != yaml.ScalarNode {
		return ""
	}
	return value.Value
}

// resolve follows alias nodes to the node they point to.
func resolve(node *yaml.Node) *yaml.Node {
	if node != nil && node.Kind == yaml.AliasNode {
		return node.Alias
	}
	return node
}

// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	gointoto "github.com/in-toto/attestation/go/v1"
)

func TestRemoteLocation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		remote   string
		expected string
		mustErr  bool
	}{
		{"https://github.com/carabiner-dev/pinpoint.git", "github.com/carabiner-dev/pinpoint", false},
		{"https://github.com/carabiner-dev/pinpoint", "github.com/carabiner-dev/pinpoint", false},
		{"ssh://git@github.com/carabiner-dev/pinpoint.git", "github.com/carabiner-dev/pinpoint", false},
		{"git@github.com:carabiner-dev/pinpoint.git", "github.com/carabiner-dev/pinpoint", false},
		{"https://gitlab.example.com/group/subgroup/repo.git", "gitlab.example.com/group/subgroup/repo", false},
		{"https://github.com/", "", true},
		{"/srv/git/repo.git", "", true},
	} {
		t.Run(tc.remote, func(t *testing.T) {
			t.Parallel()
			location, err := remoteLocation(tc.remote)
			if tc.mustErr {
				if err == nil {
					t.Fatalf("expected error parsing %q, got %q", tc.remote, location)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsing %q: %v", tc.remote, err)
			}
			if location != tc.expected {
				t.Errorf("remoteLocation(%q) = %q, want %q", tc.remote, location, tc.expected)
			}
		})
	}
}

// stubSourceResolver answers fork lookups from a fixed map.
type stubSourceResolver struct {
	sources map[string]string
	err     error
}

func (s *stubSourceResolver) SourceRepository(_ context.Context, repository string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	source, ok := s.sources[repository]
	if !ok {
		return repository, nil
	}
	return source, nil
}

func TestSourceLocation(t *testing.T) {
	t.Parallel()
	resolver := &stubSourceResolver{
		sources: map[string]string{"puerco/kubernetes": "kubernetes/kubernetes"},
	}

	for _, tc := range []struct {
		name     string
		resolver SourceResolver
		location string
		expected string
	}{
		{
			name:     "fork resolves to its source",
			resolver: resolver,
			location: "github.com/puerco/kubernetes",
			expected: "github.com/kubernetes/kubernetes",
		},
		{
			name:     "repositories that are not forks are left alone",
			resolver: resolver,
			location: "github.com/carabiner-dev/pinpoint",
			expected: "github.com/carabiner-dev/pinpoint",
		},
		{
			name:     "without a resolver nothing is looked up",
			resolver: nil,
			location: "github.com/puerco/kubernetes",
			expected: "github.com/puerco/kubernetes",
		},
		{
			name:     "other forges are left alone",
			resolver: resolver,
			location: "gitlab.com/puerco/kubernetes",
			expected: "gitlab.com/puerco/kubernetes",
		},
		{
			name:     "a failed lookup keeps the repository we scanned",
			resolver: &stubSourceResolver{err: errors.New("no network")},
			location: "github.com/puerco/kubernetes",
			expected: "github.com/puerco/kubernetes",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sourceLocation(t.Context(), tc.resolver, tc.location)
			if got != tc.expected {
				t.Errorf("sourceLocation(%q) = %q, want %q", tc.location, got, tc.expected)
			}
		})
	}
}

func TestStatement(t *testing.T) {
	t.Parallel()
	report := &Report{
		Workflows: []string{".github/workflows/ci.yaml"},
		References: []Reference{
			{Workflow: ".github/workflows/ci.yaml", Job: "build", Step: "Setup go", Uses: "actions/setup-go@v6", Kind: KindAction, Line: 14},
		},
	}

	subject := &gointoto.ResourceDescriptor{
		Name:   "github.com/carabiner-dev/pinpoint@ac881f9fa84fc51a8ead1609db049071b4a99bfa",
		Uri:    "git+https://github.com/carabiner-dev/pinpoint@ac881f9fa84fc51a8ead1609db049071b4a99bfa",
		Digest: map[string]string{"gitCommit": "ac881f9fa84fc51a8ead1609db049071b4a99bfa"},
	}

	statement, err := report.Statement(nil, subject)
	if err != nil {
		t.Fatalf("building statement: %v", err)
	}
	if statement.GetPredicateType() != PredicateType {
		t.Errorf("predicate type = %q, want %q", statement.GetPredicateType(), PredicateType)
	}
	if err := statement.Validate(); err != nil {
		t.Errorf("statement does not validate: %v", err)
	}

	data, err := MarshalStatement(statement)
	if err != nil {
		t.Fatalf("marshaling statement: %v", err)
	}

	var decoded struct {
		Type    string `json:"_type"`
		Subject []struct {
			Name   string            `json:"name"`
			Digest map[string]string `json:"digest"`
		} `json:"subject"`
		PredicateType string `json:"predicateType"`
		Predicate     struct {
			Summary    PredicateSummary     `json:"summary"`
			References []PredicateReference `json:"references"`
		} `json:"predicate"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshaling statement: %v", err)
	}

	if decoded.Type != gointoto.StatementTypeUri {
		t.Errorf("statement type = %q, want %q", decoded.Type, gointoto.StatementTypeUri)
	}
	if len(decoded.Subject) != 1 || decoded.Subject[0].Name != subject.GetName() {
		t.Fatalf("unexpected subject: %+v", decoded.Subject)
	}
	if decoded.Subject[0].Digest["gitCommit"] != subject.GetDigest()["gitCommit"] {
		t.Errorf("unexpected subject digest: %+v", decoded.Subject[0].Digest)
	}
	if decoded.PredicateType != PredicateType {
		t.Errorf("predicate type = %q, want %q", decoded.PredicateType, PredicateType)
	}
	if decoded.Predicate.Summary.Unpinned != 1 {
		t.Errorf("unpinned count = %d, want 1", decoded.Predicate.Summary.Unpinned)
	}
	if len(decoded.Predicate.References) != 1 || decoded.Predicate.References[0].Owner != "actions" {
		t.Errorf("unexpected references: %+v", decoded.Predicate.References)
	}
}

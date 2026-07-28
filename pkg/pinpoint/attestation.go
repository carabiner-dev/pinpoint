// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/go-git/go-git/v5"
	gointoto "github.com/in-toto/attestation/go/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
)

// Statement wraps the report predicate in an in-toto statement attesting to
// the received subjects. The updates are the versions available for the
// references, nil when they were not looked up.
func (r *Report) Statement(updates *Updates, subjects ...*gointoto.ResourceDescriptor) (*gointoto.Statement, error) {
	data, err := json.Marshal(r.Predicate(updates))
	if err != nil {
		return nil, fmt.Errorf("marshaling predicate: %w", err)
	}

	predicate := &structpb.Struct{}
	if err := protojson.Unmarshal(data, predicate); err != nil {
		return nil, fmt.Errorf("building predicate structure: %w", err)
	}

	return &gointoto.Statement{
		Type:          gointoto.StatementTypeUri,
		Subject:       subjects,
		PredicateType: PredicateType,
		Predicate:     predicate,
	}, nil
}

// MarshalStatement renders an in-toto statement as indented JSON.
func MarshalStatement(statement *gointoto.Statement) ([]byte, error) {
	data, err := protojson.Marshal(statement)
	if err != nil {
		return nil, fmt.Errorf("marshaling statement: %w", err)
	}

	// protojson randomizes the whitespace of its output, reindent it to
	// get a stable rendering of the statement.
	var indented bytes.Buffer
	if err := json.Indent(&indented, data, "", "  "); err != nil {
		return nil, fmt.Errorf("indenting statement: %w", err)
	}
	indented.WriteString("\n")
	return indented.Bytes(), nil
}

// githubHost is the only forge pinpoint knows how to ask about forks.
const githubHost = "github.com"

// SubjectFromRepository builds the in-toto subject describing the repository
// at path (or the first one found walking up from it) at the commit it
// currently has checked out.
//
// Scans commonly run in a fork, which is not the repository the code belongs
// to. When a resolver is received, the subject names the project the fork was
// created from and records the fork it was scanned in as an annotation. Pass
// a nil resolver to name the repository as the local remote has it.
func SubjectFromRepository(ctx context.Context, path string, resolver SourceResolver) (*gointoto.ResourceDescriptor, error) {
	repo, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, fmt.Errorf("opening git repository: %w", err)
	}

	head, err := repo.Head()
	if err != nil {
		return nil, fmt.Errorf("reading repository HEAD: %w", err)
	}
	commit := head.Hash().String()

	subject := &gointoto.ResourceDescriptor{
		Digest: map[string]string{"gitCommit": commit},
	}

	// The repository location comes from the origin remote. Repositories
	// without one are still attestable, they just get a subject that is
	// identified by its commit alone.
	scanned, err := originLocation(repo)
	if err != nil {
		return subject, nil //nolint:nilerr // an unlocatable repo is not an error
	}

	location := sourceLocation(ctx, resolver, scanned)
	subject.Name = fmt.Sprintf("%s@%s", location, commit)
	subject.Uri = repositoryURI(location, commit)

	// Note the fork the scan ran in, the subject no longer names it.
	if location != scanned {
		annotations, err := structpb.NewStruct(map[string]any{
			"fork": repositoryURI(scanned, commit),
		})
		if err == nil {
			subject.Annotations = annotations
		}
	}

	return subject, nil
}

// sourceLocation returns the location of the repository a fork was created
// from. Locations outside GitHub, and lookups that don't answer, are returned
// unchanged: naming the repository we scanned beats failing to attest it.
func sourceLocation(ctx context.Context, resolver SourceResolver, location string) string {
	if resolver == nil {
		return location
	}

	host, repository, ok := strings.Cut(location, "/")
	if !ok || host != githubHost {
		return location
	}

	source, err := resolver.SourceRepository(ctx, repository)
	if err != nil || source == "" {
		return location
	}
	return host + "/" + source
}

// repositoryURI returns the VCS locator of a repository at a commit.
func repositoryURI(location, commit string) string {
	return fmt.Sprintf("git+https://%s@%s", location, commit)
}

// originLocation returns the hostname and path of a repository's origin
// remote, for example github.com/carabiner-dev/pinpoint.
func originLocation(repo *git.Repository) (string, error) {
	remote, err := repo.Remote(git.DefaultRemoteName)
	if err != nil {
		return "", fmt.Errorf("reading origin remote: %w", err)
	}

	urls := remote.Config().URLs
	if len(urls) == 0 {
		return "", errors.New("origin remote has no URLs")
	}
	return remoteLocation(urls[0])
}

// remoteLocation normalizes a git remote URL to its hostname and repository
// path, dropping the transport and the .git suffix.
func remoteLocation(remoteURL string) (string, error) {
	// SCP style SSH remotes (git@host:org/repo.git) are not URLs.
	if !strings.Contains(remoteURL, "://") {
		_, hostAndPath, ok := strings.Cut(remoteURL, "@")
		if !ok {
			return "", fmt.Errorf("unable to parse remote %q", remoteURL)
		}
		host, path, ok := strings.Cut(hostAndPath, ":")
		if !ok {
			return "", fmt.Errorf("unable to parse remote %q", remoteURL)
		}
		return host + "/" + strings.TrimSuffix(path, ".git"), nil
	}

	parsed, err := url.Parse(remoteURL)
	if err != nil {
		return "", fmt.Errorf("parsing remote URL: %w", err)
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("remote %q has no hostname", remoteURL)
	}

	path := strings.TrimSuffix(strings.Trim(parsed.Path, "/"), ".git")
	if path == "" {
		return "", fmt.Errorf("remote %q has no repository path", remoteURL)
	}
	return parsed.Hostname() + "/" + path, nil
}

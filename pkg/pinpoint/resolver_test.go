// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// stubCaller answers API calls from a map of path to response body. Paths
// with no entry get a 404. Lookups run in parallel, so the call log is
// guarded.
type stubCaller struct {
	responses map[string]string

	mtx   sync.Mutex
	calls []string
}

func (s *stubCaller) Call(_ context.Context, _, path string, _ io.Reader) (*http.Response, error) {
	s.mtx.Lock()
	s.calls = append(s.calls, path)
	s.mtx.Unlock()

	body, ok := s.responses[path]
	if !ok {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Status:     "404 Not Found",
			Body:       io.NopCloser(strings.NewReader(`{"message":"Not Found"}`)),
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

// callCount returns how many calls the stub has answered.
func (s *stubCaller) callCount() int {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	return len(s.calls)
}

// callLog returns a copy of the paths the stub was asked for.
func (s *stubCaller) callLog() []string {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	return append([]string(nil), s.calls...)
}

func TestLatestRelease(t *testing.T) {
	t.Parallel()
	caller := &stubCaller{
		responses: map[string]string{
			"repos/actions/checkout/releases/latest":   `{"tag_name": "v5.0.0"}`,
			"repos/actions/checkout/commits/v5.0.0":    `{"sha": "08c6903cd8c0fde910a37f88322edcfb5dd907a8"}`,
			"repos/example/tagged/tags?per_page=100":   `[{"name": "v1.2.0"}, {"name": "v1.10.0"}, {"name": "nightly"}, {"name": "v2.0.0-rc1"}]`,
			"repos/example/tagged/commits/v1.10.0":     `{"sha": "11bd71901bbe5b1630ceea73d27597364c9af683"}`,
			"repos/example/untagged/tags?per_page=100": `[]`,
		},
	}
	resolver := NewGitHubResolverWithClient(caller)

	release, err := resolver.LatestRelease(t.Context(), "actions/checkout")
	if err != nil {
		t.Fatalf("resolving release: %v", err)
	}
	if release.Tag != "v5.0.0" || release.Commit != "08c6903cd8c0fde910a37f88322edcfb5dd907a8" {
		t.Errorf("unexpected release: %+v", release)
	}

	// A second lookup must be served from the cache.
	calls := caller.callCount()
	if _, err := resolver.LatestRelease(t.Context(), "actions/checkout"); err != nil {
		t.Fatalf("resolving cached release: %v", err)
	}
	if caller.callCount() != calls {
		t.Errorf("cached lookup called the API: %+v", caller.callLog()[calls:])
	}

	// Repositories with no releases fall back to their highest semver tag.
	release, err = resolver.LatestRelease(t.Context(), "example/tagged")
	if err != nil {
		t.Fatalf("resolving tagged repository: %v", err)
	}
	if release.Tag != "v1.10.0" {
		t.Errorf("tag = %q, want v1.10.0", release.Tag)
	}

	// Repositories with neither releases nor tags cannot be pinned.
	if _, err := resolver.LatestRelease(t.Context(), "example/untagged"); !errors.Is(err, ErrNoReleases) {
		t.Errorf("resolving a repository with no versions returned %v, want ErrNoReleases", err)
	}

	if _, err := resolver.LatestRelease(t.Context(), ""); err == nil {
		t.Error("expected an error resolving an empty repository")
	}
}

func TestResolveVersion(t *testing.T) {
	t.Parallel()
	tags := `[
		{"name": "v4.2.2", "commit": {"sha": "ea165f8d65b6e75b540449e92b4886f43607fa02"}},
		{"name": "v4", "commit": {"sha": "ea165f8d65b6e75b540449e92b4886f43607fa02"}},
		{"name": "v4.2.1", "commit": {"sha": "1111111111111111111111111111111111111111"}}
	]`
	caller := &stubCaller{
		responses: map[string]string{
			"repos/actions/checkout/commits/v4":        `{"sha": "ea165f8d65b6e75b540449e92b4886f43607fa02"}`,
			"repos/actions/checkout/commits/main":      `{"sha": "2222222222222222222222222222222222222222"}`,
			"repos/actions/checkout/commits/HEAD":      `{"sha": "2222222222222222222222222222222222222222"}`,
			"repos/actions/checkout/tags?per_page=100": tags,
		},
	}
	resolver := NewGitHubResolverWithClient(caller)

	// A major version tag is recorded as the patch release naming its commit.
	release, err := resolver.ResolveVersion(t.Context(), "actions/checkout", "v4")
	if err != nil {
		t.Fatalf("resolving version: %v", err)
	}
	if release.Tag != "v4.2.2" || release.Commit != "ea165f8d65b6e75b540449e92b4886f43607fa02" {
		t.Errorf("unexpected release: %+v", release)
	}

	// A second lookup of the same version is served from the cache.
	calls := caller.callCount()
	if _, err := resolver.ResolveVersion(t.Context(), "actions/checkout", "v4"); err != nil {
		t.Fatalf("resolving cached version: %v", err)
	}
	if caller.callCount() != calls {
		t.Errorf("cached lookup called the API: %+v", caller.callLog()[calls:])
	}

	// Branches keep their name, no tag points at their commit.
	release, err = resolver.ResolveVersion(t.Context(), "actions/checkout", "main")
	if err != nil {
		t.Fatalf("resolving branch: %v", err)
	}
	if release.Tag != "main" || release.Commit != "2222222222222222222222222222222222222222" {
		t.Errorf("unexpected release: %+v", release)
	}

	// References with no version track the default branch.
	release, err = resolver.ResolveVersion(t.Context(), "actions/checkout", "")
	if err != nil {
		t.Fatalf("resolving default branch: %v", err)
	}
	if release.Tag != "" || release.Commit != "2222222222222222222222222222222222222222" {
		t.Errorf("unexpected release: %+v", release)
	}

	if _, err := resolver.ResolveVersion(t.Context(), "actions/checkout", "nope"); err == nil {
		t.Error("expected an error resolving an unknown version")
	}
}

func TestEnvTokenReader(t *testing.T) {
	// Not parallel, the subtests set environment variables.
	for _, tc := range []struct {
		name     string
		env      map[string]string
		expected string
		mustErr  bool
	}{
		{
			name:     "the first variable wins",
			env:      map[string]string{"GITHUB_TOKEN": "from-github", "GH_TOKEN": "from-gh"},
			expected: "from-github",
		},
		{
			name:     "falls back to the next one",
			env:      map[string]string{"GITHUB_TOKEN": "", "GH_TOKEN": "from-gh"},
			expected: "from-gh",
		},
		{
			name:    "nothing set",
			env:     map[string]string{"GITHUB_TOKEN": "", "GH_TOKEN": ""},
			mustErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for name, value := range tc.env {
				t.Setenv(name, value)
			}

			token, err := (&EnvTokenReader{VarNames: tokenEnvVars}).ReadToken()
			if tc.mustErr {
				if err == nil {
					t.Fatalf("expected an error, got token %q", token)
				}
				return
			}
			if err != nil {
				t.Fatalf("reading token: %v", err)
			}
			if token != tc.expected {
				t.Errorf("token = %q, want %q", token, tc.expected)
			}
		})
	}
}

func TestNewGitHubResolver(t *testing.T) {
	// Not parallel, it sets the environment tokens.
	for _, token := range []string{"a-token", ""} {
		t.Setenv("GITHUB_TOKEN", token)
		t.Setenv("GH_TOKEN", "")

		// A resolver builds with a token in the environment and without
		// one: calls are made anonymously when there is nothing to
		// authenticate with.
		resolver, err := NewGitHubResolver()
		if err != nil {
			t.Fatalf("creating resolver: %v", err)
		}
		if resolver == nil {
			t.Fatal("creating resolver returned nothing")
		}
	}
}

func TestSourceRepository(t *testing.T) {
	t.Parallel()
	caller := &stubCaller{
		responses: map[string]string{
			// A fork of a fork resolves to the root of the network.
			"repos/puerco/kubernetes": `{
				"full_name": "puerco/kubernetes", "fork": true,
				"parent": {"full_name": "someone/kubernetes"},
				"source": {"full_name": "kubernetes/kubernetes"}
			}`,
			// Older forks only carry a parent.
			"repos/puerco/orphan": `{
				"full_name": "puerco/orphan", "fork": true,
				"parent": {"full_name": "upstream/orphan"}
			}`,
			// Repositories that are not forks resolve to themselves, with
			// the name the forge has for them now.
			"repos/carabiner-dev/pinpoint": `{"full_name": "carabiner-dev/pinpoint", "fork": false}`,
			"repos/carabiner-dev/old-name": `{"full_name": "carabiner-dev/new-name", "fork": false}`,
		},
	}
	resolver := NewGitHubResolverWithClient(caller)

	for _, tc := range []struct {
		repository string
		expected   string
	}{
		{"puerco/kubernetes", "kubernetes/kubernetes"},
		{"puerco/orphan", "upstream/orphan"},
		{"carabiner-dev/pinpoint", "carabiner-dev/pinpoint"},
		{"carabiner-dev/old-name", "carabiner-dev/new-name"},
	} {
		t.Run(tc.repository, func(t *testing.T) {
			t.Parallel()
			source, err := resolver.SourceRepository(t.Context(), tc.repository)
			if err != nil {
				t.Fatalf("resolving source: %v", err)
			}
			if source != tc.expected {
				t.Errorf("SourceRepository(%q) = %q, want %q", tc.repository, source, tc.expected)
			}
		})
	}

	t.Run("cached", func(t *testing.T) {
		if _, err := resolver.SourceRepository(t.Context(), "puerco/kubernetes"); err != nil {
			t.Fatalf("warming the cache: %v", err)
		}
		calls := caller.callCount()
		if _, err := resolver.SourceRepository(t.Context(), "puerco/kubernetes"); err != nil {
			t.Fatalf("resolving cached source: %v", err)
		}
		if caller.callCount() != calls {
			t.Errorf("cached lookup called the API: %+v", caller.callLog()[calls:])
		}
	})

	t.Run("unknown repository", func(t *testing.T) {
		t.Parallel()
		if _, err := resolver.SourceRepository(t.Context(), "nope/nope"); err == nil {
			t.Error("expected an error resolving an unknown repository")
		}
	})

	t.Run("no repository", func(t *testing.T) {
		t.Parallel()
		if _, err := resolver.SourceRepository(t.Context(), ""); err == nil {
			t.Error("expected an error resolving an empty repository")
		}
	})
}

func TestMoreSpecific(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		candidate string
		current   string
		expected  bool
	}{
		{"v4.2.2", "v4", true},
		{"v4", "v4.2.2", false},
		{"v4.2.2", "v4.2.1", true},
		{"v4.2.1", "v4.2.2", false},
		{"v4.2.2", "latest", true},
		{"latest", "v4.2.2", false},
	} {
		t.Run(tc.candidate+" over "+tc.current, func(t *testing.T) {
			t.Parallel()
			if got := moreSpecific(tc.candidate, tc.current); got != tc.expected {
				t.Errorf("moreSpecific(%q, %q) = %v, want %v", tc.candidate, tc.current, got, tc.expected)
			}
		})
	}
}

func TestHighestSemver(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		tags     []string
		expected string
	}{
		{"picks the highest", []string{"v1.2.0", "v1.10.0", "v1.9.9"}, "v1.10.0"},
		{"ignores non versions", []string{"latest", "nightly", "v0.1.0"}, "v0.1.0"},
		{"prefers releases over prereleases", []string{"v2.0.0-rc1", "v1.9.0"}, "v1.9.0"},
		{"prerelease when alone", []string{"v2.0.0-rc1"}, "v2.0.0-rc1"},
		{"keeps the tag as written", []string{"1.4.0"}, "1.4.0"},
		{"nothing to pick", []string{"main", "latest"}, ""},
		{"empty list", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := highestSemver(tc.tags); got != tc.expected {
				t.Errorf("highestSemver(%+v) = %q, want %q", tc.tags, got, tc.expected)
			}
		})
	}
}

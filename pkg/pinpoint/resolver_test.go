// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// stubCaller answers API calls from a map of path to response body. Paths
// with no entry get a 404.
type stubCaller struct {
	responses map[string]string
	calls     []string
}

func (s *stubCaller) Call(_ context.Context, _, path string, _ io.Reader) (*http.Response, error) {
	s.calls = append(s.calls, path)

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
	calls := len(caller.calls)
	if _, err := resolver.LatestRelease(t.Context(), "actions/checkout"); err != nil {
		t.Fatalf("resolving cached release: %v", err)
	}
	if len(caller.calls) != calls {
		t.Errorf("cached lookup called the API: %+v", caller.calls[calls:])
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

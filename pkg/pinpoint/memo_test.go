// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestMemoDo(t *testing.T) {
	t.Parallel()

	var (
		m     memo[string]
		calls atomic.Int64
		start = make(chan struct{})
	)

	// Fifty callers race for two keys. Each key is computed once and the
	// callers waiting on it get that answer.
	var wg sync.WaitGroup
	results := make([]string, 50)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start

			key := []string{"one", "two"}[i%2]
			value, err := m.Do(key, func() (string, error) {
				calls.Add(1)
				return "value of " + key, nil
			})
			if err != nil {
				t.Errorf("Do(%q): %v", key, err)
			}
			results[i] = value
		}(i)
	}
	close(start)
	wg.Wait()

	if got := calls.Load(); got != 2 {
		t.Errorf("the function ran %d times, want 2", got)
	}
	for i, got := range results {
		want := "value of " + []string{"one", "two"}[i%2]
		if got != want {
			t.Errorf("result %d = %q, want %q", i, got, want)
		}
	}
}

func TestMemoDoErrors(t *testing.T) {
	t.Parallel()

	var (
		m     memo[int]
		calls int
	)

	failing := func() (int, error) {
		calls++
		return 0, errors.New("boom")
	}

	// A failure is handed to the caller and not remembered: the next call
	// gets to try again and can succeed.
	for range 2 {
		if _, err := m.Do("key", failing); err == nil {
			t.Fatal("expected the error to reach the caller")
		}
	}
	if calls != 2 {
		t.Errorf("the failing function ran %d times, want 2", calls)
	}

	value, err := m.Do("key", func() (int, error) { return 42, nil })
	if err != nil || value != 42 {
		t.Fatalf("Do() = %v, %v, want 42, nil", value, err)
	}

	// The value that worked is remembered.
	value, err = m.Do("key", func() (int, error) { return 0, errors.New("not called") })
	if err != nil || value != 42 {
		t.Errorf("Do() = %v, %v, want the remembered 42", value, err)
	}
}

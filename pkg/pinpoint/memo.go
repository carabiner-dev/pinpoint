// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package pinpoint

import "sync"

// memo remembers the result of an expensive call by key and collapses the
// concurrent callers asking for the same key into a single execution: when
// fifty workflow entries want the same repository, only one of them talks to
// the forge and the rest wait for that answer.
//
// The zero value is ready to use.
type memo[T any] struct {
	mtx     sync.Mutex
	entries map[string]*memoEntry[T]
}

// memoEntry is a call in flight or already finished. done is closed when the
// value and the error are final.
type memoEntry[T any] struct {
	done  chan struct{}
	value T
	err   error
}

// Do returns the result of fn for a key, calling it only the first time. The
// callers that arrive while a call is running wait for it and share its
// result. Failures are not remembered: a later call gets to try again.
func (m *memo[T]) Do(key string, fn func() (T, error)) (T, error) {
	m.mtx.Lock()
	if m.entries == nil {
		m.entries = map[string]*memoEntry[T]{}
	}
	if entry, ok := m.entries[key]; ok {
		m.mtx.Unlock()
		<-entry.done
		return entry.value, entry.err
	}

	entry := &memoEntry[T]{done: make(chan struct{})}
	m.entries[key] = entry
	m.mtx.Unlock()

	entry.value, entry.err = fn()
	close(entry.done)

	if entry.err != nil {
		m.mtx.Lock()
		delete(m.entries, key)
		m.mtx.Unlock()
	}

	return entry.value, entry.err
}

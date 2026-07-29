// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestConfirmPin(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		answer    string
		updates   int
		assumeYes bool
		expected  bool
		asks      bool
		mustErr   bool
	}{
		{name: "y", answer: "y\n", updates: 3, expected: true, asks: true},
		{name: "yes", answer: "yes\n", updates: 3, expected: true, asks: true},
		{name: "uppercase", answer: "Y\n", updates: 3, expected: true, asks: true},
		{name: "padded", answer: "  y  \n", updates: 3, expected: true, asks: true},
		{name: "no answer at all", answer: "y", updates: 3, expected: true, asks: true},
		{name: "n", answer: "n\n", updates: 3, expected: false, asks: true},
		// Anything that is not a yes leaves the workflows alone, an empty
		// answer included: the question defaults to no.
		{name: "empty", answer: "\n", updates: 3, expected: false, asks: true},
		{name: "gibberish", answer: "maybe\n", updates: 3, expected: false, asks: true},
		// Nothing to write, nothing to ask.
		{name: "no updates", answer: "", updates: 0, expected: true},
		{name: "assume yes", answer: "", updates: 3, assumeYes: true, expected: true},
		// A closed input is not an answer, it is a caller that cannot be
		// asked and has to say so with the flag.
		{name: "no input", answer: "", updates: 3, mustErr: true, asks: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			cmd := &cobra.Command{}
			cmd.SetOut(&out)
			cmd.SetIn(strings.NewReader(tc.answer))

			got, err := confirmPin(cmd, tc.updates, tc.assumeYes)
			if tc.mustErr {
				if err == nil {
					t.Fatalf("expected an error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("confirmPin(): %v", err)
			}
			if got != tc.expected {
				t.Errorf("confirmPin() = %v, want %v", got, tc.expected)
			}

			question := strings.Contains(out.String(), "Pin 3 action(s) to hashes? (y/N)")
			if question != tc.asks {
				t.Errorf("asked = %v, want %v (output %q)", question, tc.asks, out.String())
			}
		})
	}
}

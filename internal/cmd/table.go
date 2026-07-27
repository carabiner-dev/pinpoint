// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"github.com/carabiner-dev/termtable"
)

// maxNarrowWidth caps the width of the columns pinned to their content so
// that an unusually long value does not starve the rest of the table.
const maxNarrowWidth = 24

// narrowWidth returns the width needed to render a header and its values in
// full, capped at maxNarrowWidth. It is used to pin the columns holding short
// values so that the whole leftover budget goes to the flexible ones.
func narrowWidth(header string, values ...string) int {
	width := termtable.DisplayWidth(header)
	for _, value := range values {
		width = max(width, termtable.DisplayWidth(value))
	}
	return min(width, maxNarrowWidth)
}

// styleFileColumn configures the column holding the path of the workflow or
// action definition. Paths that overflow are trimmed from the left as the
// tail (the file name) is the part that identifies them.
func styleFileColumn(t *termtable.Table, index int) {
	t.Column(index).Style("white-space: nowrap; text-overflow-position: start; flex: 2")
}

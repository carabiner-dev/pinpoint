// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"io"

	"github.com/carabiner-dev/termtable"
)

// maxNarrowWidth caps the width of the columns pinned to their content so
// that an unusually long value does not starve the rest of the table.
const maxNarrowWidth = 24

// newTable creates the table pinpoint reports its findings in: no frame and
// no rules between the rows, the only line is the one under the headers.
func newTable() *termtable.Table {
	return termtable.NewTable(termtable.WithTableStyle("border: none"))
}

// addHeader adds the titles row to a table. Headers are set apart from the
// findings by the rule underneath them and by being the only bright text in
// the output.
func addHeader(t *termtable.Table, titles ...string) {
	row := t.AddHeader(
		termtable.WithRowBorderBottom(termtable.BorderEdgeSolid),
		termtable.WithRowStyle("color: bright-white; font-weight: bold"),
	)
	for _, title := range titles {
		row.AddCell(termtable.WithContent(title))
	}
}

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

// printTable writes a table out with a blank line above and below it, so it
// stands apart from the headings and summaries around it.
func printTable(w io.Writer, t *termtable.Table) error {
	if _, err := fmt.Fprintf(w, "\n%s\n", t.String()); err != nil {
		return fmt.Errorf("writing table: %w", err)
	}
	return nil
}

// styleFileColumn configures the first column, the one holding the path of
// the workflow or action definition. Paths that overflow are trimmed from the
// left as the tail (the file name) is the part that identifies them.
func styleFileColumn(t *termtable.Table) {
	t.Column(0).Style("white-space: nowrap; text-overflow-position: start; flex: 2")
}

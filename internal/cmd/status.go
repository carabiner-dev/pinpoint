// SPDX-FileCopyrightText: Copyright 2026 Carabiner Systems, Inc
// SPDX-License-Identifier: Apache-2.0

package cmd

import (
	"fmt"
	"io"
	"strconv"

	"github.com/carabiner-dev/termtable"
	"github.com/protobom/protobom/pkg/formats"
	"github.com/protobom/protobom/pkg/writer"
	"github.com/spf13/cobra"

	"github.com/carabiner-dev/pinpoint/options"
	"github.com/carabiner-dev/pinpoint/pkg/pinpoint"
)

func addStatus(parent *cobra.Command) {
	opts := &options.Status{}

	statusCmd := &cobra.Command{
		Use:   "status [path]",
		Short: "show the pinning status of every action reference",
		Long: appName + ` status: show the pinning status of every action reference

The status subcommand scans the GitHub Actions workflows and action
definitions (action.yml files) found in a directory (the current one by
default) and lists every external action reference: those pointing at
other repositories or at container images. Local actions ride along the
repository and have nothing to pin, so they are left out.

Each reference is shown with the version it is using (for pinned
entries, the version named by the comment trailing the hash) and two
marks: whether it is pinned to an immutable version (a commit hash, or
an image digest for container actions) and whether it is using the
latest release of its action. References pinpoint cannot look up,
container images among them, show a question mark in the update column.

With --offline pinpoint makes no calls to the forge: the update column
is dropped and the scan only reports which references are pinned.

Using --format, the results are written as an SBOM instead of the
table: --format=spdx renders SPDX 2.3 and --format=cyclonedx renders
CycloneDX 1.7, both as JSON on standard output. The document models the
repository at its current commit with each action version as a package
hanging from it, connected to the workflow files that use it. Actions
whose pinned hash matches a tagged release carry the tag as their
version; every reference is verified against the forge, so SBOM runs
need API access and cannot be combined with --offline.

Status is a report, not a gate: the command exits cleanly whatever it
finds. Use check to fail a run over unpinned references.

Versions are read from the GitHub API through the carabiner GitHub
client. Export a token in GITHUB_TOKEN or GH_TOKEN to authenticate the
calls: anonymous calls work for public repositories but are rate limited
to a handful of scans per hour.
`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Path = "."
			if len(args) > 0 {
				opts.Path = args[0]
			}
			if err := opts.Validate(); err != nil {
				return err
			}

			if opts.Format != "" {
				return writeSBOM(cmd, opts)
			}

			// Only hand over a resolver we actually have: a nil pointer
			// stored in an interface is not a nil interface.
			var resolver pinpoint.Resolver
			if !opts.Offline {
				gh, err := newResolver(cmd)
				if err != nil {
					return err
				}
				if gh != nil {
					resolver = gh
				}
			}

			status, err := pinpoint.ScanStatus(cmd.Context(), resolver, opts.Path)
			if err != nil {
				return err
			}

			// References that got no answer render as unknown versions. Say
			// why once, a rejected token turns every row into a question mark.
			if unresolved := status.Unresolved; len(unresolved) > 0 {
				if _, err := fmt.Fprintf(
					cmd.ErrOrStderr(), "Could not look up %d reference(s): %v\n",
					len(unresolved), firstError(unresolved),
				); err != nil {
					return fmt.Errorf("writing warning: %w", err)
				}
			}

			if len(status.References) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "No external action references found.")
				return err
			}

			return writeStatusTable(cmd.OutOrStdout(), status)
		},
	}
	opts.AddFlags(statusCmd)
	parent.AddCommand(statusCmd)
}

// sbomFormats maps the format flag values to the serializations pinpoint
// writes: the most current versions protobom can render.
var sbomFormats = map[string]formats.Format{
	options.FormatSPDX:      formats.SPDX23JSON,
	options.FormatCycloneDX: formats.CDX17JSON,
}

// writeSBOM renders the scan results as an SBOM on standard output. The
// versions in the document are verified against the forge, so unlike the
// table a resolver is required.
func writeSBOM(cmd *cobra.Command, opts *options.Status) error {
	resolver, err := pinpoint.NewGitHubResolver()
	if err != nil {
		return err
	}

	doc, err := pinpoint.BuildSBOM(cmd.Context(), resolver, opts.Path)
	if err != nil {
		return err
	}

	return writer.New().WriteStreamWithOptions(
		doc, cmd.OutOrStdout(), &writer.Options{Format: sbomFormats[opts.Format]},
	)
}

// mark renders a status cell: a green tick when ok, a red cross otherwise.
func mark(ok bool) []termtable.CellOption {
	if ok {
		return []termtable.CellOption{
			termtable.WithContent("✓"), termtable.WithTextColor("green"),
		}
	}
	return []termtable.CellOption{
		termtable.WithContent("✗"), termtable.WithTextColor("red"),
	}
}

// updateMark renders the cell saying whether a reference uses the latest
// release of its action, with a mark for those pinpoint could not look up.
func updateMark(entry *pinpoint.ReferenceStatus) []termtable.CellOption {
	if !entry.Checked {
		return []termtable.CellOption{termtable.WithContent(unknownVersion)}
	}
	return mark(!entry.Outdated)
}

// writeStatusTable renders every external reference with its pinning status
// and, when the versions were looked up, whether it is on the latest release.
func writeStatusTable(w io.Writer, status *pinpoint.Status) error {
	titles := []string{"Workflow", "Line", "Action", "Version", "Pinned"}
	if status.Checked {
		titles = append(titles, "Up to date")
	}

	t := newTable()
	addHeader(t, titles...)

	// The update column is only created when the versions were looked up,
	// asking for it offline would add a column of question marks.
	styleFileColumn(t)
	t.Column(1).SetAlign(termtable.AlignRight)
	t.Column(2).Style("white-space: nowrap; flex: 3")
	t.Column(3).Style("white-space: nowrap")
	t.Column(4).SetAlign(termtable.AlignCenter)
	if status.Checked {
		t.Column(5).SetAlign(termtable.AlignCenter)
	}

	lines := make([]string, 0, len(status.References))
	versions := make([]string, 0, len(status.References))
	for i := range status.References {
		entry := &status.References[i]
		line := strconv.Itoa(entry.Reference.Line)
		version := pinnedVersion(&entry.Reference)
		lines = append(lines, line)
		versions = append(versions, version)

		row := t.AddRow()
		row.AddCell(termtable.WithContent(entry.Reference.Workflow))
		row.AddCell(termtable.WithContent(line))
		row.AddCell(termtable.WithContent(entry.Reference.Uses))
		row.AddCell(termtable.WithContent(version))
		row.AddCell(mark(entry.Pinned)...)
		if status.Checked {
			row.AddCell(updateMark(entry)...)
		}
	}

	t.Column(1).SetWidth(narrowWidth("Line", lines...))
	t.Column(3).SetWidth(narrowWidth("Version", versions...))
	t.Column(4).SetWidth(narrowWidth("Pinned"))
	if status.Checked {
		t.Column(5).SetWidth(narrowWidth("Up to date"))
	}

	return printTable(w, t)
}

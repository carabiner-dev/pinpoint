# 📍Pinpoint

Pinpoint is a GitHub Actions pinning manager. It scans the workflows of a
repository and reports the action references that are not pinned to a commit
hash, pins them in place, and keeps the pinned ones up to date. The same scan
can be exported as an in-toto attestation or as an SBOM, naming every action
a repository runs.

Pinning actions to hashes is what keeps a workflow running the code it was
reviewed with: tags and branches can be moved, commits cannot. A pinned
reference keeps the version it points to in a trailing comment, which is the
convention pinpoint reads and writes:

```yaml
- uses: actions/checkout@08c6903cd8c0fde910a37f88322edcfb5dd907a8 # v5.0.0
```

## Installing

### Released binaries

Once released, binaries for the common platforms are published on the
[releases page](https://github.com/carabiner-dev/pinpoint/releases). Each
release ships with its SLSA provenance attestation and an SBOM, so a
download can be verified before it runs.

### With go install

```
go install github.com/carabiner-dev/pinpoint@latest
```

### From source

```
git clone https://github.com/carabiner-dev/pinpoint
cd pinpoint
go build .
```

## Authenticating

Pinpoint reads versions from the GitHub API. Export a token in
`GITHUB_TOKEN` or `GH_TOKEN` to authenticate the calls: anonymous calls work
for public repositories but are rate limited to a handful of scans per hour.

```
export GITHUB_TOKEN=$(gh auth token)
```

Commands that need no version data (`check --offline`, `status --offline`)
make no API calls at all.

## Checking a repository

`pinpoint check` scans the workflows of a directory (the current one by
default) and lists the references that are not pinned, along with the
version each one would be pinned to. It also reports the pinned references
that have a newer release, covering both halves of keeping actions under
control: pinning them and keeping them current.

```
$ pinpoint check

Action references not pinned to a commit hash:

  Workflow                    Line   Job       Action                Pin to
 ──────────────────────────────────────────────────────────────────────────────────────
  .github/workflows/ci.yaml     14   build     actions/setup-go@v6   v6.5.0 (924ae3a…)

Pinned references with a newer release available:

  Workflow                    Lines   Action             Using    Latest
 ──────────────────────────────────────────────────────────────────────────────
  .github/workflows/ci.yaml      12   actions/checkout   v5.0.0   v7.0.1 (3d3c42e…)

Error: found 1 action reference(s) not pinned to a hash
```

The command exits with an error when unpinned references are found, so it
can gate a CI job. Outdated references are reported but do not fail the run.

Two flags control how much pinpoint asks the forge: `--update=false` stops
the search for newer releases, and `--offline` makes no calls at all, only
reporting which references are pinned.

## Pinning the references

`pinpoint pin` rewrites the unpinned references in place, pinning each entry
to the commit its version points at and recording the version in the
trailing comment. The changes are listed first and confirmed with a
question, so a run can be inspected before it touches the workflows:

```
$ pinpoint pin

  File                        Line   Action                Pin to
 ─────────────────────────────────────────────────────────────────────────────
  .github/workflows/ci.yaml     14   actions/setup-go@v6   v6.5.0 (924ae3a…)

Pin 1 action(s) to hashes? (y/N) y
Pinned 1 action reference(s) in 1 file(s).
```

Use `--yes` to skip the question, which is also what unattended runs need:
with no one to answer, pinpoint stops instead of guessing.

Two flags widen what a run does:

- `--update` pins the references to the latest release of each action
  instead of the version they are using, acting on what check reports as
  updatable.
- `--all` looks at every reference instead of only the unpinned ones, and
  also scans the action definitions (`action.yml` files) in the repository.
  Combined with `--update` it moves the whole repository to the newest
  releases.

References that cannot be pinned to a repository commit (local actions and
those run from container images) are left untouched.

## Seeing the whole picture

`pinpoint status` lists every external action reference: those pointing at
other repositories or at container images, in workflows and in action
definitions alike. Each row shows the version in use and whether the entry
is pinned and up to date:

```
$ pinpoint status

  Workflow                    Line   Action                     Version    Pinned   Up to date
 ─────────────────────────────────────────────────────────────────────────────────────────────
  .github/workflows/ci.yaml     12   actions/checkout@08c690…   v5.0.0       ✓          ✗
  .github/workflows/ci.yaml     14   actions/setup-go@v6        v6           ✗          ✗
  .github/workflows/ci.yaml     19   docker://alpine@sha256:…   sha256:…     ✓          ?
  .github/workflows/ci.yaml     21   docker://alpine:3.22       ?            ✗          ?
```

References pinpoint cannot look up, container images among them, show a
question mark in the update column. With `--offline` the column is dropped
and the scan only reports which references are pinned. Status is a report,
not a gate: the command exits cleanly whatever it finds.

### Exporting an SBOM

With `--format`, status writes the scan as an SBOM on standard output
instead of the table: `--format=spdx` renders SPDX 2.3 and
`--format=cyclonedx` renders CycloneDX 1.7, both as JSON.

```
pinpoint status --format=spdx > actions.spdx.json
```

The document models the repository at its current commit as the root of the
graph, with every external action version hanging from it as a development
tool and connected to the workflow files that use it. Actions are identified
by purls in the convention GitHub uses in its own SBOMs
(`pkg:githubactions/actions/checkout@08c6903…`, `pkg:oci/…` for container
actions), carry a VCS locator back to the repository hosting them, and are
versioned with the tag their reference resolves to. Every version is
verified against the forge: a reference that cannot be resolved fails the
export, so an emitted SBOM always carries verified versions.

## Attesting the results

`pinpoint check --attest` writes the full scan results (both the pinned and
the unpinned references) as an unsigned in-toto attestation instead of the
table. The subject is the scanned repository at its current commit; when the
scan runs in a fork, pinpoint names the repository the fork was created from
and notes the fork in the subject annotations.

```
pinpoint check --attest > pinning.intoto.json
```

Attestations are data, not verdicts, so the command exits cleanly when
generating one, leaving the pass/fail decision to whoever evaluates it. The
output can be signed and published with [bnd](https://github.com/carabiner-dev/bnd).

## Using pinpoint as a library

The `pkg/pinpoint` package exposes the same views the command line renders.
`ScanStatus` describes the pinning state of every external action reference
and `BuildSBOM` models the repository as a protobom graph:

```go
import "github.com/carabiner-dev/pinpoint/pkg/pinpoint"

resolver, err := pinpoint.NewGitHubResolver()
if err != nil { /* … */ }

// The pinning state of every external action reference.
status, err := pinpoint.ScanStatus(ctx, resolver, ".")
for _, ref := range status.References {
    fmt.Println(ref.Reference.Uses, ref.Pinned, ref.Outdated)
}

// The same scan as a protobom document, ready for any of its serializers.
doc, err := pinpoint.BuildSBOM(ctx, resolver, ".")
```

The lower level pieces are available too: `Scanner` collects the
references, `CheckUpdates` looks up the versions available for them and
`Updater` computes and applies the pinning plans.

## License

Copyright Carabiner Systems, Inc. Released under the
[Apache 2.0](LICENSE) license.

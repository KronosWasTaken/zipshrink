# zipshrink

A fast, lightweight Go utility to extract `.zip` and `.rar` archives while dynamically shrinking and truncating the archive file in real-time, eliminating the need for 2x disk space.

Pure Go, no external tools required. Single-volume archives only.

> **The source archive is destroyed by default.** Extraction consumes it in place — chunks are truncated as they're read, and the remainder is deleted on success. If a run fails partway, the archive is left partially shrunk and unusable. Back up anything irreplaceable, or pass `-k` to leave the source completely untouched.

---

## Why zipshrink?

Extracting a 100 GB archive typically requires 200 GB of free space (100 GB for the archive + 100 GB for the uncompressed files).

`zipshrink` reads the archive sequentially and periodically truncates processed chunks from the source file, releasing disk sectors back to the OS during extraction.

Each truncation rewrites the remaining archive toward the front, so smaller chunks reclaim space sooner but do proportionally more I/O. The 100MB default is a reasonable balance; very small chunks on a multi-gigabyte archive get slow.

---

## Installation

Prebuilt binaries for linux, macOS and windows (amd64/arm64) are attached to
each [release](../../releases). Releases are signed; verify one with:

```bash
cosign verify-blob \
  --bundle SHA256SUMS.cosign.bundle \
  --certificate-identity-regexp '^https://github\.com/.+/\.github/workflows/release\.yml@' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
```

Or build from source:

```bash
go build -o zipshrink.exe ./cmd/zipshrink
go test ./...
```

---

## Usage

```bash
# Extract and reclaim space in 100MB chunks (auto-deletes archive upon success)
./zipshrink archive.zip
./zipshrink archive.rar

# Custom chunk threshold
./zipshrink -c 512MB archive.zip

# Custom destination folder
./zipshrink -o ./output archive.zip

# Keep source archive intact
./zipshrink -k archive.zip

# Verbose file logging
./zipshrink -v archive.zip
```

### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `-o` | Destination directory | `<archive_name>` |
| `-c` | Consumption threshold before chunk truncation | `100MB` |
| `-k` | Keep source archive intact (disable auto-delete) | `false` |
| `-v` | Verbose per-file extraction log | `false` |

---

## Architecture

```
.
├── cmd/zipshrink/       # CLI entrypoint & flag parsing
├── pkg/
│   ├── streamzip/       # Single-pass streaming ZIP decompressor
│   ├── truncator/       # In-place chunk shifting & truncation engine
│   └── extractor/       # Extraction coordinators (extractor.go: ZIP, rar.go: RAR) with Zip Slip protection
├── go.mod
└── README.md
```

---

## Releases

Versioning is [semver](https://semver.org), automated with
[release-please](https://github.com/googleapis/release-please) and driven by
[Conventional Commits](https://www.conventionalcommits.org) on `main`:

| Commit prefix | Bump |
|---|---|
| `fix:` | patch — `1.0.0` → `1.0.1` |
| `feat:` | minor — `1.0.0` → `1.1.0` |
| `feat!:` or `BREAKING CHANGE:` | major — `1.0.0` → `2.0.0` |
| `chore:`, `ci:`, `docs:`, `test:` | none |

Release-please keeps a single open **"chore: release X.Y.Z"** pull request
with the generated `CHANGELOG.md` and version bump, rewriting it as commits
land. Commits accumulate there, so there is no need to release per commit:
push as many as you like and the highest bump among them wins — a `feat:`
alongside three `fix:` commits yields one minor release, not four. Nothing
ships until the PR is merged, and if only non-bumping commits have landed no
PR is opened at all.

Prefixes must match exactly: `feat:` counts, `feature:` and `Feat:` do not,
and a malformed prefix silently fails to bump.

Merging that PR is the release: it tags `vX.Y.Z`, publishes the GitHub
release, then builds and attaches for linux/macOS/windows on amd64/arm64:

- six `.tar.gz`/`.zip` archives, each containing the binary plus both licences
- `SHA256SUMS` and its keyless cosign signature (`SHA256SUMS.cosign.bundle`)
- an SPDX SBOM (`zipshrink-sbom.spdx.json`)

Tagging by hand (`git tag -a v1.2.3 && git push origin v1.2.3`) produces the
same artifacts, and `workflow_dispatch` on the release workflow can rebuild
assets for a tag that already exists.

### Workflows

| Workflow | Trigger | Does |
|---|---|---|
| `ci.yml` | push, PR, weekly | tests on linux/macOS/windows, `gofmt`/`vet`/tidy, golangci-lint (incl. gosec), actionlint, cross-compile, fuzzing, `govulncheck` |
| `release-please.yml` | push to `main` | maintains the release PR; calls the release workflow once a release is cut |
| `release.yml` | tag, `workflow_call`, `workflow_dispatch` | validates semver, builds, signs, publishes |

---

## License

MIT — see [LICENSE](LICENSE).

RAR support uses [rardecode](https://github.com/nwaples/rardecode) (BSD 2-Clause);
its notice is reproduced in [THIRD_PARTY_LICENSES](THIRD_PARTY_LICENSES) and
ships inside every release archive.

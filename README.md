# zipshrink

A fast, lightweight Go utility to extract `.zip` and `.rar` archives while dynamically shrinking and truncating the archive file in real-time, eliminating the need for 2x disk space.

Pure Go, no external tools required. Single-volume archives only.

> **The source archive is destroyed by default.** Extraction consumes it in place — chunks are truncated as they're read, and the remainder is deleted on success. If a run fails partway, the archive is left partially shrunk and unusable. Back up anything irreplaceable, or pass `-k` to leave the source completely untouched.

---

## Why zipshrink?

Extracting a 100 GB archive typically requires 200 GB of free space (100 GB for the archive + 100 GB for the uncompressed files).

`zipshrink` reads the archive sequentially and releases the consumed prefix
back to the OS as it goes, so the archive shrinks while the output grows and
the two never both occupy full size.

**Peak disk ≈ extracted size + one chunk.** A 15 GB archive extracting to
15 GB needs about 17 GB with `-c 2GB`, against ~30 GB for a conventional
extractor.

## How space is reclaimed

On filesystems that support sparse files (NTFS, ext4, XFS, btrfs) the
consumed prefix is **punched out in place**: the blocks are deallocated
without moving a single byte, and the read position is unaffected. Cost is
O(archive size) no matter how small the chunk is.

Elsewhere it falls back to copying the remainder to the front of the file and
truncating. That is correct but rewrites the tail on every reclaim, so total
I/O is **O(N²/C)** for archive size N and chunk C — a 15 GB archive with the
100 MB default rewrites roughly 1.1 TB. On the fallback path, raise `-c`
substantially for large archives.

| Path | Reclaim cost | Chunk size matters? |
|---|---|---|
| Sparse (NTFS, ext4, XFS, btrfs) | O(N) | Only for reclaim granularity |
| Fallback (everything else) | O(N²/C) | Yes — raise it for big archives |

`-v` reports which reclaim path is in use.

## Getting the best speed

```bash
zipshrink -o /mnt/other-drive/extracted archive.zip
```

Two things matter, in this order:

1. **Extract to a different physical drive than the archive.** Reading and
   writing then stop competing for the same device, which is usually the
   single largest win. On one drive, extraction is limited by the sum of both;
   across two, by the slower of them.
2. **Leave `-c` alone.** It defaults to `auto`, which sizes the reclaim step
   to the archive (1/16th, clamped to 64MB..2GB). Pinning it smaller only adds
   work; pinning it larger only holds back more disk.

| Archive | `auto` chunk |
|---|---|
| under 1 GB | 64 MB (floor) |
| 4 GB | 256 MB |
| 10 GB and above | 2 GB (cap) |

Below about 1GB the floor takes over, so a small archive reclaims in more,
proportionally smaller steps than the ratio would suggest. That costs almost
nothing on the sparse path, where a reclaim is a metadata operation; on the
rewrite fallback it is the difference that matters, so pass a larger `-c`
there if the archive is small but the filesystem has no sparse support.

Everything else is automatic: deflate entries are decoded on a worker pool
across cores, writes overlap reads, and space is reclaimed by punching holes
rather than rewriting.

**Expect to be disk-bound.** Decoding runs well ahead of what most SSDs
sustain, so the flash is usually the limit rather than the CPU. A drive that
sustains 265 MB/s of writes cannot extract 36GB in under about two and a half
minutes no matter what the code does. Check your drive's *sustained* write
rate, not its burst rating, before chasing extraction speed.

### Benchmark

10 GB archive, 2048 entries, consumer NVMe SSD, Windows/NTFS:

| | Time |
|---|---|
| Sparse, `-c auto` | **~75s** |
| Sparse, `-c 1GB` | **~75s** |
| Rewrite, `-c 100MB` | ~48 min (extrapolated) |

Measured head to head on identical work, the two reclaim strategies were
507.8s versus 12.9s — a 39x difference. Once reclaim stops rewriting the
archive, chunk size no longer drives speed: `auto` and a hand-picked 1GB land
within noise of each other.

A caveat on these figures: consumer SSDs vary enormously under sustained
load. The same 10GB extraction on this drive ranged from 54s to 337s purely
depending on how recently the drive had been written to, since garbage
collection after filling the SLC cache dwarfs anything the code does. Compare
runs only when they are interleaved.

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
# Extract, reclaiming space as it goes (auto-deletes archive upon success)
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
| `-c` | Reclaim step: `auto`, or a size such as `512MB` | `auto` |
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

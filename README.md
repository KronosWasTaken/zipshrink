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

```bash
# Build binary
go build -o zipshrink.exe ./cmd/zipshrink

# Run tests
go test -v ./...
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

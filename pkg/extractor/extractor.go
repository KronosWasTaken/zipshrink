// Package extractor coordinates progressive decompression and real-time space reclamation.
package extractor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"zipshrink/pkg/streamzip"
	"zipshrink/pkg/truncator"
)

var ErrZipSlip = errors.New("extractor: zip slip detected")

type Options struct {
	SourcePath     string
	DestinationDir string
	ChunkSize      int64
	DeleteArchive  bool
	OnShift        func(freed, remaining int64)
	OnFile         func(name string)
	// OnProgress reports archive bytes consumed against the total, rate
	// limited to a few calls a second. Rendering is left to the caller.
	OnProgress func(done, total int64)
}

type Result struct {
	FilesCount int
	BytesTotal int64
	SpaceSaved int64
	Duration   time.Duration
	// Sparse reports whether space was reclaimed by punching holes in place
	// rather than by rewriting the archive.
	Sparse bool
}

const (
	defaultDirMode  os.FileMode = 0755
	defaultFileMode os.FileMode = 0644
)

// entry is the format-agnostic member view shared by the ZIP and RAR paths.
// raw, crc and csize are set only when the reader yields compressed bytes for
// a decoder goroutine to handle.
type entry struct {
	Name     string
	IsDir    bool
	Modified time.Time
	raw      bool
	crc      uint32
	csize    uint64
	usize    uint64
}

// Extract extracts archive entries to DestinationDir while shrinking the source file in chunks.
func Extract(ctx context.Context, opts Options) (_ *Result, err error) {
	start := time.Now()
	fi, dest, stream, err := openTruncatedSource(opts)
	if err != nil {
		return nil, err
	}
	// Close deletes the archive when DeleteArchive is set, so its failure
	// must reach the caller rather than be reported as a clean run.
	defer closeStream(stream, &err)

	zr := streamzip.NewReader(newProgressReader(stream, fi.Size(), opts.OnProgress))
	zr.SetRaw(true)
	dec := newDecoders()

	count, totalBytes, err := extractLoop(ctx, dest, opts.OnFile, dec, func() (entry, io.Reader, error) {
		hdr, r, err := zr.Next()
		if err != nil {
			return entry{}, nil, err
		}
		return entry{
			Name:     hdr.Name,
			IsDir:    hdr.IsDir(),
			Modified: hdr.Modified,
			raw:      zr.Raw(),
			crc:      hdr.CRC32,
			csize:    hdr.CompressedSize,
			usize:    hdr.UncompressedSize,
		}, r, nil
	})
	// Workers must finish before the archive is removed or a result reported.
	if derr := dec.close(); derr != nil && err == nil {
		err = derr
	}
	if err != nil {
		return nil, err
	}
	// Parsing stops at the central directory, so the reader never sees EOF;
	// snap to complete so callers always finish at 100%.
	if opts.OnProgress != nil {
		opts.OnProgress(fi.Size(), fi.Size())
	}
	return buildResult(opts, fi, stream, count, totalBytes, start), nil
}

func closeStream(stream io.Closer, err *error) {
	if cerr := stream.Close(); cerr != nil && *err == nil {
		*err = cerr
	}
}

func openTruncatedSource(opts Options) (fi os.FileInfo, dest string, stream *truncator.Truncator, err error) {
	fi, err = os.Stat(opts.SourcePath)
	if err != nil {
		return nil, "", nil, fmt.Errorf("extractor: stat: %w", err)
	}
	dest, err = resolveDestination(opts.SourcePath, opts.DestinationDir)
	if err != nil {
		return nil, "", nil, err
	}
	stream, err = truncator.Open(opts.SourcePath, opts.ChunkSize, opts.DeleteArchive, opts.OnShift)
	return fi, dest, stream, err
}

// next reports io.EOF when the archive is exhausted. dec may be nil, in which
// case every entry is decoded in stream order.
func extractLoop(ctx context.Context, dest string, onFile func(string), dec *decoders, next func() (entry, io.Reader, error)) (count int, totalBytes int64, err error) {
	w := newAsyncWriter(dest)
	defer func() {
		if cerr := w.close(); cerr != nil && err == nil {
			err = fmt.Errorf("extractor: write: %w", cerr)
		}
	}()

	for {
		if err := ctx.Err(); err != nil {
			return count, totalBytes, err
		}

		e, r, err := next()
		if errors.Is(err, io.EOF) {
			return count, totalBytes, nil
		}
		if err != nil {
			return count, totalBytes, fmt.Errorf("extractor: next: %w", err)
		}

		target := filepath.Join(dest, e.Name)
		if !isSafe(dest, target) {
			return count, totalBytes, fmt.Errorf("extractor: %w: %s", ErrZipSlip, e.Name)
		}

		if e.IsDir {
			if err := w.mkdirAll(target); err != nil {
				return count, totalBytes, fmt.Errorf("extractor: %s: %w", e.Name, err)
			}
			continue
		}
		if onFile != nil {
			onFile(e.Name)
		}

		// A compressed entry small enough to buffer goes to a decoder
		// goroutine; everything else is decoded here in stream order. The
		// directory is created here either way, since only this goroutine
		// may touch the cache.
		if e.raw && dec != nil {
			if err := w.mkdirAll(filepath.Dir(target)); err != nil {
				return count, totalBytes, fmt.Errorf("extractor: %s: %w", e.Name, err)
			}
			queued, err := dec.submit(target, e, r)
			if err != nil {
				return count, totalBytes, fmt.Errorf("extractor: %s: %w", e.Name, err)
			}
			if queued {
				count++
				totalBytes += int64(min(e.usize, math.MaxInt64))
				continue
			}
			written, err := inflateEntry(target, e, r)
			if err != nil {
				return count, totalBytes, fmt.Errorf("extractor: %s: %w", e.Name, err)
			}
			count++
			totalBytes += written
			continue
		}

		if err := w.mkdirAll(filepath.Dir(target)); err != nil {
			return count, totalBytes, fmt.Errorf("extractor: %s: %w", e.Name, err)
		}
		written, err := w.stream(target, e.Modified, r)
		if err != nil {
			return count, totalBytes, fmt.Errorf("extractor: %s: %w", e.Name, err)
		}
		count++
		totalBytes += written
	}
}

func buildResult(opts Options, fi os.FileInfo, stream *truncator.Truncator, count int, totalBytes int64, start time.Time) *Result {
	var spaceSaved int64
	if opts.DeleteArchive {
		spaceSaved = fi.Size()
	}
	return &Result{
		FilesCount: count,
		BytesTotal: totalBytes,
		SpaceSaved: spaceSaved,
		Duration:   time.Since(start),
		Sparse:     stream.Sparse(),
	}
}

func resolveDestination(sourcePath, customDest string) (string, error) {
	dest := customDest
	if dest == "" {
		dest = strings.TrimSuffix(sourcePath, filepath.Ext(sourcePath))
	}
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return "", fmt.Errorf("extractor: resolve dest: %w", err)
	}
	if err := os.MkdirAll(absDest, defaultDirMode); err != nil {
		return "", fmt.Errorf("extractor: mkdir: %w", err)
	}
	return absDest, nil
}

// target has already been through filepath.Join, which resolves any "..",
// so an escape can no longer be hiding inside it and a prefix test is both
// sufficient and allocation-free.
func isSafe(base, target string) bool {
	if base == target {
		return true
	}
	return strings.HasPrefix(target, base+string(filepath.Separator))
}

// Package extractor coordinates progressive decompression and real-time space reclamation.
package extractor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"zipshrink/pkg/streamzip"
	"zipshrink/pkg/truncator"
)

var ErrZipSlip = errors.New("extractor: zip slip detected")

// Options configures extraction behavior and progress hooks.
type Options struct {
	SourcePath     string
	DestinationDir string
	ChunkSize      int64
	DeleteArchive  bool
	OnShift        func(freed, remaining int64)
	OnFile         func(name string)
}

// Result summarizes extraction metrics.
type Result struct {
	FilesCount int
	BytesTotal int64
	SpaceSaved int64
	Duration   time.Duration
}

const (
	defaultDirMode  os.FileMode = 0755
	defaultFileMode os.FileMode = 0644
)

// entry is the format-agnostic member view shared by the ZIP and RAR paths.
type entry struct {
	Name     string
	IsDir    bool
	Modified time.Time
}

// Extract extracts archive entries to DestinationDir while shrinking the source file in chunks.
func Extract(ctx context.Context, opts Options) (*Result, error) {
	start := time.Now()
	fi, dest, stream, err := openTruncatedSource(opts)
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	zr := streamzip.NewReader(stream)
	count, totalBytes, err := extractLoop(ctx, dest, opts.OnFile, func() (entry, io.Reader, error) {
		hdr, r, err := zr.Next()
		if err != nil {
			return entry{}, nil, err
		}
		return entry{Name: hdr.Name, IsDir: hdr.IsDir(), Modified: hdr.Modified}, r, nil
	})
	if err != nil {
		return nil, err
	}
	return buildResult(opts.DeleteArchive, fi.Size(), count, totalBytes, start), nil
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

// extractLoop is the shared drive loop for every archive format; next
// reports io.EOF when the archive is exhausted.
func extractLoop(ctx context.Context, dest string, onFile func(string), next func() (entry, io.Reader, error)) (count int, totalBytes int64, err error) {
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

		written, err := processEntry(dest, e, r, onFile)
		if err != nil {
			return count, totalBytes, fmt.Errorf("extractor: %s: %w", e.Name, err)
		}
		if !e.IsDir {
			count++
			totalBytes += written
		}
	}
}

func buildResult(deleteArchive bool, archiveSize int64, count int, totalBytes int64, start time.Time) *Result {
	var spaceSaved int64
	if deleteArchive {
		spaceSaved = archiveSize
	}
	return &Result{FilesCount: count, BytesTotal: totalBytes, SpaceSaved: spaceSaved, Duration: time.Since(start)}
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

func processEntry(dest string, e entry, r io.Reader, onFile func(string)) (int64, error) {
	target := filepath.Clean(filepath.Join(dest, e.Name))
	if !isSafe(dest, target) {
		return 0, fmt.Errorf("%w: %s", ErrZipSlip, e.Name)
	}
	if e.IsDir {
		return 0, os.MkdirAll(target, defaultDirMode)
	}
	if onFile != nil {
		onFile(e.Name)
	}
	return writeFile(target, e, r)
}

func writeFile(target string, e entry, r io.Reader) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(target), defaultDirMode); err != nil {
		return 0, err
	}

	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, defaultFileMode)
	if err != nil {
		return 0, err
	}

	n, copyErr := io.Copy(f, r)
	closeErr := f.Close()

	if copyErr != nil {
		_ = os.Remove(target) // Clean up corrupted partial write
		return n, copyErr
	}
	if closeErr != nil {
		return n, closeErr
	}

	if !e.Modified.IsZero() {
		_ = os.Chtimes(target, e.Modified, e.Modified)
	}
	return n, nil
}

func isSafe(base, target string) bool {
	if base == target {
		return true
	}
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".."
}

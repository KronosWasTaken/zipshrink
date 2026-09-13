package extractor

import (
	"context"
	"fmt"
	"io"
	"time"

	// Fork of github.com/nwaples/rardecode carrying a fix for matches that
	// run past the end of the decode window; drop it once that lands upstream.
	rardecode "github.com/KronosWasTaken/rardecode/v2"
)

// ExtractRAR extracts a single-volume RAR archive to DestinationDir while
// shrinking the source file in chunks, the same as Extract does for ZIP.
// Split (multi-volume) archives are not supported.
func ExtractRAR(ctx context.Context, opts Options) (_ *Result, err error) {
	start := time.Now()
	fi, dest, stream, err := openTruncatedSource(opts)
	if err != nil {
		return nil, err
	}
	defer closeStream(stream, &err)

	out := new(written)
	rr, err := rardecode.NewReader(newProgressReader(stream, fi.Size(), out, opts.OnProgress))
	if err != nil {
		return nil, fmt.Errorf("extractor: rar: %w", err)
	}

	count, totalBytes, err := extractLoop(ctx, dest, opts.OnFile, nil, out, func() (entry, io.Reader, error) {
		hdr, err := rr.Next()
		if err != nil {
			return entry{}, nil, err
		}
		return entry{Name: hdr.Name, IsDir: hdr.IsDir, Modified: hdr.ModificationTime}, rr, nil
	})
	if err != nil {
		return nil, err
	}
	// Parsing stops at the central directory, so the reader never sees EOF;
	// snap to complete so callers always finish at 100%.
	if opts.OnProgress != nil {
		opts.OnProgress(fi.Size(), fi.Size(), out.load())
	}
	return buildResult(opts, fi, stream, count, totalBytes, start), nil
}

package extractor

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/nwaples/rardecode/v2"
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

	rr, err := rardecode.NewReader(stream)
	if err != nil {
		return nil, fmt.Errorf("extractor: rar: %w", err)
	}

	count, totalBytes, err := extractLoop(ctx, dest, opts.OnFile, func() (entry, io.Reader, error) {
		hdr, err := rr.Next()
		if err != nil {
			return entry{}, nil, err
		}
		return entry{Name: hdr.Name, IsDir: hdr.IsDir, Modified: hdr.ModificationTime}, rr, nil
	})
	if err != nil {
		return nil, err
	}
	return buildResult(opts.DeleteArchive, fi.Size(), count, totalBytes, start), nil
}

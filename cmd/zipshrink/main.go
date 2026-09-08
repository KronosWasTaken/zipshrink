package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"zipshrink/pkg/extractor"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		dest     string
		chunkStr string
		keep     bool
		verbose  bool
	)

	flag.StringVar(&dest, "o", "", "Destination directory")
	flag.StringVar(&chunkStr, "c", "100MB", "Chunk threshold (e.g. 50MB, 100MB, 512MB)")
	flag.BoolVar(&keep, "k", false, "Keep source archive")
	flag.BoolVar(&verbose, "v", false, "Verbose output")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: zipshrink [options] <archive.zip|archive.rar>\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		return errors.New("no archive given")
	}

	archive := flag.Arg(0)
	chunkBytes, err := parseBytes(chunkStr)
	if err != nil {
		return err
	}
	if chunkBytes <= 0 {
		return errors.New("chunk size must be greater than zero")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	fmt.Printf("Archive:     %s\n", archive)
	fmt.Printf("Chunk Size:  %s\n", formatBytes(chunkBytes))
	fmt.Printf("Auto-delete: %v\n", !keep)

	opts := extractor.Options{
		SourcePath:     archive,
		DestinationDir: dest,
		ChunkSize:      chunkBytes,
		DeleteArchive:  !keep,
		OnShift: func(freed, remaining int64) {
			fmt.Printf("  Reclaimed %s (archive size now %s)\n", formatBytes(freed), formatBytes(remaining))
		},
		OnFile: func(name string) {
			if verbose {
				fmt.Printf("  -> %s\n", name)
			}
		},
	}

	var res *extractor.Result
	switch ext := strings.ToLower(filepath.Ext(archive)); ext {
	case ".rar":
		res, err = extractor.ExtractRAR(ctx, opts)
	case ".zip":
		res, err = extractor.Extract(ctx, opts)
	default:
		return fmt.Errorf("unsupported archive format %q (supported: .zip, .rar)", ext)
	}
	if err != nil {
		return err
	}

	fmt.Printf("\nDone. Extracted %d files (%s) in %s\n", res.FilesCount, formatBytes(res.BytesTotal), res.Duration.Round(time.Millisecond))
	if !keep {
		fmt.Printf("Space saved: %s\n", formatBytes(res.SpaceSaved))
	}
	return nil
}

var byteSizePattern = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)\s*([A-Z]*)$`)

var byteUnits = map[string]int64{"": 1, "B": 1, "K": 1024, "KB": 1024, "M": 1024 * 1024, "MB": 1024 * 1024, "G": 1024 * 1024 * 1024, "GB": 1024 * 1024 * 1024}

func parseBytes(s string) (int64, error) {
	m := byteSizePattern.FindStringSubmatch(strings.TrimSpace(strings.ToUpper(s)))
	if len(m) != 3 {
		return 0, fmt.Errorf("invalid byte size: %s", s)
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, err
	}
	mult, ok := byteUnits[m[2]]
	if !ok {
		return 0, fmt.Errorf("unknown unit %s", m[2])
	}
	return int64(val * float64(mult)), nil
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

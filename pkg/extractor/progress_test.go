package extractor

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestProgressReaderCountsToTotal(t *testing.T) {
	data := bytes.Repeat([]byte("progress"), 4096)
	var lastDone, lastTotal int64
	var calls int

	r := newProgressReader(bytes.NewReader(data), int64(len(data)), func(done, total int64) {
		calls++
		lastDone, lastTotal = done, total
	})
	if _, err := io.Copy(io.Discard, r); err != nil {
		t.Fatal(err)
	}

	if calls == 0 {
		t.Fatal("progress was never reported")
	}
	if lastDone != int64(len(data)) {
		t.Errorf("final done = %d, want %d", lastDone, len(data))
	}
	if lastTotal != int64(len(data)) {
		t.Errorf("total = %d, want %d", lastTotal, len(data))
	}
}

func TestProgressReaderNilCallbackIsPassthrough(t *testing.T) {
	data := []byte("unchanged")
	r := newProgressReader(bytes.NewReader(data), int64(len(data)), nil)
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("got %q, want %q", got, data)
	}
}

// Progress must reach 100% even when the archive is kept, which is the case
// where nothing is reclaimed and the file never shrinks.
func TestProgressReportedWhenKeepingArchive(t *testing.T) {
	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "archive.zip")
	createTestZip(t, zipPath, map[string][]byte{
		"a.txt": bytes.Repeat([]byte("alpha "), 5000),
		"b.txt": bytes.Repeat([]byte("beta "), 5000),
	})
	size, err := os.Stat(zipPath)
	if err != nil {
		t.Fatal(err)
	}

	var done, total int64
	if _, err := Extract(context.Background(), Options{
		SourcePath:     zipPath,
		DestinationDir: filepath.Join(tmp, "out"),
		DeleteArchive:  false,
		OnProgress:     func(d, tot int64) { done, total = d, tot },
	}); err != nil {
		t.Fatal(err)
	}

	if total != size.Size() {
		t.Errorf("total = %d, want archive size %d", total, size.Size())
	}
	if done != total {
		t.Errorf("finished at %d of %d bytes", done, total)
	}
}

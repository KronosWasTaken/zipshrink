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
	var lastDone, lastTotal, lastExtracted int64
	var calls int

	out := new(written)
	out.add(1234)
	r := newProgressReader(bytes.NewReader(data), int64(len(data)), out, func(done, total, extracted int64) {
		calls++
		lastDone, lastTotal, lastExtracted = done, total, extracted
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
	if lastExtracted != 1234 {
		t.Errorf("extracted = %d, want 1234", lastExtracted)
	}
}

// The counter is fed from several goroutines, so it must also work as the
// io.Writer leg the decode paths use.
func TestWrittenCountsThroughWriter(t *testing.T) {
	out := new(written)
	if _, err := io.Copy(out, bytes.NewReader(make([]byte, 4096))); err != nil {
		t.Fatal(err)
	}
	out.add(4)
	if got := out.load(); got != 4100 {
		t.Errorf("load() = %d, want 4100", got)
	}
}

func TestProgressReaderNilCallbackIsPassthrough(t *testing.T) {
	data := []byte("unchanged")
	r := newProgressReader(bytes.NewReader(data), int64(len(data)), new(written), nil)
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

	var done, total, extracted int64
	if _, err := Extract(context.Background(), Options{
		SourcePath:     zipPath,
		DestinationDir: filepath.Join(tmp, "out"),
		DeleteArchive:  false,
		OnProgress:     func(d, tot, ex int64) { done, total, extracted = d, tot, ex },
	}); err != nil {
		t.Fatal(err)
	}

	if total != size.Size() {
		t.Errorf("total = %d, want archive size %d", total, size.Size())
	}
	if done != total {
		t.Errorf("finished at %d of %d bytes", done, total)
	}
	// The two entries decompress to 30000 and 25000 bytes, and the report must
	// cover writes that happen after the archive has been fully read.
	if want := int64(55000); extracted != want {
		t.Errorf("extracted = %d, want %d", extracted, want)
	}
}

package extractor

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/flate"
)

// buildKnownSizeZip writes deflate entries with their sizes in the local
// header. Go's archive/zip always emits a data descriptor instead, which
// would keep the parallel decode path from ever being taken.
func buildKnownSizeZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer

	for name, content := range files {
		var comp bytes.Buffer
		fw, err := flate.NewWriter(&comp, flate.DefaultCompression)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(content); err != nil {
			t.Fatal(err)
		}
		if err := fw.Close(); err != nil {
			t.Fatal(err)
		}

		hdr := make([]byte, 30)
		binary.LittleEndian.PutUint32(hdr[0:4], 0x04034b50)
		binary.LittleEndian.PutUint16(hdr[4:6], 20)
		binary.LittleEndian.PutUint16(hdr[6:8], 0) // no data descriptor
		binary.LittleEndian.PutUint16(hdr[8:10], 8)
		binary.LittleEndian.PutUint32(hdr[14:18], crc32.ChecksumIEEE(content))
		binary.LittleEndian.PutUint32(hdr[18:22], uint32(comp.Len()))
		binary.LittleEndian.PutUint32(hdr[22:26], uint32(len(content)))
		binary.LittleEndian.PutUint16(hdr[26:28], uint16(len(name)))
		buf.Write(hdr)
		buf.WriteString(name)
		buf.Write(comp.Bytes())
	}

	binary.Write(&buf, binary.LittleEndian, uint32(0x02014b50)) // ends the scan
	return buf.Bytes()
}

func TestParallelDecodePath(t *testing.T) {
	files := map[string][]byte{}
	// Several entries, including one past the buffer threshold so both the
	// pooled and the inline oversized paths run.
	for i := range 12 {
		files[fmt.Sprintf("dir%d/file_%d.txt", i%3, i)] =
			bytes.Repeat([]byte(fmt.Sprintf("entry %d payload. ", i)), 4000)
	}
	files["big/oversized.bin"] = bytes.Repeat([]byte("large entry payload. "), 700_000)

	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "known.zip")
	if err := os.WriteFile(zipPath, buildKnownSizeZip(t, files), 0644); err != nil {
		t.Fatal(err)
	}
	destDir := filepath.Join(tmp, "out")

	res, err := Extract(context.Background(), Options{
		SourcePath:     zipPath,
		DestinationDir: destDir,
		ChunkSize:      64 * 1024,
		DeleteArchive:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.FilesCount != len(files) {
		t.Errorf("got %d files, want %d", res.FilesCount, len(files))
	}

	var want int64
	for name, content := range files {
		want += int64(len(content))
		got, err := os.ReadFile(filepath.Join(destDir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(got, content) {
			t.Errorf("content mismatch for %s (%d bytes vs %d)", name, len(got), len(content))
		}
	}
	if res.BytesTotal != want {
		t.Errorf("BytesTotal = %d, want %d", res.BytesTotal, want)
	}
}

// A corrupted entry must be reported rather than silently written, even when
// the failure happens on a decoder goroutine.
func TestParallelDecodeDetectsBadChecksum(t *testing.T) {
	content := bytes.Repeat([]byte("payload "), 2000)
	raw := buildKnownSizeZip(t, map[string][]byte{"a.txt": content})
	binary.LittleEndian.PutUint32(raw[14:18], 0xDEADBEEF) // wrong CRC

	tmp := t.TempDir()
	zipPath := filepath.Join(tmp, "bad.zip")
	if err := os.WriteFile(zipPath, raw, 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := Extract(context.Background(), Options{
		SourcePath:     zipPath,
		DestinationDir: filepath.Join(tmp, "out"),
		DeleteArchive:  false,
	}); err == nil {
		t.Fatal("expected a checksum error")
	}
}

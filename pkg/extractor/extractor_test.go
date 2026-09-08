package extractor

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func createTestZip(t *testing.T, zipPath string, files map[string][]byte) {
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	_ = zw.Close()
}

func TestExtractor(t *testing.T) {
	t.Run("extracts all files and removes archive", func(t *testing.T) {
		tmpDir := t.TempDir()
		zipPath := filepath.Join(tmpDir, "archive.zip")
		destDir := filepath.Join(tmpDir, "extracted")

		testFiles := map[string][]byte{
			"root.txt":       []byte("Root file content"),
			"sub/nested.txt": bytes.Repeat([]byte("Nested file content.\n"), 50),
		}
		createTestZip(t, zipPath, testFiles)

		res, err := Extract(context.Background(), Options{
			SourcePath:     zipPath,
			DestinationDir: destDir,
			ChunkSize:      512,
			DeleteArchive:  true,
		})
		if err != nil {
			t.Fatal(err)
		}

		if res.FilesCount != len(testFiles) {
			t.Errorf("got %d files, want %d", res.FilesCount, len(testFiles))
		}

		for name, expected := range testFiles {
			got, err := os.ReadFile(filepath.Join(destDir, name))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, expected) {
				t.Errorf("content mismatch for %s", name)
			}
		}

		if _, err := os.Stat(zipPath); !os.IsNotExist(err) {
			t.Error("archive should have been deleted")
		}
	})

	t.Run("shrinks source across many chunks while extracting", func(t *testing.T) {
		tmpDir := t.TempDir()
		zipPath := filepath.Join(tmpDir, "big.zip")
		destDir := filepath.Join(tmpDir, "extracted")

		testFiles := map[string][]byte{}
		for i := range 8 {
			// Incompressible content, so the archive really is multi-chunk.
			payload := make([]byte, 96*1024)
			if _, err := rand.Read(payload); err != nil {
				t.Fatal(err)
			}
			testFiles[fmt.Sprintf("data/blob_%d.bin", i)] = payload
		}
		createTestZip(t, zipPath, testFiles)

		var shifts int
		var freedTotal int64
		var seen []string
		res, err := Extract(context.Background(), Options{
			SourcePath:     zipPath,
			DestinationDir: destDir,
			ChunkSize:      64 * 1024,
			DeleteArchive:  true,
			OnShift: func(freed, _ int64) {
				shifts++
				freedTotal += freed
			},
			OnFile: func(name string) { seen = append(seen, name) },
		})
		if err != nil {
			t.Fatal(err)
		}

		if shifts < 2 {
			t.Errorf("expected multiple shifts, got %d", shifts)
		}
		if freedTotal == 0 {
			t.Error("expected OnShift to report reclaimed bytes")
		}
		if len(seen) != len(testFiles) {
			t.Errorf("OnFile fired %d times, want %d", len(seen), len(testFiles))
		}
		if res.FilesCount != len(testFiles) {
			t.Errorf("got %d files, want %d", res.FilesCount, len(testFiles))
		}
		for name, expected := range testFiles {
			got, err := os.ReadFile(filepath.Join(destDir, name))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, expected) {
				t.Errorf("content mismatch for %s", name)
			}
		}
		if _, err := os.Stat(zipPath); !os.IsNotExist(err) {
			t.Error("archive should have been deleted")
		}
	})

	t.Run("honors a cancelled context", func(t *testing.T) {
		tmpDir := t.TempDir()
		zipPath := filepath.Join(tmpDir, "archive.zip")
		createTestZip(t, zipPath, map[string][]byte{"a.txt": []byte("content")})

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := Extract(ctx, Options{
			SourcePath:     zipPath,
			DestinationDir: filepath.Join(tmpDir, "dest"),
			DeleteArchive:  false,
		})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
		if _, err := os.Stat(zipPath); err != nil {
			t.Error("archive must survive a cancelled run")
		}
	})

	t.Run("leaves the archive untouched when not deleting", func(t *testing.T) {
		tmpDir := t.TempDir()
		zipPath := filepath.Join(tmpDir, "keep.zip")
		testFiles := map[string][]byte{
			"a.bin": bytes.Repeat([]byte("keep me intact.\n"), 8*1024),
		}
		createTestZip(t, zipPath, testFiles)

		before, err := os.ReadFile(zipPath)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := Extract(context.Background(), Options{
			SourcePath:     zipPath,
			DestinationDir: filepath.Join(tmpDir, "dest"),
			ChunkSize:      4 * 1024,
			DeleteArchive:  false,
		}); err != nil {
			t.Fatal(err)
		}

		after, err := os.ReadFile(zipPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Error("archive was modified despite DeleteArchive=false")
		}
	})

	t.Run("zip slip prevention", func(t *testing.T) {
		tmpDir := t.TempDir()
		zipPath := filepath.Join(tmpDir, "slip.zip")
		createTestZip(t, zipPath, map[string][]byte{"../slip.txt": []byte("evil")})

		_, err := Extract(context.Background(), Options{
			SourcePath:     zipPath,
			DestinationDir: filepath.Join(tmpDir, "dest"),
		})
		if !errors.Is(err, ErrZipSlip) {
			t.Errorf("expected ErrZipSlip, got: %v", err)
		}
	})
}

package extractor

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractRAR(t *testing.T) {
	t.Run("reports a missing archive", func(t *testing.T) {
		tmpDir := t.TempDir()
		_, err := ExtractRAR(context.Background(), Options{
			SourcePath:     filepath.Join(tmpDir, "absent.rar"),
			DestinationDir: filepath.Join(tmpDir, "dest"),
		})
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("expected a not-exist error, got %v", err)
		}
	})

	t.Run("rejects a file that is not a RAR archive", func(t *testing.T) {
		tmpDir := t.TempDir()
		rarPath := filepath.Join(tmpDir, "bogus.rar")
		if err := os.WriteFile(rarPath, []byte("this is definitely not a rar archive"), 0644); err != nil {
			t.Fatal(err)
		}

		if _, err := ExtractRAR(context.Background(), Options{
			SourcePath:     rarPath,
			DestinationDir: filepath.Join(tmpDir, "dest"),
			DeleteArchive:  false,
		}); err == nil {
			t.Fatal("expected an error for a non-RAR file")
		}

		if _, err := os.Stat(rarPath); err != nil {
			t.Error("source must survive a failed run when not deleting")
		}
	})
}

package truncator

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestChunkTruncator(t *testing.T) {
	t.Run("shifts and truncates multi-chunk file", func(t *testing.T) {
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "payload.bin")

		originalData := make([]byte, 10*1024)
		for i := range originalData {
			originalData[i] = byte(i % 256)
		}
		if err := os.WriteFile(testFile, originalData, 0644); err != nil {
			t.Fatal(err)
		}

		shiftCount := 0
		tr, err := Open(testFile, 2048, true, func(shiftedBytes, remainingSize int64) {
			shiftCount++
		})
		if err != nil {
			t.Fatal(err)
		}

		var collected bytes.Buffer
		readBuf := make([]byte, 300)
		for {
			n, err := tr.Read(readBuf)
			if n > 0 {
				collected.Write(readBuf[:n])
			}
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}

		if !bytes.Equal(collected.Bytes(), originalData) {
			t.Fatal("data mismatch")
		}
		if shiftCount < 4 {
			t.Errorf("expected >=4 shifts, got %d", shiftCount)
		}
		if err := tr.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(testFile); !os.IsNotExist(err) {
			t.Error("file should have been deleted")
		}
	})

	t.Run("handles small file without delete", func(t *testing.T) {
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "small.bin")
		expected := []byte("small file payload")
		_ = os.WriteFile(testFile, expected, 0644)

		tr, err := Open(testFile, 1024*1024, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		actual, _ := io.ReadAll(tr)
		if !bytes.Equal(actual, expected) {
			t.Fatal("data mismatch")
		}
		_ = tr.Close()
		if _, err := os.Stat(testFile); os.IsNotExist(err) {
			t.Error("file should exist")
		}
	})

	t.Run("leaves a multi-chunk file completely unmodified when not deleting", func(t *testing.T) {
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "payload.bin")

		originalData := make([]byte, 10*1024)
		for i := range originalData {
			originalData[i] = byte(i % 256)
		}
		if err := os.WriteFile(testFile, originalData, 0644); err != nil {
			t.Fatal(err)
		}

		shiftCount := 0
		tr, err := Open(testFile, 2048, false, func(int64, int64) { shiftCount++ })
		if err != nil {
			t.Fatal(err)
		}

		actual, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual, originalData) {
			t.Fatal("data mismatch")
		}
		if shiftCount != 0 {
			t.Errorf("expected no shifts when del=false, got %d", shiftCount)
		}
		if err := tr.Close(); err != nil {
			t.Fatal(err)
		}

		onDisk, err := os.ReadFile(testFile)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(onDisk, originalData) {
			t.Fatal("source file was modified on disk despite del=false")
		}
	})
}

package truncator

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Only one reclaim strategy runs per platform, so drive both explicitly:
// whichever the platform picked, and the shift fallback forced on.
func TestReclaimStrategies(t *testing.T) {
	payload := make([]byte, 40*1024)
	for i := range payload {
		payload[i] = byte(i * 7 % 251)
	}

	for _, tc := range []struct {
		name         string
		forceFallack bool
	}{
		{"platform default", false},
		{"shift fallback", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "payload.bin")
			if err := os.WriteFile(path, payload, 0644); err != nil {
				t.Fatal(err)
			}

			var reclaims int
			var freedTotal int64
			tr, err := Open(path, 4096, true, func(freed, _ int64) {
				reclaims++
				freedTotal += freed
			})
			if err != nil {
				t.Fatal(err)
			}
			if tc.forceFallack {
				tr.sparse = false
			}

			got, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("data mismatch through the %s path", tc.name)
			}
			if reclaims < 4 {
				t.Errorf("expected several reclaims, got %d", reclaims)
			}
			if freedTotal == 0 {
				t.Error("expected reclaimed bytes to be reported")
			}
			if err := tr.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Error("archive should have been deleted")
			}
		})
	}
}

// A reader that stops short must not leave the source half-released in a way
// that corrupts what remains on disk when the archive is kept.
func TestPartialReadKeepsSourceIntact(t *testing.T) {
	payload := bytes.Repeat([]byte("zipshrink partial read test.\n"), 4096)
	path := filepath.Join(t.TempDir(), "keep.bin")
	if err := os.WriteFile(path, payload, 0644); err != nil {
		t.Fatal(err)
	}

	tr, err := Open(path, 4096, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(io.Discard, tr, 30*1024); err != nil {
		t.Fatal(err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, payload) {
		t.Error("source modified despite del=false")
	}
}

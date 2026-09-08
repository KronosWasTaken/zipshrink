package streamzip

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"
)

// FuzzReader drives the parser with arbitrary bytes. Archives are untrusted
// input, so no input may panic; malformed data must surface as an error.
func FuzzReader(f *testing.F) {
	var seed bytes.Buffer
	zw := zip.NewWriter(&seed)
	for name, method := range map[string]uint16{"a.txt": zip.Deflate, "b/c.bin": zip.Store} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			f.Fatal(err)
		}
		if _, err := w.Write([]byte("fuzz seed payload")); err != nil {
			f.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		f.Fatal(err)
	}

	f.Add(seed.Bytes())
	f.Add([]byte("PK\x03\x04"))
	f.Add([]byte{})

	f.Fuzz(func(_ *testing.T, data []byte) {
		zr := NewReader(bytes.NewReader(data))
		// Bounded so a hostile declared size cannot turn a crash into a hang.
		for range 64 {
			_, r, err := zr.Next()
			if err != nil {
				return
			}
			if _, err := io.Copy(io.Discard, io.LimitReader(r, 1<<16)); err != nil {
				return
			}
		}
	})
}

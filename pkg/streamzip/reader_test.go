package streamzip

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestStreamZipReader_MultiFileDeflateWithDescriptors(t *testing.T) {
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)

	testFiles := map[string][]byte{
		"file1.txt":        []byte("Hello from file 1!"),
		"dir/file2.log":    []byte("Log content here... 1234567890"),
		"dir/nested/f.dat": bytes.Repeat([]byte("A quick brown fox jumps over the lazy dog.\n"), 50),
	}

	for name, content := range testFiles {
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:   name,
			Method: zip.Deflate,
		})
		if err != nil {
			t.Fatalf("CreateHeader(%s) failed: %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("Write(%s) failed: %v", name, err)
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("zw.Close failed: %v", err)
	}

	zr := NewReader(bytes.NewReader(zipBuf.Bytes()))
	readFiles := make(map[string][]byte)

	for {
		header, reader, err := zr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("zr.Next() failed: %v", err)
		}

		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("ReadAll(%s) failed: %v", header.Name, err)
		}
		readFiles[header.Name] = content
	}

	if len(readFiles) != len(testFiles) {
		t.Fatalf("expected %d files, got %d", len(testFiles), len(readFiles))
	}

	for name, expected := range testFiles {
		got, ok := readFiles[name]
		if !ok {
			t.Errorf("file %s not found in read files", name)
			continue
		}
		if !bytes.Equal(got, expected) {
			t.Errorf("content mismatch for %s", name)
		}
	}
}

func TestStreamZipReader_StoreMethod(t *testing.T) {
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)

	name := "uncompressed.txt"
	content := []byte("This is uncompressed stored data.")

	fh := &zip.FileHeader{
		Name:               name,
		Method:             zip.Store,
		UncompressedSize64: uint64(len(content)),
		CompressedSize64:   uint64(len(content)),
	}
	w, err := zw.CreateHeader(fh)
	if err != nil {
		t.Fatalf("CreateHeader failed: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("zw.Close failed: %v", err)
	}

	zr := NewReader(bytes.NewReader(zipBuf.Bytes()))
	header, reader, err := zr.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}

	if header.Name != name {
		t.Errorf("expected name %s, got %s", name, header.Name)
	}

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}

	if !bytes.Equal(got, content) {
		t.Errorf("got %q, want %q", string(got), string(content))
	}

	_, _, nextErr := zr.Next()
	if !errors.Is(nextErr, io.EOF) {
		t.Errorf("expected io.EOF on Next(), got %v", nextErr)
	}
}

// Store entries written without a known size up front carry a trailing data
// descriptor instead, which is the storeDescReader path.
func TestStreamZipReader_StoreWithDataDescriptor(t *testing.T) {
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)

	files := map[string][]byte{
		"plain.bin":     bytes.Repeat([]byte("stored payload, no descriptor size.\n"), 40),
		"dir/other.bin": []byte("second stored entry"),
	}
	for name, content := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("CreateHeader(%s): %v", name, err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatalf("Write(%s): %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	got := make(map[string][]byte)
	zr := NewReader(bytes.NewReader(zipBuf.Bytes()))
	for {
		hdr, r, err := zr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		content, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("ReadAll(%s): %v", hdr.Name, err)
		}
		got[hdr.Name] = content
	}

	if len(got) != len(files) {
		t.Fatalf("got %d entries, want %d", len(got), len(files))
	}
	for name, want := range files {
		if !bytes.Equal(got[name], want) {
			t.Errorf("content mismatch for %s", name)
		}
	}
}

func TestStreamZipReader_DrainSkippedFiles(t *testing.T) {
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)

	for i := 0; i < 5; i++ {
		w, err := zw.Create(fmt.Sprintf("file_%d.txt", i))
		if err != nil {
			t.Fatal(err)
		}
		w.Write(fmt.Appendf(nil, "Content of file %d with extra bytes", i))
	}
	zw.Close()

	zr := NewReader(bytes.NewReader(zipBuf.Bytes()))
	count := 0
	for {
		header, _, err := zr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Next failed: %v", err)
		}
		// Notice: we do NOT read from `reader`, testing automatic draining
		expectedName := fmt.Sprintf("file_%d.txt", count)
		if header.Name != expectedName {
			t.Errorf("expected %s, got %s", expectedName, header.Name)
		}
		count++
	}

	if count != 5 {
		t.Errorf("expected 5 files, got %d", count)
	}
}

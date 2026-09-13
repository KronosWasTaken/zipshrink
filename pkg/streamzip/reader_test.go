package streamzip

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
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

// A Store entry with a data descriptor has no size up front, so the end is
// found by scanning for the descriptor signature. Content containing those
// bytes by chance must not truncate the entry -- this happens roughly once
// per 4GiB of random data.
func TestStreamZipReader_StoreContentContainingDescriptorSignature(t *testing.T) {
	payload := bytes.Join([][]byte{
		bytes.Repeat([]byte("before."), 300),
		{0x50, 0x4b, 0x07, 0x08}, // PK\x07\x08 embedded in the file's own data
		bytes.Repeat([]byte("after."), 300),
		{0x50, 0x4b, 0x07, 0x08},
		bytes.Repeat([]byte("tail."), 300),
	}, nil)

	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "tricky.bin", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	zr := NewReader(bytes.NewReader(zipBuf.Bytes()))
	hdr, r, err := zr.Next()
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll(%s): %v", hdr.Name, err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("truncated at an embedded signature: got %d bytes, want %d", len(got), len(payload))
	}
}

// Directory entries are stored with no data descriptor and a zero size. If
// such an entry is not bounded to zero bytes it reads on into the rest of the
// archive, which silently yields an empty extraction.
func TestStreamZipReader_ZeroLengthEntryIsBounded(t *testing.T) {
	var buf bytes.Buffer
	writeLocal := func(name string, method uint16, body []byte) {
		hdr := make([]byte, 30)
		binary.LittleEndian.PutUint32(hdr[0:4], 0x04034b50)
		binary.LittleEndian.PutUint16(hdr[8:10], method)
		binary.LittleEndian.PutUint32(hdr[14:18], crc32.ChecksumIEEE(body))
		binary.LittleEndian.PutUint32(hdr[18:22], uint32(len(body)))
		binary.LittleEndian.PutUint32(hdr[22:26], uint32(len(body)))
		binary.LittleEndian.PutUint16(hdr[26:28], uint16(len(name)))
		buf.Write(hdr)
		buf.WriteString(name)
		buf.Write(body)
	}

	payload := []byte("content that must survive the directory entry")
	writeLocal("adir/", 0, nil) // zero-length stored directory
	writeLocal("adir/file.txt", 0, payload)
	binary.Write(&buf, binary.LittleEndian, uint32(0x02014b50))

	zr := NewReader(bytes.NewReader(buf.Bytes()))

	hdr, r, err := zr.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !hdr.IsDir() {
		t.Fatalf("first entry %q should be a directory", hdr.Name)
	}
	if n, _ := io.Copy(io.Discard, r); n != 0 {
		t.Fatalf("directory entry yielded %d bytes, want 0", n)
	}

	_, r, err = zr.Next()
	if err != nil {
		t.Fatalf("second entry: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("got %q, want %q", got, payload)
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

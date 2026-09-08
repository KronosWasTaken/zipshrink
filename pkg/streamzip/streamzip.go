// Package streamzip implements sequential, single-pass ZIP decompression over an io.Reader.
package streamzip

import (
	"bufio"
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"strings"
	"time"
)

const (
	sigLocalFile    uint32 = 0x04034b50
	sigDataDesc     uint32 = 0x08074b50
	sigCentralDir   uint32 = 0x02014b50
	sigEOCD         uint32 = 0x06054b50
	sigZip64EOCD    uint32 = 0x06064b50
	sigZip64Locator uint32 = 0x07064b50

	// MethodStore and MethodDeflate are the only compression methods supported.
	MethodStore   uint16 = 0
	MethodDeflate uint16 = 8

	flagDataDesc uint16 = 1 << 3
)

var (
	ErrFormat   = errors.New("streamzip: bad format")
	ErrChecksum = errors.New("streamzip: checksum mismatch")
	ErrMethod   = errors.New("streamzip: unsupported method")
	le          = binary.LittleEndian
	dataDescSig = []byte{0x50, 0x4b, 0x07, 0x08}
)

// FileHeader describes a single entry in a ZIP archive.
type FileHeader struct {
	Name             string
	Flags, Method    uint16
	Modified         time.Time
	CRC32            uint32
	CompressedSize   uint64
	UncompressedSize uint64
}

// IsDir reports whether the entry is a directory.
func (h *FileHeader) IsDir() bool {
	return strings.HasSuffix(h.Name, "/") || strings.HasSuffix(h.Name, "\\")
}

// Reader reads ZIP archives sequentially from an unseekable stream.
type Reader struct {
	br   *bufio.Reader
	curr *entryReader
	err  error
}

// NewReader wraps an io.Reader in a streaming ZIP reader.
func NewReader(r io.Reader) *Reader {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReaderSize(r, 64*1024)
	}
	return &Reader{br: br}
}

// Next advances to the next entry, returning its metadata and uncompressed reader.
func (zr *Reader) Next() (*FileHeader, io.Reader, error) {
	if zr.err != nil {
		return nil, nil, zr.err
	}
	if zr.curr != nil && !zr.curr.drained {
		if err := zr.curr.drain(); err != nil {
			zr.err = err
			return nil, nil, err
		}
	}
	zr.curr = nil

	sigBuf, err := zr.br.Peek(4)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			zr.err = io.EOF
		} else {
			zr.err = err
		}
		return nil, nil, zr.err
	}

	sig := le.Uint32(sigBuf)
	if sig == sigCentralDir || sig == sigEOCD || sig == sigZip64EOCD || sig == sigZip64Locator {
		zr.err = io.EOF
		return nil, nil, io.EOF
	}
	if sig != sigLocalFile {
		zr.err = fmt.Errorf("%w: signature 0x%08x", ErrFormat, sig)
		return nil, nil, zr.err
	}
	if _, err := zr.br.Discard(4); err != nil {
		zr.err = err
		return nil, nil, err
	}

	hdr, err := zr.readHeader()
	if err != nil {
		zr.err = err
		return nil, nil, err
	}

	er, err := newEntryReader(zr.br, hdr)
	if err != nil {
		zr.err = err
		return nil, nil, err
	}
	zr.curr = er
	return hdr, er, nil
}

func (zr *Reader) readHeader() (*FileHeader, error) {
	var buf [26]byte
	if _, err := io.ReadFull(zr.br, buf[:]); err != nil {
		return nil, fmt.Errorf("streamzip: header: %w", err)
	}

	flags, method := le.Uint16(buf[2:4]), le.Uint16(buf[4:6])
	mTime, mDate := le.Uint16(buf[6:8]), le.Uint16(buf[8:10])
	crc := le.Uint32(buf[10:14])
	cSize, uSize := uint64(le.Uint32(buf[14:18])), uint64(le.Uint32(buf[18:22]))
	nLen, xLen := le.Uint16(buf[22:24]), le.Uint16(buf[24:26])

	name := make([]byte, nLen)
	if _, err := io.ReadFull(zr.br, name); err != nil {
		return nil, err
	}
	extra := make([]byte, xLen)
	if _, err := io.ReadFull(zr.br, extra); err != nil {
		return nil, err
	}

	uSize, cSize = parseZip64(extra, uSize, cSize)
	return &FileHeader{
		Name:             string(name),
		Flags:            flags,
		Method:           method,
		Modified:         msdosTime(mDate, mTime),
		CRC32:            crc,
		CompressedSize:   cSize,
		UncompressedSize: uSize,
	}, nil
}

type entryReader struct {
	br      *bufio.Reader
	hdr     *FileHeader
	r       io.Reader
	hasDesc bool
	crc     hash.Hash32
	drained bool
}

func newEntryReader(br *bufio.Reader, fh *FileHeader) (*entryReader, error) {
	er := &entryReader{
		br:      br,
		hdr:     fh,
		hasDesc: (fh.Flags & flagDataDesc) != 0,
		crc:     crc32.NewIEEE(),
	}

	switch fh.Method {
	case MethodStore:
		if er.hasDesc && fh.CompressedSize == 0 {
			er.r = &storeDescReader{br: br}
		} else {
			er.r = io.LimitReader(br, int64(fh.CompressedSize))
		}
	case MethodDeflate:
		if er.hasDesc || fh.CompressedSize == 0 {
			er.r = flate.NewReader(br)
		} else {
			er.r = flate.NewReader(io.LimitReader(br, int64(fh.CompressedSize)))
		}
	default:
		return nil, fmt.Errorf("%w: %d", ErrMethod, fh.Method)
	}
	return er, nil
}

func (er *entryReader) Read(p []byte) (int, error) {
	if er.drained {
		return 0, io.EOF
	}
	n, err := er.r.Read(p)
	if n > 0 {
		er.crc.Write(p[:n])
	}
	if errors.Is(err, io.EOF) {
		er.drained = true
		if c, ok := er.r.(io.Closer); ok {
			_ = c.Close()
		}
		if er.hasDesc {
			if err := er.readDesc(); err != nil {
				return n, err
			}
		}
		if er.hdr.CRC32 != 0 && er.hdr.CRC32 != er.crc.Sum32() {
			return n, fmt.Errorf("%w: entry %s", ErrChecksum, er.hdr.Name)
		}
		return n, io.EOF
	}
	return n, err
}

func (er *entryReader) drain() error {
	buf := make([]byte, 32*1024)
	for {
		if _, err := er.Read(buf); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (er *entryReader) readDesc() error {
	peek, err := er.br.Peek(4)
	if err != nil {
		return err
	}
	if le.Uint32(peek) == sigDataDesc {
		if _, err := er.br.Discard(4); err != nil {
			return err
		}
	}
	var desc [12]byte
	if _, err := io.ReadFull(er.br, desc[:]); err != nil {
		return err
	}
	er.hdr.CRC32 = le.Uint32(desc[0:4])
	er.hdr.CompressedSize = uint64(le.Uint32(desc[4:8]))
	er.hdr.UncompressedSize = uint64(le.Uint32(desc[8:12]))
	return nil
}

type storeDescReader struct {
	br   *bufio.Reader
	done bool
}

// Read scans the buffered window for the data-descriptor signature to find
// where this STORE entry ends, since its size isn't known up front. This can
// misfire if the raw file content itself contains those 4 bytes.
func (s *storeDescReader) Read(p []byte) (int, error) {
	if s.done {
		return 0, io.EOF
	}
	buf, err := s.br.Peek(s.br.Buffered())
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	if idx := bytes.Index(buf, dataDescSig); idx >= 0 {
		if idx == 0 {
			s.done = true
			return 0, io.EOF
		}
		return s.br.Read(p[:min(len(p), idx)])
	}
	safe := len(buf) - 3
	if safe <= 0 {
		b, err := s.br.ReadByte()
		if err != nil {
			return 0, err
		}
		p[0] = b
		return 1, nil
	}
	return s.br.Read(p[:min(len(p), safe)])
}

func parseZip64(extra []byte, uSize, cSize uint64) (uint64, uint64) {
	for len(extra) >= 4 {
		id, sz := le.Uint16(extra[:2]), int(le.Uint16(extra[2:4]))
		extra = extra[4:]
		if len(extra) < sz {
			break
		}
		data := extra[:sz]
		extra = extra[sz:]
		if id == 0x0001 {
			if uSize == 0xFFFFFFFF && len(data) >= 8 {
				uSize, data = le.Uint64(data[:8]), data[8:]
			}
			if cSize == 0xFFFFFFFF && len(data) >= 8 {
				cSize = le.Uint64(data[:8])
			}
		}
	}
	return uSize, cSize
}

func msdosTime(date, timeVal uint16) time.Time {
	sec, min, hr := int(timeVal&0x1f)*2, int((timeVal>>5)&0x3f), int((timeVal>>11)&0x1f)
	day, mon, yr := int(date&0x1f), time.Month((date>>5)&0x0f), int((date>>9)&0x7f)+1980
	if mon < 1 || mon > 12 || day < 1 || day > 31 {
		return time.Time{}
	}
	return time.Date(yr, mon, day, hr, min, sec, 0, time.Local)
}

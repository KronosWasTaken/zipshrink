// Package streamzip implements sequential, single-pass ZIP decompression over an io.Reader.
package streamzip

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"strings"
	"time"

	// Drop-in for compress/flate with a markedly faster decoder.
	"github.com/klauspost/compress/flate"
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

type FileHeader struct {
	Name             string
	Flags, Method    uint16
	Modified         time.Time
	CRC32            uint32
	CompressedSize   uint64
	UncompressedSize uint64
}

func (h *FileHeader) IsDir() bool {
	return strings.HasSuffix(h.Name, "/") || strings.HasSuffix(h.Name, "\\")
}

const readBufSize = 512 << 10

// Reader reads ZIP archives sequentially from an unseekable stream. Only one
// entry is live at a time, so the per-entry machinery below is allocated
// once and reused rather than per file.
type Reader struct {
	br       *bufio.Reader
	curr     *entryReader
	err      error
	entry    entryReader
	flate    io.ReadCloser
	limit    io.LimitedReader
	drainBuf []byte
	nameBuf  []byte
	extraBuf []byte
	raw      bool
}

// SetRaw makes Next hand back deflate entries of known size still compressed,
// with Entry reporting raw. The caller must then decompress and verify the
// CRC itself, which is what allows decoding to happen off this goroutine.
func (zr *Reader) SetRaw(raw bool) { zr.raw = raw }

// Raw reports whether the reader returned by the last Next is still
// compressed.
func (zr *Reader) Raw() bool { return zr.curr != nil && zr.curr.raw }

func (zr *Reader) grow(n int) []byte {
	if cap(zr.nameBuf) < n {
		zr.nameBuf = make([]byte, n)
	}
	return zr.nameBuf[:n]
}

func (zr *Reader) growExtra(n int) []byte {
	if cap(zr.extraBuf) < n {
		zr.extraBuf = make([]byte, n)
	}
	return zr.extraBuf[:n]
}

func NewReader(r io.Reader) *Reader {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReaderSize(r, readBufSize)
	}
	return &Reader{br: br}
}

func (zr *Reader) Next() (*FileHeader, io.Reader, error) {
	if zr.err != nil {
		return nil, nil, zr.err
	}
	if zr.curr != nil && !zr.curr.drained {
		if zr.drainBuf == nil {
			zr.drainBuf = make([]byte, 64<<10)
		}
		if err := zr.curr.drain(zr.drainBuf); err != nil {
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

	er, err := zr.newEntryReader(hdr)
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

	// Reused across entries: the name is copied into a string below and the
	// extra field is only parsed, so neither is retained.
	name := zr.grow(int(nLen))
	if _, err := io.ReadFull(zr.br, name); err != nil {
		return nil, err
	}
	extra := zr.growExtra(int(xLen))
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
	store   storeDescReader
	hasDesc bool
	shared  bool // r is the Reader's pooled decompressor, so do not close it
	raw     bool // r yields compressed bytes; the caller decodes and checks CRC
	crc     uint32
	drained bool
}

func (zr *Reader) newEntryReader(fh *FileHeader) (*entryReader, error) {
	// Sizes come from the archive, so a hostile value must be rejected rather
	// than wrap negative and make io.LimitReader read the entry as empty.
	if fh.CompressedSize > math.MaxInt64 {
		return nil, fmt.Errorf("%w: compressed size %d out of range", ErrFormat, fh.CompressedSize)
	}

	er := &zr.entry
	*er = entryReader{
		br:      zr.br,
		hdr:     fh,
		hasDesc: (fh.Flags & flagDataDesc) != 0,
	}

	// Bounded whenever the size is known, so this entry cannot read on into
	// the next one. A zero size must bound too: directory entries carry one,
	// and leaving them unbounded swallows the rest of the archive.
	src := io.Reader(zr.br)
	if !er.hasDesc {
		zr.limit = io.LimitedReader{R: zr.br, N: int64(fh.CompressedSize)}
		src = &zr.limit
	}

	// A deflate entry of known size can be handed over still compressed, so
	// the caller may decode it on another goroutine. CRC verification moves
	// to the caller with it.
	if zr.raw && fh.Method == MethodDeflate && !er.hasDesc && fh.CompressedSize > 0 {
		er.r = src
		er.raw = true
		return er, nil
	}

	switch fh.Method {
	case MethodStore:
		if er.hasDesc && fh.CompressedSize == 0 {
			er.store = storeDescReader{br: zr.br}
			er.r = &er.store
		} else {
			er.r = src
		}
	case MethodDeflate:
		// Reusing the decompressor keeps its 32KiB window and Huffman tables
		// off the allocator once per archive instead of once per entry.
		if zr.flate == nil {
			zr.flate = flate.NewReader(src)
		} else if err := zr.flate.(flate.Resetter).Reset(src, nil); err != nil {
			return nil, err
		}
		er.r = zr.flate
		er.shared = true
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
	if n > 0 && !er.raw {
		// Direct table update avoids the hash.Hash32 interface call per read;
		// Update dispatches to the hardware-accelerated IEEE path.
		er.crc = crc32.Update(er.crc, crc32.IEEETable, p[:n])
	}
	if errors.Is(err, io.EOF) {
		er.drained = true
		if c, ok := er.r.(io.Closer); ok && !er.shared {
			_ = c.Close()
		}
		if er.hasDesc {
			if err := er.readDesc(); err != nil {
				return n, err
			}
		}
		if !er.raw && er.hdr.CRC32 != 0 && er.hdr.CRC32 != er.crc {
			return n, fmt.Errorf("%w: entry %s", ErrChecksum, er.hdr.Name)
		}
		return n, io.EOF
	}
	return n, err
}

func (er *entryReader) drain(buf []byte) error {
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
	br      *bufio.Reader
	emitted int64
	done    bool
}

// descriptorAt reports whether a genuine data descriptor begins at idx by
// checking its uncompressed-size field against the bytes emitted so far.
// File content can contain the signature by chance -- roughly once per 4GiB
// of random data -- and without this check that coincidence silently
// truncates the entry.
func (s *storeDescReader) descriptorAt(idx int) bool {
	const descLen = 16 // signature + crc32 + compressed + uncompressed
	buf, err := s.br.Peek(idx + descLen)
	if err != nil || len(buf) < idx+descLen {
		return true // at end of stream there is nothing left to confuse it
	}
	return int64(le.Uint32(buf[idx+12:idx+16])) == s.emitted+int64(idx)
}

// Read scans the buffered window for the data-descriptor signature to find
// where this STORE entry ends, since its size isn't known up front.
func (s *storeDescReader) Read(p []byte) (int, error) {
	if s.done {
		return 0, io.EOF
	}
	buf, err := s.br.Peek(s.br.Buffered())
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}

	for search := 0; ; {
		idx := bytes.Index(buf[search:], dataDescSig)
		if idx < 0 {
			break
		}
		idx += search
		if !s.descriptorAt(idx) {
			search = idx + 1 // coincidence in the data; keep looking
			continue
		}
		if idx == 0 {
			s.done = true
			return 0, io.EOF
		}
		return s.emit(p, idx)
	}
	// Stop three bytes short so a signature straddling the window boundary is
	// not missed on the next pass.
	safe := len(buf) - 3
	if safe <= 0 {
		b, err := s.br.ReadByte()
		if err != nil {
			return 0, err
		}
		p[0] = b
		s.emitted++
		return 1, nil
	}
	return s.emit(p, safe)
}

func (s *storeDescReader) emit(p []byte, limit int) (int, error) {
	n, err := s.br.Read(p[:min(len(p), limit)])
	s.emitted += int64(n)
	return n, err
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

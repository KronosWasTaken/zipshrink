package streamzip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
	"time"
)

// A zip64 size above MaxInt64 must be rejected: converting it to int64 wraps
// negative, and io.LimitReader would then read the entry as empty instead.
func TestNextRejectsOversizedCompressedSize(t *testing.T) {
	const name = "huge.bin"
	var buf bytes.Buffer

	buf.Write([]byte{0x50, 0x4b, 0x03, 0x04})
	hdr := make([]byte, 26)
	binary.LittleEndian.PutUint16(hdr[4:6], MethodStore)
	binary.LittleEndian.PutUint32(hdr[14:18], 0xFFFFFFFF)
	binary.LittleEndian.PutUint32(hdr[18:22], 0xFFFFFFFF)
	binary.LittleEndian.PutUint16(hdr[22:24], uint16(len(name)))
	binary.LittleEndian.PutUint16(hdr[24:26], 20)
	buf.Write(hdr)
	buf.WriteString(name)

	extra := make([]byte, 4)
	binary.LittleEndian.PutUint16(extra[0:2], 0x0001)
	binary.LittleEndian.PutUint16(extra[2:4], 16)
	extra = binary.LittleEndian.AppendUint64(extra, 0)
	extra = binary.LittleEndian.AppendUint64(extra, math.MaxUint64)
	buf.Write(extra)

	if _, _, err := NewReader(bytes.NewReader(buf.Bytes())).Next(); !errors.Is(err, ErrFormat) {
		t.Fatalf("got %v, want ErrFormat", err)
	}
}

func TestParseZip64(t *testing.T) {
	const sentinel = 0xFFFFFFFF

	zip64Extra := func(uSize, cSize uint64) []byte {
		b := make([]byte, 4, 20)
		binary.LittleEndian.PutUint16(b[0:2], 0x0001)
		binary.LittleEndian.PutUint16(b[2:4], 16)
		return binary.LittleEndian.AppendUint64(binary.LittleEndian.AppendUint64(b, uSize), cSize)
	}

	t.Run("replaces both sentinels", func(t *testing.T) {
		gotU, gotC := parseZip64(zip64Extra(1<<33, 1<<34), sentinel, sentinel)
		if gotU != 1<<33 || gotC != 1<<34 {
			t.Errorf("got (%d, %d), want (%d, %d)", gotU, gotC, uint64(1<<33), uint64(1<<34))
		}
	})

	t.Run("keeps 32-bit sizes that are not sentinels", func(t *testing.T) {
		gotU, gotC := parseZip64(zip64Extra(1<<33, 1<<34), 500, 400)
		if gotU != 500 || gotC != 400 {
			t.Errorf("got (%d, %d), want (500, 400)", gotU, gotC)
		}
	})

	t.Run("ignores unrelated and truncated extra fields", func(t *testing.T) {
		unrelated := []byte{0x99, 0x99, 0x04, 0x00, 1, 2, 3, 4}
		if gotU, gotC := parseZip64(unrelated, 7, 9); gotU != 7 || gotC != 9 {
			t.Errorf("unrelated field changed sizes: (%d, %d)", gotU, gotC)
		}
		truncated := []byte{0x01, 0x00, 0x10, 0x00, 1, 2}
		if gotU, gotC := parseZip64(truncated, sentinel, sentinel); gotU != sentinel || gotC != sentinel {
			t.Errorf("truncated field was parsed: (%d, %d)", gotU, gotC)
		}
	})
}

func TestMSDOSTime(t *testing.T) {
	// 2026-09-08 14:23:44 -- seconds are stored in 2-second units.
	date := uint16((2026-1980)<<9 | 9<<5 | 8)
	clock := uint16(14<<11 | 23<<5 | 22)

	got := msdosTime(date, clock)
	want := time.Date(2026, time.September, 8, 14, 23, 44, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("got %s, want %s", got, want)
	}

	// Year is stored as year-1980, so a zero year field is the epoch itself.
	epoch := msdosTime(uint16(1<<5|1), 0)
	if wantEpoch := time.Date(1980, time.January, 1, 0, 0, 0, 0, time.Local); !epoch.Equal(wantEpoch) {
		t.Errorf("epoch: got %s, want %s", epoch, wantEpoch)
	}

	// An out-of-range month must yield the zero time so callers skip it
	// rather than stamping a bogus timestamp on the extracted file.
	if ts := msdosTime(uint16(13<<5|1), clock); !ts.IsZero() {
		t.Errorf("month 13 gave %s, want zero time", ts)
	}
	if ts := msdosTime(uint16(5<<5), clock); !ts.IsZero() { // day bits zero
		t.Errorf("day 0 gave %s, want zero time", ts)
	}
}

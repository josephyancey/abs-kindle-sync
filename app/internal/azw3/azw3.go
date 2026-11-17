// Package azw3 reads the text stream of a Kindle AZW3 (KF8) book.
//
// Amazon's Kindle reader addresses a position in an AZW3 as a byte offset into the book's
// decompressed text records (calibre calls this stream "rawml"): opening a book at
// `lastReadPosition=1316` lands on the first visible text after 1316 bytes of markup.
package azw3

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	compressionNone    = 1
	compressionPalmDoc = 2
	compressionHuff    = 17480
)

// ErrUnsupported is returned for books this package can't read (DRM, HUFF/CDIC compression).
var ErrUnsupported = errors.New("azw3: unsupported book")

// ReadFile returns the decompressed text stream of the AZW3 at path. It reads only the records
// it needs: books with artwork can be over 100 MB, most of it images.
func ReadFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	count, rec, err := fileRecords(f)
	if err != nil {
		return nil, err
	}
	return text(count, rec)
}

// fileRecords reads a Palm database's record list and returns a reader for single records.
func fileRecords(f *os.File) (int, func(int) []byte, error) {
	fi, err := f.Stat()
	if err != nil {
		return 0, nil, err
	}
	head := make([]byte, 78)
	if _, err := f.ReadAt(head, 0); err != nil {
		return 0, nil, fmt.Errorf("azw3: not a Palm database")
	}
	n := int(binary.BigEndian.Uint16(head[76:78]))
	list := make([]byte, n*8)
	if _, err := f.ReadAt(list, 78); err != nil {
		return 0, nil, fmt.Errorf("azw3: truncated record list")
	}
	offsets := make([]uint32, n)
	for i := range offsets {
		offsets[i] = binary.BigEndian.Uint32(list[i*8:])
	}
	size := fi.Size()
	return n, func(i int) []byte {
		if i < 0 || i >= n {
			return nil
		}
		start, end := int64(offsets[i]), size
		if i+1 < n {
			end = int64(offsets[i+1])
		}
		if start > end || end > size || end-start > 32<<20 {
			return nil
		}
		b := make([]byte, end-start)
		if _, err := f.ReadAt(b, start); err != nil && err != io.EOF {
			return nil
		}
		return b
	}, nil
}

// Text returns the decompressed text stream of an AZW3 (or a MOBI's KF8 part).
func Text(data []byte) ([]byte, error) {
	records, err := palmRecords(data)
	if err != nil {
		return nil, err
	}
	return text(len(records), func(i int) []byte { return record(data, records, i) })
}

func text(count int, rec func(int) []byte) ([]byte, error) {
	first := 0
	// A combined MOBI7 + KF8 file keeps its KF8 header after a boundary record; prefer it.
	if boundary := kf8Boundary(count, rec); boundary > 0 {
		first = boundary
	}
	header := rec(first)
	if len(header) < 16 {
		return nil, fmt.Errorf("azw3: header record too short")
	}
	compression := int(binary.BigEndian.Uint16(header[0:2]))
	textLength := int(binary.BigEndian.Uint32(header[4:8]))
	textRecords := int(binary.BigEndian.Uint16(header[8:10]))
	encryption := int(binary.BigEndian.Uint16(header[12:14]))
	if encryption != 0 {
		return nil, fmt.Errorf("%w: encrypted (DRM)", ErrUnsupported)
	}
	if compression == compressionHuff {
		return nil, fmt.Errorf("%w: HUFF/CDIC compression", ErrUnsupported)
	}
	if compression != compressionNone && compression != compressionPalmDoc {
		return nil, fmt.Errorf("%w: compression %d", ErrUnsupported, compression)
	}
	extraFlags := 0
	// Offsets below are from the start of record 0 (the MOBI header begins at 0x10)
	if len(header) >= 0xF4 && bytes.Equal(header[16:20], []byte("MOBI")) {
		headerLength := int(binary.BigEndian.Uint32(header[20:24]))
		if headerLength >= 0xE4 {
			extraFlags = int(binary.BigEndian.Uint16(header[0xF2:0xF4]))
		}
	}

	out := make([]byte, 0, textLength)
	for i := 1; i <= textRecords; i++ {
		raw := stripTrailing(rec(first+i), extraFlags)
		if compression == compressionPalmDoc {
			raw = palmDoc(raw)
		}
		out = append(out, raw...)
	}
	if len(out) > textLength {
		out = out[:textLength]
	}
	return out, nil
}

func palmRecords(data []byte) ([]uint32, error) {
	if len(data) < 78 {
		return nil, fmt.Errorf("azw3: not a Palm database")
	}
	n := int(binary.BigEndian.Uint16(data[76:78]))
	if len(data) < 78+n*8 {
		return nil, fmt.Errorf("azw3: truncated record list")
	}
	offsets := make([]uint32, n)
	for i := 0; i < n; i++ {
		offsets[i] = binary.BigEndian.Uint32(data[78+i*8:])
	}
	return offsets, nil
}

func record(data []byte, offsets []uint32, i int) []byte {
	if i < 0 || i >= len(offsets) {
		return nil
	}
	start := int(offsets[i])
	end := len(data)
	if i+1 < len(offsets) {
		end = int(offsets[i+1])
	}
	if start > end || end > len(data) {
		return nil
	}
	return data[start:end]
}

// kf8Boundary finds the KF8 header record of a combined file from EXTH record 121.
func kf8Boundary(records int, record func(int) []byte) int {
	rec := record(0)
	if len(rec) < 16+0x84 || !bytes.Equal(rec[16:20], []byte("MOBI")) {
		return 0
	}
	if version := binary.BigEndian.Uint32(rec[16+0x14:]); version == 8 {
		return 0
	}
	headerLength := int(binary.BigEndian.Uint32(rec[20:24]))
	exthFlags := binary.BigEndian.Uint32(rec[16+0x70:])
	if exthFlags&0x40 == 0 {
		return 0
	}
	exth := rec[16+headerLength:]
	if len(exth) < 12 || !bytes.Equal(exth[0:4], []byte("EXTH")) {
		return 0
	}
	count := int(binary.BigEndian.Uint32(exth[8:12]))
	pos := 12
	for i := 0; i < count && pos+8 <= len(exth); i++ {
		kind := binary.BigEndian.Uint32(exth[pos:])
		size := int(binary.BigEndian.Uint32(exth[pos+4:]))
		if size < 8 || pos+size > len(exth) {
			break
		}
		if kind == 121 && size >= 12 {
			if raw := binary.BigEndian.Uint32(exth[pos+8:]); raw != 0xFFFFFFFF && raw > 0 && raw < uint32(records) {
				return int(raw)
			}
		}
		pos += size
	}
	return 0
}

// stripTrailing removes the extra data entries MOBI appends to each text record.
func stripTrailing(rec []byte, flags int) []byte {
	for bit := 15; bit >= 1; bit-- {
		if flags&(1<<bit) == 0 {
			continue
		}
		size := trailingSize(rec)
		if size > len(rec) {
			return rec[:0]
		}
		rec = rec[:len(rec)-size]
	}
	if flags&1 != 0 && len(rec) > 0 {
		size := int(rec[len(rec)-1]&3) + 1
		if size > len(rec) {
			return rec[:0]
		}
		rec = rec[:len(rec)-size]
	}
	return rec
}

// trailingSize reads a backward-encoded variable-width integer from the end of rec.
func trailingSize(rec []byte) int {
	size := 0
	shift := 0
	for i := len(rec) - 1; i >= 0 && i >= len(rec)-4; i-- {
		b := int(rec[i])
		size |= (b & 0x7F) << shift
		shift += 7
		if b&0x80 != 0 {
			break
		}
	}
	return size
}

// palmDoc decompresses one PalmDOC (LZ77) record.
func palmDoc(in []byte) []byte {
	out := make([]byte, 0, 4096)
	for i := 0; i < len(in); i++ {
		c := in[i]
		switch {
		case c == 0 || (c >= 0x09 && c <= 0x7F):
			out = append(out, c)
		case c >= 0x01 && c <= 0x08:
			n := int(c)
			if i+n >= len(in) {
				n = len(in) - i - 1
			}
			out = append(out, in[i+1:i+1+n]...)
			i += n
		case c >= 0xC0:
			out = append(out, ' ', c^0x80)
		default: // 0x80..0xBF: back reference
			if i+1 >= len(in) {
				return out
			}
			pair := int(c)<<8 | int(in[i+1])
			i++
			distance := (pair >> 3) & 0x07FF
			length := (pair & 7) + 3
			if distance == 0 || distance > len(out) {
				continue
			}
			start := len(out) - distance
			for j := 0; j < length; j++ {
				out = append(out, out[start+j])
			}
		}
	}
	return out
}

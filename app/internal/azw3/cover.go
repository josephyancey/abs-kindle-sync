package azw3

import (
	"bytes"
	"encoding/binary"
	"os"
)

// Info is what the Kindle's library needs from a book's header.
type Info struct {
	ASIN    string // EXTH 113; the library files the cover thumbnail under it
	CDEType string // EXTH 501, e.g. EBOK
	Cover   []byte // the embedded cover image (JPEG/PNG/GIF), if the book names one
}

// ReadInfoFile is ReadInfo for a file, reading only the parts it needs rather than the whole book.
func ReadInfoFile(path string) (Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return Info{}, err
	}
	defer f.Close()
	count, rec, err := fileRecords(f)
	if err != nil {
		return Info{}, err
	}
	return readInfo(count, rec)
}

// ReadInfo reads the ASIN, content type and cover image from a MOBI/AZW3 file.
func ReadInfo(data []byte) (Info, error) {
	offsets, err := palmRecords(data)
	if err != nil {
		return Info{}, err
	}
	return readInfo(len(offsets), func(i int) []byte { return record(data, offsets, i) })
}

func readInfo(records int, record func(int) []byte) (Info, error) {
	var info Info
	rec := record(0)
	if len(rec) < 16+0x84 || !bytes.Equal(rec[16:20], []byte("MOBI")) {
		return info, nil
	}
	firstImage := binary.BigEndian.Uint32(rec[16+0x5C:])
	headerLength := int(binary.BigEndian.Uint32(rec[20:24]))
	if binary.BigEndian.Uint32(rec[16+0x70:])&0x40 == 0 || 16+headerLength+12 > len(rec) {
		return info, nil
	}
	exth := rec[16+headerLength:]
	if !bytes.Equal(exth[0:4], []byte("EXTH")) {
		return info, nil
	}
	cover, thumb := uint32(0xFFFFFFFF), uint32(0xFFFFFFFF)
	count := int(binary.BigEndian.Uint32(exth[8:12]))
	pos := 12
	for i := 0; i < count && pos+8 <= len(exth); i++ {
		kind := binary.BigEndian.Uint32(exth[pos:])
		size := int(binary.BigEndian.Uint32(exth[pos+4:]))
		if size < 8 || pos+size > len(exth) {
			break
		}
		value := exth[pos+8 : pos+size]
		switch kind {
		case 113:
			info.ASIN = string(value)
		case 501:
			info.CDEType = string(value)
		case 201:
			if len(value) >= 4 {
				cover = binary.BigEndian.Uint32(value)
			}
		case 202:
			if len(value) >= 4 {
				thumb = binary.BigEndian.Uint32(value)
			}
		}
		pos += size
	}
	if firstImage == 0xFFFFFFFF {
		return info, nil
	}
	for _, offset := range []uint32{cover, thumb} {
		if offset == 0xFFFFFFFF || uint64(firstImage)+uint64(offset) >= uint64(records) {
			continue
		}
		img := record(int(firstImage + offset))
		if isImage(img) {
			info.Cover = img
			return info, nil
		}
	}
	return info, nil
}

func isImage(b []byte) bool {
	return bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}) ||
		bytes.HasPrefix(b, []byte("\x89PNG")) ||
		bytes.HasPrefix(b, []byte("GIF8"))
}

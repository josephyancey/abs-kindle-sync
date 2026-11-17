// Package posmap maps reading positions between an EPUB (what Audiobookshelf stores) and its
// AZW3 copy (what Amazon's Kindle reader opens).
//
// Both files carry the same text in the same order, so every EPUB text run is located in the
// AZW3's text stream once, when a book is first synced, and the result is cached. Within a
// run, positions are treated as linear; the Kindle snaps to the start of the page anyway.
package posmap

import (
	"encoding/json"
	"html"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/josephyancey/abs-kindle-sync/app/internal/epub"
)

// Version changes when the map format or the way it's built changes, invalidating caches.
const Version = 1

// Entry is one EPUB text run and where its text starts in the Kindle stream.
type Entry struct {
	CFI         string `json:"c"`
	ChunkOffset int    `json:"o,omitempty"`
	Start       int    `json:"s"`
	Length      int    `json:"l"`
	Kindle      int    `json:"k"` // -1 when the run wasn't found
}

// Map is the position map of one book.
type Map struct {
	Version      int     `json:"version"`
	EpubLength   int     `json:"epub_length"`
	KindleLength int     `json:"kindle_length"`
	Matched      int     `json:"matched"`
	Entries      []Entry `json:"entries"`
}

// Build aligns an EPUB's runs with an AZW3 text stream.
func Build(book *epub.Book, kindleStream []byte) *Map {
	ktext, koffsets := visibleText(kindleStream)
	m := &Map{Version: Version, EpubLength: book.Length, KindleLength: len(kindleStream)}
	cursor := 0 // index into ktext (runes)
	for _, run := range book.Runs {
		needle := stripSpace(run.Text)
		if needle == "" {
			continue // whitespace between paragraphs: nothing a reader can sit on
		}
		probe := needle
		if utf8.RuneCountInString(probe) > 40 {
			probe = string([]rune(probe)[:40])
		}
		entry := Entry{CFI: run.CFI, ChunkOffset: run.ChunkOffset, Start: run.Start, Length: len(utf16(run.Text)), Kindle: -1}
		if found := indexFrom(ktext, []rune(probe), cursor, 200000); found >= 0 {
			entry.Kindle = koffsets[found]
			cursor = found + utf8.RuneCountInString(needle)
			m.Matched++
		}
		m.Entries = append(m.Entries, entry)
	}
	return m
}

// ToABS turns a Kindle position into an ABS ebookLocation (CFI) and ebookProgress.
func (m *Map) ToABS(kindlePos int) (cfi string, progress float64, ok bool) {
	i := sort.Search(len(m.Entries), func(i int) bool { return m.Entries[i].Kindle > kindlePos && m.Entries[i].Kindle >= 0 })
	// Walk back to the last matched entry at or before the position
	j := i - 1
	for j >= 0 && m.Entries[j].Kindle < 0 {
		j--
	}
	if j < 0 {
		if len(m.Entries) == 0 {
			return "", 0, false
		}
		j = 0
	}
	e := m.Entries[j]
	inside := kindlePos - e.Kindle
	if inside < 0 {
		inside = 0
	}
	if inside >= e.Length {
		// The position is in markup after this run (a page starting on a new paragraph):
		// use the start of the next matched run.
		for k := j + 1; k < len(m.Entries); k++ {
			if m.Entries[k].Kindle >= 0 {
				e, inside = m.Entries[k], 0
				break
			}
		}
		if inside >= e.Length {
			inside = e.Length - 1
		}
	}
	cfi = e.CFI + ":" + strconv.Itoa(e.ChunkOffset+inside) + ")"
	if m.EpubLength > 0 {
		progress = float64(e.Start+inside) / float64(m.EpubLength)
	}
	return cfi, progress, true
}

// ToKindle turns an ABS position into a Kindle position. A CFI is used when it names a
// text run in this book and agrees with progress; otherwise progress (0..1) is placed by
// character count. Some clients update only ebookProgress, which leaves an old CFI behind.
func (m *Map) ToKindle(cfi string, progress float64) (int, bool) {
	if pos, ok := m.fromCFI(cfi); ok {
		if _, cfiProgress, ok := m.ToABS(pos); ok && math.Abs(cfiProgress-progress) <= 0.02 {
			return pos, true
		}
	}
	if m.EpubLength == 0 || len(m.Entries) == 0 {
		return 0, false
	}
	target := int(progress * float64(m.EpubLength))
	i := sort.Search(len(m.Entries), func(i int) bool { return m.Entries[i].Start > target }) - 1
	if i < 0 {
		i = 0
	}
	for i < len(m.Entries) && m.Entries[i].Kindle < 0 {
		i++
	}
	if i >= len(m.Entries) {
		return 0, false
	}
	e := m.Entries[i]
	inside := target - e.Start
	if inside < 0 || inside >= e.Length {
		inside = 0
	}
	return e.Kindle + inside, true
}

var cfiAssertion = regexp.MustCompile(`\[[^\]]*\]`)

// fromCFI matches a CFI like epubcfi(/6/14[ch2]!/4/2/16/1:245) to a run by its step path.
func (m *Map) fromCFI(cfi string) (int, bool) {
	cfi = strings.TrimSpace(cfi)
	if !strings.HasPrefix(cfi, "epubcfi(") {
		return 0, false
	}
	body := strings.TrimSuffix(strings.TrimPrefix(cfi, "epubcfi("), ")")
	// Range CFIs (parent,start,end): use the start
	if parts := strings.Split(body, ","); len(parts) == 3 {
		body = parts[0] + parts[1]
	}
	body = cfiAssertion.ReplaceAllString(body, "")
	path, offset := body, 0
	if i := strings.LastIndex(body, ":"); i >= 0 {
		path = body[:i]
		offset, _ = strconv.Atoi(strings.TrimRightFunc(body[i+1:], func(r rune) bool { return !unicode.IsDigit(r) }))
	}
	// A CFI pointing at an element (even last step) means its first text: append "/1"
	steps := strings.Split(path, "/")
	if last := steps[len(steps)-1]; last != "" {
		if n, err := strconv.Atoi(last); err == nil && n%2 == 0 {
			path += "/1"
		}
	}
	best := -1
	for i, e := range m.Entries {
		if e.Kindle < 0 {
			continue
		}
		ep := cfiAssertion.ReplaceAllString(strings.TrimPrefix(e.CFI, "epubcfi("), "")
		if ep == path && e.ChunkOffset <= offset {
			best = i
		} else if best >= 0 && !strings.HasPrefix(ep, path) {
			if ep > path && best >= 0 {
				break
			}
		}
	}
	if best < 0 {
		// Element CFIs may point above the run (e.g. at a paragraph): take the first run inside it
		prefix := strings.TrimSuffix(path, "/1") + "/"
		for _, e := range m.Entries {
			if e.Kindle >= 0 && strings.HasPrefix(cfiAssertion.ReplaceAllString(strings.TrimPrefix(e.CFI, "epubcfi("), ""), prefix) {
				return e.Kindle, true
			}
		}
		return 0, false
	}
	e := m.Entries[best]
	inside := offset - e.ChunkOffset
	if inside >= e.Length {
		inside = e.Length - 1
	}
	if inside < 0 {
		inside = 0
	}
	return e.Kindle + inside, true
}

// Save writes the map as JSON.
func (m *Map) Save(file string) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

// Load reads a map saved by Save; a map from another Version is rejected.
func Load(file string) (*Map, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var m Map
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	if m.Version != Version {
		return nil, os.ErrNotExist
	}
	return &m, nil
}

var textNode = regexp.MustCompile(`>([^<]+)<`)
var entity = regexp.MustCompile(`&#?[A-Za-z0-9]+;`)

// visibleText is the stream's text without whitespace, and the byte offset of each rune.
func visibleText(stream []byte) ([]rune, []int) {
	var chars []rune
	var offsets []int
	for _, loc := range textNode.FindAllSubmatchIndex(stream, -1) {
		start, end := loc[2], loc[3]
		raw := stream[start:end]
		i := 0
		for i < len(raw) {
			if raw[i] == '&' {
				if m := entity.Find(raw[i:]); m != nil && len(m) > 0 && raw[i] == '&' && bytesHasPrefix(raw[i:], m) {
					for _, r := range html.UnescapeString(string(m)) {
						if !unicode.IsSpace(r) {
							chars = append(chars, r)
							offsets = append(offsets, start+i)
						}
					}
					i += len(m)
					continue
				}
			}
			r, size := utf8.DecodeRune(raw[i:])
			if !unicode.IsSpace(r) {
				chars = append(chars, r)
				offsets = append(offsets, start+i)
			}
			i += size
		}
	}
	return chars, offsets
}

func bytesHasPrefix(b, prefix []byte) bool {
	return len(b) >= len(prefix) && string(b[:len(prefix)]) == string(prefix)
}

func stripSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

func utf16(s string) []uint16 {
	out := make([]uint16, 0, len(s))
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}

// indexFrom finds needle in hay starting at from, looking at most window runes ahead.
func indexFrom(hay, needle []rune, from, window int) int {
	if len(needle) == 0 {
		return -1
	}
	end := from + window
	if end > len(hay) {
		end = len(hay)
	}
	for i := from; i+len(needle) <= end; i++ {
		if hay[i] != needle[0] {
			continue
		}
		match := true
		for j := 1; j < len(needle); j++ {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

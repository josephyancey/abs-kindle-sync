// Package epub lists the text of an EPUB as runs addressed by EPUB CFI, the way
// Audiobookshelf's web reader (epub.js) and BookBridge address positions.
//
// Text is counted over every text node under <body> in document order, whitespace
// included, with <script> and <style> skipped. CFI numbering: element children take even
// steps (2, 4, ...), the text before, between and after them odd steps; a comment's text
// stays in the chunk it interrupts.
package epub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// Run is one stretch of text in a single DOM text chunk.
type Run struct {
	// CFI is the CFI of the run's first character without the character offset, e.g.
	// "epubcfi(/6/8[ch2]!/4/12/1". Append ":<offset>)" for a full CFI.
	CFI string
	// ChunkOffset is the run's offset within its text chunk (non-zero only after a comment).
	ChunkOffset int
	// Start is the run's position in the whole book's text.
	Start int
	Text  string
}

// Book is an EPUB's text runs in reading order.
type Book struct {
	Runs   []Run
	Length int
}

var containerPath = regexp.MustCompile(`full-path="([^"]+)"`)

// ReadFile lists the runs of the EPUB at path.
func ReadFile(file string) (*Book, error) {
	archive, err := zip.OpenReader(file)
	if err != nil {
		return nil, err
	}
	defer archive.Close()
	files := map[string]*zip.File{}
	for _, f := range archive.File {
		files[f.Name] = f
	}
	read := func(name string) ([]byte, error) {
		f, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("epub: %s not found", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}

	container, err := read("META-INF/container.xml")
	if err != nil {
		return nil, err
	}
	m := containerPath.FindSubmatch(container)
	if m == nil {
		return nil, fmt.Errorf("epub: no rootfile in container.xml")
	}
	opfPath := string(m[1])
	opf, err := read(opfPath)
	if err != nil {
		return nil, err
	}
	spine, err := parseSpine(opf)
	if err != nil {
		return nil, err
	}

	book := &Book{}
	cursor := 0
	opfDir := path.Dir(opfPath)
	for index, item := range spine {
		if item.href == "" {
			continue
		}
		href := strings.SplitN(item.href, "#", 2)[0]
		name := path.Clean(path.Join(opfDir, href))
		if decoded, err := url.PathUnescape(href); err == nil {
			if _, ok := files[path.Clean(path.Join(opfDir, decoded))]; ok {
				name = path.Clean(path.Join(opfDir, decoded))
			}
		}
		doc, err := read(name)
		if err != nil {
			continue
		}
		prefix := fmt.Sprintf("epubcfi(/6/%d[%s]!", (index+1)*2, item.idref)
		runs, next := chapterRuns(doc, prefix, cursor)
		book.Runs = append(book.Runs, runs...)
		cursor = next
	}
	book.Length = cursor
	return book, nil
}

type spineItem struct{ idref, href string }

func parseSpine(opf []byte) ([]spineItem, error) {
	manifest := map[string]string{}
	var idrefs []string
	dec := xml.NewDecoder(bytes.NewReader(opf))
	dec.Strict = false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("epub: reading OPF: %w", err)
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		attr := func(name string) string {
			for _, a := range el.Attr {
				if a.Name.Local == name {
					return a.Value
				}
			}
			return ""
		}
		switch el.Name.Local {
		case "item":
			if id, href := attr("id"), attr("href"); id != "" && href != "" {
				manifest[id] = href
			}
		case "itemref":
			if id := attr("idref"); id != "" {
				idrefs = append(idrefs, id)
			}
		}
	}
	spine := make([]spineItem, len(idrefs))
	for i, id := range idrefs {
		spine[i] = spineItem{idref: id, href: manifest[id]}
	}
	return spine, nil
}

// frame is one open element while walking a chapter.
type frame struct {
	path      string      // CFI steps to this element, e.g. "/4/12"
	children  int         // element children seen so far
	chunkUsed map[int]int // characters already recorded per text chunk
	skip      bool        // inside <script>/<style>
}

func chapterRuns(doc []byte, prefix string, cursor int) ([]Run, int) {
	dec := xml.NewDecoder(bytes.NewReader(doc))
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity

	var runs []Run
	var stack []*frame
	inBody := false
	depth := 0 // element depth from the document root
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			name := strings.ToLower(t.Name.Local)
			if !inBody {
				// The body is the root's child; its step is its position among the root's element children.
				if depth == 2 && name == "body" {
					inBody = true
					stack = []*frame{{path: fmt.Sprintf("/%d", bodyStep(doc)), chunkUsed: map[int]int{}}}
				}
				continue
			}
			parent := stack[len(stack)-1]
			parent.children++
			stack = append(stack, &frame{
				path:      fmt.Sprintf("%s/%d", parent.path, parent.children*2),
				chunkUsed: map[int]int{},
				skip:      parent.skip || name == "script" || name == "style",
			})
		case xml.EndElement:
			depth--
			if !inBody {
				continue
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return runs, cursor
			}
		case xml.CharData:
			if !inBody || len(stack) == 0 {
				continue
			}
			f := stack[len(stack)-1]
			if f.skip {
				continue
			}
			text := string(t)
			if text == "" {
				continue
			}
			chunk := f.children*2 + 1
			already := f.chunkUsed[chunk]
			runs = append(runs, Run{CFI: fmt.Sprintf("%s%s/%d", prefix, f.path, chunk), ChunkOffset: already, Start: cursor, Text: text})
			f.chunkUsed[chunk] = already + utf16Len(text)
			cursor += utf16Len(text)
		}
	}
	return runs, cursor
}

// bodyStep finds <body>'s CFI step: 2 x its position among the root element's children.
func bodyStep(doc []byte) int {
	dec := xml.NewDecoder(bytes.NewReader(doc))
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity
	depth, children := 0, 0
	for {
		tok, err := dec.Token()
		if err != nil {
			return 4
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth == 2 {
				children++
				if strings.ToLower(t.Name.Local) == "body" {
					return children * 2
				}
			}
		case xml.EndElement:
			depth--
		}
	}
}

// utf16Len counts characters the way browsers (and epub.js CFIs) do: UTF-16 code units.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

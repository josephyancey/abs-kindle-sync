// posmap-check builds a position map for an EPUB and its AZW3 copy and prints a summary.
// Used to check the Go mapping against tools/kindle_map.py (calibre).
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/josephyancey/abs-kindle-sync/app/internal/azw3"
	"github.com/josephyancey/abs-kindle-sync/app/internal/epub"
	"github.com/josephyancey/abs-kindle-sync/app/internal/posmap"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: posmap-check book.epub book.azw3 [kindle positions...]")
		os.Exit(2)
	}
	book, err := epub.ReadFile(os.Args[1])
	if err != nil {
		fmt.Println("epub:", err)
		os.Exit(1)
	}
	stream, err := azw3.ReadFile(os.Args[2])
	if err != nil {
		fmt.Println("azw3:", err)
		os.Exit(1)
	}
	m := posmap.Build(book, stream)
	fmt.Printf("epub chars %d, runs %d, matched %d, kindle stream %d bytes\n", m.EpubLength, len(m.Entries), m.Matched, m.KindleLength)
	for _, arg := range os.Args[3:] {
		pos, _ := strconv.Atoi(arg)
		cfi, progress, _ := m.ToABS(pos)
		back, _ := m.ToKindle(cfi, progress)
		fmt.Printf("kindle %d -> %s (%.4f%%) -> kindle %d\n", pos, cfi, progress*100, back)
	}
	if len(os.Args) > 3 {
		_ = m.Save("map.json")
	}
}

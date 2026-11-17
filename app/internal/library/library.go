// Package library copies Audiobookshelf books that have an AZW3 onto the Kindle and builds
// each book's position map.
package library

import (
	"fmt"
	"github.com/josephyancey/abs-kindle-sync/app/internal/cover"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/josephyancey/abs-kindle-sync/app/internal/abs"
	"github.com/josephyancey/abs-kindle-sync/app/internal/azw3"
	"github.com/josephyancey/abs-kindle-sync/app/internal/epub"
	"github.com/josephyancey/abs-kindle-sync/app/internal/posmap"
	"github.com/josephyancey/abs-kindle-sync/app/internal/store"
)

// Result summarises one library sync.
type Result struct {
	Added, Updated, Mapped, Unchanged, Failed, Removed int
}

// Sync brings the Kindle's copy of the user's Kindle-readable books up to date.
func Sync(client *abs.Client, cfg *store.Config, st *store.State, s *store.Store) (Result, error) {
	var res Result
	libraries, err := client.Libraries()
	if err != nil {
		return res, err
	}
	wanted := map[string]bool{}
	for _, id := range cfg.Libraries {
		wanted[id] = true
	}
	if err := os.MkdirAll(cfg.Folder, 0o755); err != nil {
		return res, err
	}
	if err := os.MkdirAll(filepath.Dir(s.MapPath("x")), 0o755); err != nil {
		return res, err
	}
	present := map[string]bool{} // every ebook item in the libraries being synced
	for _, lib := range libraries {
		if len(wanted) > 0 && !wanted[lib.ID] {
			continue
		}
		summaries, err := client.Ebooks(lib.ID)
		if err != nil {
			return res, fmt.Errorf("listing %s: %w", lib.Name, err)
		}
		for _, sum := range summaries {
			present[sum.ID] = true
		}
		for _, sum := range summaries {
			st.Mu.Lock()
			seen, checked := st.Checked[sum.ID]
			_, synced := st.Books[sum.ID]
			st.Mu.Unlock()
			if checked && seen == sum.UpdatedAt && (!synced || bookFileExists(st, sum.ID)) {
				res.Unchanged++
				continue
			}
			it, err := client.Item(sum.ID)
			if err != nil {
				log.Printf("library: %s: %v", sum.ID, err)
				res.Failed++
				continue
			}
			if it.KindleFile() != nil {
				if err := syncItem(client, cfg, st, s, it, &res); err != nil {
					log.Printf("library: %q: %v", it.Title(), err)
					res.Failed++
					continue
				}
			}
			st.Mu.Lock()
			st.Checked[sum.ID] = sum.UpdatedAt
			_ = s.SaveState(st)
			st.Mu.Unlock()
			debug.FreeOSMemory() // hand a big book's memory back to the Kindle before the next
		}
	}

	// Books gone from Audiobookshelf, or in a library no longer synced: remove the app's copy.
	// Only reached when every listing above succeeded, so a network blip can't delete books.
	st.Mu.Lock()
	for id, b := range st.Books {
		if !present[id] {
			log.Printf("library: %q is no longer in the synced libraries, removing it", b.Title)
			s.Remove(st, id)
			res.Removed++
		}
	}
	_ = s.SaveState(st)
	st.Mu.Unlock()
	ensureCovers(client, st, s)
	return res, nil
}

// syncItem downloads and maps one book. The state lock is held only while reading or
// updating the book's record, never during downloads.
func syncItem(client *abs.Client, cfg *store.Config, st *store.State, s *store.Store, it *abs.Item, res *Result) error {
	kf := it.KindleFile()
	ef := it.EpubFile()

	st.Mu.Lock()
	book := st.Books[it.ID]
	isNew := book == nil
	if isNew {
		book = &store.Book{ItemID: it.ID, Pending: -1, LastPosition: -1}
		st.Books[it.ID] = book
	}
	book.Title, book.Author = it.Title(), it.Author()
	if book.Path == "" {
		book.Path = uniquePath(cfg.Folder, fileName(it), st, it.ID)
	}
	path := book.Path
	changed := book.KindleIno != kf.Ino || book.KindleMtime != kf.Metadata.MtimeMs
	needMap := ef != nil && (!book.HasMap || book.EpubIno != ef.Ino || book.EpubMtime != ef.Metadata.MtimeMs)
	st.Mu.Unlock()

	fi, err := os.Stat(path)
	if err != nil {
		changed = true
	} else if changed && book.KindleIno == "" && fi.Size() == kf.Metadata.Size {
		// A book this app hasn't recorded is already there, the same size as the one in
		// Audiobookshelf: left behind by an uninstall, or the state was lost. Keep it.
		log.Printf("library: %q is already on the Kindle, keeping it", it.Title())
		st.Mu.Lock()
		book.KindleIno, book.KindleMtime = kf.Ino, kf.Metadata.MtimeMs
		_ = s.SaveState(st)
		st.Mu.Unlock()
		changed = false
		if isNew {
			res.Added++
		}
	}
	if changed {
		log.Printf("library: downloading %q", it.Title())
		if err := client.Download(it.ID, kf.Ino, path); err != nil {
			return err
		}
		st.Mu.Lock()
		book.KindleIno, book.KindleMtime, book.HasMap, book.Cover = kf.Ino, kf.Metadata.MtimeMs, false, ""
		_ = s.SaveState(st)
		st.Mu.Unlock()
		needMap = ef != nil
		if isNew {
			res.Added++
		} else {
			res.Updated++
		}
	}

	// The map needs the EPUB Audiobookshelf reads positions against
	if needMap {
		if err := buildMap(client, s, it.ID, ef.Ino, path); err != nil {
			log.Printf("library: no position map for %q: %v", it.Title(), err)
		} else {
			st.Mu.Lock()
			book.HasMap, book.EpubIno, book.EpubMtime = true, ef.Ino, ef.Metadata.MtimeMs
			_ = s.SaveState(st)
			st.Mu.Unlock()
			res.Mapped++
		}
	}
	if !changed && !needMap {
		res.Unchanged++
	}
	return nil
}

// ensureCovers gives every synced book its cover in the Kindle library (see package cover).
func ensureCovers(client *abs.Client, st *store.State, s *store.Store) {
	st.Mu.Lock()
	books := make([]*store.Book, 0, len(st.Books))
	for _, b := range st.Books {
		books = append(books, b)
	}
	st.Mu.Unlock()
	for _, b := range books {
		st.Mu.Lock()
		path, known, id := b.Path, b.Cover, b.ItemID
		st.Mu.Unlock()
		// Books that don't name their cover get the one Audiobookshelf shows
		target, wrote, err := cover.Ensure(path, known, func() ([]byte, error) { return client.Cover(id, 500) })
		if err != nil {
			log.Printf("library: no cover for %q: %v", b.Title, err)
			continue
		}
		if wrote {
			log.Printf("library: cover for %q", b.Title)
			debug.FreeOSMemory()
		}
		if target != known {
			st.Mu.Lock()
			b.Cover = target
			_ = s.SaveState(st)
			st.Mu.Unlock()
		}
	}
}

func bookFileExists(st *store.State, itemID string) bool {
	st.Mu.Lock()
	b := st.Books[itemID]
	st.Mu.Unlock()
	if b == nil {
		return false
	}
	_, err := os.Stat(b.Path)
	return err == nil && (b.HasMap || b.EpubIno == "")
}

func buildMap(client *abs.Client, s *store.Store, itemID, epubIno, azw3Path string) error {
	tmp := filepath.Join(filepath.Dir(s.MapPath(itemID)), itemID+".epub")
	defer os.Remove(tmp)
	if err := client.Download(itemID, epubIno, tmp); err != nil {
		return err
	}
	book, err := epub.ReadFile(tmp)
	if err != nil {
		return err
	}
	stream, err := azw3.ReadFile(azw3Path)
	if err != nil {
		return err
	}
	m := posmap.Build(book, stream)
	if m.Matched == 0 {
		return fmt.Errorf("the EPUB and AZW3 text didn't line up")
	}
	return m.Save(s.MapPath(itemID))
}

// fileName is "Author - Title.azw3" with characters the Kindle's FAT filesystem rejects removed.
func fileName(it *abs.Item) string {
	name := it.Title()
	if it.Author() != "" {
		name = it.Author() + " - " + name
	}
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return '_'
		}
		return r
	}, name)
	if len(name) > 180 {
		name = name[:180]
	}
	return strings.TrimSpace(name) + ".azw3"
}

func uniquePath(folder, name string, st *store.State, itemID string) string {
	p := filepath.Join(folder, name)
	for i := 2; ; i++ {
		owner := st.BookByPath(p)
		if owner == nil || owner.ItemID == itemID {
			return p
		}
		p = filepath.Join(folder, strings.TrimSuffix(name, ".azw3")+fmt.Sprintf(" (%d).azw3", i))
	}
}

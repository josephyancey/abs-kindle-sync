// Package syncer keeps reading position in step between the Kindle and Audiobookshelf.
//
//   - Opening a synced book, or waking the Kindle with one open, checks Audiobookshelf. If its
//     position is newer and more than a page away, the Yes/No prompt offers to move there.
//   - Page turns are saved to Audiobookshelf after a short pause, and straight away when the
//     book is closed or the Kindle goes to sleep.
package syncer

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync/atomic"
	"time"

	"github.com/josephyancey/abs-kindle-sync/app/internal/abs"
	"github.com/josephyancey/abs-kindle-sync/app/internal/kindle"
	"github.com/josephyancey/abs-kindle-sync/app/internal/library"
	"github.com/josephyancey/abs-kindle-sync/app/internal/posmap"
	"github.com/josephyancey/abs-kindle-sync/app/internal/store"
)

const (
	// pushDelay waits for the reader to settle on a page before saving it.
	pushDelay = 15 * time.Second
	// pageBytes is roughly one page of a Kindle book's text stream; smaller differences
	// aren't worth a prompt.
	pageBytes = 1500
	// librarySyncEvery is how often new books are looked for while the Kindle is awake.
	librarySyncEvery = 30 * time.Minute
	// ownJumpQuiet ignores the reader's own open events right after the app moved it.
	ownJumpQuiet = 10 * time.Second
)

// Syncer runs the sync loop.
type Syncer struct {
	Client     *abs.Client
	Store      *store.Store
	Config     *store.Config
	State      *store.State
	PromptPath string
	FBInk      string
	// SyncRequests starts a library sync when something is sent (the setup screen's Sync now).
	SyncRequests chan struct{}
	// OnLibrarySync reports when a library sync starts (res is nil) and how it ended.
	OnLibrarySync func(res *library.Result, err error)
	// OnStorageLeaving runs once positions are saved, when USB drive mode is about to unmount /mnt/us.
	OnStorageLeaving func()

	maps        map[string]*posmap.Map
	open        *store.Book
	pushTimer   *time.Timer
	quietUntil  time.Time
	lastLibrary time.Time

	// A jump the reader hasn't confirmed yet. The reader drops jumps while it's busy, e.g.
	// showing "About This Book" the first time a book opens, so unconfirmed jumps are retried.
	libraryRunning atomic.Bool
	// libraryFailed is set when the last library sync failed (e.g. no Wi-Fi yet after boot),
	// so it runs again as soon as Wi-Fi connects.
	libraryFailed atomic.Bool
	// itemCheck is set while a check of the library item is scheduled (see checkLibraryItem)
	itemCheck atomic.Bool

	// offerPending is a book whose Audiobookshelf check couldn't reach the server (Wi-Fi is
	// usually still off right after waking); it's checked again when Wi-Fi connects.
	offerPending *store.Book

	jumpBook     *store.Book
	jumpTarget   int
	jumpTries    int
	jumpDeadline time.Time
}

const (
	jumpCheckAfter = 6 * time.Second
	jumpMaxTries   = 10 // about 40 s: long enough to read and close "About This Book"
)

// Run handles device events until ctx ends.
func (s *Syncer) Run(ctx context.Context) {
	s.maps = map[string]*posmap.Map{}
	events := make(chan kindle.Event, 64)
	kindle.Watch(ctx, events)
	push := make(chan struct{}, 1)
	libraryTick := time.NewTicker(librarySyncEvery)
	defer libraryTick.Stop()
	jumpTick := time.NewTicker(time.Second)
	defer jumpTick.Stop()

	go s.syncLibrary()
	s.State.Mu.Lock()
	if path := kindle.OpenBook(); path != "" {
		s.open = s.State.BookByPath(path)
	}
	s.State.Mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			s.State.Mu.Lock()
			s.flush()
			s.State.Mu.Unlock()
			return
		case <-libraryTick.C:
			go s.syncLibrary()
		case <-s.SyncRequests:
			go s.syncLibrary()
		case <-jumpTick.C:
			s.State.Mu.Lock()
			s.retryJump()
			s.State.Mu.Unlock()
		case <-push:
			s.State.Mu.Lock()
			s.flush()
			s.State.Mu.Unlock()
		case ev := <-events:
			s.State.Mu.Lock()
			s.handle(ev, push)
			s.State.Mu.Unlock()
		}
	}
}

func (s *Syncer) handle(ev kindle.Event, push chan struct{}) {
	switch ev.Kind {
	case kindle.BookOpened:
		book := s.State.BookByPath(ev.Path)
		if book == nil {
			s.open = nil
			return
		}
		s.open = book
		if ev.URL != "" {
			book.ReaderURL = ev.URL
		}
		if time.Now().Before(s.quietUntil) {
			return
		}
		// The reader publishes its URL (with the current position) just before this event
		s.offerABSPosition(book)
	case kindle.ReaderURL:
		if book := s.State.BookByPath(ev.Path); book != nil {
			book.ReaderURL = ev.URL
			if ev.Position >= 0 {
				book.LastPosition = ev.Position
			}
			_ = s.Store.SaveState(s.State)
		}
	case kindle.PositionSeen:
		// Where the reader opened or jumped to: remember it, but it isn't the user reading
		if s.open == nil || ev.Position < 0 {
			return
		}
		if s.jumpBook != nil && s.jumpBook == s.open && math.Abs(float64(ev.Position-s.jumpTarget)) < 2*pageBytes {
			log.Printf("sync: %q: jump confirmed at %d", s.jumpBook.Title, ev.Position)
			s.jumpBook = nil
		}
		if s.jumpBook == nil {
			s.open.LastPosition = ev.Position
		}
	case kindle.PageTurned:
		if s.open == nil || ev.Position < 0 {
			return
		}
		if s.jumpBook != nil {
			if s.jumpBook == s.open && math.Abs(float64(ev.Position-s.jumpTarget)) < 2*pageBytes {
				log.Printf("sync: %q: jump confirmed at %d", s.jumpBook.Title, ev.Position)
				s.jumpBook = nil
				s.open.LastPosition = ev.Position
			}
			// Positions while a jump is in flight are the reader's, not the user's: never save them
			return
		}
		s.open.LastPosition = ev.Position
		s.open.Pending = ev.Position
		if s.pushTimer != nil {
			s.pushTimer.Stop()
		}
		s.pushTimer = time.AfterFunc(pushDelay, func() {
			select {
			case push <- struct{}{}:
			default:
			}
		})
	case kindle.BookClosed:
		s.flush()
		s.open = nil
	case kindle.Sleeping:
		s.flush()
	case kindle.Waking:
		if path := kindle.OpenBook(); path != "" {
			s.open = s.State.BookByPath(path)
		} else {
			s.open = nil
		}
		if s.open != nil {
			s.offerABSPosition(s.open)
		}
		if time.Since(s.lastLibrary) > librarySyncEvery {
			go s.syncLibrary()
		}
	case kindle.LibraryChanged:
		s.checkLibraryItem()
	case kindle.StorageLeaving:
		s.flush()
		_ = s.Store.SaveState(s.State)
		if s.OnStorageLeaving != nil {
			s.OnStorageLeaving()
		}
	case kindle.WifiUp:
		s.flushAll()
		if s.libraryFailed.Load() {
			go s.syncLibrary()
		}
		if s.offerPending != nil && s.offerPending == s.open {
			s.offerABSPosition(s.open)
		}
	}
}

func (s *Syncer) mapFor(book *store.Book) *posmap.Map {
	if !book.HasMap {
		return nil
	}
	if m, ok := s.maps[book.ItemID]; ok {
		return m
	}
	m, err := posmap.Load(s.Store.MapPath(book.ItemID))
	if err != nil {
		log.Printf("sync: no map for %q: %v", book.Title, err)
		return nil
	}
	s.maps[book.ItemID] = m
	return m
}

// offerABSPosition prompts to move to Audiobookshelf's position when it's newer and elsewhere.
func (s *Syncer) offerABSPosition(book *store.Book) {
	progress, err := s.Client.GetProgress(book.ItemID)
	if err != nil {
		log.Printf("sync: reading progress for %q: %v (will retry when Wi-Fi connects)", book.Title, err)
		s.offerPending = book
		return
	}
	s.offerPending = nil
	if progress == nil {
		log.Printf("sync: %q: no Audiobookshelf progress yet", book.Title)
		return
	}
	if progress.LastUpdate <= book.SeenUpdate {
		log.Printf("sync: %q: Audiobookshelf position (%.2f%%) already handled", book.Title, progress.EbookProgress*100)
		return
	}
	m := s.mapFor(book)
	target := -1
	if m != nil {
		if pos, ok := m.ToKindle(progress.EbookLocation, progress.EbookProgress); ok {
			target = pos
		}
	}
	if target < 0 {
		log.Printf("sync: %q: couldn't place Audiobookshelf position %.2f%% (%q) in the Kindle book", book.Title, progress.EbookProgress*100, progress.EbookLocation)
		return // without a map there's no reliable Kindle position to offer
	}
	current := book.LastPosition
	log.Printf("sync: %q: Audiobookshelf at %.2f%% (%q) -> Kindle %d; reader at %d", book.Title, progress.EbookProgress*100, progress.EbookLocation, target, current)
	if current >= 0 && math.Abs(float64(target-current)) < pageBytes {
		book.SeenUpdate = progress.LastUpdate
		_ = s.Store.SaveState(s.State)
		return
	}

	detail := fmt.Sprintf("%.0f%% · %s", progress.EbookProgress*100, ago(progress.LastUpdate))
	answer := "yes"
	if s.Config.AskBeforeJump {
		answer = kindle.Prompt(s.PromptPath, "Audiobookshelf", "Move to your most recent page from Audiobookshelf?", detail, 60*time.Second)
	}
	book.SeenUpdate = progress.LastUpdate
	_ = s.Store.SaveState(s.State)
	if answer != "yes" {
		log.Printf("sync: %q: stayed put (%s)", book.Title, answer)
		return
	}
	s.jumpBook, s.jumpTarget, s.jumpTries = book, target, 0
	s.sendJump()
	book.LastPosition = target
	book.Pending = -1
	if !s.Config.AskBeforeJump {
		kindle.Notify(s.FBInk, "Moved to your Audiobookshelf position")
	}
	log.Printf("sync: %q: moved to %d (%s)", book.Title, target, detail)
}

func (s *Syncer) sendJump() {
	s.quietUntil = time.Now().Add(ownJumpQuiet)
	s.jumpTries++
	// The first try moves the open book in place; retries leave and reopen it
	if err := kindle.Jump(s.jumpBook.Path, s.jumpBook.ReaderURL, s.jumpTarget, s.jumpTries > 1); err != nil {
		log.Printf("sync: jumping in %q: %v", s.jumpBook.Title, err)
	}
	// Jump waits for the reader to close first, so start the clock once it's done
	s.jumpDeadline = time.Now().Add(jumpCheckAfter)
	s.quietUntil = time.Now().Add(ownJumpQuiet)
}

// retryJump sends an unconfirmed jump again, while the book is still open.
func (s *Syncer) retryJump() {
	if s.jumpBook == nil || time.Now().Before(s.jumpDeadline) {
		return
	}
	// The book is briefly closed while the jump reopens it, so only another book being open
	// means the user moved on
	if (s.open != nil && s.open != s.jumpBook) || s.jumpTries >= jumpMaxTries {
		log.Printf("sync: %q: gave up on jumping to %d after %d tries", s.jumpBook.Title, s.jumpTarget, s.jumpTries)
		// Nothing the reader showed during the attempts is the user's position
		s.jumpBook.Pending = -1
		s.jumpBook = nil
		return
	}
	log.Printf("sync: %q: reader didn't move, retrying jump (%d)", s.jumpBook.Title, s.jumpTries)
	s.sendJump()
}

// flush saves the open book's pending position to Audiobookshelf.
func (s *Syncer) flush() {
	if s.pushTimer != nil {
		s.pushTimer.Stop()
	}
	if s.open != nil {
		s.pushBook(s.open)
	}
}

// flushAll retries every position that couldn't be saved earlier (e.g. while offline).
func (s *Syncer) flushAll() {
	for _, b := range s.State.Books {
		s.pushBook(b)
	}
}

func (s *Syncer) pushBook(book *store.Book) {
	if book.Pending < 0 {
		return
	}
	var cfi string
	var fraction float64
	if m := s.mapFor(book); m != nil {
		var ok bool
		if cfi, fraction, ok = m.ToABS(book.Pending); !ok {
			return
		}
	} else {
		return // percentage-only books: the Kindle's own percent isn't comparable; skip for now
	}
	if err := s.Client.SetProgress(book.ItemID, cfi, fraction); err != nil {
		log.Printf("sync: saving %q: %v (will retry)", book.Title, err)
		_ = s.Store.SaveState(s.State)
		return
	}
	if p, err := s.Client.GetProgress(book.ItemID); err == nil && p != nil {
		book.SeenUpdate = p.LastUpdate
	}
	log.Printf("sync: %q saved at %.2f%%", book.Title, fraction*100)
	book.Pending = -1
	_ = s.Store.SaveState(s.State)
}

// checkLibraryItem puts the Audiobookshelf Sync item back in the library if the index dropped
// it. Opening a library script touches it and forces a full rescan, which deletes and re-adds
// its entry; the re-add fails when the indexer is busy (e.g. with books we're downloading), and
// the item vanishes. Checks run a few seconds after a rescan and again later, once the indexer
// has settled.
func (s *Syncer) checkLibraryItem() {
	if !s.itemCheck.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.itemCheck.Store(false)
		for _, wait := range []time.Duration{8 * time.Second, 45 * time.Second} {
			time.Sleep(wait)
			if kindle.EnsureLibraryItem() {
				log.Printf("library: the Audiobookshelf Sync item was missing from the library, indexed it again")
			}
		}
	}()
}

func (s *Syncer) syncLibrary() {
	if !s.libraryRunning.CompareAndSwap(false, true) {
		return // one library sync at a time
	}
	defer s.libraryRunning.Store(false)
	s.lastLibrary = time.Now()
	defer s.checkLibraryItem()
	if s.OnLibrarySync != nil {
		s.OnLibrarySync(nil, nil)
	}
	res, err := library.Sync(s.Client, s.Config, s.State, s.Store)
	if s.OnLibrarySync != nil {
		s.OnLibrarySync(&res, err)
	}
	s.libraryFailed.Store(err != nil)
	if err != nil {
		log.Printf("library: %v", err)
		return
	}
	log.Printf("library: %d added, %d updated, %d maps built, %d removed, %d failed", res.Added, res.Updated, res.Mapped, res.Removed, res.Failed)
}

func ago(ms int64) string {
	d := time.Since(time.UnixMilli(ms))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}

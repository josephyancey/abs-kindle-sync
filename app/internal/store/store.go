// Package store keeps the app's settings and per-book state as JSON files.
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/josephyancey/abs-kindle-sync/app/internal/abs"
)

// Config is what the user sets up.
type Config struct {
	Server    string     `json:"server"`
	Username  string     `json:"username"`
	Tokens    abs.Tokens `json:"tokens"`
	Libraries []string   `json:"libraries,omitempty"` // empty: every book library
	// Folder is where books go; the Kindle indexes everything under /mnt/us/documents.
	Folder string `json:"folder"`
	// AskBeforeJump shows the Yes/No prompt; when false the book jumps straight to the
	// Audiobookshelf position and a short notice says so.
	AskBeforeJump bool `json:"ask_before_jump"`
}

// Book is what the app knows about one synced book.
type Book struct {
	ItemID      string  `json:"item_id"`
	Title       string  `json:"title"`
	Author      string  `json:"author"`
	Path        string  `json:"path"` // the AZW3 on the Kindle
	KindleIno   string  `json:"kindle_ino"`
	KindleMtime float64 `json:"kindle_mtime"`
	EpubIno     string  `json:"epub_ino,omitempty"`
	EpubMtime   float64 `json:"epub_mtime,omitempty"`
	HasMap      bool    `json:"has_map"`
	// Cover is the library thumbnail written for the book (see package cover).
	Cover     string `json:"cover,omitempty"`
	ReaderURL string `json:"reader_url,omitempty"`
	// LastPosition is the last Kindle position seen for this book.
	LastPosition int `json:"last_position"`
	// SeenUpdate is the ABS progress lastUpdate (ms) the app last pushed or answered for, so
	// it doesn't offer to jump to its own position or ask twice about the same one.
	SeenUpdate int64 `json:"seen_update"`
	// Pending is a Kindle position not yet saved to ABS (e.g. pushed while offline), or -1.
	Pending int `json:"pending"`
}

// State is every synced book, by item id. Mu guards Books and the books in it; the library
// sync runs alongside the reading-position sync.
type State struct {
	Mu sync.Mutex `json:"-"`
	// Account is "user@server" the books were synced from.
	Account string           `json:"account,omitempty"`
	Books   map[string]*Book `json:"books"`
	// Checked is the updatedAt of every ebook item last looked at, AZW3 or not, so unchanged
	// items aren't fetched again on every library sync.
	Checked map[string]float64 `json:"checked,omitempty"`
}

// Store reads and writes the config and state under one directory.
type Store struct {
	Dir string
	mu  sync.Mutex
}

func (s *Store) path(name string) string { return filepath.Join(s.Dir, name) }

// MapPath is where a book's position map is cached.
func (s *Store) MapPath(itemID string) string { return filepath.Join(s.Dir, "maps", itemID+".json") }

// LoadConfig returns the saved config, or defaults when there is none.
func (s *Store) LoadConfig() (*Config, error) {
	c := &Config{Folder: "/mnt/us/documents/Audiobookshelf", AskBeforeJump: true}
	if err := s.load("config.json", c); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return c, nil
}

// SaveConfig writes the config.
func (s *Store) SaveConfig(c *Config) error { return s.save("config.json", c) }

// LoadState returns the saved state, or an empty one.
func (s *Store) LoadState() (*State, error) {
	st := &State{Books: map[string]*Book{}, Checked: map[string]float64{}}
	if err := s.load("state.json", st); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if st.Books == nil {
		st.Books = map[string]*Book{}
	}
	if st.Checked == nil {
		st.Checked = map[string]float64{}
	}
	return st, nil
}

// SaveState writes the state. The caller holds st.Mu.
func (s *Store) SaveState(st *State) error { return s.save("state.json", st) }

// Forget drops every synced book and deletes what the app downloaded for them. Used when the
// account changes, since the books belong to the old server. Caller holds st.Mu.
func (s *Store) Forget(st *State) {
	for id := range st.Books {
		s.Remove(st, id)
	}
	st.Checked = map[string]float64{}
}

// Remove drops one synced book and deletes what the app downloaded for it: the book file,
// the Kindle's .sdr folder beside it, and the position map. Caller holds st.Mu.
func (s *Store) Remove(st *State, itemID string) {
	b := st.Books[itemID]
	if b == nil {
		return
	}
	if b.Path != "" {
		os.Remove(b.Path)
		os.RemoveAll(strings.TrimSuffix(b.Path, filepath.Ext(b.Path)) + ".sdr")
	}
	if b.Cover != "" {
		os.Remove(b.Cover)
	}
	os.Remove(s.MapPath(itemID))
	delete(st.Books, itemID)
	delete(st.Checked, itemID)
}

// BookByPath finds the synced book stored at a Kindle path.
func (st *State) BookByPath(path string) *Book {
	for _, b := range st.Books {
		if b.Path == path {
			return b
		}
	}
	return nil
}

func (s *Store) load(name string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path(name))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func (s *Store) save(name string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path(name) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(name))
}

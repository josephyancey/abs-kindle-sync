// Package abs is a small Audiobookshelf API client: login, books with Kindle-readable files,
// file downloads and ebook progress.
package abs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// ErrUnauthorized means the stored login no longer works and the user must sign in again.
var ErrUnauthorized = errors.New("abs: not signed in")

// Tokens are what a login returns. Servers before 2.26 only return Legacy (a long-lived token).
type Tokens struct {
	Access  string `json:"access_token,omitempty"`
	Refresh string `json:"refresh_token,omitempty"`
	Legacy  string `json:"legacy_token,omitempty"`
}

// Client talks to one Audiobookshelf server as one user.
type Client struct {
	Server string // e.g. https://audiobooks.example.com (no trailing slash)
	HTTP   *http.Client

	mu     sync.Mutex
	tokens Tokens
	// OnTokens is called whenever the tokens change, so they can be saved.
	OnTokens func(Tokens)
}

// New returns a client for server with previously saved tokens.
func New(server string, tokens Tokens) *Client {
	return &Client{
		Server: strings.TrimRight(server, "/"),
		// ABS can be slow to answer while it scans new files, and books can be large: allow a
		// long time overall, but give up on a server that doesn't start answering.
		HTTP: &http.Client{
			Timeout:   10 * time.Minute,
			Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: 2 * time.Minute, TLSHandshakeTimeout: 30 * time.Second},
		},
		tokens: tokens,
	}
}

type loginResponse struct {
	User struct {
		ID           string `json:"id"`
		Username     string `json:"username"`
		Token        string `json:"token"`
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
	} `json:"user"`
}

// Login signs in with a username and password. The password isn't kept.
func (c *Client) Login(username, password string) error {
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req, err := http.NewRequest(http.MethodPost, c.Server+"/login", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// Ask for the refresh token in the body (ABS 2.26+); older servers ignore this
	req.Header.Set("x-return-tokens", "true")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("abs: wrong username or password")
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("abs: login answered %s", res.Status)
	}
	var lr loginResponse
	if err := json.NewDecoder(res.Body).Decode(&lr); err != nil {
		return fmt.Errorf("abs: reading login response: %w", err)
	}
	c.setTokens(Tokens{Access: lr.User.AccessToken, Refresh: lr.User.RefreshToken, Legacy: lr.User.Token})
	return nil
}

func (c *Client) setTokens(t Tokens) {
	c.mu.Lock()
	c.tokens = t
	cb := c.OnTokens
	c.mu.Unlock()
	if cb != nil {
		cb(t)
	}
}

// Tokens returns the current login tokens.
func (c *Client) Tokens() Tokens {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tokens
}

func (c *Client) bearer() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tokens.Access != "" {
		return c.tokens.Access
	}
	return c.tokens.Legacy
}

// refresh swaps the refresh token for a new access token.
func (c *Client) refresh() error {
	c.mu.Lock()
	refresh := c.tokens.Refresh
	c.mu.Unlock()
	if refresh == "" {
		return ErrUnauthorized
	}
	req, err := http.NewRequest(http.MethodPost, c.Server+"/auth/refresh", nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-refresh-token", refresh)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ErrUnauthorized
	}
	var lr loginResponse
	if err := json.NewDecoder(res.Body).Decode(&lr); err != nil {
		return err
	}
	t := Tokens{Access: lr.User.AccessToken, Refresh: lr.User.RefreshToken}
	if t.Refresh == "" {
		t.Refresh = refresh
	}
	c.setTokens(t)
	return nil
}

// do sends an authenticated request, refreshing the access token once on a 401.
func (c *Client) do(method, path string, body any) (*http.Response, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequest(method, c.Server+path, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+c.bearer())
		res, err := c.HTTP.Do(req)
		if err != nil {
			return nil, err
		}
		if res.StatusCode != http.StatusUnauthorized {
			return res, nil
		}
		res.Body.Close()
		if attempt == 0 {
			if err := c.refresh(); err != nil {
				return nil, err
			}
		}
	}
	return nil, ErrUnauthorized
}

func (c *Client) getJSON(path string, out any) error {
	res, err := c.do(http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("abs: GET %s answered %s", path, res.Status)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// Library is a book library on the server.
type Library struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
}

// Libraries lists the book libraries the user can see.
func (c *Client) Libraries() ([]Library, error) {
	var out struct {
		Libraries []Library `json:"libraries"`
	}
	if err := c.getJSON("/api/libraries", &out); err != nil {
		return nil, err
	}
	books := out.Libraries[:0]
	for _, l := range out.Libraries {
		if l.MediaType == "book" {
			books = append(books, l)
		}
	}
	return books, nil
}

// File is one file of a library item.
type File struct {
	Ino      string `json:"ino"`
	FileType string `json:"fileType"`
	Metadata struct {
		Filename string  `json:"filename"`
		Ext      string  `json:"ext"`
		Size     int64   `json:"size"`
		MtimeMs  float64 `json:"mtimeMs"`
	} `json:"metadata"`
}

// Item is a library item, with the parts this app uses.
type Item struct {
	ID        string  `json:"id"`
	LibraryID string  `json:"libraryId"`
	UpdatedAt float64 `json:"updatedAt"`
	Media     struct {
		Metadata struct {
			Title      string `json:"title"`
			AuthorName string `json:"authorName"`
		} `json:"metadata"`
		EbookFile *struct {
			Ino         string `json:"ino"`
			EbookFormat string `json:"ebookFormat"`
		} `json:"ebookFile"`
	} `json:"media"`
	LibraryFiles []File `json:"libraryFiles"`
}

// Title and author for file names and messages.
func (it *Item) Title() string  { return it.Media.Metadata.Title }
func (it *Item) Author() string { return it.Media.Metadata.AuthorName }

// KindleFile is the item's AZW3, if it has one. Several are possible; the newest wins.
func (it *Item) KindleFile() *File {
	var best *File
	for i := range it.LibraryFiles {
		f := &it.LibraryFiles[i]
		if strings.EqualFold(f.Metadata.Ext, ".azw3") && (best == nil || f.Metadata.MtimeMs > best.Metadata.MtimeMs) {
			best = f
		}
	}
	return best
}

// EpubFile is the EPUB Audiobookshelf reads positions against: the item's main ebook when
// it's an EPUB, otherwise any EPUB in the item.
func (it *Item) EpubFile() *File {
	if it.Media.EbookFile != nil && strings.EqualFold(it.Media.EbookFile.EbookFormat, "epub") {
		for i := range it.LibraryFiles {
			if it.LibraryFiles[i].Ino == it.Media.EbookFile.Ino {
				return &it.LibraryFiles[i]
			}
		}
	}
	for i := range it.LibraryFiles {
		if strings.EqualFold(it.LibraryFiles[i].Metadata.Ext, ".epub") {
			return &it.LibraryFiles[i]
		}
	}
	return nil
}

// Summary is a library item as the library listing returns it (no file list).
type Summary struct {
	ID        string  `json:"id"`
	UpdatedAt float64 `json:"updatedAt"`
	Media     struct {
		EbookFormat string `json:"ebookFormat"`
	} `json:"media"`
}

// Ebooks lists the items in a library that have at least one ebook file.
func (c *Client) Ebooks(libraryID string) ([]Summary, error) {
	var out struct {
		Results []Summary `json:"results"`
	}
	// Only items that have an ebook ("ebooks" filter, value base64 of "ebook"): a smaller answer
	if err := c.getJSON("/api/libraries/"+url.PathEscape(libraryID)+"/items?limit=0&filter=ebooks.ZWJvb2s%3D", &out); err != nil {
		return nil, err
	}
	var items []Summary
	for _, it := range out.Results {
		if it.Media.EbookFormat != "" {
			items = append(items, it)
		}
	}
	return items, nil
}

// Item returns one item with its file list, where supplementary files like an AZW3 appear.
func (c *Client) Item(id string) (*Item, error) {
	var it Item
	if err := c.getJSON("/api/items/"+url.PathEscape(id)+"?expanded=1", &it); err != nil {
		return nil, err
	}
	return &it, nil
}

// Download saves one file of an item to dest (written to a temp file, then renamed).
func (c *Client) Download(itemID, ino, dest string) error {
	res, err := c.do(http.MethodGet, "/api/items/"+url.PathEscape(itemID)+"/file/"+url.PathEscape(ino)+"/download", nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("abs: download answered %s", res.Status)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, res.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// Cover returns the item's cover as a JPEG about h pixels tall, or nil when it has none.
func (c *Client) Cover(itemID string, h int) ([]byte, error) {
	res, err := c.do(http.MethodGet, fmt.Sprintf("/api/items/%s/cover?format=jpeg&height=%d", url.PathEscape(itemID), h), nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("abs: cover answered %s", res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, 20<<20))
}

// Progress is the ebook part of a user's media progress.
type Progress struct {
	EbookLocation string  `json:"ebookLocation"`
	EbookProgress float64 `json:"ebookProgress"`
	IsFinished    bool    `json:"isFinished"`
	LastUpdate    int64   `json:"lastUpdate"` // ms since epoch
}

// GetProgress returns the user's progress on an item, or nil if there is none yet.
func (c *Client) GetProgress(itemID string) (*Progress, error) {
	res, err := c.do(http.MethodGet, "/api/me/progress/"+url.PathEscape(itemID), nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("abs: progress answered %s", res.Status)
	}
	var p Progress
	if err := json.NewDecoder(res.Body).Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

// SetProgress records the user's ebook position on an item.
func (c *Client) SetProgress(itemID, location string, progress float64) error {
	body := map[string]any{"ebookProgress": progress}
	if location != "" {
		body["ebookLocation"] = location
	}
	res, err := c.do(http.MethodPatch, "/api/me/progress/"+url.PathEscape(itemID), body)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("abs: saving progress answered %s", res.Status)
	}
	return nil
}

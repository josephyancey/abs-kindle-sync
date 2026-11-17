// Package kindle talks to Amazon's stock Kindle reader on a jailbroken device: what's open,
// where the reader is, jumping to a position, and the Yes/No prompt.
//
// What's used here, and why, is in docs/findings.md. In short, for AZW3 books:
//   - com.lab126.appmgrd historyChange events name the opened book and carry
//     lastReadPosition when a book is left;
//   - the reader logs "PageStartPos:HTMLPosition: <n>" to syslog on every page turn and jump;
//   - reopening a book with lastReadPosition=<n> in its reader URL jumps to <n>, instantly when
//     the book is already open;
//   - com.lab126.powerd goingToScreenSaver / outOfScreenSaver mark sleep and wake.
package kindle

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// EventKind is what happened on the device.
type EventKind int

const (
	BookOpened   EventKind = iota // Path is the book's file
	ReaderURL                     // the reader published its URL for Path, with Position = lastReadPosition
	BookClosed                    // the reader went to the background
	PageTurned                    // the user turned a page; Position is the new page's start
	PositionSeen                  // the reader is at Position, but not because the user turned a page (open, jump)
	Sleeping
	Waking
	WifiUp
	LibraryChanged // the library index removed entries or finished a full rescan
	StorageLeaving // USB drive mode is about to unmount /mnt/us
)

// Event is one thing the app reacts to.
type Event struct {
	Kind     EventKind
	Path     string // /mnt/us/documents/... for BookOpened/BookClosed
	Position int    // Kindle position, -1 when unknown
	URL      string // the reader URL, for ReaderURL events (used to jump later)
	At       time.Time
}

var (
	readerURL = regexp.MustCompile(`"(app://com\.lab126\.booklet\.reader/[^"]+)"`)
	fileURL   = regexp.MustCompile(`"file://(/mnt/us/[^"]+)"`)
	pageStart = regexp.MustCompile(`PageStartPos:HTMLPosition: (\d+)`)
	// The reader's renderer logs every move, including where a book opens
	readerGoto = regexp.MustCompile(`/command/goto_position\?[^ ]*position=(\d+)`)
	lastReadRe = regexp.MustCompile(`[?&]lastReadPosition=([0-9]+)`)
)

// Watch runs until ctx ends, sending device events to out. It follows LIPC events and the
// system log, restarting either if it exits.
func Watch(ctx context.Context, out chan<- Event) {
	go follow(ctx, out, parseLIPC, "sh", "-c", `printf 'com.lab126.appmgrd\ncom.lab126.powerd\ncom.lab126.wifid\ncom.lab126.volumd\n' | exec lipc-wait-event -m -l '*'`)
	go follow(ctx, out, parseLog, "tail", "-n", "0", "-F", "/var/log/messages")
}

func follow(ctx context.Context, out chan<- Event, parse func(string) (Event, bool), name string, args ...string) {
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, name, args...)
		stdout, err := cmd.StdoutPipe()
		if err == nil && cmd.Start() == nil {
			scanner := bufio.NewScanner(stdout)
			scanner.Buffer(make([]byte, 64*1024), 1024*1024)
			for scanner.Scan() {
				if ev, ok := parse(scanner.Text()); ok {
					ev.At = time.Now()
					select {
					case out <- ev:
					case <-ctx.Done():
						return
					}
				}
			}
			cmd.Wait()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func parseLIPC(line string) (Event, bool) {
	switch {
	case strings.Contains(line, "goingToScreenSaver"):
		return Event{Kind: Sleeping, Position: -1}, true
	case strings.Contains(line, "outOfScreenSaver"):
		return Event{Kind: Waking, Position: -1}, true
	case strings.Contains(line, "cmConnected"):
		return Event{Kind: WifiUp, Position: -1}, true
	case strings.Contains(line, "userstoreIsLikelyToUnMount"):
		return Event{Kind: StorageLeaving, Position: -1}, true
	case strings.Contains(line, "fullScanFinish"), strings.Contains(line, "scannerFileDeleted"):
		return Event{Kind: LibraryChanged, Position: -1}, true
	case strings.Contains(line, `appStateChange "com.lab126.booklet.reader" "pause" 1`):
		return Event{Kind: BookClosed, Position: -1}, true
	case strings.Contains(line, "historyChange"):
		// historyChange "<a>" "<b>" ...: the reader URL comes first both when a book opens and
		// when it's left, carrying lastReadPosition; opening from Home also sends file://<book> second.
		first := line[strings.Index(line, `"`):]
		if urls := readerURL.FindStringSubmatch(first); urls != nil && strings.HasPrefix(first, `"`+urls[1]) {
			pos := -1
			if m := lastReadRe.FindStringSubmatch(urls[1]); m != nil {
				pos, _ = strconv.Atoi(m[1])
			}
			return Event{Kind: ReaderURL, Path: readerPath(urls[1]), Position: pos, URL: urls[1]}, true
		}
		// A book opened from its reader URL (as the app's own jumps do) has that URL second
		if urls := readerURL.FindStringSubmatch(first); urls != nil {
			pos := -1
			if m := lastReadRe.FindStringSubmatch(urls[1]); m != nil {
				pos, _ = strconv.Atoi(m[1])
			}
			return Event{Kind: BookOpened, Path: readerPath(urls[1]), Position: pos, URL: urls[1]}, true
		}
		if files := fileURL.FindStringSubmatch(line); files != nil {
			p, err := url.PathUnescape(files[1])
			if err != nil {
				p = files[1]
			}
			return Event{Kind: BookOpened, Path: p, Position: -1}, true
		}
	}
	return Event{}, false
}

func parseLog(line string) (Event, bool) {
	if m := pageStart.FindStringSubmatch(line); m != nil {
		pos, _ := strconv.Atoi(m[1])
		// ReadingTimerController logs NextPage, PreviousPage, GoToPosition, ...: only the page
		// turns are the user reading; anything else is where the reader was put
		if strings.Contains(line, "::NextPage") || strings.Contains(line, "::PreviousPage") || strings.Contains(line, "::PrevPage") {
			return Event{Kind: PageTurned, Position: pos}, true
		}
		return Event{Kind: PositionSeen, Position: pos}, true
	}
	if m := readerGoto.FindStringSubmatch(line); m != nil {
		pos, _ := strconv.Atoi(m[1])
		return Event{Kind: PositionSeen, Position: pos}, true
	}
	return Event{}, false
}

// readerPath extracts the book's file path from a reader URL.
func readerPath(u string) string {
	if parsed, err := url.Parse(u); err == nil {
		if p := parsed.Query().Get("path"); p != "" {
			return p
		}
		return parsed.Path
	}
	return ""
}

// Jump opens the book at path at a Kindle position.
// knownURL, when known (from a ReaderURL event for this book), carries the reader's own
// parameters; otherwise one is built from the book's catalog entry. Without reopen, an open
// book moves in place; with it, the reader is left first, which is slower but always moves it.
func Jump(path, knownURL string, position int, reopen bool) error {
	u := knownURL
	if u == "" {
		cdeType, cdeKey, err := CatalogKeys(path)
		if err != nil {
			return err
		}
		u = buildReaderURL(path, cdeType, cdeKey, position)
	}
	// Starting the open book's URL again moves it in place, but not always (e.g. while the
	// reader is busy); opening it from Home with lastReadPosition always does
	if reopen && ActiveApp() == "com.lab126.booklet.reader" {
		if err := exec.Command("lipc-set-prop", "com.lab126.appmgrd", "start", "app://com.lab126.KPPMainApp?view=KPP_HOME").Run(); err != nil {
			return err
		}
		for i := 0; i < 20 && ActiveApp() == "com.lab126.booklet.reader"; i++ {
			time.Sleep(250 * time.Millisecond)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return exec.Command("lipc-set-prop", "com.lab126.appmgrd", "start", withPosition(u, position)).Run()
}

// buildReaderURL makes the URL the reader opens a book with. Spaces must be %20, as the
// reader writes them; a "+" makes it treat the path as a different book.
func buildReaderURL(path, cdeType, cdeKey string, position int) string {
	q := func(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }
	return fmt.Sprintf("app://com.lab126.booklet.reader%s?readerHandlerId=0&path=%s&cdeType=%s&cdeKey=%s&lastUIContext=ContentPanel&lastReadPosition=%d",
		escapePath(path), q(path), q(cdeType), q(cdeKey), position)
}

// withPosition sets lastReadPosition in a reader URL.
func withPosition(u string, position int) string {
	return lastReadRe.ReplaceAllStringFunc(u, func(m string) string {
		return m[:1] + "lastReadPosition=" + strconv.Itoa(position)
	})
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// CatalogKeys returns the cdeType and cdeKey the Kindle's catalog gave a book file.
func CatalogKeys(path string) (string, string, error) {
	out, err := exec.Command("sqlite3", "-separator", "|", "/var/local/cc.db",
		"select p_cdeType, p_cdeKey from Entries where p_location = '"+strings.ReplaceAll(path, "'", "''")+"' limit 1;").Output()
	if err != nil {
		return "", "", err
	}
	fields := strings.SplitN(strings.TrimSpace(string(out)), "|", 2)
	if len(fields) != 2 || fields[1] == "" {
		return "", "", fmt.Errorf("kindle: %s isn't in the catalog yet", path)
	}
	return fields[0], fields[1], nil
}

// LibraryItem is the library entry that opens the setup screen.
const LibraryItem = "/mnt/us/documents/Audiobookshelf Sync.sh"

// EnsureLibraryItem asks the Kindle to index the setup screen's library entry again when the
// file is there but the library has dropped it (seen after a full rescan), so it never goes
// missing from the library. It returns whether it had to.
func EnsureLibraryItem() bool {
	if _, err := os.Stat(LibraryItem); err != nil {
		return false
	}
	if _, _, err := CatalogKeys(LibraryItem); err == nil {
		return false
	}
	return exec.Command("lipc-set-prop", "com.lab126.scanner", "reScanFile", LibraryItem).Run() == nil
}

// OpenBook returns the file of the book the reader has open, from the catalog's most recently
// opened entry, or "" when the reader isn't in front. Used when the app starts or wakes with a
// book already open, so it never saw the open event.
func OpenBook() string {
	if ActiveApp() != "com.lab126.booklet.reader" {
		return ""
	}
	out, err := exec.Command("sqlite3", "/var/local/cc.db",
		"select p_location from Entries where p_location is not null order by p_lastAccess desc limit 1;").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ActiveApp is the app in front, e.g. com.lab126.booklet.reader while reading.
func ActiveApp() string {
	out, _ := exec.Command("lipc-get-prop", "com.lab126.appmgrd", "activeApp").Output()
	return strings.TrimSpace(string(out))
}

// Prompt shows the Yes/No popup (kindle/bin/abs-prompt.sh) and returns "yes", "no" or "timeout".
func Prompt(script, title, message, detail string, timeout time.Duration) string {
	out, err := exec.Command(script, title, message, detail, strconv.Itoa(int(timeout.Seconds()))).Output()
	if err != nil {
		return "timeout"
	}
	return strings.TrimSpace(string(out))
}

// Notify shows a short message at the bottom of the screen with FBInk, then clears it.
func Notify(fbink, message string) {
	exec.Command(fbink, "-q", "-y", "-3", "-m", message).Run()
	go func() {
		time.Sleep(4 * time.Second)
		exec.Command(fbink, "-q", "-y", "-3", "-m", strings.Repeat(" ", len(message))).Run()
	}()
}

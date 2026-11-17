package kindle

import "testing"

// Lines captured from a Paperwhite 5 on firmware 5.18.5 with lipc-wait-event -m -l '*'
const (
	openFromHome = `com.lab126.appmgrd historyChange "app://com.lab126.KPPMainApp?view=KPP_HOME" "file:///mnt/us/documents/Audiobookshelf/Adrian%20Tchaikovsky%20-%20Spiderlight.azw3" "KPP_HOME" "" 0 1 `
	leaveBook    = `com.lab126.appmgrd historyChange "app://com.lab126.booklet.reader/mnt/us/documents/Audiobookshelf/Adrian%20Tchaikovsky%20-%20Spiderlight.azw3?readerHandlerId=0&path=%2fmnt%2fus%2fdocuments%2fAudiobookshelf%2fAdrian%20Tchaikovsky%20-%20Spiderlight.azw3&cdeType=EBOK&cdeKey=cc38fa2b-19d7-450d-b259-889f4a02b81d&lastUIContext=ContentPanel&lastReadPosition=1316" "app://com.lab126.KPPMainApp?view=KPP_HOME" "" "" 1 0 `
	pauseReader  = `com.lab126.appmgrd appStateChange "com.lab126.booklet.reader" "pause" 1 "" `
	pageTurn     = `261115:231030 cvm[2469]: I ReadingTimerController:Information::NextPage,Verdict:Processed,PageStartPos:HTMLPosition: 208158,IntervalTime:993`
)

const book = "/mnt/us/documents/Audiobookshelf/Adrian Tchaikovsky - Spiderlight.azw3"

func TestParseOpen(t *testing.T) {
	ev, ok := parseLIPC(openFromHome)
	if !ok || ev.Kind != BookOpened || ev.Path != book {
		t.Fatalf("got %+v %v", ev, ok)
	}
}

func TestParseReaderURL(t *testing.T) {
	ev, ok := parseLIPC(leaveBook)
	if !ok || ev.Kind != ReaderURL || ev.Path != book || ev.Position != 1316 || ev.URL == "" {
		t.Fatalf("got %+v %v", ev, ok)
	}
}

func TestParseOpenFromURL(t *testing.T) {
	line := `com.lab126.appmgrd historyChange "app://com.lab126.KPPMainApp?view=KPP_HOME" "app://com.lab126.booklet.reader/mnt/us/documents/Audiobookshelf/Adrian%20Tchaikovsky%20-%20Spiderlight.azw3?readerHandlerId=0&path=%2Fmnt%2Fus%2Fdocuments%2FAudiobookshelf%2FAdrian%20Tchaikovsky%20-%20Spiderlight.azw3&cdeType=EBOK&cdeKey=cc38fa2b-19d7-450d-b259-889f4a02b81d&lastUIContext=ContentPanel&lastReadPosition=474641" "KPP_HOME" "" 0 1 `
	ev, ok := parseLIPC(line)
	if !ok || ev.Kind != BookOpened || ev.Path != book || ev.Position != 474641 || ev.URL == "" {
		t.Fatalf("got %+v %v", ev, ok)
	}
}

func TestParseClose(t *testing.T) {
	if ev, ok := parseLIPC(pauseReader); !ok || ev.Kind != BookClosed {
		t.Fatalf("got %+v %v", ev, ok)
	}
}

func TestParsePageTurn(t *testing.T) {
	if ev, ok := parseLog(pageTurn); !ok || ev.Kind != PageTurned || ev.Position != 208158 {
		t.Fatalf("got %+v %v", ev, ok)
	}
}

func TestParseReaderOpenPosition(t *testing.T) {
	line := `261115:232328 webreader[7041]: I webreader:webReaderHttpd_urlHistoryFunc:[79.686791]: webreader_history: /command/goto_position?pre_render=1&skip_rendering=1&position=1316 :`
	if ev, ok := parseLog(line); !ok || ev.Kind != PositionSeen || ev.Position != 1316 {
		t.Fatalf("got %+v %v", ev, ok)
	}
}

func TestJumpIsNotAPageTurn(t *testing.T) {
	line := `261115:231313 cvm[2469]: I ReadingTimerController:Information::GoToPosition,Verdict:Processed,PageStartPos:HTMLPosition: 299934,IntervalTime:733`
	if ev, ok := parseLog(line); !ok || ev.Kind != PositionSeen || ev.Position != 299934 {
		t.Fatalf("got %+v %v", ev, ok)
	}
}

func TestBuiltURLEncodesSpacesLikeTheReader(t *testing.T) {
	u := buildReaderURL(book, "EBOK", "cc38fa2b-19d7-450d-b259-889f4a02b81d", 474641)
	want := "app://com.lab126.booklet.reader/mnt/us/documents/Audiobookshelf/Adrian%20Tchaikovsky%20-%20Spiderlight.azw3?readerHandlerId=0&path=%2Fmnt%2Fus%2Fdocuments%2FAudiobookshelf%2FAdrian%20Tchaikovsky%20-%20Spiderlight.azw3&cdeType=EBOK&cdeKey=cc38fa2b-19d7-450d-b259-889f4a02b81d&lastUIContext=ContentPanel&lastReadPosition=474641"
	if u != want {
		t.Fatalf("\ngot  %s\nwant %s", u, want)
	}
}

func TestJumpRewritesKnownURL(t *testing.T) {
	ev, _ := parseLIPC(leaveBook)
	if got := withPosition(ev.URL, 500000); got == ev.URL || !contains(got, "lastReadPosition=500000") {
		t.Fatalf("got %s", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestParseUSBDriveMode(t *testing.T) {
	if ev, ok := parseLIPC(`com.lab126.volumd userstoreIsLikelyToUnMount `); !ok || ev.Kind != StorageLeaving {
		t.Fatalf("got %+v %v", ev, ok)
	}
}

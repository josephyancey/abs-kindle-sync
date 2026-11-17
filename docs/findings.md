# Findings: hooking Amazon's stock Kindle reader

This page covers what a script on a jailbroken Kindle can see and control in Amazon's own reader. Everything here was **verified on a Kindle Paperwhite 5 (2021, codename Malbec/Bellatrix) on firmware 5.18.5**. Other models and firmware may differ.

The reader on this firmware is a React Native app (`KPPMainApp`). It publishes an LIPC service named `com.lab126.kppReader`. LIPC is the Kindle's internal messaging system: `lipc-get-prop`, `lipc-set-prop`, `lipc-wait-event` and `lipc-probe`.

## Events

These were captured with `dev/capture.sh` while opening a book, turning pages, going home, and sleeping and waking the Kindle.

| What happened | Event |
|---|---|
| Book opened | `com.lab126.appmgrd historyChange "app://com.lab126.booklet.reader/<path>?...&cdeKey=<ASIN>&lastReadPosition=<long>:<short>" "file:///mnt/us/documents/.../<book>.kfx" ...` |
| Page turned | `com.lab126.kppReader updatePosition "{"start_pos":"423720","end_pos":"423912"}"` |
| Book closed / left | `historyChange` from the reader URL, which includes `lastReadPosition=<long>:<short>` |
| Going to sleep | `com.lab126.powerd goingToScreenSaver` |
| Waking | `com.lab126.powerd outOfScreenSaver`, then `exitingScreenSaver` |
| Active app (poll) | `lipc-get-prop com.lab126.appmgrd activeApp` returns `com.lab126.booklet.reader` while reading |

The reader also logs `Goto LPR position <n>` when it opens a book. LPR means last page read.

## Positions

The reader uses two forms of the same position:

- **short**: a single number such as `423914`. This is what `updatePosition` reports.
- **long**: 12 base64 characters such as `AcESAAAAAAAA`. They decode to 9 bytes: `0x01`, then a little-endian uint32 section id (an "eid" in the KFX book), then a little-endian uint32 offset. For example `AcESAAAAAAAA` is eid 4801, offset 0.

The reader's URLs write a position as `<long>:<short>`.

## Jumping to a position

```sh
lipc-set-prop com.lab126.kppReader gotoPosition "<long>:<short>"
```

- **The short number decides where it lands.** The reader snaps to the start of the page containing it.
  - `AcESAAAAAAAA:200000` landed at 198634.
  - `AcESAAAAAAAA:423914` landed at 423915.
  - `AZQRAAAAAAAA:0` went to the start of the book.
- **The long part must name a section id that exists in that book.** If it doesn't, the reader logs `Failed to parse position string, stay at the current position`. A long taken from the book's own `lastReadPosition` is always valid for that book.
- **A short number alone is rejected** (`Error cast to short position`). So is a long part alone.
- `lipc-set-prop` logs `LIPC access error ::status=8` even when it succeeds. The reader logs the result separately (`watchSetStringCallback_gotoPosition ... SUCCESS`), and `updatePosition` fires if the page changed.
- A 2012 MobileRead attempt at `com.lab126.acxreader gotoPosition` failed. That was a different, older reader.

### AZW3 books

AZW3 (KF8) books open in the older Java reader (`cvm`, which logs `ReadingTimerController`), not the
KFX reader, so `com.lab126.kppReader gotoPosition` isn't available for them. Their positions are a
single number. Starting the open book's reader URL again with a new `lastReadPosition`
(`lipc-set-prop com.lab126.appmgrd start "app://com.lab126.booklet.reader/...&lastReadPosition=<n>"`)
moves it in place in about half a second: the reader logs `goto_position?position=<n>` and
`GoToPosition ... PageStartPos`, and the book stays open. If that doesn't move it, leaving the reader
(`start app://com.lab126.KPPMainApp?view=KPP_HOME`) and reopening the URL always does, at the cost
of a visible close and reopen.

## Other writable `com.lab126.kppReader` properties

Listed by `lipc-probe -v com.lab126.kppReader`. Only `gotoPosition` and `showToast` have been tested:

`showToast` (works: a short notice at the bottom of the screen, auto-dismissed after about 3 s), `openDocumentWithParams`, `closeDocument`, `showUI`, `invokeChrome`, `openBev`, `closeBev`, `gotoSearchResult`, `invalidatePageDecorations`, `pinContent`, `pagePreviewRequest`, `dismissBookmark`, `clearFlashTriggerOnNextDraw`, and others.

## Dialogs: what didn't work

- `lipc-set-prop com.lab126.pillow pillowAlert '{"clientParams":{"alertId":"appAlert1","show":true,"customStrings":[{"matchStr":"alertTitle","replaceStr":"..."},{"matchStr":"alertText","replaceStr":"..."}]}}'` shows a native alert with your text, but **only a Close button**. `appAlert1` is the only alert id defined in `KPPPillow`. Dismissing it logs `handled pillow window - appAlert1, dismissed request`.
- `com.lab126.pillow customDialog` only knows `wifi_wizard_dialog`, `bt_wizard_dialog`, `browser_setting_dialog`, `about_browser_dialog` and the app menu. Anything else logs `Unable to determine the requested dialog type`.
- Amazon's own "jump to furthest page read" prompt (`MRPRPlugin`) checks a remote position from Whispersync on every open. Feeding it a position from Audiobookshelf hasn't been explored.

So the Yes/No prompt is drawn by this project, in `kindle/bin/abs-prompt.sh`:

1. **Save** `/dev/fb0`.
2. **Draw** the box with FBInk, using the system's Amazon Ember font.
3. **Wait** for a tap with `tapwait`. It holds an `EVIOCGRAB` on the touchscreen so the tap doesn't also turn the page.
4. **Restore** `/dev/fb0` and refresh the box area.

Asking the reader to repaint does **not** clear an FBInk overlay: neither `invalidatePageDecorations` nor `gotoPosition` to the current page works. That's why the prompt saves and restores the screen itself.

## Where positions are stored

- `/var/local/cc.db` (SQLite) has a `p_percentFinished` and `p_location` (file path) column for every book.
- Each book's `.sdr` sidecar folder holds the reader's state files.

These weren't needed, because the events and `gotoPosition` cover reading and setting the position.

## Device and network notes

- **SSH:** USBNetLite (notmarek/kindle-usbnetlite) gives SSH. On 5.18.5, installing MR packages through MRPI failed with `./xzdec: Permission denied`, because MRPI's staging folder in `/var/tmp` doesn't allow running programs. Running the package's own `install.sh` from `/mnt/us` through a KUAL action (wrapped in `mntroot rw` / `mntroot ro`) worked.
- **The Kindle's firewall drops incoming traffic by default.** USBNetLite adds a port 22 rule when it starts, and the Kindle rewrites its firewall when Wi-Fi reconnects. `dev/kual-abs-test` re-adds the rule.
- **Incoming connections over Wi-Fi were unreliable even with the rule in place.** They worked for under a minute after the Kindle last sent something, the same on two different VLANs, while the Kindle reported Wi-Fi up. Turning off Wi-Fi power saving (`iwconfig wlan0 power off`) and pinging the gateway helped only briefly. Connections the Kindle starts itself always worked, so development uses a reverse tunnel (see `dev/README.md`). The real app only makes outgoing connections anyway.
- **The Kindle doesn't answer ping** (ICMP is dropped).
- **SSH over USB:** in USBNet mode the Kindle shows up as a Linux CDC gadget (`USB\VID_0525&PID_A4A2`). Windows 11 binds it as a serial port and wouldn't take the RNDIS driver.

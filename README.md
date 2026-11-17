# Audiobookshelf Sync for Kindle

Sync books and reading position between [Audiobookshelf](https://www.audiobookshelf.org/) and a jailbroken Kindle, using Amazon's **own reader**, not KOReader.

> [!WARNING]
> **Alpha.** It works day to day on one Kindle: a Paperwhite 5 on firmware 5.18.5. Expect rough edges on anything else. It hooks into undocumented parts of the Kindle's reader, so a firmware update can break it. Back up your Kindle's `documents` folder before installing, and please [report what happens](../../issues) on your model.

## What it does

- **Brings your books over.** Every Audiobookshelf book that has an AZW3 ebook is copied into your Kindle library, in a `documents/Audiobookshelf` folder, with its cover. Books you remove from Audiobookshelf, or from the libraries you sync, are removed from the Kindle.
- **Saves your place to Audiobookshelf** as you turn pages. Other Audiobookshelf apps, and tools that link an audiobook to its ebook, then pick up where you stopped reading.
- **Offers to move to your latest Audiobookshelf position.** When you open a book, or wake the Kindle with one open, a Yes/No prompt appears over the page:

  > **Audiobookshelf**
  > Move to your most recent page from Audiobookshelf?
  > **Page 432 · 63%**
  > [ No ] [ Yes ]

- **A setup screen** in your library to sign in, pick libraries, turn the prompt off, and sync now.

It runs as a small background service that starts at boot and uses about 10 MB of memory while idle.

## What you need

- **A jailbroken Kindle with KPM**, the Kindle Package Manager that current jailbreaks install. On an older jailbreak, run KindleModding's [Legacy Migration](https://kindlemodding.org/jailbreaking/faq/migration.html) first.
- **Firmware 5.16.3 or newer** (the `kindlehf` platform). Older firmware has a different reader, which isn't supported. Tested only on a Paperwhite 5 with 5.18.5.
- **An Audiobookshelf server** the Kindle can reach over Wi-Fi, and an account on it.
- **Books with an AZW3 in Audiobookshelf.** Amazon's reader can't open EPUB, so each book needs an `.azw3` file in its Audiobookshelf folder. calibre can convert one: `ebook-convert book.epub book.azw3`.
  - **Keep the EPUB next to it.** Audiobookshelf stores positions against the EPUB, and the app maps between the two. A book with only an AZW3 still gets copied, but its position won't sync.

## Install

**One tap:**

1. Download [`install-audiobookshelf-sync.sh`](../../releases/latest/download/install-audiobookshelf-sync.sh).
2. Copy it to your Kindle's `documents` folder over USB.
3. Eject the Kindle and tap **Install Audiobookshelf Sync** in your library.

It adds this project's KPM repository, installs the package, opens the setup screen, and deletes itself. The **Audiobookshelf Sync** item can take up to a minute to appear in your library.

**Or by hand**, from the Kindle's search bar:

```
;kpm add-repo https://josephyancey.github.io/abs-kindle-sync/manifest.json
;kpm update
;kpm install audiobookshelf-sync
```

**Or from the file:** download the `.kpkg` from [Releases](../../releases), copy it to the Kindle, and run `/var/local/kmc/bin/kpm install file:///mnt/us/<file>.kpkg`.

Then open **Audiobookshelf Sync** from your library. Enter your server address, user name and password (only the sign-in token is kept), and pick the libraries to sync.

## Update and uninstall

- **Update:** `;kpm upgrade` (upgrades everything installed from a repository) or `;kpm install audiobookshelf-sync`. Your sign-in and settings are kept.
- **Uninstall:** `;kpm uninstall audiobookshelf-sync`. This removes the service, the boot job, the setup screen, and the sign-in. Books it synced stay in your library; delete the `documents/Audiobookshelf` folder to remove them.

## Good to know

- **Two copies of a book.** If you also bought or sent a book to your Kindle from Amazon, you'll see two copies. The one from Audiobookshelf is in the `Audiobookshelf` folder.
- **The sign-in token is stored** on the Kindle's USB drive (`/mnt/us/abs-sync`), like other Kindle homebrew. Anyone with your Kindle and a USB cable can read it. Signing out from the setup screen deletes it.
- **The boot job.** To start at boot, the package installs an upstart job in `/etc/upstart`. KPM's guidelines ask packages not to touch the system partition, but there's no supported alternative yet. Uninstalling removes it.
- **If memory runs short,** the Kindle stops this service before its own apps, and the service restarts by itself.
- **Logs** are in `/mnt/us/abs-sync/abs-sync.log`. Please attach them to bug reports. They include book titles and your server address.

## Known problems

- **Only tested on one Kindle model and one firmware.** Reports from other models are welcome.
- **Not tested yet:** the reader's "About this book" screen on a book's first open, and upgrading through the repository.
- **Large books** with many images take a while to map the first time they're synced.

## How it works

The service is written in Go (`app/`) and is a static ARMv7 binary:

- It watches the reader's LIPC events (book opened, page turned, sleep and wake).
- It translates positions between the AZW3 the reader uses and the EPUB position Audiobookshelf stores.
- It moves the open book with `com.lab126.kppReader`.

The prompt is drawn with [FBInk](https://github.com/NiLuJe/FBInk) and a small touch helper (`helper/tapwait.c`). [docs/findings.md](docs/findings.md) has the details, the evidence and the dead ends.

| Folder | Contents |
|---|---|
| `app/` | The `abs-sync` service: library sync, position maps, page sync, prompts, the setup screen's API |
| `kindle/` | Library item, installer scriptlet, setup screen, boot job, prompt script |
| `kpm/` | KPM package: manifest, hooks, `build.sh` (output in `dist/`), `repo.py` (builds the KPM repository) |
| `helper/` | `tapwait`: waits for one tap with the touchscreen grabbed, so the page doesn't turn |
| `dev/` | Tools for exploring a Kindle over SSH (see [dev/README.md](dev/README.md)) |
| `tools/` | The calibre-based reference position map and the library cover generator |

### Build

Docker is needed for both commands:

- **`sh kpm/build.sh`** builds `dist/audiobookshelf-sync_<version>_kindlehf.kpkg`. The version is in `kpm/manifest.json`.
- **`go test ./...`** in `app/` runs the tests.

## License

GPL-3.0-or-later. See [LICENSE](LICENSE). FBInk, which the prompt uses, is also GPL-3.0.

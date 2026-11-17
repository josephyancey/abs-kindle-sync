# Notes for Claude

If `notes/` exists, it is the maintainer's private handoff folder (a separate private repo,
ignored here). Start with `notes/HANDOFF.md`, then `docs/findings.md` and `dev/README.md`.

- Build in Docker: `sh kpm/build.sh` (Go `golang:1.26-bookworm`, `CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7`).
- Releases: bump `kpm/manifest.json` and build. Create the `v<version>` GitHub release with the
  `.kpkg` and `kindle/install-audiobookshelf-sync.sh` attached. Then run
  `python3 kpm/repo.py <gh-pages checkout> dist/*.kpkg` and push gh-pages. The KPM manifest points
  at the release files, so the release must exist first.
- Download counts: `python3 tools/downloads.py`.
- Keep the app generic: nothing specific to one Kindle, network or library. This repo is public.
- No Claude attribution in commits or PRs. Commit or push only when asked.

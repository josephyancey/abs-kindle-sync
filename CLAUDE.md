# Notes for Claude

If `notes/` exists, it is the maintainer's private handoff folder (a separate private repo,
ignored here). Start with `notes/HANDOFF.md`, then `docs/findings.md` and `dev/README.md`.

- Build in Docker: `sh kpm/build.sh` (Go `golang:1.26-bookworm`, `CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7`).
- Releases: bump `kpm/manifest.json`, build, add the `.kpkg` to the `gh-pages` branch with
  `python3 kpm/repo.py <gh-pages checkout> dist/*.kpkg`, push it, and attach the `.kpkg` and
  `kindle/install-audiobookshelf-sync.sh` to a GitHub release.
- Keep the app generic: nothing specific to one Kindle, network or library. This repo is public.
- No Claude attribution in commits or PRs. Commit or push only when asked.

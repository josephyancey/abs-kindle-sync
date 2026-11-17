#!/usr/bin/env python3
"""Adds packages to this project's KPM repository and rebuilds its manifest.

    python3 kpm/repo.py SITE_DIR [PACKAGE.kpkg ...]

SITE_DIR is a checkout of the gh-pages branch, which GitHub Pages serves at
https://josephyancey.github.io/abs-kindle-sync/. Each .kpkg given is copied to
packages/<id>/artifacts/. Then manifest.json (KPM manifest version 2) and a small index.html are
rebuilt from every package already there, so older versions stay installable.
"""
import html
import json
import shutil
import sys
import tarfile
from pathlib import Path

REPO = {
    "manifest_version": 2,
    "id": "abskindlesync",
    "name": "Audiobookshelf Sync",
    "description": "Audiobookshelf Sync for Amazon's own Kindle reader.",
}
URL = "https://josephyancey.github.io/abs-kindle-sync/manifest.json"
SOURCE = "https://github.com/josephyancey/abs-kindle-sync"


def package_manifest(kpkg: Path) -> dict:
    with tarfile.open(kpkg, "r:gz") as tar:
        for name in ("manifest.json", "./manifest.json"):
            try:
                return json.load(tar.extractfile(name))
            except KeyError:
                pass
    sys.exit(f"{kpkg}: no manifest.json")


def main() -> None:
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    site = Path(sys.argv[1])
    for arg in sys.argv[2:]:
        kpkg = Path(arg)
        m = package_manifest(kpkg)
        dest = site / "packages" / m["id"] / "artifacts"
        dest.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(kpkg, dest / kpkg.name)
        print(f"added {kpkg.name}")

    packages = {}
    for kpkg in sorted((site / "packages").glob("*/artifacts/*.kpkg")):
        m = package_manifest(kpkg)
        p = packages.setdefault(m["id"], {
            "name": m["name"], "author": m["author"], "description": m["description"], "artifacts": [],
        })
        # The newest package's text wins
        if all(m["version"] > a["version"] for a in p["artifacts"]):
            p.update(name=m["name"], author=m["author"], description=m["description"])
        p["artifacts"].append({
            "url": kpkg.relative_to(site).as_posix(),
            "version": m["version"],
            "dependencies": m.get("dependencies", []),
            "supported_platforms": m.get("supported_platforms", []),
        })
    for p in packages.values():
        p["artifacts"].sort(key=lambda a: a["version"])

    (site / "manifest.json").write_text(json.dumps({**REPO, "packages": packages}, indent=2) + "\n", newline="\n")
    (site / ".nojekyll").touch()

    rows = []
    for pid, p in sorted(packages.items()):
        for a in reversed(p["artifacts"]):
            v = ".".join(map(str, a["version"]))
            rows.append(f'<li><a href="{html.escape(a["url"])}">{html.escape(p["name"])} {v}</a> '
                        f'({", ".join(a["supported_platforms"]) or "all"})</li>')
    (site / "index.html").write_text(f"""<!doctype html>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Audiobookshelf Sync KPM repository</title>
<style>body{{font:16px/1.5 system-ui,sans-serif;max-width:40em;margin:2em auto;padding:0 16px}}code{{background:#eee;padding:2px 4px}}</style>
<h1>Audiobookshelf Sync: KPM repository</h1>
<p>Add it on a jailbroken Kindle by typing this in the search bar, then install the package:</p>
<p><code>;kpm add-repo {URL}</code><br><code>;kpm install audiobookshelf-sync</code></p>
<p>Source, instructions and the one-tap installer: <a href="{SOURCE}">{SOURCE}</a></p>
<h2>Packages</h2>
<ul>
{chr(10).join(rows)}
</ul>
""", newline="\n")
    print(f"manifest: {sum(len(p['artifacts']) for p in packages.values())} artifact(s)")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Prints download counts for each release file, and the totals.

    python3 tools/downloads.py

.kpkg downloads include installs and upgrades through KPM, since the KPM repository points at the
release files. The installer scriptlet is downloaded once per one-tap install. Counts include
anyone fetching the files by hand, so treat them as an upper bound on Kindles.
"""
import json
import urllib.request

URL = "https://api.github.com/repos/josephyancey/abs-kindle-sync/releases?per_page=100"
req = urllib.request.Request(URL, headers={"Accept": "application/vnd.github+json", "User-Agent": "abs-kindle-sync-downloads"})
with urllib.request.urlopen(req) as r:
    releases = json.load(r)

totals = {"kpkg": 0, "installer": 0}
for rel in releases:
    print(rel["tag_name"])
    for a in rel["assets"]:
        print(f"  {a['download_count']:6d}  {a['name']}")
        totals["kpkg" if a["name"].endswith(".kpkg") else "installer"] += a["download_count"]
print(f"\npackage downloads (KPM installs, upgrades, by hand): {totals['kpkg']}")
print(f"one-tap installer downloads: {totals['installer']}")

#!/bin/sh
# KPM install hook, run with sh from the unpacked package folder. Also runs on upgrades.
# The payload is copied over /mnt/us/abs-sync; the sign-in, state and position maps there are kept.
set -e
DEST=/mnt/us/abs-sync

mkdir -p "$DEST"
cp -rf ./abs-sync/bin ./abs-sync/setup ./abs-sync/upstart "$DEST/"
chmod +x "$DEST/bin/abs-sync" "$DEST/bin/tapwait" "$DEST/bin/abs-prompt.sh" "$DEST/bin/abs-install.sh"
# Only when it changed: rewriting it makes the library index it again, which can drop it for a while
cmp -s "./Audiobookshelf Sync.sh" "/mnt/us/documents/Audiobookshelf Sync.sh" || cp -f "./Audiobookshelf Sync.sh" "/mnt/us/documents/Audiobookshelf Sync.sh"
rm -rf ./abs-sync

sh "$DEST/bin/abs-install.sh" restart
echo "Audiobookshelf Sync: installed. Open Audiobookshelf Sync from your library to sign in (it can take a minute to appear)."

#!/bin/sh
# KPM uninstall hook, run with sh from the package folder. "upgrade" means install.sh runs next
# with the new version, so only the service is stopped.
stop abs-sync >/dev/null 2>&1
pkill -x abs-sync 2>/dev/null
[ "$1" = upgrade ] && exit 0

if [ -f /etc/upstart/abs-sync.conf ]; then
	mntroot rw && rm -f /etc/upstart/abs-sync.conf
	mntroot ro
fi
sqlite3 /var/local/appreg.db <<'SQL'
DELETE FROM properties WHERE handlerId='io.github.abskindlesync';
DELETE FROM handlerIds WHERE handlerId='io.github.abskindlesync';
SQL
rm -rf /var/local/mesquite/abs-sync /var/local/abs-sync
# Keep the book list and position maps next to the books left in the library, so a reinstall
# doesn't download and map every book again. The sign-in is removed.
rm -rf /mnt/us/abs-sync/bin /mnt/us/abs-sync/setup /mnt/us/abs-sync/upstart
rm -f /mnt/us/abs-sync/config.json /mnt/us/abs-sync/abs-sync.log
rm -f "/mnt/us/documents/Audiobookshelf Sync.sh"
echo "Audiobookshelf Sync: removed. Synced books were left in your library; delete documents/Audiobookshelf and the abs-sync folder to remove them too."

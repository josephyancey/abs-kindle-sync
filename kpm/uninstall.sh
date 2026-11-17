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
rm -rf /var/local/mesquite/abs-sync /var/local/abs-sync /mnt/us/abs-sync
rm -f "/mnt/us/documents/Audiobookshelf Sync.sh"
echo "Audiobookshelf Sync: removed. Synced books were left in your library."

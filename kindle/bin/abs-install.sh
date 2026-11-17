#!/bin/sh
# abs-install.sh [restart]
# Sets up Audiobookshelf Sync from /mnt/us/abs-sync: the boot job, the setup screen, and the
# running service. Used by the library item and by the KPM install hook. "restart" restarts a
# running service so a new binary takes over.
# Scriptlets and KUAL actions don't run as root on current jailbreaks; the jailbreak's su (gandalf) does
if [ "$(id -u)" != 0 ]; then
	for SU in /var/local/kmc/bin/su /var/local/kmc/kindlehf/bin/su /var/local/kmc/kindlepw2/bin/su /var/local/kmc/armhf/bin/su /var/local/mkk/su; do
		[ "$(timeout 5 "$SU" -s /bin/sh -c "id -u" 2>/dev/null)" = 0 ] && exec "$SU" -s /bin/sh -c "sh '$(cd "$(dirname "$0")" && pwd)/$(basename "$0")' $*"
	done
fi
BASE=/mnt/us/abs-sync
APP_ID=io.github.abskindlesync
TARGET=/var/local/mesquite/abs-sync
DB=/var/local/appreg.db
JOB=/etc/upstart/abs-sync.conf

# The service, started at boot by an upstart job
if [ -f "$BASE/upstart/abs-sync.conf" ] && ! cmp -s "$BASE/upstart/abs-sync.conf" "$JOB"; then
	mntroot rw && cp "$BASE/upstart/abs-sync.conf" "$JOB"
	mntroot ro
fi
if [ -f "$JOB" ]; then
	# upstart starts the job's script and returns before abs-sync itself is running
	[ "$1" = restart ] && stop abs-sync >/dev/null 2>&1
	status abs-sync 2>/dev/null | grep -q "start/" || start abs-sync >/dev/null 2>&1
elif ! pidof abs-sync >/dev/null 2>&1; then
	(setsid "$BASE/bin/abs-sync" run >/dev/null 2>&1 </dev/null &)
fi

# The setup screen, copied where the web app engine reads apps from
if [ ! -f "$TARGET/index.html" ] || ! diff -r "$BASE/setup" "$TARGET" >/dev/null 2>&1; then
	rm -rf "$TARGET"
	cp -r "$BASE/setup" "$TARGET" || exit 1
fi
sqlite3 "$DB" <<SQL
INSERT OR IGNORE INTO interfaces(interface) VALUES('application');
INSERT OR IGNORE INTO handlerIds(handlerId) VALUES('$APP_ID');
INSERT OR REPLACE INTO properties(handlerId,name,value) VALUES('$APP_ID','lipcId','$APP_ID');
INSERT OR REPLACE INTO properties(handlerId,name,value) VALUES('$APP_ID','command','/usr/bin/mesquite -l $APP_ID -c file://$TARGET/');
INSERT OR REPLACE INTO properties(handlerId,name,value) VALUES('$APP_ID','supportedOrientation','U');
SQL

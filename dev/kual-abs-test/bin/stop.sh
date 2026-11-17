#!/bin/sh
# Undo start.sh: stop the keep-alive and tunnel, let the Kindle sleep and save power again.
# Scriptlets and KUAL actions don't run as root on current jailbreaks; the jailbreak's su (gandalf) does
if [ "$(id -u)" != 0 ]; then
	for SU in /var/local/kmc/bin/su /var/local/kmc/kindlehf/bin/su /var/local/kmc/kindlepw2/bin/su /var/local/kmc/armhf/bin/su /var/local/mkk/su; do
		[ "$(timeout 5 "$SU" -s /bin/sh -c "id -u" 2>/dev/null)" = 0 ] && exec "$SU" -s /bin/sh -c "sh '$(cd "$(dirname "$0")" && pwd)/$(basename "$0")' $*"
	done
fi
rm -f /tmp/abs-test/keepalive.on /tmp/abs-test/tunnel.on /mnt/us/abs-test/keepalive.on /mnt/us/abs-test/tunnel.on
pkill dbclient 2>/dev/null
iwconfig wlan0 power on 2>/dev/null
lipc-set-prop com.lab126.powerd preventScreenSaver 0
eips 2 38 "ABS Test: keep-alive stopped" 2>/dev/null

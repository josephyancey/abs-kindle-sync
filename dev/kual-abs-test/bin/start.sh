#!/bin/sh
# Development helper: keep a jailbroken Kindle reachable for testing.
# - pings the gateway so Wi-Fi stays up and the Kindle keeps answering
# - turns Wi-Fi power saving off (the radio dozes and misses incoming connections)
# - re-adds USBNetLite's port 22 firewall rule; the Kindle rewrites its firewall on Wi-Fi reconnects
# - optionally holds a reverse SSH tunnel to a relay (see dev/README.md), configured in
#   /mnt/us/abs-test/tunnel.conf
# Scriptlets and KUAL actions don't run as root on current jailbreaks; the jailbreak's su (gandalf) does
if [ "$(id -u)" != 0 ]; then
	for SU in /var/local/kmc/bin/su /var/local/kmc/kindlehf/bin/su /var/local/kmc/kindlepw2/bin/su /var/local/kmc/armhf/bin/su /var/local/mkk/su; do
		[ "$(timeout 5 "$SU" -s /bin/sh -c "id -u" 2>/dev/null)" = 0 ] && exec "$SU" -s /bin/sh -c "sh '$(cd "$(dirname "$0")" && pwd)/$(basename "$0")' $*"
	done
fi
D=/mnt/us/abs-test
# The loops run from RAM: USB drive mode unmounts /mnt/us, which kills scripts read from it
R=/tmp/abs-test
mkdir -p $D $R
# USBNetLite's sshd doesn't come back after a restart unless toggled; start it directly
# (key login only, same options USBNetLite uses with ALLOW_PASSWORD_LOGIN=false)
# A jailbreak update can drop USBNetLite's /usr/bin/dropbear link; its binary stays on /mnt/us
DROPBEAR=/usr/bin/dropbear
[ -x $DROPBEAR ] || DROPBEAR="/mnt/us/usbnetlite/bin/dropbearmulti dropbear"
if ! ps | grep -q "[d]ropbear "; then
	{ id; ls -la /usr/bin/dropbear /var/local/kmc/ /var/local/kmc/bin /var/local/kmc/*/bin/su /var/local/kmc/*/bin/gandalf /var/local/mkk/
	for SU in /var/local/kmc/bin/su /var/local/kmc/kindlehf/bin/su /var/local/kmc/armhf/bin/su /var/local/mkk/su; do echo "$SU: $(timeout 5 "$SU" -s /bin/sh -c "id -u" 2>&1)"; done
	cat /var/local/kmc/version; grep -E "J:|P:|ERROR|kmc/bin" /mnt/us/jb-migration.log | head; } > $D/dropbear-start.log 2>&1
	$DROPBEAR -R -H /mnt/us -p 22 -s >> $D/dropbear-start.log 2>&1
	# Copy the log to each relay (~kindle/dropbear-start.log), readable when SSH to the Kindle is down
	if [ -f $D/tunnel.conf ] && [ -f $D/tunnel_key ]; then
		. $D/tunnel.conf
		for h in ${RELAY_HOSTS:-$RELAY_HOST}; do
			HOME=$R dbclient -y -y -i $D/tunnel_key -p "${RELAY_PORT:-2222}" "${RELAY_USER:-kindle}@$h" "cat > dropbear-start.log" < $D/dropbear-start.log >/dev/null 2>&1 &
		done
	fi
	iptables -C INPUT -i wlan0 -p tcp --dport 22 -j ACCEPT 2>/dev/null || iptables -I INPUT -i wlan0 -p tcp --dport 22 -j ACCEPT
fi
touch $R/keepalive.on
lipc-set-prop com.lab126.powerd preventScreenSaver 1
cat > $R/keepalive-loop.sh <<'LOOP'
#!/bin/sh
L=/mnt/us/abs-test/keepalive.log
n=0
while [ -f /tmp/abs-test/keepalive.on ]; do
	GW=$(route -n 2>/dev/null | awk '$1 == "0.0.0.0" && $8 == "wlan0" { print $2; exit }')
	[ -n "$GW" ] && ping -c 1 -W 2 "$GW" >/dev/null 2>&1
	case "$(iwconfig wlan0 2>/dev/null)" in *"Power Management:off"*) ;; *) iwconfig wlan0 power off 2>/dev/null ;; esac
	if ps | grep -q "[d]ropbear"; then
		iptables -C INPUT -i wlan0 -p tcp --dport 22 -j ACCEPT 2>/dev/null || {
			iptables -I INPUT -i wlan0 -p tcp --dport 22 -j ACCEPT
			echo "$(date +%T) re-added ssh rule" >> $L
		}
	fi
	n=$((n + 1))
	[ $((n % 12)) -eq 1 ] && echo "$(date +%T) gw=$GW wifi=$(lipc-get-prop com.lab126.wifid cmState 2>/dev/null)" >> $L
	sleep 5
done
LOOP
ps | grep -q "[k]eepalive-loop.sh" || (setsid sh $R/keepalive-loop.sh >/dev/null 2>&1 </dev/null &)

if [ -f $D/tunnel.conf ] && [ -f $D/tunnel_key ]; then
	cp $D/tunnel.conf $D/tunnel_key $R/
	touch $R/tunnel.on
	cat > $R/tunnel-loop.sh <<'LOOP'
#!/bin/sh
# Reverse tunnel: the Kindle dials out to the relay and forwards RELAY_FORWARD_PORT on the
# relay back to its own sshd, so the relay can reach the Kindle without incoming Wi-Fi.
. /tmp/abs-test/tunnel.conf
export HOME=/tmp/abs-test
HOST=$1
while [ -f /tmp/abs-test/tunnel.on ]; do
	dbclient -y -y -N -K 15 -o ExitOnForwardFailure=yes -i /tmp/abs-test/tunnel_key -R "${RELAY_FORWARD_PORT:-2200}:127.0.0.1:22" -p "${RELAY_PORT:-2222}" "${RELAY_USER:-kindle}@${HOST}" >> /mnt/us/abs-test/tunnel.log 2>&1
	echo "$(date +%T) tunnel to $HOST exited $?" >> /mnt/us/abs-test/tunnel.log
	sleep 10
done
LOOP
	# A tunnel left hanging by a Wi-Fi drop never reconnects; start fresh
	for p in $(ps | grep "[t]unnel-loop.sh" | awk '{print $1}'); do kill $p; done
	killall dbclient 2>/dev/null
	. $D/tunnel.conf
	for h in ${RELAY_HOSTS:-$RELAY_HOST}; do
		(setsid sh $R/tunnel-loop.sh "$h" >/dev/null 2>&1 </dev/null &)
	done
fi
eips 2 38 "ABS Test: keep-alive started" 2>/dev/null

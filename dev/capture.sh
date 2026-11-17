#!/bin/sh
# Record what the stock reader exposes for 10 minutes: LIPC events, the active app, and
# reader-related system log lines. Output goes to /mnt/us/abs-test/capture/.
# Run it, use the reader (open a book, turn pages, sleep/wake), then read the files.
D=/mnt/us/abs-test/capture
rm -rf $D; mkdir -p $D
touch /mnt/us/abs-test/capture.on
printf "com.lab126.powerd\ncom.lab126.appmgrd\ncom.lab126.kppReader\ncom.lab126.pillow\ncom.lab126.wifid\ncom.lab126.cmd\ncom.lab126.scanner\n" | lipc-wait-event -m -t -l "*" > $D/lipc.txt 2>&1 &
echo $! > $D/pids
tail -F /var/log/messages 2>/dev/null | grep -iE "booklet|reader|LPR|position|powerd|screensaver|MRPR" > $D/messages.txt &
echo $! >> $D/pids
( last=""; while [ -f /mnt/us/abs-test/capture.on ]; do a=$(lipc-get-prop com.lab126.appmgrd activeApp 2>/dev/null); [ "$a" != "$last" ] && echo "$(date +%T) activeApp=$a" >> $D/activeapp.txt; last="$a"; sleep 1; done ) &
echo $! >> $D/pids
sleep 600
rm -f /mnt/us/abs-test/capture.on
kill $(cat $D/pids) 2>/dev/null
echo stopped >> $D/activeapp.txt

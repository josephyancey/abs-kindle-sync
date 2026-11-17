#!/bin/sh
# abs-prompt.sh TITLE MESSAGE DETAIL [TIMEOUT_S]
# Draws a Yes/No dialog over the current screen with FBInk, waits for a tap with tapwait
# (which grabs the touchscreen so the book underneath doesn't turn the page), and prints
# yes, no or timeout. Layout is computed from the device FBInk reports, so it adapts to
# any Kindle's resolution, DPI and touch orientation.
#
# Afterwards xrefresh makes every window repaint, whatever the answer. The reader's own redraw
# commands don't clear an FBInk overlay. Without xrefresh, the saved screen is copied back.
DIR=$(dirname "$0")
FBINK=${FBINK:-/mnt/us/libkh/bin/fbink}
[ -x "$FBINK" ] || FBINK=/var/local/kmc/bin/fbink
[ -x "$FBINK" ] || FBINK=$(command -v fbink)
TITLE=$1; MSG=$2; DETAIL=$3; TIMEOUT=${4:-60}

# Some FBInk builds crash on -e (the one jb.sh v2.6.0 installs), so ask each one on the device
for F in "$FBINK" /var/local/kmc/bin/fbink /mnt/us/libkh/bin/fbink /mnt/us/extensions/MRInstaller/bin/KHF/fbink /mnt/us/extensions/MRInstaller/bin/PW2/fbink; do
	[ -x "$F" ] || continue
	GEOM=$("$F" -e 2>/dev/null)
	case "$GEOM" in *viewWidth=*) break ;; esac
	GEOM=
done
eval "$GEOM"
W=$viewWidth; H=$viewHeight
FONTDIR=/usr/java/lib/fonts
REG=$FONTDIR/Amazon-Ember-Regular.ttf; BOLD=$FONTDIR/Amazon-Ember-Bold.ttf
[ -f "$REG" ] || REG=$FONTDIR/Bookerly-Regular.ttf
[ -f "$BOLD" ] || BOLD=$REG

# Sized in physical units from the DPI, so it fits in portrait and landscape on any model
U=${DPI:-300}
PAD=$((U / 5)); TITLEH=$((U / 4)); BODYH=$((U * 2 / 3)); BTNH=$((U / 3))
BW=$((W * 86 / 100)); [ $BW -gt $((U * 4)) ] && BW=$((U * 4))
BH=$((PAD + TITLEH + PAD / 2 + BODYH + PAD / 2 + BTNH + PAD))
BX=$(((W - BW) / 2)); BY=$(((H - BH) / 2))
B=$((U / 100 + 2))                         # border thickness
TITLEY=$((BY + PAD)); BODYY=$((TITLEY + TITLEH + PAD / 2))
BTNW=$(((BW - PAD * 3) / 2)); BTNY=$((BY + BH - PAD - BTNH))
NOX=$((BX + PAD)); YESX=$((NOX + BTNW + PAD))

SAVE=/tmp/abs-prompt.fb
LOG=${ABS_PROMPT_LOG:-/tmp/abs-prompt.log}
cat /dev/fb0 > $SAVE 2>/dev/null
f() { $FBINK -q -b "$@" >/dev/null 2>&1; }
# Frame
f -k top=$BY,left=$BX,width=$BW,height=$BH -B BLACK
f -k top=$((BY + B)),left=$((BX + B)),width=$((BW - 2 * B)),height=$((BH - 2 * B)) -B WHITE
# Text: FBInk's -t margins are distances from the screen edges
L=$((BX + PAD)); R=$((W - BX - BW + PAD))
f -t regular=$BOLD,size=11,top=$TITLEY,bottom=$((H - TITLEY - TITLEH)),left=$L,right=$R -m "$TITLE"
# Message and detail as one block, so the detail line always sits below however many lines the message wraps to
BODY=$MSG
[ -n "$DETAIL" ] && BODY=$(printf '%s\n\n**%s**' "$MSG" "$DETAIL")
body() { f -t regular=$REG,bold=$BOLD,size=$1,top=$BODYY,bottom=$((H - BODYY - BODYH)),left=$L,right=$R,format -m "$BODY"; }
body 9 || body 7
# Buttons: No is outlined, Yes is filled
f -k top=$BTNY,left=$NOX,width=$BTNW,height=$BTNH -B BLACK
f -k top=$((BTNY + B)),left=$((NOX + B)),width=$((BTNW - 2 * B)),height=$((BTNH - 2 * B)) -B WHITE
f -k top=$BTNY,left=$YESX,width=$BTNW,height=$BTNH -B BLACK
TXT=$((BTNY + BTNH / 2 - U / 9))
f -t regular=$BOLD,size=10,top=$TXT,bottom=$((H - BTNY - BTNH)),left=$NOX,right=$((W - NOX - BTNW)) -m "No"
f -t regular=$BOLD,size=10,top=$TXT,bottom=$((H - BTNY - BTNH)),left=$YESX,right=$((W - YESX - BTNW)) -C WHITE -B BLACK -O -m "Yes"
$FBINK -q -s top=$BY,left=$BX,width=$BW,height=$BH -W GC16 >/dev/null 2>&1

ORIENT=$(lipc-get-prop com.lab126.winmgr orientation 2>/dev/null)
ANSWER=$("$DIR/tapwait" auto "$W" "$H" "$TIMEOUT" "${touchSwapAxes:-0}" "${touchMirrorX:-0}" "${touchMirrorY:-0}" "${ORIENT:-U}" \
	"no:$NOX,$BTNY,$((NOX + BTNW)),$((BTNY + BTNH))" \
	"yes:$YESX,$BTNY,$((YESX + BTNW)),$((BTNY + BTNH))" 2>>"$LOG")
# Have every window repaint itself; copying the saved screen back can bring back stale pixels
if command -v xrefresh >/dev/null && DISPLAY=:0 xrefresh 2>/dev/null; then :
elif [ -s $SAVE ]; then
	cat $SAVE > /dev/fb0 2>/dev/null && $FBINK -q -s top=$BY,left=$BX,width=$BW,height=$BH -W GC16 >/dev/null 2>&1
fi
rm -f $SAVE
echo "$ANSWER"

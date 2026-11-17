#!/bin/sh
# Build tapwait for every Kindle (static, soft-float ARMv7, so it doesn't depend on the
# device's libc or float ABI). Needs Docker. Output: kindle/bin/tapwait
set -e
cd "$(dirname "$0")/.."
# pwd -W gives a Windows path under Git Bash, which Docker Desktop needs for the mount
SRC=$(pwd -W 2>/dev/null || pwd)
MSYS_NO_PATHCONV=1 docker run --rm -v "$SRC:/src" -w /src debian:bookworm-slim sh -c '
	apt-get update -qq >/dev/null &&
	apt-get install -y -qq gcc-arm-linux-gnueabi libc6-dev-armel-cross >/dev/null 2>&1 &&
	arm-linux-gnueabi-gcc -O2 -Wall -static -march=armv7-a -o kindle/bin/tapwait helper/tapwait.c'
ls -l kindle/bin/tapwait

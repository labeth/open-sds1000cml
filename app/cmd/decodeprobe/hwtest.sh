#!/usr/bin/env bash
# hwtest.sh — decode + serial-trigger verification for one protocol across all
# timebases. Usage: hwtest.sh <proto> <expect-substring> [triggerbytes-json]
# Requires the FPGA to be transmitting <proto>'s signal and the app deployed.
set -u
PROTO="${1:-can}"; EXPECT="${2:-ID:123 DLC:2 AB CD CRC:7F3C}"
SCOPE_ADDR="${SCOPE_ADDR:-192.168.1.209:8080}"
OTA_ADDR="${OTA_ADDR:-${SCOPE_ADDR%%:*}:5900}"
REPO="${REPO:-$(cd "$(dirname "$0")/../../.." && pwd)}"
OTA="$REPO/ota/dist/otactl -tcp $OTA_ADDR"
HTTP="http://$SCOPE_ADDR"
cd "$REPO/app"
export GOTMPDIR=/home/labeth/gotmp TMPDIR=/home/labeth/gotmp

echo "### $PROTO — DECODE ACROSS ALL TIME DOMAINS (expect: \"$EXPECT\")"
BEST=""
for td in 20E-9 50E-9 100E-9 200E-9 500E-9 1E-6 2E-6 5E-6 10E-6 20E-6 50E-6 100E-6; do
  $OTA scpi "TDIV $td" >/dev/null 2>&1; sleep 1.2
  out=$(go run ./cmd/decodeprobe "$PROTO" 2>/dev/null)
  spb=$(echo "$out" | grep -oE "spb=[0-9.]+" | head -1)
  n=$(echo "$out" | grep -oF "$EXPECT" | wc -l)
  ok="·"; [ "$n" -gt 0 ] && { ok="PASS x$n"; [ -z "$BEST" ] && BEST="$td"; }
  printf "  %-8s %-10s %s\n" "$td" "${spb:-spb=?}" "$ok"
done
echo "  first-good timebase: ${BEST:-NONE}"

#!/bin/sh
# coldstart.sh — bring the instrument to a known-good, freshly-powered state and
# hand it back to our app, so an experiment starts from silicon reset.
#
#   tools/hw/coldstart.sh            # power cycle, take over, verify, report
#   tools/hw/coldstart.sh --no-cycle # take over and verify without cycling
#   PRIME=A tools/hw/coldstart.sh    # ... and prime the SRAM through the vendor app first
#   PRIME=B tools/hw/coldstart.sh    # ... with the opposite nibble
#
# Why: the MAX V and the SRAM hold state we cannot read and cannot reset from
# the Cyclone. An experiment that drove them can leave them somewhere odd, and
# the next experiment would then be measuring the wreckage of the last one
# rather than the board. A mains cycle is the only reset we have that reaches
# every chip, so every trial starts with one.
#
# A power cycle returns the unit to the VENDOR app (auto_takeover is false), so
# this re-takes it and waits for our fabric to verify before reporting ready.
#
# PRIME uses that vendor window. the acq2 analysis branch records
# a known SRAM content written by the vendor firmware before a takeover, and that
# "a power cycle destroys the prime" — which made it single-use and made this
# protocol look like it spent our ground truth. It does not: the prime needs the
# vendor FIRMWARE, and a power cycle is precisely what hands us that. We drive it
# over SCPI while it owns the instrument, let it acquire into the SRAM, STOP it,
# and only then take over. Nothing here opens, hashes or copies the vendor
# bitstream; the vendor app loads its own image at boot as it always does.
#
#   PRIME=A   OFST +1.5 V, 0.5 V/div, TDIV 50 NS (500 MSa/s) -> codes 0010_xxxx
#   PRIME=B   OFST -1.6 V, same otherwise                    -> codes 1100_xxxx
#
# Two distinct, known fills mean a read experiment gets a VALUE test rather than
# a change test: a lane that moves must move toward a predicted pattern.
set -eu
DEV=${DEV:-192.168.1.209}
PORT=${PORT:-5900}
SHELLY=${SHELLY:-192.168.1.223}
HTTP=${HTTP:-http://$DEV:8080}
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
OTACTL=$ROOT/ota/dist/otactl
[ -x "$OTACTL" ] || make -C "$ROOT/ota" otactl >/dev/null

say() { printf '%s %s\n' "$(date +%H:%M:%S)" "$*"; }

PRIME=${PRIME:-}

if [ "${1:-}" != "--no-cycle" ]; then
	if [ -n "$PRIME" ]; then
		# taken_over survives a power cycle by design, so the agent relaunches OUR
		# app ~24 s into boot and the vendor never runs. Release first, and sync,
		# or the state file is still saying taken_over=true when the power drops.
		say "releasing control so the vendor app owns the next boot"
		"$OTACTL" -tcp "$DEV:$PORT" untakeover >/dev/null 2>&1 || true
	fi
	say "sync + mains power cycle via $SHELLY"
	"$OTACTL" -tcp "$DEV:$PORT" exec sync >/dev/null 2>&1 || true
	"$OTACTL" -shelly "$SHELLY" power cycle
	sleep 5
fi

say "waiting for the device to answer"
n=0
until ping -c1 -W2 "$DEV" >/dev/null 2>&1; do
	n=$((n+1)); [ $n -lt 90 ] || { say "FAIL: no ping after 90 tries"; exit 1; }
	sleep 2
done
say "waiting for the OTA agent"
n=0
until "$OTACTL" -tcp "$DEV:$PORT" ping >/dev/null 2>&1; do
	n=$((n+1)); [ $n -lt 90 ] || { say "FAIL: agent never answered"; exit 1; }
	sleep 2
done

if [ -n "$PRIME" ]; then
	case "$PRIME" in
	A) OFST=1.5 ;;
	B) OFST=-1.6 ;;
	*) say "FAIL: PRIME must be A or B"; exit 2 ;;
	esac
	# Prove the vendor is the one answering before priming anything. Our own app
	# serves a SCPI stub that answers *IDN? too (serial SDS00000000000, version
	# 8.01.01.99R9); the real firmware answers SDS10BA2160661 / 6.01.01.21R2. If
	# we prime against our own stub we prime nothing and would not notice.
	say "checking who owns the instrument before priming"
	idn=$("$OTACTL" -tcp "$DEV:$PORT" scpi "*IDN?" 2>/dev/null | tr -d '\r\n')
	case "$idn" in
	*SDS00000000000*|*8.01.01.99R9*)
		say "FAIL: *IDN? came back [$idn] — that is OUR stub, not the vendor."
		say "      The takeover state survived the cycle, so nothing would be primed."
		exit 3 ;;
	"")
		say "FAIL: no answer to *IDN? — cannot confirm the vendor owns the instrument"
		exit 3 ;;
	esac
	say "  vendor confirmed: [$idn]"
	say "priming the SRAM through the vendor app: prime $PRIME (OFST ${OFST}V)"
	for cmd in "CHDR OFF" "C1:TRA ON" "C2:TRA ON" "C1:VDIV 0.5V" "C2:VDIV 0.5V" 	           "C1:OFST ${OFST}V" "C2:OFST ${OFST}V" "TDIV 50NS" "TRMD AUTO" "RUN"; do
		"$OTACTL" -tcp "$DEV:$PORT" scpi "$cmd" >/dev/null 2>&1 || say "  (scpi '$cmd' did not answer)"
	done
	sleep 3
	"$OTACTL" -tcp "$DEV:$PORT" scpi "STOP" >/dev/null 2>&1 || true
	sara=$("$OTACTL" -tcp "$DEV:$PORT" scpi "SARA?" 2>/dev/null | tr -d '\r\n')
	say "  sample rate now [$sara]"
	say "  SRAM now holds prime $PRIME; it survives the takeover and a fabric reload, not a power cycle"
fi

say "taking over from the vendor app"
"$OTACTL" -tcp "$DEV:$PORT" takeover >/dev/null

say "waiting for our app and a verified fabric"
n=0
until curl -sS -m 5 "$HTTP/api/diag/status" 2>/dev/null | grep -q '"ok":true'; do
	n=$((n+1)); [ $n -lt 90 ] || { say "FAIL: app/fabric never came up"; exit 1; }
	sleep 2
done

curl -sS -m 20 "$HTTP/api/diag/status" | python3 -c '
import json,sys
d=json.load(sys.stdin); i=d["identity"]
print("           fabric build-ID %s (want %s) ok=%s conf_done=%s" % (i["build_id"], i["want_build_id"], i["ok"], i["conf_done"]))'
curl -sS -m 20 "$HTTP/api/diag/gpmc" | python3 -c '
import json,sys
d=json.load(sys.stdin)
print("           gpmc boot source: %s  rd_cycle=%s" % (d["boot"]["source"], d["current"]["rd_cycle"]))'
curl -sS -m 20 "$HTTP/api/status" | python3 -c '
import json,sys
d=json.load(sys.stdin)
print("           engine running=%s fps=%s bus_errors=%s wedged=%s" % (d["running"], d["fps"], d["bus_errors"], d["wedged"]))'
say "COLD START READY"

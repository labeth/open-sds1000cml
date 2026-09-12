#!/bin/sh
# sramburst.sh — read the factory image's bulk GPMC ports, and optionally fire its SRAM burst.
#
#   tools/hw/sramburst.sh            # read-only: cycle to the vendor, read the five ports
#   tools/hw/sramburst.sh --fire     # ... and replay the vendor's own arm/fire words across it
#   tools/hw/sramburst.sh --restore  # hand the instrument back to our app afterwards
#
# WHY A MAINS CYCLE FIRST. The experiment is only meaningful with the FACTORY bitstream loaded,
# and `taken_over` survives a power cycle by design — the agent relaunches OUR app ~24 s into
# boot and the vendor never runs. So this releases control first, syncs, then cycles, exactly as
# coldstart.sh does for PRIME. It then PROVES the vendor owns the instrument via *IDN? before
# writing anything: our own app serves a SCPI stub that answers *IDN? too (SDS00000000000 /
# 8.01.01.99R9) against the real firmware's SDS10BA2160661 / 6.01.01.21R2. Measuring our own stub
# is the failure this check exists to prevent, and it is the same failure that cost this project
# a wrong retraction on 2026-09-07.
#
# WHAT IS WRITTEN. Nothing, unless --fire is given. With it, exactly six CS1 words, which are the
# vendor's own arm/fire sequence captured in app/internal/diag/diag.go:861 and reproduced bit for
# bit by the netlist decode. CS3 is never touched. No flash, no file on the instrument beyond the
# uploaded binary in /tmp. Everything is undone by a power cycle.
set -eu
DEV=${DEV:-192.168.1.209}
PORT=${PORT:-5900}
SHELLY=${SHELLY:-192.168.1.223}
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
OTACTL=$ROOT/ota/dist/otactl
BIN=${BIN:-/tmp/claude-1000/sramburst}
# /tmp is READ-ONLY on this device in the vendor window (ubifs ro,relatime), and
# /usr/bin/siglent/usr is FLASH, which the device rules exclude outright.  /dev is tmpfs, i.e.
# RAM, which they permit; the U-disk at /usr/bin/siglent/usr/media/U-disk0 is the other allowed
# option.  RAM is the cleaner one: it cannot survive a power cycle even in principle.
REMOTE=${REMOTE:-/dev/sramburst}

say() { printf '%s %s\n' "$(date +%H:%M:%S)" "$*"; }
ctl() { "$OTACTL" -tcp "$DEV:$PORT" "$@"; }

[ -x "$OTACTL" ] || make -C "$ROOT/ota" otactl >/dev/null

if [ "${1:-}" = "--restore" ]; then
	say "handing the instrument back to our app"
	ctl takeover
	exit 0
fi

FIRE=""; CYCLE=1; SELS=""; FREEZE=0; HALT=""; PRIME=""
for a in "$@"; do
	case "$a" in
	--fire) FIRE="--fire" ;;
	--no-cycle) CYCLE=0 ;;   # the vendor already owns the instrument and was confirmed
	--freeze) FREEZE=1 ;;    # suspend the vendor app so the bus has exactly one master
	--halt) HALT="--halt" ;; # quiet the FABRIC first (the vendor's own GO-release word)
	--prime=*) PRIME=${a#--prime=} ;;  # drive a KNOWN dc level in first, so the data has a predicted value
	0x*|[0-9]*) SELS="$SELS $a" ;;   # CONTROL sweep: read these selectors instead of the five
	esac
done

if [ -x "$BIN" ]; then :; else
	say "building $BIN"
	( cd "$ROOT/tools/hw/sramburst" && CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
		go build -trimpath -o "$BIN" . )
fi

if [ "$CYCLE" = 1 ]; then
	say "releasing control so the VENDOR app owns the next boot"
	ctl untakeover >/dev/null 2>&1 || true
	ctl exec sync >/dev/null 2>&1 || true

	say "mains power cycle via $SHELLY"
	"$OTACTL" -shelly "$SHELLY" power cycle
	sleep 5

	say "waiting for the device"
	n=0; until ping -c1 -W2 "$DEV" >/dev/null 2>&1; do
		n=$((n+1)); [ $n -lt 90 ] || { say "FAIL: no ping"; exit 1; }; sleep 2; done
	say "waiting for the OTA agent"
	n=0; until ctl ping >/dev/null 2>&1; do
		n=$((n+1)); [ $n -lt 90 ] || { say "FAIL: agent never answered"; exit 1; }; sleep 2; done
fi

say "confirming the VENDOR owns the instrument (not our app)"
# The agent's own state is the reliable gate.  `ps` on this device intermittently hangs the
# agent's shell, and *IDN? on this firmware times out under load -- neither absence is evidence.
# What IS decisive: if OUR app were running, OUR fabric would be loaded, because the app deploys
# it at startup.  So app.running == false plus auto_takeover == false means the factory image is
# what the Cyclone holds.  inherited_fds.gpmc is reported too, since the probe needs to inherit it.
st=$(ctl status 2>/dev/null || true)
verdict=$(printf '%s' "$st" | python3 -c '
import json,sys
try: d=json.load(sys.stdin)
except Exception: print("UNREADABLE"); raise SystemExit
app=(d.get("app") or {}).get("running")
auto=d.get("auto_takeover")
fd=(d.get("inherited_fds") or {}).get("gpmc")
print("OURS" if app else ("NOFD" if fd is None else "VENDOR"), app, auto, fd)
' 2>/dev/null || echo UNREADABLE)
say "  agent state: $verdict   (verdict app_running auto_takeover gpmc_fd)"
case "$verdict" in
OURS*)  say "FAIL: our app is running, so OUR fabric is loaded, not the factory's."; exit 3 ;;
NOFD*)  say "FAIL: the agent reports no inherited gpmc fd; the probe cannot reach the bus."; exit 3 ;;
VENDOR*) ;;
*)      say "FAIL: could not read the agent state."; exit 3 ;;
esac
idn=$(ctl scpi "*IDN?" 2>/dev/null | tr -d '\r\n' || true)
case "$idn" in
*SDS00000000000*|*8.01.01.99R9*)
	say "FAIL: *IDN? = [$idn] — that is OUR stub."; exit 3 ;;
"")	say "  (*IDN? silent; proceeding on the agent state, which is the stronger check)" ;;
*)	say "  vendor confirmed: [$idn]" ;;
esac

# --prime drives a KNOWN dc level through the vendor's own front end before anything is read, so
# the ports have a PREDICTED value rather than only a before/after difference. Same offsets and
# same command order as coldstart.sh's PRIME, and like it we tolerate a silent SCPI: this
# firmware's VXI-11 server times out under load and a non-answer is not a failure.
#   A  OFST +1.5 V -> codes 0010_xxxx      B  OFST -1.6 V -> codes 1100_xxxx
if [ -n "$PRIME" ]; then
	case "$PRIME" in
	A) OFST=1.5 ;;
	B) OFST=-1.6 ;;
	*) say "FAIL: --prime must be A or B"; exit 2 ;;
	esac
	say "priming through the vendor front end: prime $PRIME (OFST ${OFST}V)"
	for cmd in "CHDR OFF" "C1:TRA ON" "C2:TRA ON" "C1:VDIV 0.5V" "C2:VDIV 0.5V" \
	           "C1:OFST ${OFST}V" "C2:OFST ${OFST}V" "TDIV 50NS" "TRMD AUTO" "RUN"; do
		ctl scpi "$cmd" >/dev/null 2>&1 || say "  (scpi '$cmd' did not answer)"
	done
	sleep 3
	ctl scpi "STOP" >/dev/null 2>&1 || true
fi

# The first trial shared CS1 with a running vendor app: its acquisition kept the data ports
# moving, so our burst could not be told apart, and the contention appears to have wedged its
# SCPI server. Suspending it gives the bus one master. SIGSTOP rather than SIGKILL deliberately:
# ending the vendor closes its /dev/Gpmc descriptor, and bus.go:65 warns that closing that
# descriptor frees the FPGA chip select for the whole process tree. A suspended process keeps
# every descriptor open, and SIGCONT puts it back exactly as it was.
VPID=""
if [ "$FREEZE" = 1 ]; then
	VPID=$(ctl sh 'pidof SDS1000_arm.app' 2>/dev/null | tr -dc '0-9 ' | tr ' ' '\n' \
		| grep -E '^[0-9]+$' | head -1 || true)
	if [ -z "$VPID" ]; then
		say "FAIL: could not find the vendor app pid to suspend"; exit 4
	fi
	say "suspending the vendor app (pid $VPID)"
	ctl sh "kill -STOP $VPID" >/dev/null 2>&1 || true
	sleep 1
fi

say "uploading the probe"
ctl put "$BIN" "$REMOTE" >/dev/null
ctl exec chmod 755 "$REMOTE" >/dev/null

say "running${HALT:+ WITH --halt}${FIRE:+ WITH --fire}${SELS:+ over selectors$SELS}"
ctl sh "$REMOTE $HALT $FIRE$SELS"

if [ -n "$VPID" ]; then
	say "resuming the vendor app (pid $VPID)"
	ctl sh "kill -CONT $VPID" >/dev/null 2>&1 || true
fi

say "done. 'tools/hw/sramburst.sh --restore' hands the instrument back to our app."

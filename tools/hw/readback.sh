#!/bin/sh
# Hand the instrument back to the vendor firmware and read the SRAM through it.
#
# Why this exists: we can WRITE a known constant into the SRAM (coldstart.sh
# PRIME drives the vendor's own acquisition at a railed offset), and we can put
# our fabric on the bus afterwards -- but until now nothing closed the loop.
# Every witness surface we built reads the SRAM's *neighbours*, never the SRAM.
# The vendor firmware is the only reader on this board, and E3 records [BENCH]
# that SRAM contents "survive a warm reboot and the fabric reload over CS3".
# If that is true, handing control back is a read port.
#
# The loop this completes:
#     coldstart.sh PRIME=x     vendor writes a known nibble into the SRAM
#     (our fabric does whatever the experiment is)
#     readback.sh              vendor reads it out again -> we see the value
#
# Nothing here opens, hashes or copies the vendor bitstream: the vendor app
# loads its own image, exactly as it does at every boot.
#
# The race: on relaunch the vendor resumes acquiring and overwrites the record.
# We STOP as early as we can reach it and report how long that took, so a null
# can be told apart from "we were too slow".
set -eu
DEV=${DEV:-192.168.1.209}
PORT=${PORT:-5900}
OTACTL=${OTACTL:-./ota/bin/otactl}
OUT=${OUT:-/tmp/wf.bin}
CH=${CH:-C1}

say() { printf '[readback] %s\n' "$*"; }
scpi() { "$OTACTL" -tcp "$DEV:$PORT" scpi "$@"; }

say "releasing control"
"$OTACTL" -tcp "$DEV:$PORT" untakeover >/dev/null 2>&1 || true
"$OTACTL" -tcp "$DEV:$PORT" exec sync >/dev/null 2>&1 || true

say "relaunching the vendor app in place (no power cycle: the SRAM keeps its contents)"
"$OTACTL" -tcp "$DEV:$PORT" restore-factory >/dev/null

# Poll *IDN? and STOP the instant the vendor answers, to take the record before
# it has refilled. Report the elapsed time so a null result is interpretable.
say "waiting for the vendor and stopping it as early as possible"
t0=$(date +%s)
n=0
while :; do
	idn=$(scpi "*IDN?" 2>/dev/null | tr -d '\r\n' || true)
	case "$idn" in
	*SDS00000000000*|*8.01.01.99R9*)
		say "FAIL: *IDN? is still OUR stub [$idn] -- the vendor never took over"; exit 3 ;;
	?*) break ;;
	esac
	n=$((n+1)); [ $n -lt 120 ] || { say "FAIL: the vendor never answered *IDN?"; exit 1; }
	sleep 1
done
scpi "STOP" >/dev/null 2>&1 || true
t1=$(date +%s)
say "  vendor up: [$idn]"
say "  STOP sent $((t1-t0)) s after the relaunch request"

scpi "CHDR OFF" >/dev/null 2>&1 || true
tdiv=$(scpi "TDIV?" 2>/dev/null | tr -d '\r\n' || true)
sara=$(scpi "SARA?" 2>/dev/null | tr -d '\r\n' || true)
ofst=$(scpi "${CH}:OFST?" 2>/dev/null | tr -d '\r\n' || true)
say "  TDIV [$tdiv]  SARA [$sara]  ${CH}:OFST [$ofst]"

say "reading the ${CH} record"
scpi "${CH}:WF? DAT2" > "$OUT" 2>/dev/null || { say "FAIL: waveform query failed"; exit 1; }
say "  $(wc -c < "$OUT") bytes into $OUT"

python3 - "$OUT" <<'PY'
import sys, collections
raw = open(sys.argv[1], 'rb').read()
# SCPI definite-length block: ...#9NNNNNNNNN<payload>
i = raw.find(b'#9')
if i >= 0:
    n = int(raw[i+2:i+11]); body = raw[i+11:i+11+n]
else:
    body = raw
    print("  (no #9 block header found -- histogramming the whole response)")
print("  payload %d bytes" % len(body))
if not body:
    print("  VERDICT: empty record -- no read-back")
    sys.exit(0)
hi = collections.Counter(b >> 4 for b in body)
tot = len(body)
print("  top-nibble histogram (the primed constant lives here):")
for nib, cnt in hi.most_common(6):
    print("    %s  %7d  %5.1f%%" % (format(nib, '04b'), cnt, 100.0*cnt/tot))
top, cnt = hi.most_common(1)[0]
frac = cnt / tot
label = {0b0010: "prime A (OFST +1.5V)", 0b1100: "prime B (OFST -1.6V)"}.get(top, "not a primed value")
print("  VERDICT: dominant nibble %s at %.1f%% -- %s" % (format(top, '04b'), 100*frac, label))
if frac < 0.5:
    print("           no single value dominates: this looks like a fresh acquisition, not a survivor")
PY

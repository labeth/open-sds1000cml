#!/bin/sh
# bus_probe.sh — drive the 27-ball bus the way the factory image does and watch
# it at the same rate (the acq2 analysis branch). The static sweeps of E3
# could not see a reaction that only exists while the bus is moving; this one
# can. Runs through the deployed app's /api/diag/busprobe.
#
#   tools/hw/bus_probe.sh                # the default sweep, results under ./busprobe-out
#   tools/hw/bus_probe.sh listen         # drive nothing, only watch (trigger on any departure)
#   RATE=2 tools/hw/bus_probe.sh         # 200 MHz phase clock instead of 50 MHz
#   OUT=the acq2 analysis branch$(date +%F) tools/hw/bus_probe.sh
set -u
DEV=${DEV:-192.168.1.209}
HTTP=${HTTP:-http://$DEV:8080}
OUT=${OUT:-./busprobe-out}
RATE=${RATE:-0}
CONFIRM=${CONFIRM:-2}   # samples a departure must hold: 0=1, 1=2, 2=4, 3=8. 0 fires on noise.
PHASES=${PHASES:-5}
mkdir -p "$OUT"

post() { # name json
	curl -sS -m 120 -H 'Content-Type: application/json' -d "$2" "$HTTP/api/diag/busprobe" -o "$OUT/$1.json"
}

report() { # name
	python3 - "$OUT/$1.json" "$1" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
if d.get("err"):
    print("%-22s ERROR %s" % (sys.argv[2], d["err"])); sys.exit(0)
mov = d.get("moving") or []
sing = [b["ball"] for b in (d.get("singles") or []) if b["changed"]]
if d.get("p6", {}).get("changed"): sing.append("P6")
print("%-22s samples=%-5d trig=%-5s wait=%-5s moving=%s%s" % (
    sys.argv[2], d.get("samples", 0), d.get("triggered"), d.get("waiting"),
    ",".join(mov) or "-", (" singles=" + ",".join(sing)) if sing else ""))
for b in (d.get("balls") or []):
    if b["changed"] and not b["driven"]:
        print("    %-4s first=%d last=%d high=%d edges=%d" % (b["ball"], b["first"], b["last"], b["high"], b["edges"]))
PY
}

case "${1:-sweep}" in
selftest)
	# drive a rotating one-hot on every phase and read it straight back: proves
	# the generator, the pad registers and the capture agree before anything is
	# claimed about the other end.
	post selftest "{\"rate\":$RATE,\"phases\":5,\"oe_phase\":31,\"pattern\":[1,2,4,8,16],\"raw\":true,\"samples\":32}"
	report selftest
	python3 - "$OUT/selftest.json" <<'PY2'
import json, sys
d = json.load(open(sys.argv[1]))
seen = sorted(set(d.get("bus") or []))
print("    distinct words: %s" % ", ".join("0x%07x" % w for w in seen[:8]))
print("    expected the one-hot rotation 0x1 0x2 0x4 0x8 0x10")
PY2
	;;
listen)
	post listen "{\"rate\":$RATE,\"phases\":1,\"oe_phase\":0,\"trigger\":true,\"confirm\":$CONFIRM}"
	report listen
	;;
k1)
	# K1 carries the inverse of the bus output enable in the factory image
	# (the acq2 analysis branch). Drive half the group with a
	# release window so K1 swings, listen on the other half.
	for k1 in 0 5 6 7; do
		post "k1-$k1" "{\"rate\":$RATE,\"phases\":5,\"oe_phase\":15,\"oe_mask\":16383,\"k2\":3,\"k1\":$k1,\"pattern\":[1,2,4,8,16],\"trigger\":true,\"confirm\":$CONFIRM}"
		report "k1-$k1"
	done
	;;
sweep)
	# 1. the group released, K2 forwarding the phase clock: does anything move?
	for k2 in 0 3 4 5; do
		post "listen-k2$k2" "{\"rate\":$RATE,\"phases\":$PHASES,\"oe_phase\":0,\"k2\":$k2,\"trigger\":true,\"confirm\":$CONFIRM}"
		report "listen-k2$k2"
	done
	# 2. driving a rotating word, one phase at a time released so the balls can
	#    answer inside the rotation — the shape §10 says the factory uses.
	for oe in 31 15 1 30; do
		post "drive-oe$oe" "{\"rate\":$RATE,\"phases\":$PHASES,\"oe_phase\":$oe,\"k2\":3,\"pattern\":[1,2,4,8,16]}"
		report "drive-oe$oe"
	done
	# 3. the same, with each clock-shaped ball in turn carrying the phase clock
	for aux in d1 d2 g2; do
		post "aux-$aux" "{\"rate\":$RATE,\"phases\":$PHASES,\"oe_phase\":15,\"k2\":3,\"$aux\":3,\"pattern\":[1,2,4,8,16],\"trigger\":true,\"confirm\":$CONFIRM}"
		report "aux-$aux"
	done
	;;
*)
	echo "usage: $0 [sweep|listen|selftest|k1]" >&2; exit 2 ;;
esac
echo "results in $OUT"

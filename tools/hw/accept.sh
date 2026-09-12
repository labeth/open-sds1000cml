#!/bin/sh
# accept.sh — hardware acceptance of the deployed default image through the
# app's /api/diag (workplan §5 rungs 1–3, 04-DESIGN §4 E2). Run by the
# orchestrator only; the instrument is never touched from a work package.
#
#   tools/hw/accept.sh                      # all checks, results under ./accept-out
#   OUT=the acq2 analysis branch$(date +%F) tools/hw/accept.sh
#   RAMP=1 tools/hw/accept.sh               # CH1 carries the bench Au +1 ramp: demand 0 ramp breaks
#
# Checks (each writes its JSON to $OUT and prints PASS/FAIL):
#   1 identity   BUILDID/VERSION/FABRIC_ID match the app's iface; CONF_DONE set
#   2 pll        CLK_STAT PLLA_LOCK and PLLB_LOCK
#   3 census     lane toggle counters: ≥ MIN_LANES of 80 ADC lanes toggling with encode on
#   4 e2         bus ownership follow-rate table (D2 0/1, 3 alternating blocks) — reported, not gated
#   5 drain      a full-record capture (20478 words = PRETRIG_MAX, the largest record the fabric
#                finalizes): payload non-zero and ≥ 2 distinct codes per channel;
#                with RAMP=1 also 0 ramp breaks on CH1 (rung 3 — the schema has no in-fabric
#                ramp source, so the ramp comes from the bench source)
set -u
DEV=${DEV:-192.168.1.209}
HTTP=${HTTP:-http://$DEV:8080}
OUT=${OUT:-./accept-out}
MIN_LANES=${MIN_LANES:-40}
RAMP=${RAMP:-0}
mkdir -p "$OUT"
fail=0

py() { python3 -c "$1" "$OUT/$2.json"; }
fetch() { # name url [post-json]
	if [ -n "${3:-}" ]; then
		curl -sS -m 120 -H 'Content-Type: application/json' -d "$3" "$HTTP$2" -o "$OUT/$1.json"
	else
		curl -sS -m 120 "$HTTP$2" -o "$OUT/$1.json"
	fi
}
verdict() { # name ok message
	if [ "$2" = 1 ]; then echo "PASS $1: $3"; else echo "FAIL $1: $3"; fail=1; fi
}

fetch status /api/diag/status || { echo "FAIL: $HTTP unreachable"; exit 1; }
r=$(py 'import json,sys;d=json.load(open(sys.argv[1]));i=d["identity"];print(int(bool(i["ok"]) and bool(i["conf_done"])),i["build_id"],i["conf_port"],(i.get("err") or "-"))' status)
set -- $r; verdict identity "$1" "build_id=$2 conf_port=$3 $4"
r=$(py 'import json,sys;d=json.load(open(sys.argv[1]));print(int(bool(d["pll_a_lock"]) and bool(d["pll_b_lock"])),d["pll_a_lock"],d["pll_b_lock"],d.get("m2_c2_ratio_x16"))' status)
set -- $r; verdict pll "$1" "plla=$2 pllb=$3 ratio_x16=$4"

fetch census /api/diag/census
r=$(py 'import json,sys;d=json.load(open(sys.argv[1]));n=sum(1 for l in d["lanes"] if l["idx"]<80 and l["tog"]>0);print(int(n>='"$MIN_LANES"'),n,d["summary"].get("bus_balls_toggling"))' census)
set -- $r; verdict census "$1" "adc lanes toggling=$2/80 (min $MIN_LANES); bus balls toggling=$3"

fetch e2 /api/diag/e2 '{"blocks":3,"repeats":4}'
r=$(py 'import json,sys;d=json.load(open(sys.argv[1]));print(int("conditions" in d and bool(d.get("restored"))),("|".join(d.get("summary",[])) or d.get("err") or "no-result").replace(" ","_"))' e2)
set -- $r; verdict e2 "$1" "$2"

fetch capture /api/diag/capture '{"words":20478}'
r=$(py 'import json,sys;d=json.load(open(sys.argv[1]));ok=d.get("words",0)>=20000 and d.get("words")==d.get("requested_words") and d.get("nonzero_words",0)>0 and d.get("distinct_ch1",0)>=2 and d.get("distinct_ch2",0)>=2 and bool(d.get("restored"))
ramp='"$RAMP"'=="1"
if ramp: ok = ok and d.get("ramp_breaks_ch1",1)==0
print(int(ok),d.get("words"),d.get("nonzero_words"),d.get("distinct_ch1"),d.get("distinct_ch2"),d.get("ramp_breaks_ch1"),d.get("burst_remain"),(d.get("err") or "-"))' capture)
set -- $r; verdict drain "$1" "words=$2 nonzero=$3 distinct=$4/$5 ramp_breaks_ch1=$6 burst_remain=$7 $8"

echo "results in $OUT"
exit $fail

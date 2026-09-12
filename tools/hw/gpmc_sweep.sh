#!/bin/sh
# gpmc_sweep.sh — rung R1 (06-TIERS §6): the GPMC CS1 read-timing sweep on a
# TSRC ramp, through the deployed app's /api/diag/gpmc. Run by the
# orchestrator only; the instrument is never touched from a work package.
#
#   tools/hw/gpmc_sweep.sh                 # status, sweep, table; results under ./gpmc-out
#   tools/hw/gpmc_sweep.sh status          # timing in force, boot decision, persisted file
#   PERSIST=1 tools/hw/gpmc_sweep.sh       # sweep, then persist the chosen timing next to the app
#   APPLY=1 tools/hw/gpmc_sweep.sh         # ... and apply it now through the ramp gate
#   tools/hw/gpmc_sweep.sh apply           # apply the persisted timing (ramp-gated) without sweeping
#   tools/hw/gpmc_sweep.sh factory         # back to the factory timing
#   GAP=1 tools/hw/gpmc_sweep.sh           # also sweep CYCLE2CYCLEDELAY (default: off, 4 was the first clean gap)
#   OUT=the acq2 analysis branch$(date +%F) tools/hw/gpmc_sweep.sh
#
# Pass (rung R1): 0 breaks and 0 underruns over >= 400 k words at the chosen
# setting, one tick of margin below the first failure on every knob; words/s
# stated; FCLK from the slope of ns/word vs RDCYCLETIME. The sweep restores
# the timing it started from; only "persist"+"apply" (or the next boot, after
# its own ramp check) puts the chosen timing in force.
set -u
DEV=${DEV:-192.168.1.209}
HTTP=${HTTP:-http://$DEV:8080}
OUT=${OUT:-./gpmc-out}
DRAINS=${DRAINS:-7}
BLOCKS=${BLOCKS:-3}
VERIFY=${VERIFY:-400000}
GAP=${GAP:-0}
PERSIST=${PERSIST:-0}
APPLY=${APPLY:-0}
mkdir -p "$OUT"

py() { python3 -c "$1" "$OUT/$2.json"; }
fetch() { # name url [post-json]
	if [ -n "${3:-}" ]; then
		curl -sS -m 1800 -H 'Content-Type: application/json' -d "$3" "$HTTP$2" -o "$OUT/$1.json"
	else
		curl -sS -m 120 "$HTTP$2" -o "$OUT/$1.json"
	fi
}

status() {
	fetch status /api/diag/gpmc || { echo "FAIL: $HTTP unreachable"; exit 1; }
	py 'import json,sys
d=json.load(open(sys.argv[1]))
if d.get("err"): print("timing:", d["err"])
c=d.get("current") or {}
print("in force : rdcycle=%s rdaccess=%s oe=%s..%s cs=%s..%s gap=%s (port=%s)"%(c.get("rd_cycle"),c.get("rd_access"),c.get("oe_on"),c.get("oe_off"),c.get("cs_on"),c.get("cs_rd_off"),c.get("gap"),d.get("port")))
b=d.get("boot") or {}
print("boot     : source=%s rejected=%s %s"%(b.get("source"),b.get("rejected"),b.get("reason","")))
p=d.get("persisted")
if p: f=p["fields"]; print("persisted: rdcycle=%s rdaccess=%s gap=%s  %.0f ns/word  fclk~%.1f MHz  saved %s on %s"%(f["rd_cycle"],f["rd_access"],f["gap"],p["ns_per_word"],p["fclk_mhz"],p["saved"],p["build_id"]))
else: print("persisted: none", d.get("file_err",""))' status
}

table() {
	py 'import json,sys
d=json.load(open(sys.argv[1]))
if d.get("err"): print("sweep error:", d["err"])
print("start: rdcycle=%(rd_cycle)s rdaccess=%(rd_access)s oe=%(oe_on)s..%(oe_off)s cs=%(cs_on)s..%(cs_rd_off)s gap=%(gap)s"%d["start"], " fast_drain=%s rec=%s"%(d["fast_drain"],d["rec_len"]))
def row(s):
    t=s["timing"]; print("  cyc=%2d acc=%2d oeoff=%2d csoff=%2d gap=%d | %5.0f ns/w (base %5.0f, spread %4.1f) breaks=%d pops_bad=%d under=%d mism=%d %s"%(t["rd_cycle"],t["rd_access"],t["oe_off"],t["cs_rd_off"],t["gap"],s["ns_per_word"],s["baseline_ns_per_word"],s["spread_ns"],s["breaks"],s["pops_mismatch"],s["underruns"],s["mismatch"],"PASS" if s["pass"] else "FAIL"))
for name in ("access","cycle","gap"):
    ph=d.get(name) or []
    if ph: print("phase %s:"%name); [row(s) for s in ph]
print("floors: rdaccess=%s rdcycle=%s gap=%s"%(d["floor_access"],d["floor_cycle"],d["floor_gap"]))
print("chosen: rdcycle=%(rd_cycle)s rdaccess=%(rd_access)s oe=%(oe_on)s..%(oe_off)s cs=%(cs_on)s..%(cs_rd_off)s gap=%(gap)s"%d["chosen"])
v=d["verify"]; print("verify: %d words %.0f ns/word = %.2f Mw/s breaks=%d pops_bad=%d under=%d mism=%d %s"%(v["words"],v["ns_per_word"],v["words_per_s"]/1e6,v["breaks"],v["pops_mismatch"],v["underruns"],v["mismatch"],"PASS" if v["pass"] else "FAIL"))
print("FCLK estimate: %.1f MHz (0 = fewer than 3 clean cycle points)"%d["fclk_mhz"])
print("restored: %s"%d["restored"])
print("R1 %s"%("PASS" if d["ok"] and d["restored"] else "FAIL"))
sys.exit(0 if d["ok"] and d["restored"] else 1)' sweep
}

case "${1:-sweep}" in
status)
	status
	;;
apply)
	fetch apply /api/diag/gpmc '{"action":"apply","which":"persisted"}'
	py 'import json,sys;d=json.load(open(sys.argv[1]));b=d.get("boot",{});print("apply:",b.get("source"),"rejected=%s"%b.get("rejected"),b.get("reason",""));c=(b.get("check") or {});print("check: %s words %.0f ns/word ok=%s"%(c.get("words"),c.get("ns_per_word",0),c.get("ok")));sys.exit(0 if d.get("ok") else 1)' apply
	;;
factory)
	fetch factory /api/diag/gpmc '{"action":"apply","which":"factory"}'
	status
	;;
sweep)
	status
	gap=false; [ "$GAP" = 1 ] && gap=true
	echo "sweep: drains=$DRAINS blocks=$BLOCKS verify=$VERIFY words, gap sweep=$gap (this takes a minute or two)"
	fetch start /api/diag/gpmc "{\"action\":\"sweep\",\"drains\":$DRAINS,\"blocks\":$BLOCKS,\"verify_words\":$VERIFY,\"sweep_gap\":$gap}" || { echo "FAIL: sweep request"; exit 1; }
	py 'import json,sys;d=json.load(open(sys.argv[1]));
print("started:", d.get("running"), d.get("err",""));
sys.exit(0 if d.get("running") or d.get("finished") else 1)' start || exit 1
	# The sweep runs on the device as a background job of short steps (the app
	# must keep writing its health token, 05-WORKPLAN 4.3), so poll it.
	while :; do
		fetch poll /api/diag/gpmc || { echo "FAIL: $HTTP unreachable during the sweep"; exit 1; }
		done_=$(py 'import json,sys
d=json.load(open(sys.argv[1])); s=d.get("sweep") or {}
print("  %-7s %-22s steps=%-4s %5.0fs %s"%(s.get("phase",""),s.get("setting",""),s.get("steps"),s.get("elapsed_s",0),s.get("err","")), file=sys.stderr)
print("1" if s.get("finished") else "0")' poll)
		[ "$done_" = 1 ] && break
		sleep 3
	done
	python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));json.dump(d.get("last_sweep") or {},open(sys.argv[2],"w"))' "$OUT/poll.json" "$OUT/sweep.json"
	table || exit 1
	if [ "$PERSIST" = 1 ]; then
		fetch persist /api/diag/gpmc '{"action":"persist"}'
		py 'import json,sys;d=json.load(open(sys.argv[1]));print("persist:", "ok" if d.get("ok") else d.get("err"));sys.exit(0 if d.get("ok") else 1)' persist || exit 1
		if [ "$APPLY" = 1 ]; then
			exec "$0" apply
		fi
	fi
	echo "results in $OUT"
	;;
*)
	echo "usage: $0 [status|sweep|apply|factory]" >&2
	exit 2
	;;
esac

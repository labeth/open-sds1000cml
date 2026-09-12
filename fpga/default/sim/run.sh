#!/usr/bin/env bash
# run.sh -- every self-checking iverilog testbench of the default image (+ fpga/common's).
# Exit non-zero on any failure. A bench passes only if vvp exits 0, prints "PASS <name>" and no
# "FAIL"/"error". Uses the `ifdef SIM stand-ins for altpll / altddio_out (Icarus cannot compile
# the Quartus altera_mf.v library); the megafunction instantiations are checked by Quartus only.
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
DEF="$(dirname "$HERE")"
FPGA="$(dirname "$DEF")"
COMMON="$FPGA/common"
OUT="${SIM_OUT:-$(mktemp -d)}"
IVL="${IVERILOG:-iverilog}"
VVP="${VVP:-vvp}"
SRC="$COMMON/sync.v $COMMON/ddio_pair.v $COMMON/pll_m2.v $COMMON/lane_in.v $COMMON/gpmc_slave.v \
     $DEF/adc_front.v $DEF/capture.v $DEF/drain.v $DEF/diag.v $DEF/default.v"
fail=0
echo "== fpga/common =="
SIM_OUT="$OUT" "$COMMON/sim/run.sh" || fail=1
echo "== fpga/default =="
run_tb() {
    local tb="$1"; shift
    local log="$OUT/$tb.log"
    if ! "$IVL" -g2005 -DSIM -I "$DEF" -o "$OUT/$tb.vvp" "$@" $SRC "$HERE/$tb.v" >"$log" 2>&1; then
        echo "[FAIL] $tb (compile)"; sed -n 1,20p "$log"; fail=1; return
    fi
    if "$VVP" -N "$OUT/$tb.vvp" >>"$log" 2>&1 && grep -q "^PASS $tb" "$log" && ! grep -qE "FAIL|error" "$log"; then
        echo "[PASS] $tb"
    else
        echo "[FAIL] $tb"; grep -E "FAIL|error" "$log" | head -20; fail=1
    fi
}
run_tb tb_adc_front
run_tb tb_capture
run_tb tb_drain
run_tb tb_diag
run_tb tb_top "$COMMON/sim/gpmc_bfm.v"
if [ $fail -eq 0 ]; then echo "ALL PASS"; else echo "SOME FAILED"; fi
exit $fail

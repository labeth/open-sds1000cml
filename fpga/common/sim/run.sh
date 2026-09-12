#!/usr/bin/env bash
# run.sh -- self-checking iverilog testbenches for fpga/common. Exits non-zero on any failure.
# A bench passes only if vvp exits 0, prints "PASS <name>" and prints no "FAIL".
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
COMMON="$(dirname "$HERE")"
OUT="${SIM_OUT:-$(mktemp -d)}"
IVL="${IVERILOG:-iverilog}"
VVP="${VVP:-vvp}"
SRC="$COMMON/sync.v $COMMON/ddio_pair.v $COMMON/pll_m2.v $COMMON/lane_in.v $COMMON/gpmc_slave.v"
fail=0
run_tb() {
    local tb="$1"; shift
    local log="$OUT/$tb.log"
    if ! "$IVL" -g2005 -DSIM -o "$OUT/$tb.vvp" "$@" $SRC "$HERE/$tb.v" >"$log" 2>&1; then
        echo "[FAIL] $tb (compile)"; sed -n 1,20p "$log"; fail=1; return
    fi
    if "$VVP" -N "$OUT/$tb.vvp" >>"$log" 2>&1 && grep -q "^PASS $tb" "$log" && ! grep -qE "FAIL|error" "$log"; then
        echo "[PASS] $tb"
    else
        echo "[FAIL] $tb"; grep -E "FAIL|error" "$log" | head -20; fail=1
    fi
}
run_tb tb_sync
run_tb tb_ddio
run_tb tb_pll
run_tb tb_lane_in
run_tb tb_gpmc_slave "$HERE/gpmc_bfm.v"
exit $fail

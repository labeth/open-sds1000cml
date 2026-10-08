#!/usr/bin/env bash
# ENGMODEL-OWNER-UNIT: FU-FPGA-BUILD
# sim.sh -- run the self-checking iverilog testbenches under */sim and
# images/*/sim. Testbenches the app's Go tests drive with fixtures (a Go test
# names them) are left to `go test` in app/. Exits non-zero on any failure.
set -u
FPGA="$(cd "$(dirname "$0")" && pwd)"
APP="$FPGA/../app"
OUT="$(mktemp -d)"
LIB="$OUT/lib"
mkdir -p "$LIB"
# iverilog finds a module in a library by file name: link every module name
# to the file that defines it.
for f in "$FPGA"/common/*.v "$FPGA"/trigger/*.v "$FPGA"/images/*/*.v "$FPGA"/common/sim/ddr_model.v "$FPGA"/common/sim/gpmc_bfm.v; do
  for m in $(grep -oE '^\s*module\s+\w+' "$f" | awk '{print $2}'); do
    [ -e "$LIB/$m.v" ] || ln -s "$f" "$LIB/$m.v"
  done
done
fail=0 ran=0 skipped=0
for tb in "$FPGA"/common/sim/tb_*.v "$FPGA"/trigger/sim/tb_*.v "$FPGA"/images/*/sim/tb_*.v; do
  n="$(basename "$tb" .v)"
  if grep -rqs --include='*_test.go' "\b$n\b" "$APP/internal"; then skipped=$((skipped+1)); continue; fi
  ran=$((ran+1))
  if ! iverilog -g2012 -DSIM -I "$FPGA/common" -I "$FPGA/trigger" -y "$LIB" -o "$OUT/$n.vvp" "$tb" >"$OUT/$n.log" 2>&1; then
    echo "[FAIL] $n (compile)"; sed -n 1,10p "$OUT/$n.log"; fail=1; continue
  fi
  if vvp -N "$OUT/$n.vvp" >>"$OUT/$n.log" 2>&1 && ! grep -qE '^FAIL|FATAL|ERROR' "$OUT/$n.log"; then
    echo "[PASS] $n"
  else
    echo "[FAIL] $n"; grep -E '^FAIL|FATAL|ERROR' "$OUT/$n.log" | head -5; fail=1
  fi
done
echo "$ran run, $skipped left to app Go tests"
exit $fail

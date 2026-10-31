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
# Each image's top level declares every net it uses: an undeclared one is an
# implicit wire, undriven, which synthesis takes as a constant, dropping the
# logic it feeds with only a warning (an edge trigger that never fired).
if node --experimental-strip-types -e 0 2>/dev/null; then
  for img in general packet frame line qualify stacking stream bits fast; do
    d="$(SDS_FPGA_BUILD_LOCKED=1 node --experimental-strip-types "$FPGA/build.ts" --image="$img" --prepare-only 2>/dev/null | sed -n 's/^Prepared //p')"
    ran=$((ran+1))
    if [ ! -d "$d" ]; then echo "[FAIL] $img top level (prepare)"; fail=1; continue; fi
    top="$(grep -m1 -oE '^module +[A-Za-z_0-9]+' "$d/bench.v" | awk '{print $2}')"
    # iverilog also names a net used above its declaration; only one declared
    # nowhere in the image's preprocessed source is undriven
    names="$(cd "$d" && iverilog -g2012 -Wimplicit -o /dev/null -s "$top" bench.v $(ls *.v | grep -v '^bench.v$') 2>&1 | sed -n "s/.*implicit definition of wire '\([A-Za-z_0-9]*\)'.*/\1/p" | sort -u)"
    (cd "$d" && iverilog -g2012 -E -o pre.v bench.v 2>/dev/null)
    w=""
    for n in $names; do
      grep -qE "\b(wire|reg|input|output|inout|integer|genvar)\b[^;]*\b$n\b" "$d/pre.v" || w="$w $n"
    done
    if [ -n "$w" ]; then echo "[FAIL] $img top level: undeclared net(s)$w"; fail=1; else echo "[PASS] $img top level declares its nets"; fi
    rm -rf "$d"
  done
else
  echo "[SKIP] the images' top levels: node with --experimental-strip-types is needed for build.ts"
fi
echo "$ran run, $skipped left to app Go tests"
exit $fail

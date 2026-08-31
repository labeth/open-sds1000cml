#!/usr/bin/env bash
# BUILD.sh -- build clkmeas.rbf WITH its timing constraints.
#
# ⚠ WHY THIS SCRIPT EXISTS AND WHY YOU MUST NOT USE `buildacq` FOR THIS DESIGN
#
#   fpga/cmd/buildacq copies only `*.v` (readSources) and the `include-d headers
#   (ResolveIncludes) into its scratch work dir. It does NOT copy `.sdc` files,
#   even though the QSF names one with `set_global_assignment -name SDC_FILE`.
#   Measured on this design, 2026-08-20:
#
#     Critical Warning (332012): Synopsys Design Constraints File file not found:
#     'clkmeas.sdc'. ... Without it, the Compiler will not properly optimize the design.
#     Info (332105): create_clock -period 1.000 -name clk clk          <-- 1 GHz on ball C2
#     Info (332105): create_clock -period 1.000 -name trig_sense trig_sense
#     ... all twelve counted balls auto-clocked at 1.000 ns ...
#     Critical Warning (332148): Timing requirements not met
#
#   That is precisely the failure mode `standard/acq.sdc`'s own header documents.
#   A bitstream fitted that way is not the bitstream whose STA you read, and for a
#   MEASUREMENT fabric that matters more than usual: an over-clocked counter does
#   not error, it silently undercounts, and an undercount is indistinguishable
#   from a slower clock in the answer.
#
#   The fix in buildacq is small (glob `*.sdc` beside `*.v` and add them to
#   `writes`), but that tool is shared by every design in this tree and is not
#   this design's to change. Until it lands, build clkmeas with this script.
#
# Output: clkmeas.rbf, which must be exactly 368,011 bytes (the uncompressed
# EP4CE10F17C8 CRAM size). Any other size means the fit or the device changed.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
QROOT="${QUARTUS_ROOTDIR:-$HOME/intelFPGA_lite/21.1/quartus}"
WORK="${WORK:-$HOME/fpgabuild/clkmeas}"
NAME=clkmeas

# Never two flows at once -- the same gate buildacq applies (quartus.GateReady).
if pgrep -x quartus_map >/dev/null || pgrep -x quartus_fit >/dev/null \
   || pgrep -x quartus_sta >/dev/null || pgrep -x quartus_asm >/dev/null \
   || pgrep -x quartus_cpf >/dev/null; then
    echo "BUILD.sh: a Quartus flow is already running -- refusing to start a second" >&2
    exit 1
fi

rm -rf "$WORK"; mkdir -p "$WORK"
cp "$HERE/$NAME.v" "$HERE/$NAME.qsf" "$HERE/$NAME.sdc" "$WORK/"
printf 'PROJECT_REVISION = "%s"\n' "$NAME" > "$WORK/$NAME.qpf"

cd "$WORK"
export QUARTUS_ROOTDIR="$QROOT"
export PATH="$QROOT/bin:$PATH"

"$QROOT/bin/quartus_map" "$NAME"
"$QROOT/bin/quartus_fit" "$NAME"
"$QROOT/bin/quartus_sta" "$NAME"
"$QROOT/bin/quartus_asm" "$NAME"
"$QROOT/bin/quartus_cpf" -c -o bitstream_compression=off \
    "output_files/$NAME.sof" "$HERE/$NAME.rbf"

# The SDC must actually have been read. If it was not, everything above is a
# 1 GHz fantasy and the .rbf must not be shipped.
if grep -q "332012" "$WORK/output_files/$NAME.map.rpt" "$WORK/output_files/$NAME.fit.rpt" 2>/dev/null; then
    echo "BUILD.sh: FAILED -- Quartus reported 332012 (SDC not found)" >&2
    rm -f "$HERE/$NAME.rbf"
    exit 1
fi

SZ=$(stat -c %s "$HERE/$NAME.rbf")
if [ "$SZ" != "368011" ]; then
    echo "BUILD.sh: FAILED -- $NAME.rbf is $SZ bytes, want 368011" >&2
    exit 1
fi

echo "OK: $HERE/$NAME.rbf ($SZ bytes)"
echo "reports: $WORK/output_files/$NAME.{fit,sta}.summary"
grep -c "^Type" "$WORK/output_files/$NAME.sta.summary" >/dev/null 2>&1 && \
  awk '/^Slack/{if ($3+0 < 0) n++} END{print "negative-slack analyses:", n+0}' \
      "$WORK/output_files/$NAME.sta.summary"

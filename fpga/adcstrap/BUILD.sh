#!/bin/bash
# Headless Quartus flow for one adcstrap variant, keeping every report.
set -euo pipefail
NAME="$1"
SRC=/home/labeth/ws/open-sds1000cml/fpga/adcstrap
WORK=/home/labeth/quartus_work/adcstrap-prep/$NAME
Q=/home/labeth/intelFPGA_lite/21.1/quartus
export QUARTUS_ROOTDIR=$Q
export PATH=$Q/bin:$PATH

if pgrep -f 'quartus_(map|fit|asm|sta|cpf)' >/dev/null; then
  echo "REFUSING: a Quartus flow is already running"; exit 1
fi

rm -rf "$WORK"; mkdir -p "$WORK"
cp "$SRC/adcstrap.v" "$WORK/"
cp "$SRC/adcstrap.sdc" "$WORK/"
cp "$SRC/$NAME.qsf" "$WORK/$NAME.qsf"
printf 'PROJECT_REVISION = "%s"\n' "$NAME" > "$WORK/$NAME.qpf"

cd "$WORK"
for t in quartus_map quartus_fit quartus_asm quartus_sta; do
  echo "--- $t $NAME"
  "$Q/bin/$t" "$NAME" > "$WORK/$t.log" 2>&1 || { echo "$t FAILED"; tail -30 "$WORK/$t.log"; exit 1; }
done
SOF="$WORK/output_files/$NAME.sof"
[ -f "$SOF" ] || SOF="$WORK/$NAME.sof"
"$Q/bin/quartus_cpf" -c -o bitstream_compression=off "$SOF" "$SRC/$NAME.rbf" > "$WORK/quartus_cpf.log" 2>&1
ls -l "$SRC/$NAME.rbf"

#!/bin/sh
# mkdiagstick.sh <sdsdiag-arm> <out.zip>: the diagnostics stick's zip. Its
# contents go on the root of an MBR + FAT32 stick: startup.sh, README.txt and
# sds-diag/ (the collector and its MODE). With SOURCE_DATE_EPOCH set, every
# entry carries that time, so two builds of a tree are byte-identical.
set -eu
bin=$1
out=$2
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/.." && pwd)
mkdir -p "$(dirname "$out")"
out="$(cd "$(dirname "$out")" && pwd)/$(basename "$out")"
staging=$(mktemp -d)
trap 'rm -rf "${staging:?}"' EXIT
mkdir -p "$staging/sds-diag"
cp "$here/diagstick/startup.sh" "$staging/startup.sh"
cp "$here/diagstick/README.txt" "$staging/README.txt"
cp "$here/diagstick/MODE" "$staging/sds-diag/MODE"
cp "$bin" "$staging/sds-diag/sdsdiag"
cp "$repo/LICENSE" "$staging/LICENSE.txt"
cp "$repo/SAFETY.txt" "$staging/SAFETY.txt"
chmod 0755 "$staging/startup.sh" "$staging/sds-diag/sdsdiag"
if [ -n "${SOURCE_DATE_EPOCH:-}" ]; then
  ts=$(date -u -d "@$SOURCE_DATE_EPOCH" +%Y%m%d%H%M.%S 2>/dev/null || date -u -r "$SOURCE_DATE_EPOCH" +%Y%m%d%H%M.%S)
  ( cd "$staging" && TZ=UTC find . -mindepth 1 -exec touch -t "$ts" {} + )
fi
rm -f "$out"
( cd "$staging" && find . -mindepth 1 | sed 's,^\./,,' | LC_ALL=C sort | TZ=UTC zip -Xq "$out" -@ )
echo ">> diagnostics zip: $out ($(du -h "$out" | cut -f1))"

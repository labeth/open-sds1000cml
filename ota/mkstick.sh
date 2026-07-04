#!/bin/sh
# mkstick.sh — build a ready-to-run open-sds1000cml USB boot stick.
#
# The SDS1000CML+ firmware, at boot, mounts a USB stick at
#   /usr/bin/siglent/usr/media/U-disk0   (device node /dev/sda1)
# and runs  startup.sh  from its root. For the firmware to see it, the stick
# MUST be:
#   * partitioned with an MBR (msdos) table — NOT GPT;
#   * a single primary partition of type 0x0c (FAT32 LBA), FAT32-formatted;
#   * that partition is what comes up as /dev/sda1 on the instrument.
#
# This makes a USABLE stick, not just the OTA agent: it builds and pre-loads the
# clean-room app into both app slots + a known-good emergency backstop, points
# the active slot at it, and ships agent.env with auto-takeover ON — so a new
# user just boots the stick and gets the working scope + web UI, no network
# deploy needed. (Update the app later over the network with `otactl update-app`.)
#
# Workflow: run this ON YOUR COMPUTER to build + populate the stick, then move
# the stick to the scope and REBOOT it (the firmware reads the stick only at
# boot). Then `ota/checkdev.sh <ip>` to confirm it came up.
#
# USAGE
#   ota/mkstick.sh <mountpoint>          build + copy onto an already-mounted,
#                                        already-FAT32 stick (no root needed if
#                                        the mount is user-writable)
#   ota/mkstick.sh --zip [out.zip]       build the release ZIP (its contents unzip
#                                        onto the root of an MBR+FAT32 stick)
#   ota/mkstick.sh --verify <mountpoint> only check an existing stick's layout
#   sudo ota/mkstick.sh --format /dev/sdX
#                                        DESTRUCTIVE: MBR-partition + FAT32-format
#                                        the whole device, then build + populate
#
# What lands on the stick:
#   startup.sh  commands
#   ota/{agent.A,agent.B,agent.active,agent.confirmed,agent.env,logs/}
#   agent-slots/A/app  B/app        <- the clean-room scope app (runnable)
#   agent-slots/emergency/app       <- known-good backstop (stubapp)
#   agent-slots/{active,confirmed}  <- point at A
#   agent-slots/staging/            <- upload staging for future OTA
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)   # the ota/ dir
repo=$(dirname -- "$here")                           # repo root
tree="$here/dist/stick"
appbin="$repo/app/dist/app-arm"                      # clean-room scope app
stubbin="$here/dist/stubapp-arm"                     # emergency backstop

die() { echo "mkstick: $*" >&2; exit 1; }

build() {
  echo ">> building OTA tree + agent (make -C ota dist) ..."
  ( cd "$here" && make dist >/dev/null )
  [ -f "$tree/startup.sh" ] && [ -d "$tree/ota" ] || die "make dist did not produce $tree"
  echo ">> building the clean-room app (make -C app app) ..."
  ( cd "$repo/app" && make app >/dev/null )
  [ -f "$appbin" ] || die "app build failed ($appbin)"
  echo ">> building the emergency backstop (make -C ota stubapp) ..."
  ( cd "$here" && make stubapp >/dev/null )
  [ -f "$stubbin" ] || die "stubapp build failed ($stubbin)"
}

# Required entries every ready-to-run boot stick must have.
REQUIRED="startup.sh commands ota/agent.A ota/agent.B ota/agent.active ota/agent.env \
          agent-slots/A/app agent-slots/B/app agent-slots/emergency/app \
          agent-slots/active agent-slots/confirmed"

verify() {
  mnt=$1
  [ -d "$mnt" ] || die "mountpoint '$mnt' is not a directory"
  ok=1
  for f in $REQUIRED; do
    if [ -e "$mnt/$f" ]; then echo "  ok   $f"; else echo "  MISSING $f"; ok=0; fi
  done
  if grep -qi 'OTA_AUTO_TAKEOVER=1' "$mnt/ota/agent.env" 2>/dev/null; then
    echo "  ok   agent.env: auto-takeover ON (boots straight into the app)"
  else
    echo "  note agent.env: auto-takeover NOT set — the stick will coexist, not take over"
  fi
  fstype=$(stat -f -c %T "$mnt" 2>/dev/null || echo "?")
  case "$fstype" in
    msdos|vfat|fat*) echo "  ok   filesystem is FAT ($fstype)";;
    *) echo "  WARN filesystem reports '$fstype' — the scope only mounts FAT32";;
  esac
  [ "$ok" = 1 ] || die "stick is missing required files (see above)"
  echo ">> stick is ready: $mnt"
}

ver() { git -C "$repo" describe --tags --always --dirty 2>/dev/null || echo dev; }

# Lay the full tree (base OTA layout + the app slots) into a directory. Shared by
# the mounted-stick, format, and zip paths.
write_files() {
  mnt=$1
  cp -a "$tree/startup.sh" "$tree/commands" "$mnt/"
  # License + safety text ride along on the stick (MIT requires the notice with
  # all copies; the takeover firmware reaches the user with safety text present).
  [ -f "$repo/LICENSE" ]    && cp -a "$repo/LICENSE"    "$mnt/LICENSE.txt"
  [ -f "$repo/SAFETY.txt" ] && cp -a "$repo/SAFETY.txt" "$mnt/SAFETY.txt"
  mkdir -p "$mnt/ota"; cp -a "$tree/ota/." "$mnt/ota/"
  for d in A B emergency staging; do mkdir -p "$mnt/agent-slots/$d"; done
  cp -f "$appbin"  "$mnt/agent-slots/A/app"
  cp -f "$appbin"  "$mnt/agent-slots/B/app"
  cp -f "$stubbin" "$mnt/agent-slots/emergency/app"
  printf 'A\n' > "$mnt/agent-slots/active"
  printf 'A\n' > "$mnt/agent-slots/confirmed"
}

# Write the whole tree to a mounted stick.
populate() {
  mnt=$1
  [ -d "$mnt" ] || die "mountpoint '$mnt' does not exist — mount the stick first"
  echo ">> writing boot anchor + agent + app slots to $mnt ..."
  write_files "$mnt"
  sync
  echo ">> done. Files on the stick:"
  verify "$mnt"
}

# Build a release ZIP whose CONTENTS unzip onto the root of a FAT32 stick.
zip_stick() {
  out=$1
  [ -n "$out" ] || out="$here/dist/open-sds1000cml-$(ver)-usb.zip"
  mkdir -p "$(dirname "$out")"
  # Make the output path ABSOLUTE — zip runs inside the staging tmpdir below, so a
  # relative path would resolve against that (and fail). (release-review B1.)
  out="$(cd "$(dirname "$out")" && pwd)/$(basename "$out")"
  staging=$(mktemp -d)
  write_files "$staging"
  rm -f "$out"
  ( cd "$staging" && zip -rq "$out" startup.sh commands ota agent-slots LICENSE.txt SAFETY.txt )
  rm -rf "$staging"
  echo ">> USB image zip: $out  ($(du -h "$out" | cut -f1))"
  echo "   Unzip its CONTENTS onto the ROOT of an MBR + FAT32 USB stick; the top"
  echo "   level of the stick must be startup.sh, ota/, agent-slots/."
  echo "   Contents:"; unzip -l "$out" | awk 'NR>3 && $4 !~ /agent-slots\/(A|B|emergency)\/app$/ && $4!=""{print "     "$4}' | grep -E '^\s+(startup|commands|ota|agent-slots)' | head; echo "     ... (+ the app binaries in agent-slots/)"
}

format_stick() {
  dev=$1
  # ---- safety gauntlet (formatting is destructive) ----
  case "$dev" in
    /dev/sd[a-z]) : ;;
    *) die "refusing: --format needs a whole USB disk like /dev/sdb (got '$dev'); NOT a partition, NOT the system disk" ;;
  esac
  base=$(basename "$dev")
  [ "$(cat "/sys/block/$base/removable" 2>/dev/null || echo 0)" = 1 ] \
    || die "refusing: $dev is not a REMOVABLE device (/sys/block/$base/removable != 1)"
  sectors=$(cat "/sys/block/$base/size" 2>/dev/null || echo 0)
  gib=$(( sectors / 2 / 1024 / 1024 ))
  [ "$sectors" -gt 0 ] || die "cannot read size of $dev"
  [ "$gib" -le 256 ] || die "refusing: $dev is ${gib} GiB — too big to be a USB boot stick (>256 GiB)"
  if lsblk -no MOUNTPOINT "$dev" 2>/dev/null | grep -q '^/$'; then
    die "refusing: $dev appears to hold the root filesystem"
  fi
  model=$(cat "/sys/block/$base/device/model" 2>/dev/null || echo "?")
  echo "About to ERASE and reformat:"
  echo "    device : $dev  (${gib} GiB, model: $model, removable)"
  echo "    scheme : MBR + one primary FAT32 (0x0c) partition"
  echo "ALL DATA ON $dev WILL BE LOST."
  printf "Type the device path again to confirm: "
  read confirm
  [ "$confirm" = "$dev" ] || die "confirmation did not match — aborted"

  echo ">> unmounting any partitions on $dev ..."
  for p in "$dev"?*; do umount "$p" 2>/dev/null || true; done
  echo ">> writing MBR + one FAT32-LBA partition ..."
  printf 'label: dos\n,,0x0c,*\n' | sfdisk --force "$dev" >/dev/null
  sync; sleep 1
  part="${dev}1"; [ -b "$part" ] || part="${dev}p1"
  [ -b "$part" ] || die "partition node not found after partitioning ($dev)"
  echo ">> formatting $part as FAT32 (label OPENSDS) ..."
  mkfs.vfat -F 32 -n OPENSDS "$part" >/dev/null
  mnt=$(mktemp -d)
  echo ">> mounting $part -> $mnt ..."
  mount "$part" "$mnt"
  populate "$mnt"
  umount "$mnt"; rmdir "$mnt"
  echo ">> $dev is ready. Move it to the scope and REBOOT (power-cycle) the scope;"
  echo "   it will boot straight into the clean-room app. Then: checkdev.sh <ip>"
}

case "${1:-}" in
  --verify) [ $# -ge 2 ] || die "usage: mkstick.sh --verify <mountpoint>"; verify "$2" ;;
  --format) [ $# -ge 2 ] || die "usage: sudo mkstick.sh --format /dev/sdX"; build; format_stick "$2" ;;
  --zip)    build; zip_stick "${2:-}" ;;
  ""|-h|--help)
    sed -n '2,46p' "$0" | sed 's/^# \{0,1\}//' ;;
  /dev/*) die "to format a device use:  sudo $0 --format $1" ;;
  *) build; populate "$1"
     echo ">> move the stick to the scope and REBOOT it — it boots straight into the app." ;;
esac

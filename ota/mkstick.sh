#!/bin/sh
# mkstick.sh — prepare the USB boot stick for open-sds1000cml.
#
# The SDS1000CML+ firmware, at boot, mounts a FAT32 USB stick at
#   /usr/bin/siglent/usr/media/U-disk0   (device node /dev/sda1)
# and runs  startup.sh  from its root. For the firmware to see it, the stick
# MUST be:
#   * partitioned with an MBR (msdos) table — NOT GPT;
#   * a single primary partition of type 0x0c (FAT32 LBA), FAT32-formatted;
#   * that partition is what comes up as /dev/sda1 on the instrument.
#
# This script lays out the required tree (via `make dist`) and writes it to the
# stick. It can also format a blank stick correctly (MBR + FAT32) for you.
#
# USAGE
#   ota/mkstick.sh <mountpoint>          copy the tree onto an already-mounted,
#                                        already-FAT32 stick (no root needed if
#                                        the mount is user-writable)
#   ota/mkstick.sh --verify <mountpoint> only check an existing stick's layout
#   sudo ota/mkstick.sh --format /dev/sdX
#                                        DESTRUCTIVE: MBR-partition + FAT32-format
#                                        the whole device, then copy the tree
#
# The tree written to the stick:
#   startup.sh   commands   ota/{agent.A,agent.B,agent.active,agent.confirmed,agent.env,logs/}
#   agent-slots/{A,B,emergency,staging}/    (app slots; filled later by otactl update-app)
#
# After a first-time stick: plug it into the powered-OFF scope, power on, then
#   ota/checkdev.sh <ip>     to confirm the agent came up.
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)   # the ota/ dir
tree="$here/dist/stick"

die() { echo "mkstick: $*" >&2; exit 1; }

build_tree() {
  echo ">> building the stick tree (make dist) ..."
  ( cd "$here" && make dist >/dev/null )
  [ -f "$tree/startup.sh" ] && [ -d "$tree/ota" ] || die "make dist did not produce $tree"
}

# Required top-level entries every valid boot stick must have.
REQUIRED="startup.sh commands ota/agent.A ota/agent.B ota/agent.active ota/agent.env agent-slots"

verify() {
  mnt=$1
  [ -d "$mnt" ] || die "mountpoint '$mnt' is not a directory"
  ok=1
  for f in $REQUIRED; do
    if [ -e "$mnt/$f" ]; then echo "  ok   $f"; else echo "  MISSING $f"; ok=0; fi
  done
  # Filesystem must be vfat/FAT32 (best-effort: the firmware only mounts FAT32).
  fstype=$(stat -f -c %T "$mnt" 2>/dev/null || echo "?")
  case "$fstype" in
    msdos|vfat|fat*) echo "  ok   filesystem is FAT ($fstype)";;
    *) echo "  WARN filesystem reports '$fstype' — the scope only mounts FAT32"; ;;
  esac
  [ "$ok" = 1 ] || die "stick is missing required files (see above)"
  echo ">> stick layout OK: $mnt"
}

copy_tree() {
  mnt=$1
  [ -d "$mnt" ] || die "mountpoint '$mnt' does not exist — mount the stick first"
  [ -w "$mnt" ] || die "mountpoint '$mnt' is not writable — mount rw (or run with sudo)"
  echo ">> copying tree to $mnt ..."
  # Preserve the tree; do NOT delete the app slots if the stick already has them.
  cp -a "$tree/startup.sh" "$tree/commands" "$mnt/"
  mkdir -p "$mnt/ota" "$mnt/agent-slots"
  cp -a "$tree/ota/." "$mnt/ota/"
  # agent-slots: create the dirs but never clobber existing app binaries.
  for d in A B emergency staging; do mkdir -p "$mnt/agent-slots/$d"; done
  sync
  echo ">> done. Files on the stick:"
  verify "$mnt"
}

format_stick() {
  dev=$1
  # ---- safety gauntlet (formatting is destructive) ----
  case "$dev" in
    /dev/sd[a-z]) : ;;
    *) die "refusing: --format needs a whole USB disk like /dev/sdb (got '$dev'); NOT a partition, NOT /dev/sda-system disk" ;;
  esac
  base=$(basename "$dev")
  [ "$(cat "/sys/block/$base/removable" 2>/dev/null || echo 0)" = 1 ] \
    || die "refusing: $dev is not a REMOVABLE device (/sys/block/$base/removable != 1)"
  # size sanity: sticks are small; refuse to nuke a big disk
  sectors=$(cat "/sys/block/$base/size" 2>/dev/null || echo 0)
  gib=$(( sectors / 2 / 1024 / 1024 ))
  [ "$sectors" -gt 0 ] || die "cannot read size of $dev"
  [ "$gib" -le 256 ] || die "refusing: $dev is ${gib} GiB — too big to be a USB boot stick (>256 GiB)"
  if grep -q " / " /proc/mounts && lsblk -no MOUNTPOINT "$dev" 2>/dev/null | grep -q '^/$'; then
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
  # single primary partition spanning the disk, type 0x0c (W95 FAT32 LBA)
  printf 'label: dos\n,,0x0c,*\n' | sfdisk --force "$dev" >/dev/null
  # settle + find the partition node (sdb1 or sdb-p1 style)
  sync; sleep 1
  part="${dev}1"; [ -b "$part" ] || part="${dev}p1"
  [ -b "$part" ] || die "partition node not found after partitioning ($dev)"
  echo ">> formatting $part as FAT32 (label OPENSDS) ..."
  mkfs.vfat -F 32 -n OPENSDS "$part" >/dev/null
  mnt=$(mktemp -d)
  echo ">> mounting $part -> $mnt and copying the tree ..."
  mount "$part" "$mnt"
  # copy runs as root here; that's fine
  cp -a "$tree/startup.sh" "$tree/commands" "$mnt/"
  mkdir -p "$mnt/ota" "$mnt/agent-slots"
  cp -a "$tree/ota/." "$mnt/ota/"
  for d in A B emergency staging; do mkdir -p "$mnt/agent-slots/$d"; done
  sync
  verify "$mnt"
  umount "$mnt"; rmdir "$mnt"
  echo ">> $dev is ready. Plug it into the powered-OFF scope, then power on."
}

case "${1:-}" in
  --verify) [ $# -ge 2 ] || die "usage: mkstick.sh --verify <mountpoint>"; verify "$2" ;;
  --format) [ $# -ge 2 ] || die "usage: sudo mkstick.sh --format /dev/sdX"; build_tree; format_stick "$2" ;;
  ""|-h|--help)
    sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//' ;;
  /dev/*) die "to format a device use:  sudo $0 --format $1" ;;
  *) build_tree; copy_tree "$1" ;;
esac

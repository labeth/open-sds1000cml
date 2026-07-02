#!/bin/sh
# open-sds F-1 boot anchor  (B1) — the dumb, dependency-free, NEVER-OTA'd anchor.
# ---------------------------------------------------------------------------
# Runs as root from the USB stick at boot, as a descendant of the boot process
# that already holds the inherited /dev/Gpmc (+ /dev/fpga_key) fd. Its ONLY
# jobs, in this exact order:
#
#   1. Set up environment / paths.
#   2. Run an optional `commands` file from the stick if present — a
#      network-independent root exec fallback (B7 tier c).
#   3. Launch the agent in a respawn loop so it can never stay dead (B3), with
#      A/B self-update + crash-loop revert.
#
# All remote access and control is through the agent + the host `otactl` tool
# (NATS, plus a local TCP control listener). There is NO shell service on the
# device. Recovery of last resort is the external mains power-cycle (otactl
# power, via the Shelly plug), not any on-device login.
#
# Contract: touches only the USB stick and RAM; never writes the instrument
# NAND. This file is the anchor — the OTA path must NEVER modify it (G5). The
# agent inherits fds from THIS shell, so this shell must never close them.
#
# Everything is overridable by env so the off-device harness can drive it with
# stubs (agent, commands) and assert ordering deterministically.

# --- 1. environment: LOCATE THE PAYLOAD -------------------------------------
# The stick lives at the vendor's FIXED U-disk mountpoint, which is also where
# the payload (ota/agent.env, ota/agent.A) resides. This is authoritative even
# when the firmware runs a COPY of this script from RAM — in which case $0's
# directory is NOT the stick, so we must NOT derive the stick from dirname("$0")
# (that was a regression from the proven original). OTA_USB overrides for the
# off-device harness. Kept to plain POSIX idioms for busybox ash.
_selfdir=$(dirname "$0" 2>/dev/null)
find_usb() {
    [ -n "$OTA_USB" ] && { echo "$OTA_USB"; return; }
    # First candidate that actually carries the payload; the fixed vendor mount
    # is tried FIRST because that is the known-good location.
    for _c in /usr/bin/siglent/usr/media/U-disk0 "$_selfdir" \
              /mnt/udisk /mnt/usb0 /mnt/usb /media/usb /media/U-disk0; do
        [ -n "$_c" ] && [ -f "$_c/ota/agent.env" ] && { echo "$_c"; return; }
    done
    echo /usr/bin/siglent/usr/media/U-disk0
}
USB=$(find_usb)

# The stick can come up (or flake to) read-only (vfat errors=remount-ro under
# write load, or after a power-cut fsck). Remount it rw UP FRONT so the marker,
# logging, and the agent's slot writes work at all — otherwise every write here
# fails silently and it looks like the script never ran. Device node is
# /dev/sda1 on this unit. Harmless if already rw or not the USB. (From the
# proven original anchor.)
case "$USB" in /usr/bin/siglent/usr/media/U-disk0)
  mount -o remount,rw "$USB" 2>/dev/null || mount -o remount,rw /dev/sda1 "$USB" 2>/dev/null ;;
esac

OTA_DIR="${OTA_DIR:-$USB/ota}"
mkdir -p "$OTA_DIR/logs" 2>/dev/null

# --- 0. EARLY EXECUTION MARKER ----------------------------------------------
# Plain guarded appends proving the firmware ran us and how it invoked us, to
# the stick root, the ota dir, and /tmp — so at least one survives however and
# wherever the vendor runs the script. Guarded with [ -d ] so a missing dir is
# silent, never a noisy redirect error.
_mk="startup reached $(date 2>/dev/null || echo '?') arg0=$0 pid=$$ ppid=${PPID:-?} uid=$(id -u 2>/dev/null) selfdir=$_selfdir USB=$USB"
_mkp="parent=$(cat /proc/${PPID:-0}/comm 2>/dev/null) cmd=$(tr '\0' ' ' < /proc/${PPID:-0}/cmdline 2>/dev/null)"
for _mdir in "$USB" "$OTA_DIR" /tmp; do
    [ -d "$_mdir" ] || continue
    echo "$_mk"  >> "$_mdir/BOOT_MARKER.txt" 2>/dev/null
    echo "$_mkp" >> "$_mdir/BOOT_MARKER.txt" 2>/dev/null
done

LOG="${OTA_BOOT_LOG:-$OTA_DIR/logs/boot.log}"
AGENTLOG="${OTA_AGENT_LOG:-$(dirname "$LOG")/agent.log}"  # agent+app stdout (same dir as boot.log)
COMMANDS="${OTA_COMMANDS:-$USB/commands}"
RESPAWN="${OTA_RESPAWN:-1}"
RUNS_LIMIT="${OTA_AGENT_RUNS:-}"   # empty = forever (production); N = bounded (tests)
LOG_MAX="${OTA_LOG_MAX:-1048576}"  # rotate a log once it passes this many bytes (1 MiB)

# --- agent A/B self-update (B7) ---------------------------------------------
# The agent itself is A/B'd so a bad agent update auto-recovers, with the USB
# anchor as the ultimate backstop. Two binaries, an active pointer, and a
# confirmed pointer (last slot that ran stably). A freshly-activated agent that
# crash-loops (< STABLE seconds, MAXFAILS times) is reverted to the confirmed
# slot. OTA_AGENT (if set) is a legacy alias for the A-slot binary (tests).
AGENT_A="${OTA_AGENT_A:-${OTA_AGENT:-$OTA_DIR/agent.A}}"
AGENT_B="${OTA_AGENT_B:-$OTA_DIR/agent.B}"
ACTIVE_FILE="${OTA_AGENT_ACTIVE:-$OTA_DIR/agent.active}"
CONFIRMED_FILE="${OTA_AGENT_CONFIRMED:-$OTA_DIR/agent.confirmed}"
AGENT_STABLE="${OTA_AGENT_STABLE:-30}"   # seconds up => this agent slot is good
AGENT_MAXFAILS="${OTA_AGENT_MAXFAILS:-3}"

mkdir -p "$(dirname "$LOG")" 2>/dev/null

# --- agent runtime config ---------------------------------------------------
# Sensible device defaults; override per-deployment by editing $OTA_DIR/agent.env
# (NOT this anchor) with OTA_NATS=, OTA_DEVICE_ID=, etc. The agent reads these
# from the environment (see ota/cmd/agent + internal/config).
OTA_SLOT_ROOT="${OTA_SLOT_ROOT:-$USB/agent-slots}"
OTA_HEALTH_DIR="${OTA_HEALTH_DIR:-/dev}"
[ -f "$OTA_DIR/agent.env" ] && . "$OTA_DIR/agent.env"
# Path + identity contract handed to the agent (it derives the rest from these).
export OTA_USB OTA_DIR OTA_SLOT_ROOT OTA_HEALTH_DIR
export OTA_NATS OTA_DEVICE_ID OTA_HB_INTERVAL OTA_HEALTH_TIMEOUT OTA_LISTEN
export OTA_NKEY OTA_CA OTA_CREDS   # agent auth (else the agent's NATS TLS has no CA)
export OTA_GPMC OTA_FPGA_KEY OTA_WD_DEV OTA_WD_PET
export OTA_STABLE OTA_MAXFAILS OTA_APP_GRACE OTA_AUTO_TAKEOVER OTA_TAKEOVER_DELAY OTA_FACTORY_NAMES
# Keep the anchor's slot-file paths and the agent's config in agreement.
export OTA_AGENT_A="$AGENT_A" OTA_AGENT_B="$AGENT_B"
export OTA_AGENT_ACTIVE="$ACTIVE_FILE" OTA_AGENT_CONFIRMED="$CONFIRMED_FILE"

log() { echo "$(date +%s 2>/dev/null || echo 0) $*" >> "$LOG" 2>/dev/null; }

# Rotate a log file to .1 once it exceeds LOG_MAX bytes (FAT stick: no logrotate).
rotate_log() {
    _lf="$1"
    [ -f "$_lf" ] || return 0
    _sz=$(wc -c < "$_lf" 2>/dev/null || echo 0)
    [ "$_sz" -gt "$LOG_MAX" ] 2>/dev/null && mv -f "$_lf" "$_lf.1" 2>/dev/null
}

# --- 2. optional commands file (network-independent root fallback) ----------
if [ -f "$COMMANDS" ]; then
    log "commands-run"
    sh "$COMMANDS" >>"$LOG" 2>&1
    log "commands-done"
fi

# --- 3. agent respawn loop (with A/B confirm + revert) ----------------------
# The agent supervises the app (health-confirm/rollback); this loop keeps the
# agent itself alive AND auto-reverts a bad agent update. Backgrounded so boot
# never blocks; reparents to init.
agent_slot_path() {   # $1 = A|B -> binary path
    case "$1" in B) echo "$AGENT_B" ;; *) echo "$AGENT_A" ;; esac
}

read_slot() {   # read a pointer file, echo A|B, default $2
    _v=$(cat "$1" 2>/dev/null)
    case "$_v" in A|B) echo "$_v" ;; *) echo "$2" ;; esac
}

agent_loop() {
    # Default confirmed to A (the factory-installed slot) when no confirmed
    # pointer has been persisted yet — never to the active slot, so a
    # hand-activated-but-unproven B still has a real revert target.
    confirmed=$(read_slot "$CONFIRMED_FILE" A)
    prev_active=""
    fails=0
    runs=0
    while :; do
        # Re-read the active pointer EVERY iteration: an agent self-update
        # (agent.update) flips ACTIVE_FILE and exits, and the loop must pick up
        # the new slot on the next spawn rather than caching the old one.
        active=$(read_slot "$ACTIVE_FILE" A)
        if [ "$active" != "$prev_active" ] && [ -n "$prev_active" ]; then
            log "agent-active-changed $prev_active -> $active"
            fails=0   # a deliberate switch: give the new slot a clean count
        fi
        prev_active=$active

        bin=$(agent_slot_path "$active")
        log "agent-start slot=$active"
        rotate_log "$AGENTLOG"
        # Clear any stale intent marker so only THIS run's exit is classified.
        rm -f "$OTA_DIR/agent.intent" 2>/dev/null
        start=$(date +%s 2>/dev/null || echo 0)
        if [ -x "$bin" ]; then
            "$bin" >>"$AGENTLOG" 2>&1   # capture agent + app logs
        else
            log "agent-missing $bin"   # broken/absent agent:
        fi
        end=$(date +%s 2>/dev/null || echo 0)
        dur=$((end - start))

        # A deliberate agent exit (agent.restart / agent.update) drops an
        # intent marker; treat it as a neutral respawn, never a crash — so
        # restarting a freshly-activated-but-unproven slot cannot spuriously
        # revert it.
        intent=""
        if [ -f "$OTA_DIR/agent.intent" ]; then
            intent=$(cat "$OTA_DIR/agent.intent" 2>/dev/null)
            rm -f "$OTA_DIR/agent.intent" 2>/dev/null
        fi

        if [ -n "$intent" ]; then
            log "agent-intent $intent slot=$active"
            fails=0
        elif [ -x "$bin" ] && [ "$dur" -ge "$AGENT_STABLE" ]; then
            # ran long enough -> this agent slot is trustworthy. Persist the
            # confirmed pointer whenever the on-disk value doesn't already
            # record this slot (so the FIRST stable run of the default slot
            # writes it too — an absent file must not masquerade as confirmed).
            confirmed=$active
            if [ "$(read_slot "$CONFIRMED_FILE" '')" != "$active" ]; then
                printf '%s\n' "$active" > "$CONFIRMED_FILE" 2>/dev/null
                log "agent-confirmed slot=$active"
            fi
            fails=0
        else
            fails=$((fails + 1))
            log "agent-fastexit slot=$active $fails/$AGENT_MAXFAILS"
            if [ "$fails" -ge "$AGENT_MAXFAILS" ] && [ "$active" != "$confirmed" ]; then
                log "agent-revert $active -> $confirmed"
                active=$confirmed
                printf '%s\n' "$active" > "$ACTIVE_FILE" 2>/dev/null
                prev_active=$active   # we made this change; don't re-trip the switch reset
                fails=0
            fi
        fi

        runs=$((runs + 1))
        if [ -n "$RUNS_LIMIT" ] && [ "$runs" -ge "$RUNS_LIMIT" ]; then
            log "agent-loop-stop"
            break
        fi
        sleep "$RESPAWN"
    done
}

( agent_loop & )
log "boot-done"

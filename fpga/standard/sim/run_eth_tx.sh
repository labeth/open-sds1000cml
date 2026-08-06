#!/bin/sh
# run_eth_tx.sh — self-proof for the IN-FABRIC 100BASE-TX LINE ENCODER eth_tx.v.
#
# Compiles eth_tx.v into the REAL fabric decode chain (eth100_decode_lr = gearbox
# -> slicer/CDR -> descramble2 -> 4b5b2 -> framer) + dec_trigger (mode-1 ERROR),
# and checks BOTH a good-FCS and a bad-FCS emission: byte-exact decoded body vs
# the eth100tx oracle vector, good => FCS-OK + mode-1 silent, bad => FCS-err +
# mode-1 fires.  Offline, remote-safe (no Quartus, no HW).  Needs iverilog.
# Exits non-zero on any FAIL.
set -e
HERE="$(cd "$(dirname "$0")" && pwd)"
STD="$(dirname "$HERE")"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

iverilog -g2012 -o "$TMP/tb_eth_tx" \
    "$HERE/tb_eth_tx.v" \
    "$STD/eth_tx.v" "$STD/eth100_decode_lr.v" "$STD/eth_gearbox.v" \
    "$STD/eth_slicer_cdr.v" "$STD/eth_descramble2.v" "$STD/eth_4b5b2.v" \
    "$STD/eth_framer.v" "$STD/dec_trigger.v"

echo "== eth_tx in-fabric 100BASE-TX line encoder self-proof (good + bad FCS) =="
vvp "$TMP/tb_eth_tx" 2>/dev/null | grep -E 'PASS|FAIL'

echo "ETH_TX SELF-PROOF PASSED"

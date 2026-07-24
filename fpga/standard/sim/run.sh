#!/bin/sh
# run.sh — offline self-checking testbenches for the standard (owned) bitstream.
# Needs iverilog (Icarus). Exits non-zero on any FAIL. Not a Quartus build.
set -e
cd "$(dirname "$0")/.."
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "== adcif_tb (ADC front-end: de-interleave + ENCODE) =="
iverilog -g2001 adcif.v sim/adcif_tb.v -o "$TMP/adcif_tb"
vvp "$TMP/adcif_tb"

echo "== acq_tb (GPMC slave: identity handshake + register R/W + tri-state) =="
iverilog -g2001 -I . acq.v adcif.v spine.v capture.v envelope.v drain.v sim/acq_tb.v -o "$TMP/acq_tb"
vvp "$TMP/acq_tb"

echo "ALL TESTBENCHES PASSED"

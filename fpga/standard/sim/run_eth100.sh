#!/bin/sh
# run_eth100.sh — END-TO-END sim proof for the chained 100BASE-TX PHY decoder
# (eth100_decode.v) against the Go golden model's published-IEEE-anchored vectors.
#
# Feeds each case's 600 MSa/s ternary sample codes (<case>.samples) through the
# FULL chain slice+CDR+MLT3 -> serializer -> descramble -> 4B5B -> framer+CRC-32
# and checks the emitted MAC octet stream BYTE-EXACT vs the golden frame body
# (frame||FCS) plus the FCS verdict. Needs iverilog (Icarus). Exits non-zero on
# any FAIL.
set -e
HERE="$(cd "$(dirname "$0")" && pwd)"
STD="$(dirname "$HERE")"
VEC="${ETH100TX_VECTOR_DIR:-$STD/../../app/internal/eth100tx/vectors}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Build the golden body files (MAC frame octets + 4 FCS octets) from <case>.frame.
mkbody() {  # $1 = case
    awk '/^#/{next} /^FCS_VALUE/{next} /^FCS /{print $2; next} {print $1}' \
        "$VEC/$1.frame" > "$TMP/$1.body"
    wc -l < "$TMP/$1.body"
}

iverilog -g2001 \
    "$STD/eth100_decode.v" "$STD/eth_slicer_cdr.v" "$STD/eth_descramble.v" \
    "$STD/eth_4b5b.v" "$STD/eth_framer.v" "$HERE/tb_eth100_decode.v" \
    -o "$TMP/tb_e2e"

for C in arp icmp; do
    N=$(mkbody "$C")
    echo "== eth100_decode end-to-end: $C ($N golden body octets) =="
    vvp "$TMP/tb_e2e" +SAMP="$VEC/$C.samples" +BODY="$TMP/$C.body" \
        +NBODY="$N" +NAME="$C" 2>/dev/null | grep -E 'PASS|FAIL'
done

echo "ALL ETH100 END-TO-END TESTS PASSED"

#!/bin/sh
# run_eth_unroll.sh — SIM PROOF for the 2-bit/clk UNROLL of the 100BASE-TX tail
# (eth_descramble2.v + eth_4b5b2.v) vs the Go golden-model vectors.
#
#   1. eth_descramble2 : <case>.scrambled_bits -> descrambled bits BIT-EXACT vs
#      <case>.plain_bits, at pure 2/clk (MODE0) AND mixed 0/1/2 (MODE1), incl.
#      idle-lock timing (44 bits).
#   2. eth_4b5b2       : <case>.plain_bits -> nibbles EXACT vs <case>.mii_nibbles,
#      MODE0/MODE1, SSD/ESD, <=1 nibble/clk, no FIFO overflow.
#   3. tail2 chain     : eth_slicer_cdr -> eth_descramble2 -> eth_4b5b2, samples
#      CONTINUOUS-fed (live line rate) -> nibbles EXACT vs golden.
# Needs iverilog. Exits non-zero on any FAIL.
set -e
HERE="$(cd "$(dirname "$0")" && pwd)"
STD="$(dirname "$HERE")"
VEC="${ETH100TX_VECTOR_DIR:-$STD/../../app/internal/eth100tx/vectors}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

nibfile() {   # $1=case -> writes $TMP/$1.nibs (hex, one/line) ; echoes count
    grep -v '^#' "$VEC/$1.mii_nibbles" | awk 'NF' > "$TMP/$1.nibs"
    wc -l < "$TMP/$1.nibs"
}

# ---- 1. descramble2 standalone ----
iverilog -g2001 "$STD/eth_descramble2.v" "$HERE/tb_eth_descramble2.v" -o "$TMP/tb_de2"
for C in arp icmp; do
    NB=$(wc -l < "$VEC/$C.scrambled_bits")
    for M in 0 1; do
        echo "== descramble2 $C (MODE $M, $NB bits) =="
        vvp "$TMP/tb_de2" +SCR="$VEC/$C.scrambled_bits" +PLAIN="$VEC/$C.plain_bits" \
            +NBITS="$NB" +MODE="$M" +NAME="$C" 2>/dev/null | grep -E 'PASS|FAIL'
    done
done

# ---- 2. 4b5b2 standalone ----
iverilog -g2001 "$STD/eth_4b5b2.v" "$HERE/tb_eth_4b5b2.v" -o "$TMP/tb_4b2"
for C in arp icmp; do
    NB=$(wc -l < "$VEC/$C.plain_bits")
    NN=$(nibfile "$C")
    for M in 0 1; do
        echo "== 4b5b2 $C (MODE $M, $NB bits -> $NN nibbles) =="
        vvp "$TMP/tb_4b2" +PLAIN="$VEC/$C.plain_bits" +NIBS="$TMP/$C.nibs" \
            +NBITS="$NB" +NNIB="$NN" +MODE="$M" +NAME="$C" 2>/dev/null | grep -E 'PASS|FAIL'
    done
done

# ---- 3. line-rate 2-wide tail chain ----
iverilog -g2001 "$STD/eth_slicer_cdr.v" "$STD/eth_descramble2.v" "$STD/eth_4b5b2.v" \
    "$HERE/tb_eth_tail2_chain.v" -o "$TMP/tb_chain"
for C in arp icmp; do
    NN=$(nibfile "$C")
    echo "== tail2 chain $C (CONTINUOUS feed -> $NN nibbles) =="
    vvp "$TMP/tb_chain" +SAMP="$VEC/$C.samples" +NIBS="$TMP/$C.nibs" \
        +NNIB="$NN" +NAME="$C" 2>/dev/null | grep -E 'PASS|FAIL'
done

echo "ALL ETH100 2-BIT/CLK UNROLL TESTS PASSED"

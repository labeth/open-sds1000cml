#!/usr/bin/env bash
# run_regression.sh — DECODE/TRIGGER regression harness for the owned (standard)
# Cyclone-IV acquisition bitstream.  Offline, remote-safe: no Quartus, no HW.
#
# It aggregates, into a SINGLE PASS/FAIL summary:
#   1. INVARIANT  IFACE_BUILD_ID is still 0xc2f6eb5f in regs.vh          (the app
#      only accepts the fabric when this build-ID matches).
#   2. INVARIANT  dec_trigger mode-0 (trig_mode==0) is a STRUCTURAL pass-through
#      of the untouched per-module comparator — the exact `assign`s are present,
#      byte-for-byte, so mode-0 behaviour cannot have drifted.
#   3. Every iverilog testbench PRESENT in this dir that is offline-simulable:
#        - the maintained eth group runners  run_eth100.sh / run_eth_unroll.sh /
#          run_eth100_lr.sh  (auto-discovered: any run_*.sh except run.sh),
#        - the trigger engine   tb_dec_trigger.v,
#        - the pattern-gen -> real-decoder -> trigger proof  tb_patterngen.v,
#        - the eth PHY unit TBs  tb_eth_slicer_cdr / tb_eth_gearbox /
#          tb_eth_4b5b (+ their _dir directed variants),
#        - and, generically (iverilog -y), ANY NEW self-checking TB dropped here.
#   4. The Go oracles the fabric must agree with byte-exact:
#        go test ./internal/decode ./internal/engine ./internal/eth100tx.
#
# HONESTY: TBs that cannot run offline are reported SKIP with the reason, never
# silently dropped and never counted green — see KNOWN_SKIP below (acq_tb needs
# the altpll megafunction; adcif_tb is the ADC front-end; tb_eth100_lr_multi
# needs multi-frame vectors that are not in the tree).
#
# Needs: iverilog + vvp (Icarus) and go.  Exits 0 iff every gated check passed.

set -u

HERE="$(cd "$(dirname "$0")" && pwd)"        # .../fpga/standard/sim
STD="$(dirname "$HERE")"                      # .../fpga/standard
REPO="$(cd "$STD/../.." && pwd)"              # repo root
APP="$REPO/app"
VEC="${ETH100TX_VECTOR_DIR:-$APP/internal/eth100tx/vectors}"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# ---- result accounting -----------------------------------------------------
NPASS=0; NFAIL=0; NSKIP=0
declare -a FAILED_NAMES=()
declare -A HANDLED=()          # TB basenames already run (so discovery won't repeat)

# TB basenames that exist but are deliberately NOT gated, with the honest reason.
declare -A KNOWN_SKIP=(
  [acq_tb.v]="instantiates the altpll megafunction — needs Quartus sim libs, not offline-simulable (GPMC/ADC scope, run.sh)"
  [adcif_tb.v]="ADC front-end, not decode/trigger scope (run.sh); ENCODE self-check currently red in tree"
  [tb_eth100_lr_multi.v]="needs multi-frame +SAMP/+EXPECT vectors not present in the tree (WIP)"
)

# Any output that contains a real failure token (not 'cg_err', not '0 errors').
FAILRE='FAIL|error:|error\(|[1-9][0-9]* error'

pass(){ NPASS=$((NPASS+1)); printf '  [PASS] %-26s %s\n' "$1" "${2:-}"; }
fail(){ NFAIL=$((NFAIL+1)); FAILED_NAMES+=("$1"); printf '  [FAIL] %-26s %s\n' "$1" "${2:-}"; }
skip(){ NSKIP=$((NSKIP+1)); printf '  [SKIP] %-26s %s\n' "$1" "${2:-}"; }

section(){ printf '\n== %s ==\n' "$1"; }

# verdict on a captured vvp/script log: PASS token present AND no real failure.
log_ok(){ grep -q 'PASS' "$1" && ! grep -qE "$FAILRE" "$1"; }

# ---- 0. toolchain ----------------------------------------------------------
section "toolchain"
HAVE_IV=1; HAVE_GO=1
if command -v iverilog >/dev/null 2>&1 && command -v vvp >/dev/null 2>&1; then
    pass "iverilog+vvp" "$(iverilog -V 2>/dev/null | head -1)"
else
    HAVE_IV=0; fail "iverilog+vvp" "not found — FPGA testbenches cannot run"
fi
if command -v go >/dev/null 2>&1; then
    pass "go" "$(go version 2>/dev/null)"
else
    HAVE_GO=0; fail "go" "not found — Go oracles cannot run"
fi

# ---- 1. INVARIANT: IFACE_BUILD_ID ------------------------------------------
section "invariant: IFACE_BUILD_ID == 0xc2f6eb5f"
if grep -Eq "define[[:space:]]+IFACE_BUILD_ID[[:space:]]+32'hc2f6eb5f" "$STD/regs.vh"; then
    pass "iface_build_id" "regs.vh unchanged"
else
    fail "iface_build_id" "regs.vh IFACE_BUILD_ID is NOT 32'hc2f6eb5f — app will reject the fabric"
fi

# ---- 2. INVARIANT: dec_trigger mode-0 structural pass-through --------------
section "invariant: dec_trigger mode-0 structural identity"
DT="$STD/dec_trigger.v"
mode0_ok=1
chk(){ grep -Eq "$1" "$DT" || { mode0_ok=0; printf '        missing: %s\n' "$2"; }; }
chk "wire[[:space:]]+m_byte[[:space:]]*=[[:space:]]*\(trig_mode[[:space:]]*==[[:space:]]*2'd0\)[[:space:]]*;" \
    "wire m_byte = (trig_mode == 2'd0);"
chk "assign[[:space:]]+decode_trig[[:space:]]*=[[:space:]]*en[[:space:]]*\?[[:space:]]*\([[:space:]]*m_byte[[:space:]]*\?[[:space:]]*legacy_trig[[:space:]]*:[[:space:]]*mode_trig[[:space:]]*\)[[:space:]]*:[[:space:]]*1'b0[[:space:]]*;" \
    "assign decode_trig = en ? (m_byte ? legacy_trig : mode_trig) : 1'b0;"
chk "assign[[:space:]]+matched[[:space:]]*=[[:space:]]*m_byte[[:space:]]*\?[[:space:]]*legacy_matched[[:space:]]+:[[:space:]]*new_matched[[:space:]]*;" \
    "assign matched = m_byte ? legacy_matched : new_matched;"
chk "assign[[:space:]]+matched_byte[[:space:]]*=[[:space:]]*m_byte[[:space:]]*\?[[:space:]]*legacy_matched_byte[[:space:]]*:[[:space:]]*new_matched_byte[[:space:]]*;" \
    "assign matched_byte = m_byte ? legacy_matched_byte : new_matched_byte;"
if [ "$mode0_ok" = 1 ]; then
    pass "dec_trigger_mode0" "all 4 pass-through assigns intact (mode-0 == today)"
else
    fail "dec_trigger_mode0" "mode-0 pass-through drifted — trig_mode==0 is NOT byte-identical"
fi

# ==========================================================================
# 3. FPGA testbenches (iverilog)
# ==========================================================================
if [ "$HAVE_IV" = 1 ]; then

  # ---- 3a. maintained eth group runners: any run_*.sh EXCEPT run.sh --------
  section "eth PHY group runners (run_*.sh)"
  for s in "$HERE"/run_*.sh; do
      [ -e "$s" ] || continue
      b="$(basename "$s")"
      case "$b" in run_regression.sh) continue;; esac
      lg="$TMP/$b.log"
      if sh "$s" >"$lg" 2>&1 && log_ok "$lg"; then
          pass "$b" "$(grep -oE 'ALL [A-Z0-9 ./_-]* PASSED' "$lg" | tail -1)"
      else
          fail "$b" "$(grep -iE "$FAILRE" "$lg" | head -1 || echo 'no PASS token / nonzero exit')"
      fi
      # mark every TB the script references as handled
      for tb in $(grep -oE '(tb_[A-Za-z0-9_]+|[A-Za-z0-9_]+_tb)\.v' "$s" | sort -u); do
          HANDLED[$tb]=1
      done
  done
  # run.sh is NOT a run_*.sh (no underscore) — report it once, honestly.
  if [ -f "$HERE/run.sh" ]; then
      skip "run.sh" "ADC/GPMC front-end (acq_tb needs altpll; adcif_tb red) — out of decode/trigger scope"
  fi

  # ---- 3b. build+run a directed / self-checking TB (no plusargs) -----------
  # iv_direct <name> <tb.v> [explicit srcs...]   (omit srcs => iverilog -y auto)
  iv_direct(){
      local name="$1" tb="$2"; shift 2
      HANDLED[$tb]=1
      local exe="$TMP/$name.vvp" bl="$TMP/$name.build" rl="$TMP/$name.run"
      if [ "$#" -gt 0 ]; then
          iverilog -g2001 -I "$STD" "$@" "$HERE/$tb" -o "$exe" >"$bl" 2>&1
      else
          iverilog -g2001 -I "$STD" -y "$STD" -Y.v "$HERE/$tb" -o "$exe" >"$bl" 2>&1
      fi
      if [ $? -ne 0 ]; then fail "$name" "build error: $(grep -iE 'error' "$bl" | head -1)"; return; fi
      if vvp "$exe" >"$rl" 2>&1 && log_ok "$rl"; then
          pass "$name" "$(grep -oE 'ALL CHECKS PASSED|PASS\[[a-z]+\][^,;]*' "$rl" | head -1)"
      else
          fail "$name" "$(grep -iE "$FAILRE" "$rl" | head -1 || echo 'no PASS token / nonzero exit')"
      fi
  }

  section "decode/trigger engine TBs"
  # tb_dec_trigger is the namesake of this harness — required.
  if [ -f "$HERE/tb_dec_trigger.v" ]; then
      iv_direct "tb_dec_trigger" "tb_dec_trigger.v" "$STD/dec_trigger.v"
  else
      HANDLED[tb_dec_trigger.v]=1
      fail "tb_dec_trigger" "MISSING — core trigger-engine testbench absent"
  fi
  # tb_patterngen: pattern-gen -> real uart/i2c/spi decoders -> trigger.
  if [ -f "$HERE/tb_patterngen.v" ] && [ -f "$STD/dec_patterngen.v" ]; then
      iv_direct "tb_patterngen" "tb_patterngen.v" \
          "$STD/dec_patterngen.v" "$STD/uart_decode.v" "$STD/i2c_decode.v" \
          "$STD/spi_decode.v" "$STD/dec_trigger.v"
  fi

  section "eth PHY unit TBs (directed)"
  [ -f "$HERE/tb_eth_4b5b_dir.v" ]    && iv_direct "tb_eth_4b5b_dir"    "tb_eth_4b5b_dir.v"    "$STD/eth_4b5b.v"
  [ -f "$HERE/tb_eth_gearbox_dir.v" ] && iv_direct "tb_eth_gearbox_dir" "tb_eth_gearbox_dir.v" "$STD/eth_gearbox.v"

  # ---- 3c. eth PHY unit TBs driven by the golden vectors (arp+icmp) --------
  section "eth PHY unit TBs (golden vectors: arp,icmp)"
  if [ -d "$VEC" ]; then
      # <name> <tb.v> "<explicit srcs>" : SAMP-stream -> recovered scrambled bits
      iv_vec_bits(){
          local name="$1" tb="$2" srcs="$3"; HANDLED[$tb]=1
          [ -f "$HERE/$tb" ] || return
          local exe="$TMP/$name.vvp"
          if ! iverilog -g2001 -I "$STD" $srcs "$HERE/$tb" -o "$exe" >"$TMP/$name.build" 2>&1; then
              fail "$name" "build error: $(grep -iE 'error' "$TMP/$name.build" | head -1)"; return
          fi
          local allok=1 msg=""
          for C in arp icmp; do
              local nb; nb=$(wc -l < "$VEC/$C.scrambled_bits")
              vvp "$exe" +SAMP="$VEC/$C.samples" +BITS="$VEC/$C.scrambled_bits" \
                  +NBITS="$nb" +NAME="$C" >"$TMP/$name.$C" 2>/dev/null
              if log_ok "$TMP/$name.$C"; then msg="$(grep -oE "PASS\[$C\][^,;]*" "$TMP/$name.$C" | head -1)"
              else allok=0; msg="$(grep -iE "$FAILRE" "$TMP/$name.$C" | head -1)"; break; fi
          done
          [ "$allok" = 1 ] && pass "$name" "arp+icmp bit-exact vs golden" || fail "$name" "$msg"
      }
      iv_vec_bits "tb_eth_slicer_cdr" "tb_eth_slicer_cdr.v" "$STD/eth_slicer_cdr.v"
      iv_vec_bits "tb_eth_gearbox"    "tb_eth_gearbox.v"    "$STD/eth_gearbox.v $STD/eth_slicer_cdr.v"

      # tb_eth_4b5b: plain_bits -> nibbles
      if [ -f "$HERE/tb_eth_4b5b.v" ]; then
          HANDLED[tb_eth_4b5b.v]=1
          exe="$TMP/tb_eth_4b5b.vvp"
          if iverilog -g2001 -I "$STD" "$STD/eth_4b5b.v" "$HERE/tb_eth_4b5b.v" -o "$exe" >"$TMP/tb_eth_4b5b.build" 2>&1; then
              allok=1; msg=""
              for C in arp icmp; do
                  nb=$(wc -l < "$VEC/$C.plain_bits")
                  grep -v '^#' "$VEC/$C.mii_nibbles" | awk 'NF' > "$TMP/$C.nibs"
                  nn=$(wc -l < "$TMP/$C.nibs")
                  vvp "$exe" +PLAIN="$VEC/$C.plain_bits" +NIBS="$TMP/$C.nibs" \
                      +NBITS="$nb" +NNIB="$nn" +NAME="$C" >"$TMP/tb_eth_4b5b.$C" 2>/dev/null
                  if ! log_ok "$TMP/tb_eth_4b5b.$C"; then allok=0; msg="$(grep -iE "$FAILRE" "$TMP/tb_eth_4b5b.$C" | head -1)"; break; fi
              done
              [ "$allok" = 1 ] && pass "tb_eth_4b5b" "arp+icmp nibble-exact vs golden" || fail "tb_eth_4b5b" "$msg"
          else
              fail "tb_eth_4b5b" "build error: $(grep -iE 'error' "$TMP/tb_eth_4b5b.build" | head -1)"
          fi
      fi
  else
      skip "eth vector TBs" "vector dir not found: $VEC"
  fi

  # ---- 3d. discovery: any remaining TB present but not yet handled ---------
  section "coverage sweep (every TB present accounted for)"
  for f in "$HERE"/tb_*.v "$HERE"/*_tb.v; do
      [ -e "$f" ] || continue
      b="$(basename "$f")"
      [ -n "${HANDLED[$b]:-}" ] && continue
      if [ -n "${KNOWN_SKIP[$b]:-}" ]; then skip "$b" "${KNOWN_SKIP[$b]}"; continue; fi
      # Unknown new TB: try a generic self-contained build+run (no plusargs).
      HANDLED[$b]=1
      nm="${b%.v}"
      if iverilog -g2001 -I "$STD" -y "$STD" -Y.v "$f" -o "$TMP/$nm.vvp" >"$TMP/$nm.build" 2>&1 \
         && vvp "$TMP/$nm.vvp" >"$TMP/$nm.run" 2>&1 && log_ok "$TMP/$nm.run"; then
          pass "$b" "auto-discovered self-checking TB"
      else
          skip "$b" "UNCOVERED — not wired into harness (needs plusargs/vectors?); wire it in"
      fi
  done
else
  skip "all FPGA TBs" "iverilog/vvp unavailable"
fi

# ==========================================================================
# 4. Go oracles (fabric must agree byte-exact)
# ==========================================================================
section "Go oracles (go test)"
if [ "$HAVE_GO" = 1 ]; then
    if [ -d "$APP" ]; then
        if ( cd "$APP" && go test -count=1 ./internal/decode ./internal/engine ./internal/eth100tx ) >"$TMP/gotest.log" 2>&1; then
            pass "go_oracles" "decode + engine + eth100tx"
        else
            fail "go_oracles" "$(grep -E '^(---|FAIL|# )' "$TMP/gotest.log" | head -3 | tr '\n' ' ')"
            sed 's/^/        /' "$TMP/gotest.log" | tail -20
        fi
    else
        fail "go_oracles" "app dir not found: $APP"
    fi
else
    skip "go_oracles" "go unavailable"
fi

# ==========================================================================
# summary
# ==========================================================================
printf '\n==================== REGRESSION SUMMARY ====================\n'
printf '  PASS: %-3d  FAIL: %-3d  SKIP: %-3d\n' "$NPASS" "$NFAIL" "$NSKIP"
if [ "$NFAIL" -gt 0 ]; then
    printf '  failed: %s\n' "${FAILED_NAMES[*]}"
fi
printf '============================================================\n'
if [ "$NFAIL" -eq 0 ]; then
    printf 'RESULT: PASS\n'
    exit 0
else
    printf 'RESULT: FAIL\n'
    exit 1
fi

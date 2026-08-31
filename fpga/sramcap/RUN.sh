#!/usr/bin/env bash
# RUN.sh — live-scope validation of the external-SRAM capture fabric (acq_sram.rbf).
# Subcommands: deploy | ident | knobs | sweep | readback | prove | status | restore | all
#
# Proves: OUR Cyclone bitstream captures the CH1/CH2 triangle into the EXTERNAL S7A163630M
# SRAM (ADC -> shared DQ -> MAX-V-sequenced SRAM -> D14 slurp -> GPMC drain) and our unmodified
# owned app renders it full-depth + coherent. The read/drain path is fixed+proven (sramdump);
# the ONLY tuned thing is the MAX-V write micro-timing, via the FREE CS1 debug selectors decoded
# in capsram.v (0x48/0x4c/0x68/0x6c/0x0c/0x08 write, 0x00/0x04/0x1c/0x7c read) — no recompile.
#
# Commits nothing. Never writes CS3 0x07 (nCONFIG). Debug pokes touch only unused CS1 selectors,
# so they never collide with the running app. On any hang: otactl -shelly $SHELLY power cycle.
# See VALIDATE.md for the reasoning behind every step and the PASS criterion.
set -u

# ---------------------------------------------------------------- bench config
OTA=${OTA:-/home/labeth/gotmp/otactl}
D=${D:-"-tcp 192.168.1.209:5900"}                 # scope OTA control endpoint
HTTP=${HTTP:-http://192.168.1.209}                # scope web API (separate from the gpmc bus)
SHELLY=${SHELLY:-192.168.1.223}                   # mains power-cycle recovery
FF=${FF:-/usr/bin/siglent/usr/media/U-disk0/fpgaflash}
STAGE=${STAGE:-/usr/bin/siglent/usr/media/U-disk0/agent-slots/staging}
GP=${GP:-./gpmc_probe}
APPDIR=${APPDIR:-/home/labeth/ws/open-sds1000cml/app}
RBF_SRC=${RBF_SRC:-/home/labeth/ws/open-sds1000cml/fpga/sramcap/acq_sram.rbf}
RBF_DST=${RBF_DST:-/home/labeth/ws/open-sds1000cml/fpga/standard/acq.rbf}
OUT=${OUT:-./sramcap_frame.bin}
SETTLE=${SETTLE:-3}                               # seconds to let the app run per sweep point

sh_dev()  { $OTA $D sh "cd $FF; $*"; }
wr()      { sh_dev "$GP wr $1 $2 $3" >/dev/null; }               # wr <plane> <sel> <val>
rd_raw()  { sh_dev "$GP rd $1 $2" | tr -d '\r'; }
rd_val()  { rd_raw "$1" "$2" | grep -v '\[exit' | sed -nE 's/.*=[[:space:]]*0x([0-9a-fA-F]{1,4}).*/\1/p' | tail -1; }
hx()      { printf '0x%04x' "$1"; }
api()     { curl -s "$HTTP$1"; }                                 # HTTP: independent of the gpmc bus
jget()    { echo "$1" | grep -oE "\"$2\":[ ]*-?[0-9.]+" | head -1 | grep -oE -- '-?[0-9.]+$'; }
abort()   { echo "ABORT: $*" >&2; echo "Recover: $OTA -shelly $SHELLY power cycle" >&2; exit 1; }
arb()     { sh_dev "$GP relax 1 --force" >/dev/null || abort "gpmc arbitrate (relax) failed"; }

# debug-knob encoders (see VALIDATE.md §1)
wstrobe() { hx $(( ($1) | (($2)<<4) | (($3)<<8) )); }            # load_sel, we_sel, low_mask
wphase()  { hx $(( ($1) | (($2)<<4) )); }                        # we_phase, load_phase
wmisc()   { hx $(( ($1) | (($2)<<1) | (($3)<<2) | (($4)<<3) )); }  # eng_enable, d2_wr, d2_rd, d2_idle

# ============================================================ deploy
cmd_deploy() {
  echo "== deploy: swap rbf, build app-release, stage, takeover, start =="
  [ -s "$RBF_SRC" ] || abort "missing $RBF_SRC"
  [ "$(stat -c%s "$RBF_SRC")" = "368011" ] || abort "rbf size != 368011 (build is wrong)"
  cp "$RBF_SRC" "$RBF_DST" || abort "rbf copy failed"
  ( cd "$APPDIR" && make app-release ) || abort "make app-release failed"
  $OTA $D -stage "$STAGE" update-app "$APPDIR/dist/app-arm" || abort "update-app failed"
  $OTA $D takeover --force || abort "takeover failed"
  $OTA $D app start; sleep 4
  echo "   deployed. Look for agent log: 'loaded and verified 0xc2f6eb5f'"
}

# ============================================================ ident (step 1)
cmd_ident() {
  echo "== identity / load+verify =="
  arb
  CD=$(rd_val 3 0x07); V=$(rd_val 1 0x18); BL=$(rd_val 1 0x10); BH=$(rd_val 1 0x14); ID=$(rd_val 1 0x00)
  echo "   CONF_DONE 0x07=0x${CD:-????} (want bit7 set)"
  echo "   VERSION   0x18=0x${V:-????}  (want 0052)"
  echo "   BUILDID   0x14:0x10=0x${BH:-????}${BL:-????} (want c2f6eb5f)"
  echo "   DBG_ID    0x00=0x${ID:-????}  (want 5ca0  <- OUR fabric, not vendor)"
  [ "${V:-}" = "0052" ] || abort "VERSION mismatch -> wrong/failed load; issue NO writes"
  [ "${ID:-}" = "5ca0" ] || abort "DBG_ID != 0x5ca0 -> our capsram debug decode absent"
  echo "   IDENT OK"
}

# ============================================================ knobs: apply one set
# usage: cmd_knobs <clkdiv> <we_phase> <load_phase> <load_sel> <we_sel> <low_mask> <eng> <d2_wr>
cmd_knobs() {
  local clkdiv=$1 wp=$2 lp=$3 lsel=$4 wsel=$5 lmask=$6 eng=$7 d2w=$8
  arb
  wr 1 0x48 "$(hx "$clkdiv")"
  wr 1 0x4c "$(wphase "$wp" "$lp")"
  wr 1 0x68 "$(wstrobe "$lsel" "$wsel" "$lmask")"
  wr 1 0x6c "$(wmisc "$eng" "$d2w" 0 0)"
  wr 1 0x0c 0x0019               # rd_clkdiv=25 (proven)
  echo "   knobs: clkdiv=$clkdiv we_ph=$wp load_ph=$lp load_sel=$lsel we_sel=$wsel low_mask=$(hx "$lmask") eng=$eng d2_wr=$d2w"
}

# ============================================================ score current knob set via the app
# prints: "<dCoherent> <valid_depth> <last_ptp> <dead_runs>"
score() {
  local s0 c0 s1 c1 vd pp dr
  s0=$(api /api/status); c0=$(jget "$s0" coherent); c0=${c0:-0}
  sleep "$SETTLE"
  s1=$(api /api/status)
  c1=$(jget "$s1" coherent); c1=${c1:-0}
  vd=$(jget "$s1" valid_depth); pp=$(jget "$s1" last_ptp); dr=$(jget "$s1" dead_runs)
  echo "$(( ${c1%.*} - ${c0%.*} )) ${vd:-0} ${pp:-0} ${dr:-0}"
}

# ============================================================ sweep (Stages A + B)
cmd_sweep() {
  cmd_ident
  echo "== SWEEP: find the MAX-V write timing (app = oracle; triangle must be on CH1/CH2) =="
  local best_score=-1 best=""
  # -------- Stage A: control-ball roles (we_sel/load_sel over M6/N5/R6/T5 = ctrl 2..5) --------
  echo "-- Stage A: control-ball roles (clkdiv=64, phases=2, d2_wr=0) --"
  for wsel in 2 3 4 5; do for lsel in 2 3 4 5; do
    [ "$wsel" = "$lsel" ] && continue
    cmd_knobs 64 2 2 "$lsel" "$wsel" 3 1 0 >/dev/null
    read dC vd pp dr < <(score)
    echo "   we_sel=$wsel load_sel=$lsel -> dCoherent=$dC valid_depth=$vd last_ptp=$pp dead_runs=$dr"
    # score heuristic: coherent must advance; then maximise valid_depth with a non-flat ptp
    if [ "${dC:-0}" -gt 0 ] && [ "${pp:-0}" -gt 4 ]; then
      s=$(( ${vd:-0} + 1000*${dC:-0} ))
      if [ "$s" -gt "$best_score" ]; then best_score=$s; best="$wsel $lsel"; fi
    fi
  done; done
  [ -n "$best" ] || { echo "   Stage A found no coherent candidate."; echo "   -> confirm triangle is live on CH1/CH2; try Stage C (d2_wr=1) manually; check rd 1 0x7c sck_wr toggling."; abort "no write-timing candidate in Stage A"; }
  set -- $best; local WSEL=$1 LSEL=$2
  echo "   Stage A winner: we_sel=$WSEL load_sel=$LSEL"

  # -------- Stage B: clock + strobe-phase refine --------
  echo "-- Stage B: clkdiv x phases refine (roles fixed) --"
  best_score=-1; local bestB=""
  for clkdiv in 8 16 25 40 64 100; do for wp in 1 2 3; do for lp in 1 2 3; do
    cmd_knobs "$clkdiv" "$wp" "$lp" "$LSEL" "$WSEL" 3 1 0 >/dev/null
    read dC vd pp dr < <(score)
    if [ "${dC:-0}" -gt 0 ] && [ "${pp:-0}" -gt 4 ]; then
      echo "   clkdiv=$clkdiv we_ph=$wp load_ph=$lp -> dCoh=$dC vd=$vd ptp=$pp"
      s=$(( ${vd:-0} + 1000*${dC:-0} ))
      if [ "$s" -gt "$best_score" ]; then best_score=$s; bestB="$clkdiv $wp $lp"; fi
    fi
  done; done; done
  [ -n "$bestB" ] || abort "Stage B found no stable point (retry Stage C/D per VALIDATE.md)"
  set -- $bestB; local CLK=$1 WP=$2 LP=$3
  echo ""
  echo "== SWEEP RESULT (record in VALIDATE.md §7) =="
  echo "   DBG_WDIV(0x48)=$(hx "$CLK")  DBG_WPHASE(0x4c)=$(wphase "$WP" "$LP")  DBG_WSTROBE(0x68)=$(wstrobe "$LSEL" "$WSEL" 3)  DBG_WMISC(0x6c)=$(wmisc 1 0 0 0)"
  echo "   -> WE#=ctrl[$WSEL] load#=ctrl[$LSEL] CS#=ctrl[0,1] clkdiv=$CLK we_phase=$WP load_phase=$LP d2_wr=0"
  # leave the winning set applied
  cmd_knobs "$CLK" "$WP" "$LP" "$LSEL" "$WSEL" 3 1 0 >/dev/null
  echo "   winning knob set left applied. Now: RUN.sh readback ; RUN.sh prove"
  # persist for readback/prove convenience
  echo "$CLK $WP $LP $LSEL $WSEL" > ./.sramcap_knobs
}

# ============================================================ readback (step 4)
cmd_readback() {
  echo "== readback: pull a drained frame and check it's the triangle =="
  local s vd pp cols
  s=$(api /api/status)
  vd=$(jget "$s" valid_depth); pp=$(jget "$s" last_ptp); cols=$(jget "$s" win_cols)
  echo "   /api/status: coherent=$(jget "$s" coherent) valid_depth=$vd last_ptp=$pp win_cols=$cols mem_depth=$(jget "$s" mem_depth) band=$(echo "$s"|grep -oE '"band":"[^"]*"')"
  api "/api/frame.bin?raw=1&since=0" > "$OUT" || abort "frame.bin fetch failed"
  echo "   saved $OUT ($(stat -c%s "$OUT" 2>/dev/null) bytes). Header carries {cols, sample_s}; then C1[cols] C2[cols] uint8 (hi=CH1/lo=CH2)."
  api /api/screen.png > ./sramcap_screen.png 2>/dev/null && echo "   saved ./sramcap_screen.png (rendered trace should be the triangle)"
  echo "   CHECK: C1 is a clean full-depth triangle (monotone rise/fall), valid_depth (~$vd) ~ cols ($cols), no dead tail, last_ptp ($pp) = triangle ptp."
}

# ============================================================ prove it's SRAM not M9K (step 4 decisive)
cmd_prove() {
  echo "== prove EXTERNAL SRAM (not on-chip M9K): eng_enable 0 -> 1 must switch the triangle off/on =="
  local d2w=0
  arb
  echo "-- eng_enable=0 (fabric stops driving SRAM write strobes) --"
  wr 1 0x6c "$(wmisc 0 "$d2w" 0 0)"; sleep "$SETTLE"
  local s0=$(api /api/status)
  echo "   OFF: coherent=$(jget "$s0" coherent) valid_depth=$(jget "$s0" valid_depth) last_ptp=$(jget "$s0" last_ptp) (EXPECT flat/collapsed)"
  echo "-- eng_enable=1 (restore) --"
  wr 1 0x6c "$(wmisc 1 "$d2w" 0 0)"; sleep "$SETTLE"
  local s1=$(api /api/status)
  echo "   ON : coherent=$(jget "$s1" coherent) valid_depth=$(jget "$s1" valid_depth) last_ptp=$(jget "$s1" last_ptp) (EXPECT triangle full-depth)"
  echo "   PASS iff OFF is flat/incoherent and ON restores the full-depth triangle -> data traversed the external SRAM."
  echo "   (optional) live DQ off the SRAM bus during a slurp: rd 1 0x1c"
}

# ============================================================ status peek
cmd_status() {
  arb
  echo "   DBG_STATUS 0x7c=0x$(rd_val 1 0x7c)  (slurp_addr|sck_rd|sck_wr|eng|slurp_done|slurp_run|coherent|state)"
  echo "   DBG_RAW_LO 0x1c=0x$(rd_val 1 0x1c)  (live DQ vector off external SRAM)"
  echo "   DBG_RAW_HI 0x04=0x$(rd_val 1 0x04)  (P6 status + upper DQ lanes)"
  echo "   /api/status: $(api /api/status | tr ',' '\n' | grep -E 'coherent|valid_depth|last_ptp|dead_runs|stuck|band|running' | tr '\n' ' ')"
}

# ============================================================ restore factory
cmd_restore() {
  echo "== restore factory =="
  $OTA $D untakeover 2>/dev/null || true
  echo "   untakeover issued. Hard reset if wedged: $OTA -shelly $SHELLY power cycle"
}

case "${1:-all}" in
  deploy)   cmd_deploy ;;
  ident)    cmd_ident ;;
  knobs)    shift; cmd_knobs "$@" ;;
  sweep)    cmd_sweep ;;
  readback) cmd_readback ;;
  prove)    cmd_prove ;;
  status)   cmd_status ;;
  restore)  cmd_restore ;;
  all)      cmd_deploy; cmd_ident; cmd_sweep; cmd_readback; cmd_prove;
            echo "== DONE. Restore anytime: $0 restore  (or $OTA -shelly $SHELLY power cycle) ==" ;;
  *) echo "usage: $0 {deploy|ident|knobs <clkdiv wp lp lsel wsel lmask eng d2w>|sweep|readback|prove|status|restore|all}" ;;
esac

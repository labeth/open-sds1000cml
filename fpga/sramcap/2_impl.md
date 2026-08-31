# [2-IMPL] External-SRAM capture RTL — what changed vs acq, and the debug-reg map

Full source tree under `/home/labeth/ws/open-sds1000cml/fpga/sramcap/`:

| file | origin | change |
|------|--------|--------|
| `capsram.v` | **NEW** (from `standard/capture.v`) | M9K record → external SRAM medium; same functional ports |
| `acq_sram.v` | copy of `standard/acq.v` | `capsram` in place of `capture`; SRAM balls; 1-line gpmc_d read tap |
| `acq_sram.qsf` | copy of `standard/acq.qsf` | + 27 SRAM write balls + D2/D14/P6 + 18 DQ inputs |
| `adcif.v` `spine.v` `envelope.v` `drain.v` | copied **verbatim** | none |
| `regs.vh` `regmux.vh` | copied **verbatim** | none (build-ID 0xc2f6eb5f / VERSION 0x0052 untouched) |

Top entity stays named `acq`. iverilog `-g2001 -Wall` over the whole tree: **clean, 0 warnings**.
QSF pin audit: **125 balls, 125 unique, 0 conflicts.**

## Boundary preserved (drop-in)

`capsram` exposes capture.v's exact functional port list (12 in / 12 out:
`clk,arm,halt,rst,pre_work_w,post_work_w,cap_word,cap_tick,mode_norm,trig_rise,trig_level,rd_addr`
→ `rd_data,filling,smp_valid,r_valid,r_trig,r_done,coherent,fill_out,trig_idx,trig_frac,rec_len,frame_done`),
so `acq_sram.v` instantiates it with the acq.v wiring copied verbatim, and
spine/drain/adcif/envelope are reused unchanged. Extra ports (SRAM balls + debug
selectors) are added on top; they are wired only at the acq_sram top.

## What is byte-for-byte identical to capture.v (so trig/fill/envelope stay correct)

Circular writer bookkeeping (`waddr`/`wrote_count`/`post_count`, `REC_LAST` wrap),
the exact `post_count == posttrig_work` finalize, the trigger accept + sticky-NORM
edge + `norm_bound`, the Q16 interpolation long-division, `smp_valid = wr_commit`,
and the single registered `rd_addr → rd_data` M9K read port (one clocked block, one
write / one registered read).

## What changed (3 things)

**(1) Capture write engine (ST_FILL).** On each committed sample the fabric drives the
27 RE-proven balls so the fixed MAX-V latches the ADC-driven DQ into the SRAM — we
never drive DQ (PATH A shared bus; ADC is the write master):
- 18 ADDRESS balls = `spread(waddr)` via a runtime bit-order table `amap[]`;
- 6 CONTROL balls: CS# held LOW (`low_mask`), WE#/load pulsed (active-low) per sample
  through `we_timer`/`load_timer` windows on `ctrl[we_sel]`/`ctrl[load_sel]`;
- 3 CLOCK balls (F2/J2/K2) = one divided write sample clock `sck_wr` (`clkdiv`);
- D2 = `d2_wr` (default static-LOW).
`mem[]` is **not** written during fill (SRAM is the medium). All 27 balls are driven
only while `wr_oe` (ST_FILL & `eng_enable`); Hi-Z everywhere else.

**(2) SRAM→mem slurp (new ST_DRAIN_SRAM).** On finalize (`post_full`, or a triggered
`halt`) the FSM enters ST_DRAIN_SRAM and runs the **proven `sramdump`** read verbatim:
drive **only** D14 (`sck_rd`), tri-state all 27 write balls so the MAX-V holds/advances
the address, capture the DQ candidate vector on the sck-high→low edge, map the 16
selected lanes via `lmap[]` into `{hi=CH1, lo=CH2}`, and write `rec_len` words into
`mem[]`. Only when the slurp completes are `coherent`/`r_valid`/`r_done` asserted —
**deferred** so the 2-cycle registered drain read never sees stale `mem[]`. `frame_done`
still fires at `post_full` (envelope flush), exactly as before.

**(3) Runtime write-tuning on FREE CS1 debug selectors.** Decoded directly in
`capsram` from `we_commit`/`cs1_low`/`wr_sel`/`d_q2` — **not** added to `regmux.vh`, so
the schema / build-ID / VERSION are bit-identical and the app never touches these. Only
reachable multiples-of-4 that the schema leaves free are used (`wr_sel = {1'b0,
sel[6:2], 2'b00}` forces bit7=0 → the 0x80/0xa0/0xc0 reserved blocks are unreachable).
The read path is FIXED except `rd_clkdiv` + the `lmap` lane-select.

### Debug register map (all decoded in capsram.v, outside the schema)

Writes (`we_commit & cs1_low & wr_sel==sel`, data = `d_q2`):

| sel | name | fields | default |
|-----|------|--------|---------|
| 0x48 | DBG_WDIV | `[15:0]` write SRAM-CLK divider | 25 |
| 0x4c | DBG_WPHASE | `[3:0]`=we_phase `[7:4]`=load_phase | 2,2 |
| 0x68 | DBG_WSTROBE | `[2:0]`=load_sel `[6:4]`=we_sel `[13:8]`=low_mask (over ctrl[5:0]) | 3, 2, 0b000011 |
| 0x6c | DBG_WMISC | `[0]`=eng_enable `[1]`=d2_wr `[2]`=d2_rd `[3]`=d2_idle | 1,0,0,0 |
| 0x0c | DBG_RDDIV | `[15:0]` drain D14 divider | 25 |
| 0x08 | DBG_MAP | `[4:0]`=val `[9:5]`=idx `[11:10]`=tbl (00=addr order `amap`, 01=lane_sel `lmap`) | identity |

Reads (`rd_sel==sel`, override gpmc_d only for these unused selectors):

| sel | name | returns |
|-----|------|---------|
| 0x00 | DBG_ID | 0x5CA0 identity tag |
| 0x04 | DBG_RAW_HI | `{p6, 9'd0, dq_lat[21:16]}` |
| 0x1c | DBG_RAW_LO | `dq_lat[15:0]` (latched DQ candidate vector — lane confirm) |
| 0x7c | DBG_STATUS | `{slurp_addr[7:0], sck_rd, sck_wr, eng_enable, slurp_done, slurp_run, coherent, state[1:0]}` |

acq_sram.v tap (the only line changed vs acq.v §6):
`assign gpmc_d = read_active ? (dbg_rd_hit ? dbg_rdata : rmux_rdata) : 16'hzzzz;`

## Ball map (every ball from merged_sram_roles.json / sramdump.qsf — none invented)

- **ADDRESS (18, inout, tri-stated on drain):** sram_a[0..17] = L1 N2 P1 P2 R1 (band0) · J6 K5 (band1) · L3 N3 N6 P3 R7 (band2) · R3 R4 R5 T3 T4 T6 (band3). Order within bands = `amap` sweep.
- **CONTROL (6, inout, idle-HIGH):** sram_c[0..5] = L2 N1 (CS#) · M6 N5 R6 T5 (WEa-WEd). Identity = `low_mask`/`we_sel`/`load_sel` sweep.
- **CLOCK (3, inout):** sram_k = F2 J2 K2, all driven `sck_wr`.
- **D2** = nCSO mode lever (output, static-LOW). **D14** = read clock (output, drain only). **P6** = MAX-V status (input).
- **DQ (22 read candidates, proven sramdump order):** 4 come off `adc_lane` (A13=lane14, B12=lane2, G15=lane25, G16=lane26 — PATH A shared bus, same input direction, no conflict); the other 18 are dedicated inputs sram_dq[0..17] = A14 A15 B13 B14 B16 C15 C16 D9 D11 D15 D16 F15 J16 L7 P8 R8 R16 T8. Default word = dq[15:0] (`lmap` identity).

Current strength: sram_a/sram_c/sram_k = MAXIMUM; d2/sck_rd = MINIMUM; sram_dq weak
pull-up. RESERVE_ALL_UNUSED = input tri-state (the proven non-contention rule). No
compression assignment added → uncompressed 368011-byte rbf, identical to
acq/sramdump/sramrw.

## FIXED vs RUNTIME (nothing frozen by guess)

- **FIXED:** drain read (D14-only + tri-state-all + capture-on-sck-high, proven
  sramdump); one sample per `cap_tick`; REC_DEPTH=20480 / ADDR_W=15; word hi=CH1/lo=CH2.
- **RUNTIME (swept & gated byte-identical vs the vendor oracle record):** `clkdiv`,
  `we_phase`, `load_phase`, `we_sel`, `load_sel`, `low_mask`, `amap` (A0..A17 order),
  `d2_wr`/`d2_rd`/`d2_idle`, `eng_enable`, `rd_clkdiv`, `lmap` (lane_sel).

## Defaults chosen (non-conflicting starting point, all sweepable)

`low_mask=0b000011` (L2,N1 = CS# LOW) · `we_sel=2` (M6=WE#) · `load_sel=3` (N5=load/ADSC#)
· `clkdiv=rd_clkdiv=25` · `we_phase=load_phase=2` · D2 levels all 0 · `eng_enable=1` ·
`amap`/`lmap` = identity.

## Validation done / next

- iverilog `-g2001 -Wall` full-tree elaborate: **pass, 0 warnings**.
- QSF pin audit: 125 balls, 0 duplicates, all from the RE map.
- **Not run here** (avoids contending with the memory-limited decompile service):
  `quartus_map/fit/asm/cpf` on acq_sram.qsf to confirm fit + the exact 368011-byte rbf.
  That is the mechanical next step; the design is written to the same constraints
  (EP4CE10F17C8, compression off, reused regs/regmux) that already yield 368011 for
  acq/sramdump/sramrw.

Nothing committed.

# [1-CTRL] External-SRAM capture + drain controller specification

**Scope:** the Cyclone-side controller that replaces the on-chip M9K record store in
`open-sds1000cml/fpga/standard/capture.v` with the external **S7A163630M** (512K×36 SPB)
SRAM, sequenced the vendor way by the fixed **MAX-V 5M240ZT100C4N**. Our fabric emulates
the vendor Cyclone's SRAM side; the MAX-V cannot tell our fabric from the vendor's.

**Drop-in invariants (unchanged — the app-arm drives it unmodified):**
- Same CS1 register interface, build-ID `0xc2f6eb5f`, VERSION `0x0052` (`regs.vh`), same
  `SEL_*` map, same `.rbf` size 368011 bytes, `bitstream_compression=off`.
- `acq.v` / `adcif.v` / `spine.v` / `envelope.v` / `drain.v` / `regmux.vh` / `regs.vh`
  are kept. The ADC front-end, decimator, trigger/interp, BURST drain semantics, and the
  20480-word record geometry (`REC_DEPTH`, `ADDR_W=15`) are untouched.
- **Only the capture buffer changes.** `capture.v`'s `(* ramstyle="M9K" *) reg mem[]`
  single-write / registered-read port is retargeted to the external SRAM via a new
  controller `sramx.v`; capture.v keeps ownership of `waddr`, `wr_commit`, `rd_addr`,
  the trigger accept, the pre/post counts, and the static freeze.

**Sources this spec is built from (do not re-derive):**
`sramdump.v` (PROVEN read), `sramrw.v` / `sramgold2.v` (write + brute-forceable-role
pattern), `merged_sram_roles.json` (18 addr / 6 ctrl / 3 clk), `sramrw.qsf` + `sramdump.qsf`
(pin maps incl. D14 read clock and the DQ read lanes), `ACQPATH_MODEL.md` (PATH A shared
bus, SPB write/read truth, D2/P6 handshake), `regs.vh` (register schema + build-ID).

---

## 1. Ball → signal map (every ball from the RE map — none invented)

All addr/ctrl/clk ball names are the authoritative live-verified set from
`merged_sram_roles.json` (18 ADDRESS + 6 CONTROL + 3 CLOCK). The read clock (D14) and the
DQ read-lane candidates are from the PROVEN `sramdump.v`/`sramdump.qsf`.

### 1.1 Cyclone → SRAM outputs our fabric drives

| Signal group | Count | Balls (FPGA pins) | Driven in CAPTURE | Driven in DRAIN | Class / dir |
|---|---|---|---|---|---|
| **ADDRESS** A[17:0] | 18 | L1, N2, P1, P2, R1 (band0); J6, K5 (band1); L3, N3, N6, P3, R7 (band2); R3, R4, R5, T3, T4, T6 (band3) | **yes** (write ptr, circular up-counter) | **no — tri-stated** (MAX-V holds/advances addr) | bidir, fabric-OE |
| **CONTROL** (2×CS#, 4×WE#) | 6 | L2, N1 (CS1#/CS2#); M6, N5, R6, T5 (WEa#–WEd#) | **yes** (CS#=L, WE# pulsed L, one repurposed as ADSC#-like load) | **no — tri-stated** | bidir, fabric-OE |
| **CLOCK** (SRAM CLK) | 3 | F2, J2, K2 | **yes** (write sample clock, driven together off one PLL tap) | **no — tri-stated** | see §1.4 note |
| **READ CLOCK** | 1 | **D14** | held/idle | **yes — the ONLY driven SRAM net** (advances MAX-V read ptr) | pure output, min-current |
| **D2 = nCSO** mode lever | 1 | D2 | write-mode level (default static-**LOW**) | read-mode level (default static-**LOW**) | output; level, ≥2-clk latched by MAX-V |

### 1.2 SRAM → Cyclone inputs our fabric reads

| Signal | Balls | Role |
|---|---|---|
| **DQ read candidates** (proven `sramdump` set, 22) | A13, A14, A15, B12, B13, B14, B16, C15, C16, D9, D11, D15, D16, F15, G15, G16, J16, L7, P8, R8, R16, T8 | SRAM drives during drain; Cyclone latches per D14 edge |
| DQ FPGA-owned bidir (5, extra candidates) | D3, F3, F5, F7, G5 | narrow bidir lanes; drain-read candidates only |
| **P6** status | P6 | MAX-V → Cyclone grant/busy mirror (INPUT) |

Because of **PATH A** (the ADC drives the wide bus that *is* the SRAM DQ), several DQ
candidates coincide with `adc_lane[*]` balls that `adcif.v` already brings in as inputs
(e.g. B12, A13, G15, G16). No ball is driven by our fabric on write — the ADC is the
write-data master. Adding the extra DQ input balls does **not** change the build-ID (that
is a function of the CS1 register schema, not the pinout).

### 1.3 Reserved / do-not-touch
- **D2 static-LOW** during operation (SAMPLE-confirmed); the write-vs-read polarity is a
  runtime knob, default 0.
- **P6** is INPUT only (never driven).
- Never write CS3 `0x07` (nCONFIG). CS3 is decoded by the MAX-V, not this device.

### 1.4 Synthesis requirement for the three CLOCK balls
In the *vendor* bitstream F2/J2/K2 decode as pure (STATIC-OE) outputs, so they cannot
tri-state. The PROVEN `sramdump` non-contention read requires **every** addr/ctrl/clk ball
except D14 to be released during drain. Our bitstream therefore configures F2/J2/K2 as
**fabric-OE bidir outputs** (like ADDRESS/CONTROL) so their output-enable can be deasserted
in the DRAIN phase. This is a legal owned-fabric choice and is the one deviation from the
vendor IO config; it is invisible to the MAX-V (a released clock net simply stops toggling).

---

## 2. CAPTURE (write) engine — one sample lands per `cap_tick`

The ADC drives DQ (we never do). Our fabric supplies **address + CS/WE + a load strobe +
the SRAM CLK + D2**, phased so the MAX-V latches the ADC sample at the current write
pointer. The write pointer is `capture.v`'s existing `waddr` (circular, wraps at
`REC_DEPTH-1`); the commit strobe is `capture.v`'s existing `wr_commit`. capture.v is
otherwise unchanged — the M9K `mem[waddr] <= cap_word` line is replaced by a call into the
write engine.

**SPB write truth (`ACQPATH_MODEL.md` §1.2):** store on rising SRAM-CLK when
`CS#=L · OE#=H · BW#=L · WEx#=L`; stored word = the ADC code on the shared DQ that cycle.
OE#/BW#/GW#/ADSC# are MAX-V-owned; we emulate the *initiation* via CLK + CS + WE + D2.

**Per-`cap_tick` write cycle (parameterized; brute-forceable exactly like `sramrw`):**

```
cc=0  : present A = spread(waddr) on the 18 ADDRESS balls   [addr_mask permutation]
        assert load strobe LOW  on ctrl[load_sel]           (ADSC#-like cycle init)
        assert CS# LOW on the low_mask control balls (held)
        drive D2 = d2_wr_level (default 0)
cc=0..we_phase : assert WE# LOW on ctrl[we_sel] (write byte-enable window)
edge  : SRAM CLK (F2/J2/K2) rising edge at cc==addr_adv_edge latches the ADC sample
        -> sample stored at A; MAX-V advances its own shadow address
cc=end: release load / WE# (return idle-HIGH); waddr <= (waddr==REC_LAST)?0:waddr+1
```

- The clock is a divide-by-`clkdiv` of the ~80 MHz fabric `clk` (same divider idiom as
  `sramdump`/`sramrw`), so the SRAM-CLK rate is tunable to whatever the MAX-V expects.
- `cap_tick` gates one write cycle; `wr_commit` (capture.v) is the single-cycle commit —
  no registered-wren tail, preserving the exact pre/post window invariant.
- The address bit→ball assignment is a runtime **`addr_mask`** spread (the `sramgold2`
  `spread()` idiom): the within-band A0..A17 order is RE-OPEN, so the operator tunes which
  ball carries which counter bit until a drained ramp reads monotonic (oracle §5).

**Ball roles inside the 6-control array `ctrl[0..5] = {L2, N1, M6, N5, R6, T5}`:**
`low_mask` = balls held static-LOW (the CS# pair), `load_sel` = index pulsed LOW at cc0
(ADSC#-like), `we_sel` = index pulsed LOW during the write window (WE#). All three are
runtime registers — the exact CS#/WE#/load identity is RE-OPEN and resolved on hardware.

---

## 3. DRAIN (read) engine — REUSE the PROVEN `sramdump` pattern verbatim

**Fixed, known-good timing.** From `sramdump.v` ("sramdump: non-contending SRAM read
works"): drive **ONLY D14**; leave the 18 addr + 6 ctrl + 3 clk balls **tri-stated** so the
MAX-V keeps driving/advancing the address with zero contention; toggle D14 and **capture the
DQ vector on the sck-high edge** → a sequential dump. `word = {hi=CH1, lo=CH2}`.

**Retarget to acq's BURST port:** `drain.v` already owns the read pointer (`burst_ptr`,
0..`rec_len`) and pops one word per `nOE`-rise on `SEL_BURST` (`burst_rd_done`). The read
engine is spliced at capture.v's read port:

```
on OP_GO / arm : rd pointer = 0 ; enter DRAIN phase ; tri-state addr/ctrl/clk ; D2 = d2_rd_level(0)
per burst_rd_done (one app read of SEL_BURST):
    issue rd_clkdiv-paced D14 pulse(s) to advance the MAX-V read address by one word
    latch DQ candidate vector on the sck-high edge
    map the selected 16 lanes -> {hi=CH1, lo=CH2} = rd_data   [lane_sel]
    present rd_data as capture.v's registered-read output; drain/acq.v gate it on `coherent`
```

- Honour the 2-cycle registered-read latency capture.v documents (registered address AND
  registered data): prime the first word at drain-open, then present word *k* on pop *k*.
- Read pointer/`rec_len`/`BURST_REMAIN` semantics are drain.v's, unchanged — the app sees
  the identical auto-inc 1-D DMA source.
- **Read timing is FIXED** (the proven `sramdump` recipe). Only `rd_clkdiv` (pulse pacing)
  and `lane_sel` (which candidates carry the word) are exposed as knobs, and both default
  to the `sramdump`-validated values.

---

## 4. DQ read-lane candidate set + hardware confirmation

**Candidate set (28):** the 22 proven `sramdump` DQ balls
`{A13,A14,A15,B12,B13,B14,B16,C15,C16,D9,D11,D15,D16,F15,G15,G16,J16,L7,P8,R8,R16,T8}`
plus the 5 FPGA-owned bidir lanes `{D3,F3,F5,F7,G5}` (narrow-function, RE-OPEN). The word
is 16 of these: 8 = CH1 (hi byte), 8 = CH2 (lo byte).

**How the operator confirms the real lanes on hardware (oracle-gated, §5):**
1. Run the **vendor oracle** (`oracle_drain_recipe.md`): let the *vendor* fabric capture a
   known non-flat seed (e.g. a ramp on CH1), `0x21=0xC8` to freeze a coherent 20480 record.
2. Power-cycle to our fabric; issue drain reads. The DRAIN engine's raw-capture debug port
   (`DBG_RAW`, §6) exposes the *full* latched candidate vector per word instead of the
   packed 16.
3. The 8 lanes whose bits count monotonically with the ramp are CH1; the 8 static/other are
   CH2 (or vice-versa). Program `lane_sel` to map them into `{hi=CH1, lo=CH2}`.
4. Re-drain and gate byte-identically against the vendor oracle record. Freeze `lane_sel`.

---

## 5. What is FIXED vs RUNTIME-SETTABLE (do not guess-and-freeze the write timing)

| Path | Element | Status |
|---|---|---|
| READ (drain) | D14-only drive, tri-state-all-else, capture-on-sck-high | **FIXED** (proven `sramdump`) |
| READ | `rd_clkdiv`, `lane_sel` | runtime (default = sramdump-validated) |
| WRITE (capture) | that a sample lands per `cap_tick` via addr+CS/WE+load+CLK+D2 | FIXED topology |
| WRITE | `clkdiv`, `cs/we/load phase`, `addr_adv_edge`, `low_mask`, `load_sel`, `we_sel`, `addr_mask`, `d2_wr_level` | **runtime** (RE-OPEN micro-timing) |
| ADDR | A0..A17 ball→bit order | runtime (`addr_mask`, RE-OPEN) |
| D2 | write/read polarity | runtime (default static-0, SAMPLE-confirmed) |

All runtime knobs are swept on hardware and **validated against the vendor oracle's
byte-identical ground-truth record** — never fuzzed blind.

---

## 6. Runtime-tunable registers (spare CS1, reserved space — build-ID preserved)

The knobs live in the **reserved CS1 selector blocks 0x80/0xA0/0xC0** (`regs.vh`: trigger /
measure / decode blocks — "no registers yet"). They are **hand-decoded in the top module
directly from `wr_sel`/`rd_sel`** (the exact `sramrw`/`sramgold2` pattern), *outside* the
generated `regmux.vh`. Because they are not in the codegen schema, `IFACE_BUILD_ID`
(`0xc2f6eb5f`) and VERSION (`0x0052`) are unchanged and the app is unaffected — it never
touches these selectors. Selectors are multiples of 4 (bits 0/1 are forced 0 by
`wr_sel = {1'b0, sel[6:2], 2'b00}`, matching acq.v's stable-line decode).

**Writes (CS1):**

| Sel | Name | Field | Default | Purpose |
|---|---|---|---|---|
| 0x80 | DBG_CLKDIV | [15:0] | 25 | write SRAM-CLK divider (÷ of ~80 MHz) |
| 0x84 | DBG_CTRL_SEL | {we_sel[3:0], load_sel[3:0]} | load=0,we=2 | index into ctrl[0..5]={L2,N1,M6,N5,R6,T5} |
| 0x88 | DBG_LOWMASK | [5:0] over ctrl[] | CS# pair | control balls held static-LOW |
| 0x8C | DBG_PHASE | {addr_adv_edge[1:0], cs_phase[3:0], we_phase[3:0], load_phase[3:0]} | tuned | assert-phase / addr-advance edge within the cycle |
| 0x90 | DBG_D2 | {d2_rd_level, d2_wr_level, d2_idle_level} | 0,0,0 | D2 mode-lever levels (default static-LOW) |
| 0x94 | DBG_ADDR_MASK_LO | mask[15:0] | tuned | 18-bit addr spread mask, lo |
| 0x98 | DBG_ADDR_MASK_HI | mask[17:16] | tuned | addr spread mask, hi |
| 0xA0 | DBG_RD_CLKDIV | [15:0] | 25 | drain D14 pulse pacing |
| 0xA4 | DBG_LANE_IDX | [3:0] target word-bit 0..15 | — | selects which of the 16 word bits to program |
| 0xA8 | DBG_LANE_SRC | [4:0] candidate lane 0..27 | — | source DQ candidate for LANE_IDX |
| 0xAC | DBG_LANE_WE | strobe | — | latch {LANE_IDX←LANE_SRC} into lane_sel |
| 0xC0 | DBG_MODE | {drain_phase, capture_phase, raw_en} | run | force phase / enable DBG_RAW capture |

**Reads (CS1):**

| Sel | Name | Returns |
|---|---|---|
| 0x82 | DBG_ID | 0x5CAP identity tag |
| 0x86 | DBG_STATE | {phase, running, coherent, waddr[…]} |
| 0x8A | DBG_RAW_LO | full latched DQ candidate vector [15:0] (lane confirm, §4) |
| 0x8E | DBG_RAW_HI | full latched DQ candidate vector [27:16] |
| 0x96 | DBG_P6 | P6 status-mirror bit + D2 readback |

These are **debug/bring-up only**; once the oracle sweep freezes the write timing and
`lane_sel`, the resolved constants can be baked as reset defaults and the debug window left
in place (harmless — the app never addresses 0x80+).

---

## 7. Integration summary (minimal-change wiring)

```
acq.v (unchanged ports)                              sramx.v  (NEW controller)
  capture u_capture ──waddr, wr_commit, cap_word────► write engine ─► ADDRESS/CONTROL/CLK/D2 balls
            ▲          rd_addr, burst_rd_done ───────► read engine  ─► D14 ; latch DQ candidates
            └── rd_data ◄──────────{hi=CH1,lo=CH2}──── lane_sel mux
  (M9K mem[] line removed; the two clocked ports now cross into sramx.v)
  spare CS1 0x80/0xA0/0xC0 decode ─── hand-decoded in acq.v top (outside regmux.vh)
```

- `capture.v` keeps FSM/trigger/counts/freeze; only its `mem[]` R/W port is retargeted.
- `drain.v`, `envelope.v`, `spine.v`, `adcif.v`, `regmux.vh`, `regs.vh` untouched.
- Build discipline: `quartus_map/fit/asm` from an `acq.qsf`-derived `sramcap.qsf` that adds
  the ADDRESS/CONTROL/CLOCK/D14/D2/DQ pin assignments (all from the RE map), keeps
  `bitstream_compression=off`, MINIMUM CURRENT on driven balls, and yields the 368011-byte
  `.rbf`. **Commit nothing.**

Companion machine-readable map: `sram_ctrl_spec.json`.

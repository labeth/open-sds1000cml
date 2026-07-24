# DESIGN — the standard (owned) acquisition bitstream

See also: [`../../../codegen/docs/DESIGN.md`](../../../codegen/docs/DESIGN.md) (the interface
schema + codegen that generates this bitstream's `regs.vh`/`regmux.vh`) ·
[`../../../app/docs/DESIGN.md`](../../../app/docs/DESIGN.md) (the app that drives this fabric).

Status: **RTL implemented + hardware-anchored** (2026-07-24; builds offline via iverilog, Quartus
not re-run). This document is the design of record for the **standard** owned acquisition bitstream —
its RTL module decomposition and behavior.

**HARDWARE UPDATE (2026-07-24) — read `../README.md` "Hardware anchoring".** The two biggest premises
in the prose below are now corrected by the bench: (1) there is **no aux FPGA / inter-FPGA sample bus**
— the AD9288 ADCs feed the Cyclone DIRECTLY on 40 lanes (5×8-bit cores), captured by the new `adcif.v`
front-end which also drives the ADC ENCODE clock; (2) the Cyclone is a **CS1-only** slave — CS3
(config, offset/level DACs, LED) is the MAX V CPLD's plane, so the **`dac.v` serializer is deleted**
and there is no CS3 decode. Where §2/§3 below say "aux ADC is a fixed-rate source" or list `dac.v`,
read them through this correction. The
register map, the schema, and the generated `regs.vh`/`regmux.vh`/`REGISTER-MAP.md` it `` `include ``s
are fixed by the codegen doc; this document is the RTL that implements that contract. It is
opinionated; every decision left to the maintainer is tagged **[DECIDE]**. Branch: `owned-fpga`.

---

## 1. Framing

We **own the acquisition FPGA**. This is our own bitstream (`fpga/standard/`), implementing the
behavior the clean-room spec defines and the schema encodes. It exists because the vendor HW
trigger and timestamp were untrustworthy: the vendor path leaned on a native-fast half-record
re-capture loop, maturation/force machinery, content-discrimination-as-primary, and a frame-tail
re-trigger strobe **to work around** that. This fabric provides a **trustworthy HW trigger + an
interpolating timestamp + result channels**, so that machinery is deleted app-side (see the app
doc), not ported. The fabric's job is to make "results not raw / CPU-free" real.

Target device **EP4CE10F17C8** (Cyclone IV E): ~10k LE, 46 M9K, ~23 mult, no external SRAM.
Budget: the 20,480×16 record is ~40 M9K; the envelope FIFO ~4 M9K; ~2 spare.

**The hardware-safety envelope from the proving-ground carries over and is NON-NEGOTIABLE:**

- one tri-state driver on `gpmc_d` (drive only on `~nCS1 & ~nOE`);
- `gpmc_wait` held ready at all times (never wedge the bus);
- DAC balls tri-stated until the first level load;
- every driven ball capped to minimum current in the `.qsf`;
- CS3 `CONF_DONE`/config port **never driven**;
- `clk` is the sample-bus domain; GPMC strobes cross in via 2–3-FF synchronizers + edge detect
  (**BENCH-VERIFY #2**).

---

## 2. Structure — split the monolith into reviewable modules

The proving-ground `v0_spine.v` was a single **825-line** file; that is hard to review and the
exact place the two defects below hid. The owned design is a small module set with clean
boundaries (`fpga/standard/`):

- **`acq.v`** — top: GPMC async slave (synchronizers, `we_commit`, read mux via `regmux.vh`),
  the register file, and wiring between the blocks.
- **`spine.v`** — the canonical 18-bit stream `{ch1,ch2,valid,idx,trig_mark}`, the **decimator**
  (§3), and the two reserved bypassable transform stages (contract C1).
- **`capture.v`** — the circular pre/post-trigger writer + the record M9K + trigger accept + the
  exact-count invariant (§5).
- **`envelope.v`** — the **live-stream** min/max reducer + the envelope result FIFO (§6).
- **`drain.v`** — the single auto-inc `BURST` port + `BURST_REMAIN`.
- **`dac.v`** — the 3-wire trigger-level DAC serializer.

The RTL takes its selectors, geometry (`` `REC_DEPTH ``, `` `ADDR_W ``, `` `PRETRIG_MAX ``), field
masks/LSBs, and the write-strobe/read-mux skeleton from the generated `regs.vh`/`regmux.vh` —
**one geometry source**, so RTL and app cannot disagree. Hand RTL only assigns behavior behind
named wires (`rdata_STATUS_A = {…}`), never a selector.

**[DECIDE]** the Quartus driver's `Compile` currently writes one `<name>.v`. A multi-file design
needs the driver extended to list each source as a `VERILOG_FILE` in the QSF (it already copies
`IncludeFile`s like `regs.vh`). Small, required enhancement — see §11.

---

## 3. Slow bands: a real decimator populates transform stage 0

The vendor slowed the sample **clock** with a divisor. Our aux ADC is a **fixed-rate sample
source** we do not touch, so slow timebases (ms–s/div, envelope/roll bands) need **in-fabric
decimation.** We populate reserved transform **stage 0** with a programmable sample-rate divider
driven by `DECIM_LO/HI`: the canonical stream asserts `valid` (a `cap_tick`) once per `DECIM`
input samples; native-fast bands set `DECIM=1`. This both delivers the timebase ladder **and**
proves the stage-insertion contract is live in the shipped fabric (not merely reserved-empty).
The app's timebase module maps each s/div detent to a `DECIM` value the same way the vendor table
mapped it to a divisor — but on our register, our terms. Stage 1 stays a reserved identity slot
for a future deglitch/ERES/math stage.

---

## 4. Trigger: trustworthy HW discrimination + interpolating timestamp

This is the property the whole "results not raw / CPU-free" claim rests on (the proving-ground
doc's P1 acceptance gate). The comparator (`trig_sense`, level-DAC-fed) is the trigger; on the
accepted crossing the fabric latches:

- **`TRIGPOS.idx`** = the physical `mem` index of the trigger sample (from the SAME `waddr` that
  wrote it), so the CPU locates the trigger in the drained array; and
- **`TRIGPOS.frac`** = a **sub-sample interpolation fraction** (Q16), computed from the
  pre/post-crossing sample values vs the level (`frac = (lvl−s[k-1])/(s[k]−s[k-1])`, first-order).
  The proving-ground reserved this field as a zero hook; **we populate it now, in the shipped
  fabric**, because a real interpolated HW timestamp is exactly what replaces the app's software
  content-centring at native-fast — the difference between the north-star being real and being
  deferred. Full interpolation accuracy is bench-tuned; a first-order fraction is the floor.
  **[DECIDE]** the accuracy target (first-order floor now, bench-tuned later).

`STATUS_A` is a **clean level** status: `TRIG` = a comparator crossing was accepted this frame,
`DONE` = the post-trigger record is complete and drain is open, `VALID` = a coherent record is
present. Unlike the vendor's bimodal done bit, `DONE` here means done — the app gates on it
directly and never content-discriminates as the primary path.

---

## 5. Capture: exact pre/post window (fixes the proving-ground over-capture)

**Adversarial-review finding (MEDIUM):** the proving-ground circular writer over-captured by ~2
samples — a post-count off-by-one plus a registered-write tail — so `pretrig + posttrig ==
REC_DEPTH` clobbered the oldest pre-trigger cells. The owned capture makes this exact and states
the invariant:

- **Exact post count.** `post_count` increments in the SAME cycle the post-trigger sample is
  *committed* to `mem` (aligned to the actual registered `wren`, not the decode cycle), and the
  frame finalizes when `post_count == posttrig_work` **exactly** (equality, not `>=`), so total
  post-trigger writes equal the programmed depth with no tail slop.
- **Arm-time clamp.** At `OP_GO`, `pretrig_work + posttrig_work <= REC_DEPTH − MARGIN`
  (`MARGIN ≥ 2`, covering the registered-write pipeline tail). A full window can never overwrite a
  still-needed pre-trigger cell in the circular buffer.
- **Invariant (state it in the RTL header and `specs/03`):** *the drained physical array holds
  exactly `pretrig_work` pre-trigger samples, then the trigger sample at index `pretrig_work`,
  then `posttrig_work − 1` post-trigger samples; `pretrig_work + posttrig_work ≤ REC_DEPTH − 2`.*
- **Sim gate (learning baked in):** capture a known ramp; assert the trigger sample sits at
  exactly index `pretrig_work` in the drained array **and** the oldest pre-trigger sample is
  intact (not clobbered by post-trigger wrap).

---

## 6. Envelope: compute min/max ON THE LIVE STREAM (fixes the proving-ground BLOCKER)

**Adversarial-review finding (BLOCKER):** the proving-ground reducer swept the frozen record M9K
post-halt with a `FETCH → ACC` FSM that advanced in **one** cycle — but the record M9K has a
**registered read address AND registered read data** (2-cycle latency from "choose address" to
"data valid"). So it folded `mem[k−1]`: it corrupted column 0 with a stale word and **never
folded the last sample**. The drain/`BURST` ports get away with the same 2-cycle latency only
because the CPU holds `nOE` low ≥ ~4·T_clk (**BENCH-VERIFY #1**); a self-clocked internal reduce
loop has no such slack.

**Primary design — no post-halt re-read at all.** Compute the min/max envelope **on the canonical
stream as each sample is written during FILL** (`envelope.v` taps `{cap_word, cap_tick, waddr}`).
Per sample: fold into the current column's per-channel running min/max; a divider-free Bresenham
accumulator keyed on the write count closes a column and pushes its packed record into the
envelope FIFO. This is O(1)/sample, needs **zero** reads of the record M9K, sidesteps the
read-latency class entirely, and the envelope is ready the instant the record freezes — so a
slow/envelope band can drain **only** `ENV_DATA` (O(columns)), never the 20,480 raw record. That
is the strongest possible "results not raw."

- **Flush the tail at halt.** The mirror of the "never folds the last sample" bug: at finalize,
  close the final (partial) column so the last written sample is folded.
- **Invariant:** every sample in `[0, wrote_count)` is folded into exactly one column; the final
  column is closed at halt; column count is exact.
- **Sim gate (learning baked in, call it out explicitly):** *column 0 folds sample 0, and the
  last sample is folded* — plus total columns == programmed `ENV_COLS` (clamped).

**If any future tap must re-read the frozen M9K** (e.g. a future raw re-scan), the read pipeline
**must** account for the 2-cycle registered latency — a `FETCH → SETTLE → ACC` pipeline, or a
combinational read-address path during the internal sweep, or a prefetch-one-ahead — **and**
initialize `raddr` (and prime the pipeline) at sweep start. This rule goes in the module header
so it is impossible to reintroduce the BLOCKER silently.

**C3 overflow is first-class:** the envelope channel's `OVERFLOW` bit sets (never a silent drop)
if the FIFO would overflow (`ENV_COLS` set too large for the FIFO depth). This is the reusable
`result_fifo` contract every future channel inherits (see the codegen doc's channel-port
contract).

---

## 7. Static-freeze guarantee (enables the app to retire the quiet-lock)

After HALT the record M9K is a genuinely **registered, static freeze**: fill stopped, nothing
writes `mem`, the single read port is CPU-paced by the `BURST` reads. So the CPU/DMA drain **and**
a concurrent LCD framebuffer blit on the shared memory bus see a coherent, immutable record. This
is the hardware property that lets the app **drop `quiet.Lock()` across the drain** (see the app
doc) — the single biggest app simplification. It is a load-bearing claim, **validated at P1**
(§10): a raw `BURST` drain concurrent with a forced LCD blit must be byte-identical to a quiet
drain.

---

## 8. Fast re-arm + single-owner DMA window

`OP_GO` is a clean single-cycle re-arm (min dead time). The static-freeze premise must hold for
the **whole** drain, so the single-owner engine owns halt→drain→re-arm — a re-arm can never fire
mid-drain (the vendor's ~1 ms halt window becomes however long the `BURST`/DMA drain takes; the
fabric never auto-re-arms). `BURST_REMAIN.READY`/`REMAIN` flow-controls a self-paced EDMA /
prefetch drain (GPMC-DRAIN levers B/C over a single fixed address).

**[DECIDE]** whether to also expose a top-level DMA-request ball for bus-paced sync burst
(GPMC-DRAIN lever D) is bench-gated (a burst-clock pin) — reserve the option, don't build it in
the first cut.

---

## 9. Improvements over the proving-ground design (summary)

1. Live-stream envelope — no post-halt M9K re-read (kills the BLOCKER class) (§6).
2. Exact pre/post capture window + arm-time clamp + stated invariant (§5).
3. Populated interpolating trigger timestamp (§4) — the north-star made real.
4. Modular decomposition of the 825-line monolith into 6 reviewable modules (§2).
5. Single auto-inc `BURST` drain replaces the 5-port round-robin — DMA-1-D, simpler both sides.
6. Decimator populates a live transform stage → real slow bands + proves the stage contract (§3).
7. Generated RTL decode (`regmux.vh`) — no hand-written selector `case` to drift (see codegen doc).
8. Uniform `result_fifo` channel contract — future taps are instances, not rewrites (§6).
9. Clean level `STATUS_A` (real `DONE`) — no bimodal-done content-discrimination (§4).
10. One geometry source (schema → `regs.vh`) — RTL and app can't disagree (§2).

**The two adversarial-review learnings, baked in as sim gates:**

- Envelope: *column 0 folds sample 0; the last sample is folded; column count exact.* (§6)
- Capture: *trigger sample at drained index == `pretrig_work`; oldest pre-trigger sample intact;
  `pre+post ≤ REC_DEPTH−2`.* (§5)

Both gates are simulation gates that run before Quartus — a violation fails without hardware.

---

## 10. Build order (this module's slice — full plan in the codegen doc)

This module ships **Phase B** of the whole-effort plan (see the
[codegen doc](../../../codegen/docs/DESIGN.md) §5 for the full A–E sequence). Phase A (the schema,
`ifacedef.Standard()`, and the generated `regs.vh`/`regmux.vh`/`REGISTER-MAP.md` this module
`` `include ``s) must land first.

**Phase B — the standard bitstream:**

1. `acq.v` + `spine.v` + `capture.v` + `envelope.v` + `drain.v` + `dac.v`, `` `include "regs.vh" ``
   / `regmux.vh`, `acq.qsf`. The HW-safety envelope (§1) reviewed line-by-line.
2. Extend `internal/quartus` for **multi-file** `VERILOG_FILE` (§11); `cmd/buildacq` assembles
   QSF + generated includes + bench pins, memory-gates (`GateReady`), and compiles to
   `fpga/standard/acq.rbf`.

   **Gates:** RTL review (single tri-state driver, WAIT held ready, DAC tri-state, `CONF_DONE`
   never driven, M9K inference preserved). The **simulation gates from §9** (envelope
   column-0/last-sample, capture exact-window). On the bench box: `acq.rbf` compiles to exactly
   **368011** bytes with M9K preserved. Pins are bench-supplied (§11). **Do not run Quartus in
   CI.**

**Phase E — hardware validation (bench, gated; not CI):** flash `acq.rbf`; the `iface.Verify`
handshake passes; the two north-stars end-to-end — (1) drain is not the limit (envelope/measure
drain O(columns); raw only on zoom; CPU free during DMA drain), and (2) the **static-freeze
byte-identity** test (§7). Only then does the app retire the quiet-lock across the drain.

---

## 11. Build tooling: `buildacq` + the Quartus driver (bench-gated inputs)

The `fpga/` module owns the FPGA-specific build tooling:

- **`fpga/internal/quartus/quartus.go`** — a pure-Go headless Quartus driver, memory-gated
  (`GateReady`; the 3.8 GiB box runs one flow at a time) and **multi-file**. The proving-ground
  driver's `Compile` wrote one `<name>.v`; the modular design (§2) needs each source listed as a
  `VERILOG_FILE` in the generated QSF (it already copies `IncludeFile`s like `regs.vh`). This is a
  small, required enhancement — **[DECIDE]** confirmed as required.
- **`fpga/cmd/buildacq/main.go`** — assembles the QSF + the generated includes + the bench pins,
  gates on memory, and compiles → `fpga/standard/acq.rbf`. Run via `fpga/Makefile`'s `bitstream`
  target; **NOT run in CI** (needs Quartus + the memory gate).

`acq.qsf` carries device/pins/IO. Pins are **bench-supplied**:

- **[DECIDE]** the `acq.qsf` **pin assignments** come from the pin-discovery work; this design
  does not invent them. Until the bench supplies the map, `cmd/buildacq` can compile against a
  **placeholder/candidate** pinout for timing/fit only — the shipped `.rbf` waits on real pins.
  The pin map blocks only the shipped `.rbf`, not the RTL or the fit check.
- **[DECIDE]** the DMA-request ball for bus-paced sync burst (GPMC-DRAIN lever D) — reserve now,
  build later behind a burst-clock pin (§8).

`.gitattributes` gets `*.rbf binary` (the bitstream must be exactly 368011 bytes) and
`*.vh text eol=lf` (generated Verilog headers stay LF). The built `.rbf` is a **tracked** bench
artifact — not gitignored. A cheap CI check asserts that, when `acq.rbf` is present, it is exactly
`RBFBytes` (368011); a wrong size fails.

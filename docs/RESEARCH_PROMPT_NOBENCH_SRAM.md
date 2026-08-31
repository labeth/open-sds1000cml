# Research prompt — crack the SDS1102CML+ external-SRAM round-trip with NO bench access

## Your mission
Propose **novel, concrete, remotely-executable experiments** that could let an *own* FPGA bitstream
capture ADC data into, and read it back from, the external SyncBurst SRAM on a Siglent SDS1102CML+
oscilloscope — **without any bench access** (no logic analyzer, no oscilloscope-on-traces, no physical
probing, no chip removal, no soldering, no access to the MAX-V CPLD's JTAG TAP). Everything must run
over the remote interfaces listed below. We have exhausted the "obvious" attacks (detailed under
*Ruled out*); we need **fresh angles**, especially ones that exploit the one under-used observable:
**P6, a MAX-V status output that the Cyclone (and thus we) can read.**

Treat this as an adversarial black-box reverse-engineering problem. Be specific and technical. For every
idea, give: the hypothesis, the exact stimulus, the observable + pass/fail criterion, and why it could
work given the constraints. Rank by expected value. Flag anything that secretly needs a bench.

---

## The hardware (verified)
- **Cyclone IV EP4CE10F17C8** (main acquisition FPGA) — we can load arbitrary bitstreams into it.
- **MAX V 5M240ZT100C4 CPLD** — sits between the Cyclone and the SRAM; **owns and generates the SRAM
  command strobes** ADSP#/ADSC#/ADV#/OE#/BW#/GW# (device pins 83–88). It is a **black box**: off the
  reachable JTAG chain, no config dump obtainable remotely.
- **NETSOL S7A163630M SRAM** — 512K×36 SyncBurst *Pipelined* (traditional ADSP#/ADSC#/ADV#/GW#/BWE#,
  NOT NoBL/ZBT). Depth in use ≈ 20480 samples/channel.
- **3× AD9288** dual-8-bit ADCs — drive the wide DQ bus directly during capture (the ADC, not the
  Cyclone, is the write-data master). AD9288 outputs go high-Z ONLY via S1/S2 standby (no per-cycle OE).
- **AM3352 ARM** SoC — host bus (GPMC) master to the Cyclone.

### Signal ownership (bitstream-decode-proven + JTAG-verified)
- **Cyclone drives:** 18 address balls (a hardware ARITH up-counter, not CPU-loaded), 6 control balls
  (L2, N1, M6, N5, R6, T5 — idle-HIGH, active-low, class = CS#/WE#), 3 SRAM write-clock balls (F2/J2/K2,
  free-running), D2 (= nCSO, **held static LOW = the MAX-V chip-enable**), D14 (= the ADC differential
  sample clock — NOT the SRAM read clock).
- **MAX-V drives:** the 6 SRAM command strobes (pins 83–88), and **P6 = a 1-bit status output back to
  the Cyclone** (readable by us). P6 tracks D2 through a ≥2-clock synchronizer; reads 0 (asserted) in all
  observed vendor states.
- **The SRAM DQ bus IS the shared ADC-lane bus** (verified by a content differential: railing the vendor
  to two distinct constant SRAM contents and JTAG-diffing all balls — the data moves on the ADC-lane
  balls adc_lane[1,10,14,15,16,17,29], NOT on the balls previously mislabeled "sram_dq").
- Per-ball fine identity (which address bit → which ball; which control ball is CS# vs WE#; which of
  F2/J2/K2 is the primary clock; read/write pipeline latency) is **REFUSED** by bitstream RE (routes hit
  the LEIM interior wall) — these are unknowns.

---

## What we can do remotely (the complete toolbox)
1. **Load any Cyclone bitstream** (`fpga_reload`), volatile CRAM; power-cycle restores factory.
2. **GPMC register access** to the loaded Cyclone fabric (read/write CS1/CS3 planes via `gpmc_probe`) —
   this is how we poke debug knobs and read drained frames.
3. **JTAG boundary-scan on the Cyclone** (CMSIS-DAP, IDCODE 0x020F10DD): SAMPLE (read every ball's
   input level / drive-enable / toggle rate) and EXTEST (drive chosen Cyclone balls to chosen levels).
   **Reaches ONLY the Cyclone** — the MAX-V TAP is not on this chain.
4. **Recover to a STABLE factory boot** (untakeover + `sync` + power-cycle) — the factory firmware +
   bitstream then run untouched; the factory does real capture+drain through the SRAM.
5. **Factory ground-truth readout**: VXI-11 `C1:WF? DAT2` returns the 20480-sample record the factory
   itself read back from the SRAM — i.e. we can see exactly what the SRAM contained.
6. **Our app's frame API** (`/api/frame.bin`, `/api/status`) renders whatever our fabric drains.
7. **Set the analog input** (offset/vdiv/timebase via SCPI) — we can make the ADC produce chosen
   constants (rail high/low) or a live signal, i.e. we can WRITE chosen content into the SRAM *via the
   factory capture path*.
8. A complete, validated **MAX-V FSM decoder** (`pof_cfm → cone_decode → fsm_sim`) that decodes a MAX-V
   design's state machine bit-exact **from its .pof** — but we have no vendor .pof to feed it.

### Key observables (what we can measure)
- **P6** (MAX-V status output) — via JTAG SAMPLE and/or a Cyclone fabric that latches it to a GPMC reg.
- **The DQ bus** (ADC-lane balls) — whether/what the SRAM drives, via a Cyclone fabric that captures it,
  or JTAG SAMPLE.
- **The factory's own SRAM readback** (VXI waveform) — ground truth for what is stored.
- **All Cyclone ball states** during any frozen condition (JTAG SAMPLE: level, drive, toggle).

---

## What is exhaustively RULED OUT (do not re-propose)
1. **Driving the SRAM strobes ourselves** — ADSC#/OE#/ADV#/BW#/GW# are MAX-V pins, not Cyclone pins; no
   Cyclone EXTEST or fabric can reach them.
2. **"Just drive the right posture" for a read** — we built the exact decompile-prescribed read posture
   (address counter WALKING, CS# low, WE# high, F2/J2/K2 clocked, D2 static low, capture ADC-lane DQ) and
   JTAG-VERIFIED the fabric drives it correctly. Swept every CS#-ball choice against a *verified* 0xFF
   SRAM prime: **0/6144 samples read 0xFF**. The MAX-V does not assert OE# for our stimulus.
3. **Write handshake by parameter sweep** — 900+ config sweep of write timing/roles = **zero commits**;
   plus the corrected-model round-trip (our capture + our read, one session) = eng_enable has no effect
   (no data traverses the SRAM).
4. **Factory-prime → our-fabric read** — factory captures a verified constant into the SRAM; we take over
   and read with our fabric = float artifact, 0/6144 match. Reconfiguring the Cyclone to load our read
   fabric appears to **break the MAX-V's post-capture state** (the only proven reads — sramdump/draintest
   — do capture AND drain in the *same* factory fabric with NO reconfigure between).
5. **Dumping the MAX-V .pof** — the CPLD TAP is not on the reachable chain; treated as bench-only.
6. **Passive JTAG to resolve DQ/strobe identity** — the async ~80 MHz demux aliases; refused.

---

## The crux (state it plainly)
The MAX-V will sequence a real SRAM read/write **only in a context our cold-configured Cyclone never
establishes.** Its command generation is a function of internal FSM state + its inputs {clk, cs_n, we_n,
ncso(=D2)}; driving those inputs to the vendor-observed static/posture values does **not** make it act,
and any Cyclone reconfigure (needed to run our fabric) appears to reset whatever "primed" state a real
capture leaves. We cannot see inside it and cannot dump it — **but we can read one of its outputs (P6),
we can drive all of its inputs, and we can use the factory as a working oracle.**

---

## Seed directions (to spark thinking — we want these critiqued AND expanded, plus wholly new ones)
Explore, poke holes in, and go beyond:

1. **Black-box FSM identification via the P6 oracle.** P6 is a MAX-V output we can read. Design input
   sequences (over D2, the 6 control balls, F2/J2/K2 phase/count, address) from our fabric, latch P6 into
   a GPMC-readable register every clock, and reconstruct the MAX-V's next-state/output relation from the
   P6 response — enough to learn what input sequence drives it into "read" or "write" mode. What input
   alphabet + sequence length makes this tractable? Is P6 informative enough (1 bit) or must we also use
   "does the SRAM drive DQ this cycle" as a second output bit?

2. **Hypothesis-space model selection instead of a dump.** We have a MAX-V FSM *simulator*. Enumerate
   plausible SyncBurst/SPB controller FSMs (a small, structured family), simulate each one's P6 + strobe
   behavior for a battery of our probe sequences, and **select the hypothesis whose predicted
   P6/DQ-drive pattern matches the hardware.** Does the observable set (P6 + DQ-driven-or-not + factory
   ground truth) distinguish the candidates? What probe battery maximizes discriminability?

3. **Glitch-free Cyclone reconfiguration to preserve the prime.** The factory can capture into SRAM; the
   only thing that kills our read is the reconfigure that resets the MAX-V's primed state. Is there any
   way to swap in our read fabric **without glitching the MAX-V-facing pins** (D2, F2/J2/K2, the 6
   control balls)? E.g. Cyclone IV config options that hold I/O states through reconfiguration; loading a
   bitstream that differs only in the read logic while pin drivers stay put; partial/però-region tricks;
   or driving D2/clocks from an external-to-Cyclone source during the swap. If the MAX-V never sees its
   inputs glitch, does its prime survive?

4. **Make the factory prime *robust to reconfigure*.** Instead of preserving MAX-V state, find a MAX-V
   entry condition our cold fabric CAN re-establish. What minimal input sequence would re-drive the MAX-V
   from power-up/idle into the same "armed to read the current SRAM contents" state the factory capture
   leaves it in? (Ties to #1/#2 — we need to know the FSM to know the sequence.)

5. **Reach the MAX-V TAP without a bench.** Are the MAX-V JTAG pins (TDI/TDO/TMS/TCK) physically routed
   to anything we already control — a Cyclone I/O we could bit-bang JTAG from a custom bitstream, or an
   AM3352 GPIO? Could a Cyclone fabric act as a JTAG master to the MAX-V TAP over shared board traces?
   (If yes, dumping the .pof becomes remote and the whole thing collapses to the ready decoder.)

6. **Use the factory as the SRAM controller and steer it.** We can make the factory capture arbitrary
   ADC content (drive the analog input) and read it back (VXI). Is there a way to *co-opt the factory's
   own capture→drain* as our read/write channel — e.g. drive the input to encode data, or interleave
   GPMC pokes during a factory acquisition — achieving "read the ADC data written" even if the controller
   is the vendor's, not ours? Where is the line between "our fabric does it" and "we achieve the goal"?

7. **Timing/phase as the missing variable.** Everything we swept was level/role. The MAX-V is synchronous;
   maybe it only sequences when the control edges land at a specific phase vs F2/J2/K2, or when ADSC#-
   request cadence matches its NoBL-vs-burst expectation. Design a *phase/cadence* sweep (not a
   level/role sweep) with a per-config pass criterion.

8. **Side-channel state inference.** Beyond P6: can the *pattern of DQ-bus activity* the SRAM produces
   (even garbage) leak the MAX-V's state or clock phase? Can power/timing of the factory's drain (visible
   in frame timing) constrain the FSM?

---

## Deliverable
A ranked list of candidate attacks. For each: **hypothesis · exact remote stimulus (which tool, which
signals) · observable + quantitative pass/fail criterion · why it could work · estimated effort · honest
failure modes / whether it secretly needs a bench.** Prioritize ideas that (a) exploit P6 and/or the
factory oracle, (b) could recover the MAX-V behavior WITHOUT its .pof, or (c) preserve/re-establish the
MAX-V prime across a Cyclone reconfigure. Call out any assumption above that, if wrong, would open a
simpler path — and how to test that assumption cheaply. Novel mechanisms welcome; do not merely restate
the ruled-out list.

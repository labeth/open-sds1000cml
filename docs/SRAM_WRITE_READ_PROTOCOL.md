# SRAM write/read protocol + bench plan (research-derived, 2026-08-02)

Datasheet-grounded protocol for the fitted **NETSOL S7A163630M** (Samsung K7A163630B-class,
512K×36 **SyncBurst Pipelined / SPB**) SRAM, and the resulting bench plan for the MAX-V write
handshake. Confidence: **[V]** verified-from-datasheet, **[I]** inferred.

## Interface / part corrections (important)
- **[V]** The right protocol reference is **Cypress CY7C1480V33** (traditional `ADSP#/ADSC#/ADV#/GW#/BWE#/BWx#`),
  **NOT CY7C1370C NoBL/ZBT** (that uses `ADV/LD#`, `WE#`, `CEN#` — different write-data timing). Do not copy the NoBL truth table.
- **[V]** Signal-name map (NETSOL/Samsung → Micron/Cypress): `BW#`→`BWE#` (master byte-write gate), `WEa#..WEd#`→`BWa#..BWd#` (per-9-bit-lane selects), `GW#`→`GW#` (full-width write).
- **[V]** `-PC25` speed grade limits (K7A `-25`): tCYC≥4.0ns, setup≥1.2ns, hold≥0.3ns, tCD≤2.6ns.

## The core fact — why a held address + WE# pulse never writes
**[V]** A new external address enters the SRAM **only** when `ADSP#` or `ADSC#` is sampled LOW on a rising SRAM `CLK` edge.
A statically-held address with an unrelated FPGA-side `WE#` pulse is **not a valid write**. The write itself is decided by
`GW#` (full-width) or `BWE#`+byte selects — never by our `WE#` directly. **Our FPGA `WE#` is a REQUEST into the MAX-V, not the SRAM write command.**

Two legal write starts:
1. **ADSC# start** (1 clock): on the SAME rising edge — `ADSC#=0`, `ADSP#=1`, CE active, `GW#=0`, address=A0, DQ=D0 → writes D0→A0.
2. **ADSP# start** (2 clocks): edge P0 `ADSP#=0` loads A0 but is FORCED READ (write controls ignored); edge P1 `ADSP#=ADSC#=1`, **`ADV#=1`**, `GW#=0`, DQ=D0 → writes D0→A0.

**[I] Most likely factory capture sequence** (one externally-supplied address written per clock — matches the Cyclone sweeping the full address bus):
```
each SRAM CLK rising edge:  CE selected; ADSP#=1; ADSC#=0; ADV#=X; OE#=1; GW#=0; address=A[n]; DQ=ADC[n]
```
For full-width writes use `GW#=0` and hold every byte control (`BWE#`/`BWa..d#`) HIGH — removes byte-mask ambiguity.

## Read (pipelined, +1 clock latency)
**[V]** Address A0 accepted at R0 (`ADSP#=0` or `ADSC#=0` with write controls inactive, `OE#=0`); `Q(A0)` appears tCD **after the NEXT rising edge** (R1). A same-clock FPGA input register therefore captures `Q(A0)` at **R2**, not R1. `ADV#=0` advances the burst. `OE#` is asynchronous — keep LOW through the output interval; it does not start a read.
- **[V]** At 250 MHz a falling-edge capture is NOT safe: half-cycle 2.0ns < worst-case tCD 2.6ns. Capture on the following rising edge or a phase-shifted/delayed clock with constraints.

## Why our reads came back high-entropy garbage (NOT just a pipeline offset)
**[V/I]** A one-word pipeline error shifts a low-entropy waveform; it does not make the whole record high-entropy. Whole-record entropy points to:
1. **ADC bus contention** — the **AD9288 has NO per-cycle output-enable**; its outputs go high-Z ONLY via `S1/S2` standby (S1=0,S2=0 → both channels high-Z; ~15 encode-clock recovery). If any ADC on the shared DQ bus stays in normal mode during drain, it drives against the SRAM → contention → garbage.
2. SRAM never selected / `OE#` never low → floating DQ.
3. Wrong sampling phase (esp. falling-edge at high rate).
4. MAX-V read counter never initialized by the expected start sequence (old/current address repeated).

## Bench probe plan (2 channels; SRAM CLK is always the reference)
| Priority | Pair | Correct factory capture | Failing replica |
|---|---|---|---|
| 1 | **CLK + GW#** | `GW#` LOW across ≥1 CLK rising edge | `GW#` never falls, or pulses only between edges → MAX-V never entered write mode |
| 2 | **CLK + ADSC#** | `ADSC#` LOW at each write edge (or each burst base-load); continuous-low is valid | no LOW sampled at a rising edge → no new address ever loaded |
| 3 | **FPGA-WE# + SRAM-GW#** | deterministic WE-request→GW translation/latency | WE moves, GW stays high → the MAX-V predicate we're missing |
If `ADSC#` is static high in a known-good factory capture, repeat #2 with `ADSP#` (correct ADSP shows the write one edge later; confirm `ADV#=1` on that edge). **If only one capture: CLK + GW#** — it answers whether the SRAM ever samples a full-width write at all.

## Newly-actionable REMOTE experiments (before the bench)
1. **Read fix — ADC standby during drain.** Drive the AD9288 `S1/S2` to standby (both-channels high-Z) during `ST_DRAIN_SRAM` so the ADCs release the shared DQ bus, then re-run the factory-write→capsram-read faithfulness test. S1/S2 are among our 7 mode controls (F1/L4/T2/T7 held H, G1/G2/K1 held L) — make them state-dependent (normal in FILL, standby in DRAIN) and sweep which controls. If the read goes low-entropy (matches ground truth) → the read is fixed and the garbage was contention.
2. **Read capture phase.** capsram samples DQ on the sck-high edge (rd_clkdiv). Per the pipeline, capture the following rising edge / a delayed phase — sweep the slurp capture phase.
3. **Write — WE# as a LEVEL, not pulses.** Hold the write-mode indication (CE#/WE#) as a level across the whole acquisition (idle→assert early→hold through→deassert after), advancing address/data once per edge — tests whether the MAX-V latches direction at a start/CE transition rather than per narrow WE# pulse. (Lower odds without observing the MAX-V, but cheap.)

## Public RE survey (nothing decisive found)
EEVblog SDS1102CML thread (board photos, no SRAM trace); Digitech QC1934 = CML-equivalent w/ SRAM fitted; SDS1052DL+ teardown (component id only); SDS1000CML service manual (disassembly, no acquisition schematic/CPLD equations); 360nosc0pe (later SDS1x0xX-E family, different acq HW, not transferable). No public CML+ netlist, MAX-V equations, or ADSP/ADSC/GW capture. **SRAM-side target waveform is now well-defined; the Cyclone-side MAX-V predicate must be found at the bench.**

## Datasheet index
- NETSOL S7A163630M Rev.1.2 (general desc: 512K×36, GW/BW/WEx, ADSP/ADSC/ADV).
- Samsung K7A163630B Rev.3.0: p7 function, p8 sync/write truth tables, p11 AC timing, pp12-14 waveforms.
- Infineon/Cypress CY7C1480V33: p5 pins, pp6-7 operation, pp8-9 truth tables, Fig3 p14 read, Fig4 p15 write.
- Micron MT58L/MT58V 512K×36: pp4-5 pins, pp10-11 truth tables, read/write timing pp15-16/19-21.
- Analog Devices AD9288 Rev.C: Fig2 p5 timing; p14 Digital Outputs + Table4 S1/S2 standby modes.

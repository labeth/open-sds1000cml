# SDS1102CML+ external-SRAM — status report (2026-08-02)

Consolidated state of "get the external SRAM working with our own implementation, the vendor way."
Supersedes `SRAM_AND_DATAPATH_SPEC.md`. Confidence tags: **[bench]** HW-verified · **[decode]** bit-exact
from the vendor bitstream/device file · **[bscan]** JTAG boundary-scan · **[model]** SPB datasheet /
reasoned · **[REFUSED]** needs the vendor's own compile project or a bench logic-analyzer.

---

## 1. Goal
Our own Cyclone bitstream + our own app doing the vendor's capture path — ADC → external S7A163630M SRAM →
GPMC drain — emulating the vendor Cyclone so the fixed MAX-V cooperates. The MAX-V is a vendor part we
interoperate with, not reprogram.

## 2. Hardware topology + full datapath

```
                ENCODE clk (Cyclone→ADC, K8..M8, ~10 MHz = clk/8)
   ┌────────┐                                 ┌──────────────┐  GPMC CS1/CS3 (ARM↔Cyclone)
   │  ARM   │                                 │   CYCLONE    │  0x21 arm · 0x35 run · 0x30-34 drain
   │ AM3352 │◄───────── GPMC ────────────────►│   EP4CE10    │  CS3: 0x14/34 trig-DAC · 0x10/30 off-DAC
   └────────┘                                 └───┬──────┬───┘
                     addr A0..A17 (18)            │      │ CLK F2/J2/K2 · CS# L2/N1 · WEa-d# M6/N5/R6/T5
                     + A18 strap (off-FPGA)       │      │ D14 read-clk · D2=nCSO(→MAXV,low) · P6(←MAXV)
                                                  ▼      ▼ reads DQ
   ┌────────┐  shared wide bus (== SRAM DQ ×36)  ╔══════════════════════════════════════╗
   │  ADC   │═══════════════════════════════════►║ CAPTURE: ADC drives → SRAM           ║
   │ 3×9288 │                                    ║ DRAIN:  SRAM drives → Cyclone reads  ║
   └────────┘                                    ╚═════════════════╤════════════════════╝
                                    ┌─────────────────────────────▼─────────┐   ┌─────────┐
                                    │ SRAM S7A163630M 512K×36 SPB            │◄──│  MAX V  │
                                    │ A0..A18 · CLK · CS1#/CS2# · WEa-d#     │   │ 5M240ZT │
                                    └────────────────────────────────────────┘   └────┬────┘
                                        cmd pins 83-88 = ADV#/ADSP#/ADSC#/OE#/BW#/GW# ◄─┘
```

**Chips:** Cyclone IV E EP4CE10F17C8 (BGA, main acq FPGA) · MAX V 5M240ZT100C4N (TQFP CPLD, **JTAG
unreachable** — sequences the SRAM command strobes) · SRAM NETSOL S7A163630M-PC25 (512K×36 sync
pipelined-burst) · 3× AD9288-class dual-8-bit ADC (40 data lanes = 5 cores × 8 bit) · TI AM3352 ARM.

**Three phases** (PATH A, decode-proven): **capture** — Cyclone clocks the ADC + sweeps SRAM address +
pulses CS/WE + holds D2 low; the **ADC drives the shared DQ bus**; the MAX-V issues the SRAM write.
**drain** — Cyclone drives **only D14**, tri-states the 27 addr/ctrl/clk balls so the MAX-V holds the read
address, and latches DQ → GPMC ports 0x30-0x34. **re-arm.** Word = hi CH1 / lo CH2, 20480 samples/record.

## 3. SRAM interface — exact Cyclone ball map  [decode + bscan]

| Group | Balls |
|---|---|
| 18 ADDRESS (A0..A17; A18 = off-FPGA strap) | L1 N2 P1 P2 R1 · J6 K5 · L3 N3 N6 P3 R7 · R3 R4 R5 T3 T4 T6 |
| 6 CONTROL (idle-HIGH active-low) | **L2=CS1#, N1=CS2#, M6=WEa#, N5=WEb#, R6=WEc#, T5=WEd#** |
| 3 write CLOCK | F2 J2 K2 |
| read CLOCK | **D14** (`sck_rd`) — only net driven during the non-contending drain |
| DQ | shared bus with the ADC (PATH A); 5 Cyclone-bidir (F3 F5 G5 D3 F7) + wide input lanes; true width [5,36] **[REFUSED]** |
| MAX-V levers | **D2**=nCSO (static LOGIC-0, bit-exact + bscan) · **P6**=status in |
| ADC | encode K8-M8; static-ctrl F1/L4/T2/T7=1, G1/G2/K1=0; 40 data lanes (33 decode-clean + ~7 in the shared ADC/DQ pool) |

MAX-V owns SRAM pins 83-88 (ADV#/ADSP#/ADSC#/OE#/BW#/GW#), off-Cyclone.

## 4. Write-enabling + toggling — the mechanism (UPDATED this session)

**Key reframe:** the write-enable and the write-sync toggle are the **same signal**. All three **F2/J2/K2
free-run as the SRAM clock** in every phase (SPB-model + SAMPLE-confirmed) — none is a "write-sync
strobe." The write-sync toggle **is the WEx# strobe** (M6/N5/R6/T5): idle-HIGH, **pulsed LOW per write**,
in lockstep with the free-running clock + the address counter. So the vendor write recipe is:
*free-run F2/J2/K2 as CLK · pulse WE# low each write · hold D2 low · MAX-V issues ADSC#/write · ADC drives DQ.*

**What the completed decoder now reads (bit-exact, new this session):**
- Every control ball's role + IOE chain `LE register → BLOCK_INPUT_MUX (sel = source-LI I-index, 19/19)`.
- **Interior row-clock net map** (`SCLK_TO_ROWCLK_BUF`): rows Y3/5/6/7/10 ← GLOBAL_CLK_net3 (dominant),
  Y4←net9, Y8←net6; driver-register rows narrowed (L2→net6, N1/R6→net3).
- F2←X0Y19, J2←X0Y10 output-LI; **M6+T5 share one live upstream LE (`LE_BUFFER 27861@X8Y1`)** = the WEa#/WEd# pair.
- D2/nCSO static-0 confirmed.

**What stays [REFUSED]:** the boolean **FSM that generates the WE# pulse** — each strobe register's LUT
function, FF/clock, and data-input source. Blocked by routing↔CRAM frame misalignment (driver LABs decode
to empty tiles) + unanchored LE-index + an un-grounded LE-input mux. Closing it needs **one directed
interior-LAB compile of the vendor's own project** (unavailable) or a **bench logic-analyzer**.

## 5. What is PROVEN on hardware  [bench]

- **Our software reads the external SRAM the vendor way.** `draintest` drove the factory bitstream's
  arm→capture→drain over GPMC and pulled **real triangle data** out of the external SRAM (full-depth 20480
  records occur).
- **Non-contending SRAM read from our own fabric** (`sramdump`): drive only D14, tri-state the 27, MAX-V
  holds address, latch DQ.
- The verified GPMC protocol: fill gate `0x46 ≥ 0x200`, arm `0x21` C0/C3/C8, drain `0x30-0x34`, front-end
  DACs on CS3.

## 6. Our own implementation (`capsram` / `acq_sram.rbf`) — status

Drop-in for `standard/acq.rbf` (same register interface + build-ID `0xc2f6eb5f`), swaps the M9K capture
buffer for the external SRAM; `mem[]` is filled ONLY by the D14 slurp reading the SRAM (so a coherent frame
is by construction proof it traversed the external part). Runtime write-tuning knobs on free CS1 debug sels.

- **Built** (368011 B, 0 fit errors, all 27 SRAM balls + D2 + D14 + DQ placed, Fmax 59 MHz), **loads +
  verifies on hardware, register interface works, the SRAM read STRUCTURE produces full 20480 records.** [bench]
- **Deploy mechanism proven:** `OTA_AUTO_TAKEOVER=1` in the device `agent.env` → our app auto-loads the
  FPGA on boot; same build-ID → fpgaload skips reconfigure unless CRAM is cleared (Shelly power-cycle first).

## 7. Current blockers (why it doesn't yet capture the triangle into the SRAM)

1. **Front-end / ADC delivers a flat signal under our app.** Both the M9K and capsram fabrics drain
   all-zero at every vdiv/offset — the app's `SetVdiv` emits (relay+gain, `ok:true`) yet the ADC reads a
   constant. Our clean-room analog front-end / ADC-drive config isn't fully reproducing the factory's, so
   there's no live signal to capture. This blocks capture on **any** of our fabrics and must be fixed
   first. (git says the standard fabric "CONVERTS + captures real data" — reconcile the regression /
   confirm the known-good vdiv/offset/coupling recipe: C1 500 mV/div, C2 50 mV/div, TDIV 25 µs, DC.)
2. **The MAX-V write handshake for a standalone fabric** is downstream of #1 (untestable with no signal),
   and its exact WE#-pulse timing is the [REFUSED] FSM above — a hardware-tuned knob in capsram.
3. **capsram DBG read decode** returns 0 (writes fine) — blind on-fabric diagnosis; small RTL fix + rebuild.

## 8. What each open item needs

| Open item | Needs |
|---|---|
| Exact WE#-pulse FSM (write timing) | vendor's own Quartus project **or** a bench logic-analyzer on the SRAM/MAX-V + F2/J2/K2 during a live acquisition |
| DQ true width / ADC-vs-DQ lane separation | bench probe of the shared bus (decode can't separate — same nets) |
| Our-app front-end delivering signal | reconcile the ADC/front-end config to the known-good recipe (device-file/RE + the app's analog path) |

## 9. Path forward
The interface, datapath, pin map, clocking, and the *structure* of the write (free-run CLK + WE# pulse per
write + D2-low) are recovered — enough to build the replica. `capsram` is that replica; its remaining gaps
are (a) get a real signal to the ADC under our app, then (b) tune the WE#-pulse phase/width on hardware
(the one piece the bitstream can't yield, needing the vendor sources or a scope). A single **bench
logic-analyzer capture** of the SRAM/MAX-V/clock pins during one vendor acquisition would settle the write
timing, the DQ separation, and the primary-CLK identity in one shot — the highest-leverage physical step.

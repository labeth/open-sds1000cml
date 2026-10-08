# 12 — FPGA Board Interface

Everything an FPGA image needs in order to connect to the rest of the board: the device, its
configuration, clocks, and every connected ball with its function, direction, level and timing.
The register map an image presents to the ARM is the image's own choice and is not part of this
interface.

All balls are **3.3-V LVTTL**. Unused balls are reserved **as inputs, tri-stated**.

---

## 1. Device

| property | value |
|---|---|
| Part | Cyclone IV E `EP4CE10F17C8` |
| Logic elements | 10,320 |
| M9K blocks | 46 (9,216 bits each) |
| 18×18 multipliers | 23 |
| PLLs | 2 |
| Configuration | passive serial, volatile, loaded by the ARM at every boot (§2) |
| Bitstream | uncompressed `.rbf`, **368,011 bytes** |

Project settings an image must keep: on-chip bitstream decompression **off**; nCEO and the flash
nCE pin **used as regular I/O**.

---

## 2. Configuration

The ARM configures the FPGA through a 16-bit port at GPMC **CS3 selector `0x07`**:

| bit | write | read |
|---|---|---|
| 0 | DCLK | |
| 1 | nCONFIG | |
| 2 | DATA0 | |
| 6 | | nSTATUS |
| 7 | | CONF_DONE |

Any write with bit 1 low resets the running fabric.

Sequence:

1. Write `0x0000` (nCONFIG low). Hold **2 ms**.
2. Write `0x0002` (nCONFIG high).
3. Poll until nSTATUS (bit 6) is high; poll every 10 ms, at most 21 times.
4. For every byte of the `.rbf`, **least significant bit first**: write `0x0002 | DATA0`, then
   the same word with DCLK (bit 0) set.
5. Clock **128** further DCLK cycles with DATA0 low.
6. Write `0x0002`.
7. Poll CONF_DONE (bit 7) every 1 ms for up to 5 s. If it stays low, repeat from step 1;
   give up after **3** attempts.

Configuration is volatile. A power cycle restores the factory image.

---

## 3. Clocks

| ball | signal | rate | use |
|---|---|---|---|
| `M2` | `mclk_in` | **100 MHz** | board reference; input to both PLLs |
| `C2` | `clk` | **50 MHz** | ARM GPMC clock; host-side logic and the panel scan run on it |

`mclk_in` and `clk` have no phase relationship.

The shipping images derive from `mclk_in`:

| PLL | outputs | use |
|---|---|---|
| core | 250 MHz at 0 ns; 250 MHz at +1 ns; 125 MHz | SRAM port and record logic; SRAM read capture; packing |
| ADC phase | five 100 MHz clocks at +3, +4, +2, 0, +1 ns (compensated on the 0 ns output) | encode pairs E1–E5 (§4) |

---

## 4. ADCs

Five **AD9288** dual 8-bit converters: **ten cores**, each running at **100 MS/s**. Each input
channel has five cores, which together give **500 MS/s per channel** (1 GS/s aggregate).

### 4.1 Encode clocks

Each converter has one differential encode pair: `enc_p` clocks one core, `enc_n` the other. Drive
both legs as **complementary 100 MHz square waves** (DDIO outputs), **minimum drive strength**.
The leg that rises at the pair's base phase is given; the other leg rises 5 ns later.

| pair | base | `enc_p` ball | core, rising edge | `enc_n` ball | core, rising edge |
|---|---:|---|---|---|---|
| E1 | 3 ns | `K8` | CH2, 3 ns | `M8` | CH1, 8 ns |
| E2 | 4 ns | `C14` | CH1, 4 ns | `D14` | CH2, 9 ns |
| E3 | 2 ns | `K9` | CH2, 7 ns | `L10` | CH1, 2 ns |
| E4 | 0 ns | `L8` | CH1, 0 ns | `M7` | CH2, 5 ns |
| E5 | 1 ns | `K10` | CH1, 6 ns | `L9` | CH2, 1 ns |

Holding a leg low stops its core.

### 4.2 Sample order

Within each 10 ns period the ten samples are taken in this order (sample instant in ns):

| 0 | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 |
|---|---|---|---|---|---|---|---|---|---|
| E4.CH1 | E5.CH2 | E3.CH1 | E1.CH2 | E2.CH1 | E4.CH2 | E5.CH1 | E3.CH2 | E1.CH1 | E2.CH2 |

CH1 samples fall on even nanoseconds and CH2 on odd ones, so CH2 is offset +1 ns from CH1. Per-core
offset, gain and aperture skew are not matched on the board.

### 4.3 Data lanes

The converters drive **80 data lanes** continuously, eight per core. There is no output enable.
Register each lane in the I/O element (fast input register). Register the 16 lanes of pair E*n*
on the **inverse** of that pair's base-phase clock, i.e. **5 ns after the base phase**.

Codes are 8-bit offset binary. **The code falls as the input voltage rises.** Invert every sample
(`~code`) to get codes that rise with voltage.

Ball for each bit, LSB (bit 0) first:

| core | bit 0 | 1 | 2 | 3 | 4 | 5 | 6 | 7 |
|---|---|---|---|---|---|---|---|---|
| E1 CH1 | `G11` | `F10` | `E9` | `J11` | `K11` | `K12` | `L14` | `L13` |
| E1 CH2 | `N11` | `N12` | `N13` | `P14` | `N14` | `M12` | `L11` | `L12` |
| E2 CH1 | `C15` | `B16` | `A15` | `B14` | `A14` | `B13` | `A13` | `B12` |
| E2 CH2 | `D16` | `D15` | `F16` | `F15` | `G16` | `G15` | `J16` | `K16` |
| E3 CH1 | `R16` | `P15` | `P16` | `N15` | `N16` | `L15` | `L16` | `K15` |
| E3 CH2 | `T15` | `R14` | `T14` | `R13` | `T13` | `R12` | `T12` | `R11` |
| E4 CH1 | `R8` | `T8` | `P8` | `T9` | `R9` | `T10` | `R10` | `T11` |
| E4 CH2 | `L7` | `M9` | `N8` | `N9` | `P9` | `M10` | `M11` | `P11` |
| E5 CH1 | `E10` | `C11` | `E8` | `D9` | `C9` | `D8` | `C8` | `F8` |
| E5 CH2 | `F14` | `F13` | `F11` | `C16` | `D12` | `E11` | `D11` | `F9` |

### 4.4 Mode straps

The converters' mode pins share balls with the SRAM data bus: `L4` (`dq[8]`), `T7` (`dq[26]`)
and `T2` (`dq[28]`) must rest **high**. The SRAM data bus idles high when released (§5.2), so
an image meets this by tri-stating `dq` whenever it is not writing.

---

## 5. External SRAM

| property | value |
|---|---|
| Part | NETSOL S7A163630M, 512K × 36 synchronous pipelined burst |
| Words | 524,288 (19-bit address); the FPGA uses **32** of the 36 data bits |
| Maximum word rate | **250 MHz** |

The FPGA does not drive the SRAM address or command pins. They are driven by an external counter
that the FPGA steps with `K2`. The FPGA has four controls and the data bus.

### 5.1 Balls

| ball | signal | dir | function |
|---|---|---|---|
| `G1` | enable | out | high = the SRAM port runs. Low resets the address counter to 0. |
| `K1` | direction | out | high = read, low = write |
| `K2` | word clock | out | one pulse per word; each pulse transfers one word and advances the address by 1, modulo 524,288 |
| 32 balls | `dq[31:0]` | in/out | data |

Data balls, `dq[0]` first:

| bits | balls |
|---|---|
| 0–7 | `J6` `F5` `L2` `L1` `L3` `N2` `N1` `K5` |
| 8–15 | `L4` `R1` `P2` `P1` `F3` `G5` `N3` `P3` |
| 16–23 | `N5` `N6` `D3` `M6` `R5` `T5` `R6` `T6` |
| 24–31 | `R3` `R7` `T7` `T3` `T2` `R4` `T4` `F7` |

Drive settings: `dq` at **8 mA**, with fast input and fast output registers. `K1`, `K2` and `G1` at
minimum drive strength.

### 5.2 Protocol

The reference design runs the port from the 250 MHz core clock.

* **K2** comes from a DDIO output whose high half is held 0 and low half carries the pulse, so each
  pulse is a half-period high in the second half of a core clock period.
* **Write.** Hold `K1` low. Present the word on `dq` from the core clock edge that starts its K2
  period; the data is stable for half a period before K2 rises. Drive `dq` **only** during a write
  burst; otherwise tri-state it (it then rests high, §4.4).
* **Read.** Hold `K1` high and `dq` tri-stated. The SRAM output is a **two-stage pipeline**: the
  word addressed by a pulse arrives two pulses later. Capture `dq` on the core clock shifted by
  **+1 ns**. A fresh read burst therefore needs one extra flush pulse at its end. The first
  **16 words** of a fresh read burst are unreliable and must be discarded.
* **Direction change.** Wait **15** core clocks after changing `K1` before the first pulse, and
  **15** core clocks after the last write pulse before switching to read.
* **Seeking.** The address can only move forward by pulsing. To reach an address, issue read pulses
  and discard the data.

**Address bookkeeping.** Count every K2 pulse (reads, writes, flushes and discards) modulo 524,288,
from 0 when `G1` rises. On a read, the count is the address of the next pulse. On a write, the first
word offered when the count is *P* is stored at **P + 2** when K2 pulses on every core clock, and at
**P + 3** when the pulses are spaced (data slower than the core clock). Begin each write burst with
16 discarded words.

---

## 6. ARM bus (GPMC)

The FPGA is a 16-bit, non-multiplexed slave on chip select **CS1** of the ARM's GPMC. The data balls
are shared with the ARM's NAND flash.

### 6.1 Balls

| ball(s) | signal | dir |
|---|---|---|
| `B4` | `nCS1` | in |
| `E6` | `nOE` | in |
| `B10` | `nWE` | in |
| `B1` `A2` `C3` `D4` `A3` `B3` `A4` | address A1 … A7 | in |
| `A10` `B9` `A9` `B8` `A8` `B7` `A7` `B6` `A6` `D6` `C6` `B5` `A5` `F6` `D5` `E5` | data D0 … D15 | in/out |

### 6.2 Behaviour

* Registers are 16 bits. A1–A7 select one of **128** registers; register `s` is at byte offset
  `2s` within CS1. Higher address bits are not connected, so the space repeats every `0x100` bytes.
* **Drive D0–D15 only while `nCS1` and `nOE` are both low.** At any other time the FPGA must
  release the bus, or it corrupts NAND traffic.
* **Write:** address and data are valid until `nWE` rises; latch them on the rising edge of `nWE`
  while `nCS1` is low.
* **Read:** read data must be valid **30 ns** after `nOE` falls, the fastest read timing the ARM
  applies. A register with a read side effect (a FIFO pop) acts once per read, at the end of the
  cycle (`nOE` rising).
* GPMC timing for CS1, in ticks of the ARM's 100 MHz GPMC clock:

  | | CS on | OE on | data latched | OE off | CS off | cycle |
  |---|---:|---:|---:|---:|---:|---:|
  | read | 0 | 4 | 13 | 16 | 20 | 31 |

  | | CS on | WE on | WE off | CS off | cycle |
  |---|---:|---:|---:|---:|---:|
  | write | 0 | 4 | 16 | 20 | 20 |

  Same-chip-select accesses are separated by a 5-tick gap, plus 1 tick of bus turnaround.
* `nCS1`, `nOE` and `nWE` are asynchronous to every FPGA clock; synchronise them before use.

### 6.3 Host access

The ARM reaches CS1 through `/dev/Gpmc`. The device node accepts **one opener**, at boot; every
later user inherits that descriptor. Each access is one ioctl on a 6-byte record
`{plane, 0, sel_lo, sel_hi, val_lo, val_hi}`: `0x80026700` reads, `0x40026701` writes; `plane` is
1 for CS1 and 3 for CS3. A read returns its value in `val_lo | val_hi << 8` of the same record.
DMA reads of CS1 need the destination's CPU cache invalidated.

---

## 7. Front panel

The keys and knobs are a **64-bit parallel-load shift chain** scanned by the FPGA. The FPGA also
drives the ARM's key interrupt.

| ball | signal | dir | function |
|---|---|---|---|
| `J2` | load | out | high = load the chain |
| `F2` | shift clock | out | idles low |
| `J1` | data | in | serial data, first bit first |
| `F1` | key IRQ | out | to ARM `gpio3_19` (IRQ 275); a low pulse raises the interrupt |

`J2`, `F2` and `F1` at minimum drive strength.

**Scan.** Each frame:

1. Hold `J2` high for two shift-clock half periods (load), then return it low. The first bit is
   now on `J1`.
2. Clock 64 bits on `F2`: about **300 kHz** (half period 83 clocks of the 50 MHz `clk`).
   Sample `J1` before each rising edge of `F2`.
3. Leave a gap of about 200 half periods before the next frame.

Number the bits in arrival order, 0 to 63. Bits are **active low** (0 = pressed or phase low).

**Bit layout.** Bit `8r + p` is byte *r* (0–7), bit *p* (0–7):

* *p* = 0 and 1: the raw quadrature phases of the knob in byte *r*, if the byte has one. The sequence 00 → 01 → 11 → 10
  of (bit 1, bit 0) is one direction.
* *p* = 2–7: keys.

The key in byte *r*, bit *p* is the key that `08-front-panel.md` lists at register
`0x67 − ⌊r/2⌋`, bit `B + (8 if r is even, else 0)`, where `B` = 6, 7, 5, 4, 3, 2, 1, 0 for
*p* = 0 … 7.

**Interrupt.** Hold `F1` low for 255 shift-clock half periods (about 0.4 ms) after each frame that
differs from the previous one. The ARM receives this as SIGIO on `/dev/fpga_key`, which carries
no data, and then reads the panel state from the FPGA over CS1.

---

## 8. Other balls

| ball | dir | level |
|---|---|---|
| `D1` | out | low |
| `D2` | out | low |
| `G2` | out | low |
| `A11` | out | high |

`D1`, `G2` and `A11` at minimum drive strength. All other balls are unused.

---

## 9. Not on the FPGA

The ARM drives these without the FPGA:

* GPMC **CS3**: the configuration port (§2), the trigger-level DAC, the offset DACs and the panel
  LED latch (`02-register-map.md`).
* ARM SPI: the coupling, bandwidth-limit and range relays and the gain DAC
  (`06-vertical-and-analog.md`).
* The LCD framebuffer (`07-display-and-rendering.md`).

# 12 — Acquisition FPGA and External SRAM Capture

The acquisition FPGA is an Altera **Cyclone IV E EP4CE10F17C8**. It owns the ADC front end, the
external SRAM, and the GPMC slave the ARM drives. Its configuration is **volatile**: the CRAM is
loaded at every boot over the GPMC CS3 configuration port and nothing about it survives a power
cycle.

This spec defines the device's fixed properties, its **ADC front end**, its **GPMC slave
interface**, the **external SRAM** interface, and the register ABI of the **full-depth capture
image** (`fpga/acq_sram`), which records a complete external-SRAM frame, freezes it on trigger,
and serves it to the host for repeated recall.

Sections 1–5 describe the device and its external interfaces and apply to **any** image built for
this board. Sections 6–12 are specific to `fpga/acq_sram`. **§13 states what this document is and
is not sufficient to build**, and names the machine-readable artifacts that carry the physical
constants prose cannot.

`fpga/acq_sram` is a **separate image from `fpga/default`** and its register ABI is deliberately
different. A host must check `FABRIC_ID` before driving either.

---

## 1. Device and resources

| property | value |
|---|---|
| Part | Cyclone IV E `EP4CE10F17C8` |
| Logic elements | 10,320 |
| M9K blocks | 46 |
| Total block memory | 423,936 bits (46 × 9,216 incl. parity) |
| Usable data bits, ×16 mode | 376,832 (46 × 8,192; 512 × 16 words per block) |
| Embedded 9-bit multiplier elements | 46 (= 23 × 18×18) |
| PLLs | 2 |
| Configuration | passive serial, volatile, via GPMC CS3 port `0x07` |

**Configuration port CS3 `0x07`.** Write bits: `b0` DCLK, `b1` nCONFIG, `b2` DATA0, `b3` SPI route
(unused). Read bits: `b6` nSTATUS, `b7` CONF_DONE. Any write with `b1` low collapses the running
fabric, which is how a reconfiguration cycle starts.

---

## 2. Clocking

One board reference enters the device and both PLLs derive from it.

| clock | rate | role |
|---|---|---|
| reference | 100 MHz | board oscillator into the left PLL |
| ADC encode | 5 differential pairs at 100 MHz | converter encode; the complementary legs are 5 ns apart, so a core clocked by both samples at ~200 MSPS (§3.2) |
| packing clock | 125 MHz | 64-bit assembly stage |
| SRAM port clock | 250 MHz | external SRAM write |

The capture image takes the 80-bit / 100 MHz converter stream, buffers it in a shallow FIFO,
assembles 64-bit words at 125 MHz, and writes **32 bits at 250 MHz** to the external SRAM.

---

## 3. ADC front end

### 3.0 The converters

The converters are **Analog Devices AD9288** — a **dual 8-bit ADC**, two independent cores in one
package sharing a single per-chip `ENCODE` input, rated **100 MSPS** per core, with a minimum
`ENCODE` rate of **1 MHz**. Normal dual operation is the `S1 = 1 / S2 = 0` mode-pin setting
(§3.3). The parts have **no per-cycle output enable**: an AD9288 that is out of standby drives its
byte lanes continuously and cannot be tri-stated by the FPGA.

Ten converter cores are driven, as **five differential encode pairs whose two legs clock different
cores** — one CH1 core and one CH2 core, 5 ns apart. Measured pin/core/phase map:

| pair | base | positive leg | core | rising phase | negative leg | core | rising phase |
|---|---:|---|---|---:|---|---|---:|
| E1 | 3 ns | K8 | CH2 | 3 ns | M8 | CH1 | 8 ns |
| E2 | 4 ns | C14 | CH1 | 4 ns | D14 | CH2 | 9 ns |
| E3 | 2 ns | K9 | CH2 | 7 ns | L10 | CH1 | 2 ns |
| E4 | 0 ns | L8 | CH1 | 0 ns | M7 | CH2 | 5 ns |
| E5 | 1 ns | K10 | CH1 | 6 ns | L9 | CH2 | 1 ns |

CH1 is therefore clocked in the order E4, E3, E2, E5, E1 and CH2 in the order E5, E1, E4, E3, E2 —
which is what produces the frame ordering of §7. `[BENCH]`

**Board configuration.** Three AD9288 packages are fitted, giving six cores, of which **five are
connected** and one is unused. The scope's two input channels are served **3 : 2** across those
five. `[OPERATOR]` for the package and connection count; `[BENCH]` for the 3 : 2 split.

### 3.1 Port surface

The FPGA presents:

| port | width | direction | role |
|---|---|---|---|
| `lane` | 80 | in | ten cores × 8 bits, one parallel byte lane per core |
| `enc_p` / `enc_n` | 5 + 5 | out | differential encode clock, one pair per converter pair |
| static mode straps | — | out | converter mode pins, held at fixed levels (§3.3) |

### 3.2 Encode and rate

Five connected converter cores produce the full aggregate rate by **time interleaving**, each core
encoded at **~200 MHz**:

```
5 connected cores × 200 MSPS = 1 GS/s aggregate = 500 MS/s per input channel
```

Equivalently, one 10 ns frame carries ten byte slots (§7) — two per core.

**The cores are deliberately run above their rating.** The AD9288 is rated 100 MSPS; at ~200 MHz
encode it still converts, at a measurable cost in linearity — DC linearity residual is about
0.2 codes at 50–100 MHz encode and about 0.7 codes at 200 MHz. An implementation must not treat
the 100 MSPS figure as a ceiling it is observing, nor treat the extra residual as a fault.

Samples are **8-bit unsigned offset binary, centred at 128**. Code decreases as the applied offset
voltage increases.

> **Not established here: which frame slot belongs to which physical core.** §7 gives ten slots
> per frame and five connected cores serve them, but this document does not state the slot → core
> assignment, and the 3 : 2 channel split does not divide the five CH1 and five CH2 slots of a
> frame evenly. Per-core calibration therefore cannot be derived from this spec alone; use
> `MAP_ID` (§3.4) and the artifacts named in §13.

### 3.3 Mode straps

The converters have static mode pins that must be held at fixed levels for the parts to leave
standby. An implementation **must** drive them and **must not** repurpose them:

| group | level |
|---|---|
| `adc_ctl_hi[0..3]` | **1** |
| `adc_ctl_lo[0..2]` | **0** |

Three of the `adc_ctl_hi` pins are shared with the external-SRAM data group (§5). This is
consistent because that group is released outside the burst window and rests high on its
pull-ups, which is the level the straps require. **An implementation that drives the SRAM data
group outside a burst, or that drives those three pins low, puts the converters into standby.**

No pin the FPGA controls places the converters in standby by design; they drive the 80 lanes
continuously whenever they are out of standby.

### 3.4 Lane map

The mapping from the 80 physical lanes to (core, bit) is a **build-time constant** baked into the
image and identified at runtime by `MAP_ID` (§8.1). The qualified map is **`0xe192`**. A host that
reads a different `MAP_ID` must not assume this ordering.

Per-core gain, offset and AC aperture skew are **not** corrected by the fabric.

---

## 4. GPMC slave interface

The FPGA is a **slave on the ARM's GPMC bus**. It answers chip select **CS1**; the companion CPLD
answers **CS3**, which also carries the FPGA's configuration port (§1).

| plane | window | owner |
|---|---|---|
| CS1 | `0x01000000` | acquisition FPGA — all registers in §8 |
| CS2 | `0x02000000` | configured, unpopulated |
| CS3 | `0x03000000` | companion CPLD, and the FPGA configuration port at selector `0x07` |

### 4.1 Address decode

Registers are **16 bits**. The fabric decodes **GPMC `A1`–`A7`**, so:

* there are **128 selectors**, and selector `s` sits at **byte offset `2s`**;
* address bits above `A7` are **not decoded**, so the register space **mirrors at `+0x80`** —
  selector `s` and `s + 0x80` are the same register.

An implementation must mask host selectors to 7 bits rather than relying on the mirror.

### 4.2 Host access

The host reaches the bus through a single device node. The kernel driver hands the chip select to
**exactly one opener**, and that open happens at boot:

* a second `open()` returns **`EPERM`** while the boot holder's descriptor is live — and, worse,
  **may succeed** once that holder is gone, but then lacks the boot-time chip-select
  initialisation and the first reads **wedge the bus for seconds**. Never open it fresh
  (`02-register-map.md` §1.2);
* a process needing the bus must therefore **inherit** the descriptor and locate it by scanning
  `/proc/self/fd`;
* the driver frees the chip select on the **last** close, so the descriptor must never be closed
  by the last holder.

Accesses are ioctl transactions carrying a 6-byte record `{plane, 0, sel_lo, sel_hi, val_lo,
val_hi}`: request `0x80026700` reads, `0x40026701` writes. The `plane` byte is **1 for CS1** and
**3 for CS3**; no other value is legal. A read returns its 16-bit result **in the caller's own
buffer**, as `val_lo | val_hi << 8` — not as the ioctl return value.

Bus cycle timing (read/write cycle, access, OE and WE windows, cycle-to-cycle gap) is host GPMC
controller configuration, not an FPGA property. Burst drains over DMA require the CPU cache to be
invalidated for the destination region; without it a drained frame carries zero runs at cache-line
granularity.

### 4.3 Ownership

Everything in §8 assumes the caller holds the bus exclusively for the duration of a transaction
sequence. `01-system-architecture.md` defines the single-owner discipline the firmware uses;
`03-acquisition-engine.md` defines why a capture-halt is only coherent when no other access
overlaps it.

---

## 5. External SRAM

| property | value |
|---|---|
| Part | NETSOL S7A163630M, 100-TQFP, 3.3 V |
| Organisation | 512K × 36 synchronous **pipelined burst** (not NoBL/ZBT) |
| Address width | 19 bits |
| `tCYC` | ≥ 4.0 ns (250 MHz) |
| `tCD` | ≤ 2.6 ns |
| `tOH` | ≥ 1.5 ns |
| setup / hold, every synchronous input | 1.2 ns / 0.3 ns |

**Write commit.** A burst write begins **iff**, on one rising `CLK` edge:

```
ADSC = L  and  ADSP = H  and  CS1 = L, CS2 = H, CS3 = L  and  Write = L
```

where `Write` is `GW` low, or `GW` high with `BW` low and at least one of `WEa..WEd` low. `ADV` is
don't-care on that edge. **There is no path to an externally supplied write address that does not
go through `ADSC`.**

The capture image uses 32 of the 36 data bits. The four parity/DQP bits are not driven.

**The port runs at the part's limit.** `tCYC ≥ 4.0 ns` is exactly 250 MHz, which is the SRAM port
clock of §2. There is **no frequency margin**: an image that fails 250 MHz timing closure will not
meet `tCYC`, and the 1.2 ns setup / 0.3 ns hold on every synchronous input must be met by
constraint, not by inspection.

---

## 6. Record geometry

| quantity | value |
|---|---|
| Record | **524,288 × 32-bit words** (`1 << 19`) |
| Bytes | 2,097,152 |
| Samples per channel | 1,048,576 |
| Sample rate, per channel | 500 MS/s |
| Aggregate sample rate | 1 GS/s |
| Full-depth span, per channel | 2.097152 ms |
| On-chip memory for host readout | 512 × 32 bits |
| FIFO + readout buffer | 18,944 logical memory bits |

The readout buffer is **512 words**, which is exactly one readout pass including its warm-up
prefix (§10.2). The three datapath stages are rate-matched at **8.0 Gbit/s**: 80 bits × 100 MHz
= 64 bits × 125 MHz = 32 bits × 250 MHz.

Each 32-bit SRAM word holds **two consecutive sample pairs**, in byte order:

```
CH1[n], CH2[n], CH1[n+1], CH2[n+1]
```

Samples are 8-bit unsigned offset binary, centred at 128.

---

## 7. Interleave and core ordering

Five differential encode pairs clock the five connected converter cores (§3.0 gives the measured
pin/core/phase map; §3.2 the rate). One 10 ns frame carries ten byte slots, ordered:

```
E4.CH1, E5.CH2, E3.CH1, E1.CH2, E2.CH1, E4.CH2, E5.CH1, E3.CH2, E1.CH1, E2.CH2
```

CH2 has a nominal **+1 ns** offset from CH1.

Capture may begin at any CH1/CH2 pair within the ten-byte frame. Chronological order is preserved
across that rotation, but per-core calibration must account for it. AC aperture skew and per-core
gain/offset are **not** calibrated by the fabric.

The active lane map is identified by `MAP_ID` (read 14). The qualified map is **`0xe192`**.

---

## 8. Register ABI — CS1

All accesses are on the CS1 plane. Selector `s` sits at byte offset `2s`.

### 8.1 Reads

| sel | width | name | meaning |
|---|---|---|---|
| 0 | 16 | `FABRIC_ID` | image identity. This image answers **`0x5a52`**. A host **must** read this first and refuse to drive anything else. |
| 1 | 16 | `STATUS` | see §8.3 |
| 2 | 32 | `LENGTH` | words **actually captured** — see §10.1. Not what the host programmed. |
| 4 | 32 | `START` | the captured record's first word, as a logical word index |
| 6 | 32 | `TRIGGER_INDEX` | **word** index of the triggering word within the record (not interpolated, not a sample half) |
| 8 | 32 | `POSITION` | the bus engine's **next physical transaction address**, including read-pipeline flush clocks. It is **not** a count of delivered words and **not** the next deliverable word — see §10.2. |
| 10 | 32 | `ORIGIN` | the physical address of logical word 0; maps `START`-relative indices onto the SRAM |
| 12 | 16 | `FILL` | words actually present in the readout buffer after a fetch |
| 13 | 16 | `REVISION` | image revision; gates behaviour, see §12 |
| 14 | 16 | `MAP_ID` | lane map identity; qualified value `0xe192` |
| 15 | 16 | `RATE` | per-channel MS/s. `500` in interleave mode. Present from revision 6; a host that reads any other value **must** refuse the image. |
| 17 | 32 | `DATA` | the readout buffer window selected by `BUFFER_INDEX` (§8.2) |
| 19 | 16 | `FLAGS` | `b0` sticky FIFO data fault, `b1` ADC PLL lock. The fault is **self-halting** — it stops the acquisition when it occurs — and is **cleared only by `ARM`**. There is no write to this selector. |
| 20–24 | 5 × 16 | `SNAPSHOT[0..4]` | the ten-byte atomic core snapshot latched by `SNAPSHOT` (§9.2); selector `20+i` carries bytes `2i` (low half) and `2i+1` (high half) |

A 32-bit read occupies the named selector and the next one; the named selector carries the **low**
half.

### 8.2 Writes

| sel | width | name | meaning |
|---|---|---|---|
| 1 | 16 | `COMMAND` | opcode, see §8.4 |
| 2 | 32 | `PRE_WORDS` | pre-trigger words, **excluding** the triggering word |
| 4 | 32 | `POST_WORDS` | post-trigger words, **including** the triggering word |
| 6 | 16 | `CONFIG` | see §8.5 |
| 7 | 16 | `TRIGGER_LEVEL` | 8-bit level, compared against the raw sample code |
| 8 | 32 | `SKIP` / `FETCH_LEN` | **dual purpose, by the command that follows it.** Before `ADVANCE` it is a **relative** word count to advance the read position. Before `FETCH` or `FETCH_CONTINUE` it is the **number of words to fetch** into the readout buffer. |
| 16 | 16 | `BUFFER_INDEX` | word index **within the readout buffer**, `0 .. FILL-1`; selects what `DATA` (§8.1) returns |

### 8.3 `STATUS` (read 1)

| bit | mask | meaning |
|---|---|---|
| 2 | `0x0004` | `READY` |
| 3 | `0x0008` | `RUNNING` |
| 4 | `0x0010` | `FROZEN` |
| 5 | `0x0020` | `TRIGGERED` |
| 6 | `0x0040` | PLL `LOCKED` |
| 7–8 | `0x0180` | command rejected |
| 9 | `0x0200` | command acknowledge — **toggles** on completion |
| 10 | `0x0400` | `PREFETCHED` |
| 11 | `0x0800` | atomic snapshot pending |

Metadata counters are stable only while `READY` **and** `FROZEN` are set.

### 8.4 Commands (write 1)

| op | name | effect |
|---|---|---|
| 1 | `ARM` | begin acquisition with the currently written configuration |
| 2 | `FETCH` | fill the readout buffer with `FETCH_LEN` words starting at the current read position |
| 3 | `ADVANCE` | advance the read position by `SKIP` words. `SKIP` is **relative** and reduces **modulo 524,288** — the whole SRAM, not modulo `LENGTH`. |
| 4 | `FORCE` | force a trigger |
| 5 | `HALT` | stop the current acquisition |
| 6 | `SNAPSHOT` | latch a ten-byte atomic core snapshot |
| 7 | `FETCH_CONTINUE` | as `FETCH`, but continues the previous burst without re-addressing; legal **only** under the conditions in §10.2 |

**Completion protocol.** A command is not acknowledged by a level. The host **must**:

1. read `STATUS` and keep bit 9;
2. write the opcode;
3. poll `STATUS` until bit 9 **differs** from the value kept in step 1;
4. on that transition, check bits 7–8 — if either is set the command was **rejected**.

Bit 9 toggles on every completion, so a host that tests for a fixed value will hang. A command
that does not complete within **1 s** must be treated as failed.

Separately, operations that leave the engine busy clear `READY`. After `ADVANCE`, `FETCH` and
`FETCH_CONTINUE` the host **must** wait for `READY` (bit 2) to be set again before issuing the
next step; allow **3 s**. A poll interval of 100 µs is sufficient.

### 8.5 `CONFIG` (write 6)

| bit | mask | meaning |
|---|---|---|
| 0 | `0x0001` | source: 1 = ADC, 0 = internal counter |
| 1–3 | `0x000E` | pair select; **must be 0** in interleave mode (all five pairs are used) |
| 4 | `0x0010` | normal trigger (else auto) |
| 5 | `0x0020` | falling edge **in code space** — see the warning below |
| 6 | `0x0040` | trigger channel: 1 = CH2 |

**Polarity is in code space, not volts.** The comparator tests the raw 8-bit sample against
`TRIGGER_LEVEL`. Because code *decreases* as the applied offset voltage increases (§3.2), a
**falling-code** edge is a **rising-voltage** edge. A host presenting a voltage-domain control to
a user must invert this bit.

The comparator examines **both sample halves of every word**, plus the boundary against the
preceding word, so no crossing is missed. `TRIGGER_INDEX` remains **word-granular**: it names the
word, never which half of it.

---

## 9. Capture lifecycle

### 9.1 Acquire

```
precondition: READY set and RUNNING clear
              (otherwise ARM is refused — halt the current acquisition first)

write PRE_WORDS (2), POST_WORDS (4), CONFIG (6), TRIGGER_LEVEL (7)
COMMAND = ARM (1)
        → RUNNING set

   wait for the trigger, or COMMAND = FORCE (4)

poll STATUS until READY *and* FROZEN are BOTH set   (mask 0x0014)
        → the record is frozen and its metadata is stable

read LENGTH (2) / START (4) / TRIGGER_INDEX (6) / ORIGIN (10)
readout (§10) — may be repeated any number of times

COMMAND = HALT (5), then wait for READY, before re-arming
```

`TRIGGERED` (bit 5) reports that a trigger was seen; it is **not** the readiness condition.
A host must gate on `READY ∧ FROZEN`, because metadata is stable only when both are set.

A frozen record survives repeated readout. A failed or partial host-side write of the data leaves
the record intact for another attempt.

### 9.2 Snapshot

`SNAPSHOT` (command 6) latches ten converter bytes atomically, for diagnostics and calibration:

1. issue `SNAPSHOT` (6);
2. poll `STATUS` until bit 11 (snapshot pending) **clears**;
3. read selectors 20–24; selector `20+i` yields byte `2i` in its low half and byte `2i+1` in its
   high half.

Snapshot is an idle-time facility. It does not disturb a frozen record.

## 10. Readout

Readout is **windowed**: a host asks for `count` words starting at word `offset` within the frozen
record. It does not have to read the whole record, and it may read any window repeatedly.

### 10.1 Preconditions and addressing

A host **must** refuse to read out unless all hold:

* `FABRIC_ID` (read 0) is `0x5a52`;
* `READY` and `FROZEN` are set and `RUNNING` is clear;
* `FLAGS` bit 0 (sticky FIFO data fault) is **clear** — if set the record has missing samples and
  must be discarded, not repaired;
* `offset + count ≤ LENGTH`, and `LENGTH ≤ 524,288`.

**`LENGTH` is what was captured, not what was asked for.** It is the post-trigger count when a
trigger was accepted, the filled count when the acquisition was halted without one, and **0** when
neither has happened. A host must read it rather than derive it from its own `PRE_WORDS` /
`POST_WORDS`. `TRIGGER_INDEX` is likewise the pre-trigger count actually achieved.

A request of **zero length is an empty window, not a full-depth one**: to read the whole record
pass `count = LENGTH` (up to 524,288), never 0. The fabric rejects a request unless
`frozen ∧ LENGTH ≤ 524,288 ∧ offset + count ≤ LENGTH`.

All SRAM address arithmetic is **modulo 524,288** (mask `0x7FFFF`). The physical address of
record word `k` is:

```
addr(k) = (ORIGIN + START + k) mod 524288
```

### 10.2 The transfer loop

The fabric has a **512-word readout buffer**. Each pass fills it, then the host indexes words out
of it one at a time.

From revision 7 each fetch is preceded by a **discarded warm-up prefix** of **16 words**: a
burst-start defect corrupts the leading words of a fresh burst, and the prefix absorbs it. Earlier
revisions use a prefix of **0**. Let `prefix` be that value; the usable payload per pass is
therefore `512 − prefix` words (**496** from revision 7). Prefix reads wrap physically and add
nothing to the returned window.

For each pass, with `copied` words already returned:

1. `n = min(count − copied, 512 − prefix)`
2. `target = (ORIGIN + START + offset + copied − prefix) mod 524288`
3. read `POSITION` (read 8) and `STATUS` (read 1)
4. **choose the opcode.** If `prefix == 0` **and** `PREFETCHED` (status bit 10) is set **and**
   `target == (POSITION − 1) mod 524288`, the previous burst can be continued: use
   `FETCH_CONTINUE` (7) and skip step 5. Otherwise use `FETCH` (2).
5. `skip = (target − POSITION) mod 524288`. If `skip ≠ 0`: write `skip` to selector 8, issue
   `ADVANCE` (3), wait for `READY`.

> **Why steps 4 and 5 compare against different values.** `POSITION` is the engine's next
> *physical transaction* address, which runs ahead of the next *deliverable* word by the read
> pipeline. A fresh `FETCH` seeks so that the next transaction lands on `target`, hence
> `skip = target − POSITION`. `FETCH_CONTINUE` instead resumes a burst that has **already
> prefetched** one word, and that word sits one address behind the transaction cursor, hence
> `target = POSITION − 1`. The two conditions describe the same cursor at two different stages,
> not two cursors. Both reduce modulo 524,288.
6. write `n + prefix` to selector 8, issue the opcode from step 4, wait for `READY`.
7. read `FILL` (read 12). It **must** equal `n + prefix`; a smaller value is a short read and the
   transfer has failed — do not pad it.
8. for `i` in `0 .. n−1`: write `i + prefix` to `BUFFER_INDEX` (write 16), then read `DATA`
   (read32 17). **Indices below `prefix` are never emitted.**
9. emit those `n` words **little-endian**; `copied += n`

### 10.3 Chunk arithmetic

A full-depth read is **not** a whole number of passes. At revision 7:

```
524288 = 1057 × 496 + 16
```

so the final pass carries **16 payload words**, not 496. An implementation must take `n` from
step 1 and must not assume a constant payload.

### 10.4 Sample order

Each returned 32-bit word contains two sample pairs in the byte order of §6. For returned byte
array `b`, `b[2i]` is CH1 and `b[2i+1]` is CH2, both in chronological order across the whole
window.

## 11. Constraints

* **Single owner.** Everything in §8 assumes the caller has exclusive ownership of the bus for the
  duration; `01-system-architecture.md` defines the discipline. This image does not open, close or
  load anything itself and never touches CS3.
* **Volatile only.** Loading this image writes no flash. A power cycle restores the factory
  configuration.
* **Timing.** A build that fails 250 MHz timing must not be loaded.
* **Pair selection** must be zero in interleave mode.
* **Stacking** is not implemented in this image.

---

## 12. Revision gating

`REVISION` (read 13) gates behaviour a host must branch on:

| revision | behaviour |
|---|---|
| < 6 | no `RATE` register; sample rate is the 100 MHz non-interleaved rate; pair select is meaningful |
| ≥ 6 | interleaved: `RATE` (read 15) reads `500`, `FLAGS` (read 19) is present, and `CONFIG` pair select **must be 0** |
| ≥ 7 | readout requires the 16-word warm-up prefix (§10.2) |

A host that does not recognise `REVISION` must refuse the image rather than assume the newest
behaviour.

---

## 13. Scope — what this document is sufficient to build

**Sufficient.** A host-side driver for `fpga/acq_sram`: identify the image, arm a capture, wait for
the freeze, read out any window, and demux to per-channel chronological byte arrays. Every
register, opcode, status bit, handshake, timeout, modulus and byte order that path needs is in
§4 and §8–§12.

**Not sufficient on its own, by design.** Two classes of information are deliberately not restated
here, because prose is the wrong carrier and a stale copy would be worse than none:

| what | where it actually lives |
|---|---|
| Device pin assignments — the 80 lanes, `enc_p`/`enc_n`, the strap pins, the SRAM group, the GPMC interface | `fpga/default/default.qsf`. This is **authoritative**: `fpga/acq_sram/build.py` derives its own assignments from that file rather than duplicating them. |
| The 80-entry lane map — which physical lane carries which (core, bit) | `fpga/default/lanemap_seed.vh`, a generated file. §3.4 gives the identity (`MAP_ID`) a host checks; this file gives the contents. |

An implementer building an **FPGA image** needs both of the above in addition to this spec. An
implementer writing a **host driver** needs neither.

**Not specified anywhere yet.** These are real gaps in the documentation set, not omissions with a
pointer:

* **The passive-serial configuration protocol.** §1 gives the port and its bit map, but not the
  reset-pulse timing, the inter-signal ordering, the bit order within a configuration byte, the
  init-clock count, or the container format. An implementer must read the loader
  (`app/internal/fpgaload`) — and note that `01-system-architecture.md` constrains *when* it may
  run, because the descriptor that reaches the port is inherited at boot.
* **The SRAM read side.** §5 specifies the write-commit predicate because that is what the capture
  path needs. It does not give a read-commit predicate, `OE` timing, or the burst length, all of
  which the part's datasheet carries and an image that reads the SRAM directly would require.
* **Host GPMC controller timing.** §4.2 names the six parameters and states that they are host
  configuration; it does not give values. A fresh bus open without the boot-time initialisation
  wedges reads (`02-register-map.md` §1.2).

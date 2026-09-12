# 12 — Acquisition FPGA and External SRAM Capture

The acquisition FPGA is an Altera **Cyclone IV E EP4CE10F17C8**. It owns the ADC front end, the
external SRAM, and the GPMC slave the ARM drives. Its configuration is **volatile**: the CRAM is
loaded at every boot over the GPMC CS3 configuration port and nothing about it survives a power
cycle.

This spec defines the device's fixed properties, the external SRAM interface, and the register ABI
of the **full-depth capture image** (`fpga/acq_sram`), which records a complete external-SRAM
frame, freezes it on trigger, and serves it to the host for repeated recall.

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
| ADC phase clocks | 100 MHz × 5 phases | converter encode, five phases |
| packing clock | 125 MHz | 64-bit assembly stage |
| SRAM port clock | 250 MHz | external SRAM write |

The capture image takes the 80-bit / 100 MHz converter stream, buffers it in a shallow FIFO,
assembles 64-bit words at 125 MHz, and writes **32 bits at 250 MHz** to the external SRAM.

---

## 3. External SRAM

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

---

## 4. Record geometry

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

Each 32-bit SRAM word holds **two consecutive sample pairs**, in byte order:

```
CH1[n], CH2[n], CH1[n+1], CH2[n+1]
```

Samples are 8-bit unsigned offset binary, centred at 128.

---

## 5. Interleave and core ordering

Five encode pairs drive ten converter cores. The ten byte streams are ordered:

```
E4.CH1, E5.CH2, E3.CH1, E1.CH2, E2.CH1, E4.CH2, E5.CH1, E3.CH2, E1.CH1, E2.CH2
```

CH2 has a nominal **+1 ns** offset from CH1.

Capture may begin at any CH1/CH2 pair within the ten-byte frame. Chronological order is preserved
across that rotation, but per-core calibration must account for it. AC aperture skew and per-core
gain/offset are **not** calibrated by the fabric.

The active lane map is identified by `MAP_ID` (read 14). The qualified map is **`0xe192`**.

---

## 6. Register ABI — CS1

All accesses are on the CS1 plane. Selector `s` sits at byte offset `2s`.

### 6.1 Reads

| sel | width | name | meaning |
|---|---|---|---|
| 1 | 16 | `STATUS` | see §6.3 |
| 2 | 32 | `LENGTH` | record length, words |
| 4 | 32 | `START` | record start address, words |
| 6 | 32 | `TRIGGER_INDEX` | **word** index of the triggering word (not interpolated) |
| 8 | 32 | `POSITION` | current readout address, words |
| 10 | 32 | `ORIGIN` | record origin, words |
| 13 | 16 | `REVISION` | image revision |
| 14 | 16 | `MAP_ID` | lane map identity; qualified value `0xe192` |
| 15 | 16 | `RATE` | per-channel MS/s; `500` in interleave mode (revision ≥ 6) |
| 19 | 16 | `FLAGS` | `b0` sticky FIFO data fault, `b1` ADC PLL lock |

32-bit reads occupy the named selector and the next one.

### 6.2 Writes

| sel | width | name | meaning |
|---|---|---|---|
| 1 | 16 | `COMMAND` | opcode, see §6.4 |
| 2 | 32 | `PRE_WORDS` | pre-trigger words, **excluding** the triggering word |
| 4 | 32 | `POST_WORDS` | post-trigger words, **including** the triggering word |
| 6 | 16 | `CONFIG` | see §6.5 |
| 7 | 16 | `TRIGGER_LEVEL` | 8-bit level |
| 8 | 32 | `READ_ADDRESS` | readout address, words |
| 15 | 16 | `ENCODE_MASK` | ten-bit encode-enable diagnostic mask, **idle only**; bit `2p` is pair `p`'s positive leg, bit `2p+1` the negative leg |
| 16 | 16 | `CHUNK` | readout chunk index |

### 6.3 `STATUS` (read 1)

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

### 6.4 Commands (write 1)

| op | name | effect |
|---|---|---|
| 1 | `ARM` | begin acquisition with the currently written configuration |
| 3 | `ADVANCE` | advance the readout window by one chunk |
| 4 | `FORCE` | force a trigger |
| 5 | `HALT` | stop the current acquisition |
| 6 | `SNAPSHOT` | latch a ten-byte atomic core snapshot |

A command completes when `STATUS` bit 9 **toggles** relative to its value before the write. If
bits 7–8 are set at that point the command was rejected. The host must not re-arm while `RUNNING`
is set or `READY` is clear.

### 6.5 `CONFIG` (write 6)

| bit | mask | meaning |
|---|---|---|
| 0 | `0x0001` | source: 1 = ADC, 0 = internal counter |
| 1–3 | `0x000E` | pair select; **must be 0** in interleave mode (all five pairs are used) |
| 4 | `0x0010` | normal trigger (else auto) |
| 5 | `0x0020` | falling edge |
| 6 | `0x0040` | trigger channel: 1 = CH2 |

---

## 7. Capture lifecycle

```
write PRE_WORDS, POST_WORDS, CONFIG, TRIGGER_LEVEL
COMMAND = ARM (1)                     → RUNNING set
   ... trigger, or COMMAND = FORCE (4)
                                      → TRIGGERED, then FROZEN
read LENGTH / START / TRIGGER_INDEX / ORIGIN   (stable while READY and FROZEN)
readout (§8)
COMMAND = HALT (5) before re-arming
```

A frozen record may be read out **repeatedly** without re-acquiring.

---

## 8. Readout

Readout proceeds in chunks. For each chunk the host writes `READ_ADDRESS`, then reads a
**discarded 16-word prefix** followed by the **496-word payload**. The prefix wraps physically; it
changes neither the record nor the usable capacity.

The prefix is mandatory: a burst-start read defect corrupts the first words of a chunk without it.

Backends must reject records whose metadata is inconsistent, and must treat `FLAGS` bit 0 (sticky
FIFO data fault) as invalidating the record.

---

## 9. Constraints

* **Single owner.** Everything in §6 assumes the caller has exclusive ownership of the bus for the
  duration; `01-system-architecture.md` defines the discipline. This image does not open, close or
  load anything itself and never touches CS3.
* **Volatile only.** Loading this image writes no flash. A power cycle restores the factory
  configuration.
* **Timing.** A build that fails 250 MHz timing must not be loaded.
* **Pair selection** must be zero in interleave mode.
* **Stacking** is not implemented in this image.

# EP4CE10F17C8 ball → function map (SDS1102CML+ Cyclone)

Consolidated from the owned design QSF + bitstream IO-direction decode + JTAG boundary-scan.
125 functional nets. Physical balls are fixed by the board, so this is the real pinout; the
ADC-lane *index→ball* assignment is the owned de-interleave (CH1 proven, CH2 partial).

Provenance: **[bench]** HW-verified · **[decode]** RE decode-proven from the vendor bitstream/device file ·
**[bscan]** JTAG boundary-scan verified · **[crack]** cracked from boundary-scan of the running factory.

## GPMC bus — ARM ↔ Cyclone (CS1 acq plane / CS3 config plane)  [bench]
| Net | Balls |
|---|---|
| gpmc_d[0..15] | A10 B9 A9 B8 A8 B7 A7 B6 · A6 D6 C6 B5 A5 F6 D5 E5 |
| nCS1 | B4 |
| nOE | E6 |
| nWE | B10 |
| gpmc_wait | L6 |
| sel[0..6] (register selector A1..A7) | M2 D1 C3 D4 A3 B3 A4 |

## Clock + trigger + config
| Net | Ball | Note |
|---|---|---|
| clk (reference) | C2 | ~80 MHz reference; source of ENCODE + all fabric clocks [bench] |
| trig_sense | A12 | analog comparator input (trigger) [bench] |
| (config port) | DCLK/DATA0/nCONFIG/CONF_DONE/nSTATUS | dedicated passive-serial port on `/dev/spidev1.1` — how the app loads the fabric (not user I/O) |

## ADC — 3× AD9288-class dual 8-bit  [crack]
The physical ADC is **40 data lanes (5 cores × 8 bit)**. Decode cleanly binds only 33; the remaining
~7 sit in a 43-ball `ADC_or_FRONTPANEL` ambiguous pool that **overlaps the SRAM DQ** (PATH A shared bus
— the wide ADC data lanes ARE the SRAM DQ read lanes, so a ball decodes as input either way and cannot
be split by bitstream alone). The QSF (`adc_lane[0..32]`) wired only the 33 it needed.

| Group | Count | Balls |
|---|---|---|
| data_lane (clean decode) | 33 | A11 A13 B12 C8 C9 C11 D8 D12 E8 E11 F8 F9 G15 G16 K15 K16 L12 L13 L14 L16 M10 M12 N14 N15 P9 P11 P15 R10 R11 R12 T9 T11 T13 |
| encode_out (common ENCODE clk, ~10 MHz = clk/8) | 8 | K8 K9 K10 L8 L9 L10 M7 M8 |
| static_ctrl (held HIGH F1 L4 T2 T7 / LOW G1 G2 K1) | 7 | F1 L4 T2 T7 · G1 G2 K1 |
| sample_clock_diff | 2 | C14 D14 (D14 doubles as SRAM read clock) |
| ADC_or_FRONTPANEL (ambiguous; holds the remaining ~7 data lanes; overlaps SRAM DQ) | 43 | A14 A15 B11 B13 B14 B16 C15 C16 D9 D11 D15 D16 E9 E10 F10 F11 F13 F14 F15 G11 J11 J16 K11 K12 L11 L15 M9 M11 N9 N11 N12 N13 N16 P14 P16 R9 R13 R14 R16 T10 T12 T14 T15 |
| UNRESOLVED | 4 | A2 B1 D1 J1 |

Census totals (`maxv_pins.json`, 162 balls): GPMC_ARM 26 · ADC 50 · ADC_or_FRONTPANEL 43 · SRAM 32 ·
MAXV 7 · UNRESOLVED 4 (+D2). De-interleave: **CH1 bit7..0 = adc_lane[3,0,1,11,9,12,6,5]** [proven];
CH2 = adc_lane[18,23,28,24,30,27,20,22] [partial]. Full 40-lane bind + ADC/DQ separation = bench probe of the shared bus.

## External SRAM S7A163630M — Cyclone-driven side  [decode + bscan]
| Net | Balls | Role |
|---|---|---|
| sram_a[0..17] (18 ADDRESS) | L1 N2 P1 P2 R1 · J6 K5 · L3 N3 N6 P3 R7 · R3 R4 R5 T3 T4 T6 | A0..A17 (A18 = off-FPGA strap) |
| sram_c[0..5] (6 CONTROL, idle-HIGH) | **L2=CS1#, N1=CS2#**, **M6/N5/R6/T5 = WEa#/WEb#/WEc#/WEd#** | chip-select + byte writes |
| sram_k[0..2] (3 write CLOCK) | F2 J2 K2 | write sample clock |
| sck_rd (read clock) | **D14** | the ONLY net driven during the non-contending drain read [bench, `sramdump`] |
| sram_dq[0..17] (DQ read lanes) | A14 A15 B13 B14 B16 C15 C16 D9 D11 D15 D16 F15 J16 L7 P8 R8 R16 T8 | + shared with ADC: A13 B12 G15 G16 (PATH A shared bus) |

DQ is a **shared bus** (PATH A): ADC drives it on capture, Cyclone reads it on drain. True DQ width
∈ [5,36] is **unresolved** (the wide lanes decode bit-identical to ADC input lanes).

## MAX V levers (5M240ZT100C4N — fixed CPLD, JTAG unreachable)
| Net | Ball | Role |
|---|---|---|
| d2 = nCSO | **D2** | Cyclone→MAX-V **mode lever**, STATIC-LOW in operation [bench SAMPLE] |
| p6 | **P6** | MAX-V→Cyclone status return [bench] |

The MAX-V (not the Cyclone) drives the SRAM command strobes **ADV# / ADSP# / ADSC# / OE# / BW# / GW#**
(part pins 83-88) — off-FPGA, protocol **not decodable remotely** (TAP unreachable, too fast for SAMPLE).

## Notes
- The **27 SRAM addr/ctrl/clk balls** + **D14** are decode-proven Cyclone outputs and JTAG-SAMPLE-active
  during a live acquisition. The **6 control balls** are idle-HIGH active-low.
- The board is fixed, so the **vendor** fabric uses these same physical balls; only the ADC-lane index
  mapping above is our owned de-interleave (CH1 proven).
- ~40 remaining package balls (of ~166 user I/O on F17) are power/ground/unused/dedicated-config.

# SDS1102CML+ acquisition datapath + SRAM spec (consolidated)

Everything established so far about the external SRAM, the MAX‑V, the ADC, and the full
ADC→SRAM→GPMC capture/drain path — proven vs inferred vs unknown. Target: EP4CE10F17C8
Cyclone IV E, factory bitstream `sds1000_fpga.rbf` (368011 B, Quartus 21.1 Build 842).

Confidence tags: **[PROVEN]** bit‑exact from the vendor rbf/device file or HW‑verified;
**[HW]** confirmed live on the bench; **[INFER]** reasoned/labelled; **[REFUSED]** needs a
bench logic‑analyzer or a directed RE we could not do remotely.

---

## 1. The chips and who owns what

| Part | Role | Access |
|---|---|---|
| **Cyclone IV E EP4CE10F17C8** | main acquisition FPGA: clocks ADC, drives SRAM addr/ctrl/clk, reads DQ, GPMC register slave to the ARM | BGA (pins hidden); JTAG SAMPLE/EXTEST via Atmel‑ICE; volatile config over passive‑serial `/dev/spidev1.1` |
| **MAX V 5M240ZT100C4N** | fixed CPLD that **sequences the SRAM command strobes**; a slave the Cyclone commands via D2 | TQFP (pins visible); **JTAG TAP unreachable** — black box |
| **SRAM NETSOL S7A163630M‑PC25** | 512K×36 synchronous pipelined‑burst (SPB), K7A163630B‑compatible, 100‑TQFP | pins visible on TQFP |
| **ADC: 3× AD9288‑class dual 8‑bit** | analog→digital; **40 raw data lanes = 5 cores × 8 bit** (CH1 = 3 cores, CH2 = 2 cores). NOT free‑running — Cyclone drives the ENCODE clock | — |
| **ARM TI AM3352 (Cortex‑A8, Linux 3.2)** | runs the app; drives the Cyclone over the GPMC parallel bus (CS1 acq plane, CS3 config plane) | our otactl / gpmc_probe |

---

## 2. The full path (signal flow)

```
                    ENCODE clk (Cyclone→ADC, K8 K9 K10 L8 L9 L10 M7 M8, ~10 MHz)
   ┌────────┐                                    ┌───────────────┐   GPMC CS1/CS3 (ARM↔Cyclone)
   │  ARM   │                                    │   CYCLONE      │   0x21 arm · 0x35 run · 0x19/1a/1b div
   │ AM3352 │◄──────────── GPMC ────────────────►│   EP4CE10      │   0x39/0x46 status · 0x30‑34 drain
   └────────┘                                    │  (engine)      │   CS3: 0x14/34 trig‑DAC · 0x10/30 off‑DAC
                                                 └───┬────────┬───┘
                          addr A0..A17 (18, ARITH)   │        │  CLK F2/J2/K2 · CS# L2/N1 · WEa‑d# M6/N5/R6/T5
                          + A18 strap (off‑FPGA)     │        │  D14 read clk · D2=nCSO(→MAXV) · P6(←MAXV)
                                                     ▼        ▼  reads DQ
   ┌────────┐  ~33 data lanes  ╔═══════ SHARED WIDE DATA BUS  (== SRAM DQ ×36) ═══════╗
   │  ADC   │═════════════════►║ CAPTURE: ADC drives → SRAM   ·   DRAIN: SRAM drives → Cyclone ║
   │ 3×9288 │                  ╚═══════════════════════════╤══════════════════════════╝
   └────────┘                                              │
                                   ┌──────────────────────▼──────────────────┐   ┌─────────┐
                                   │  SRAM S7A163630M 512K×36 SPB             │◄──│  MAX V  │
                                   │  A0..A18 · CLK · CS1#/CS2# · WEa‑d#      │   │ 5M240ZT │
                                   └──────────────────────────────────────────┘   └────┬────┘
                                       cmd pins 83‑88 = ADV#/ADSP#/ADSC#/OE#/BW#/GW# ◄──┘
```

**Three phases:**
1. **CAPTURE (write):** Cyclone clocks the ADC + drives the SRAM address counter + CS/WE + CLK +
   holds D2 low. The **ADC drives the shared DQ bus** (ADC is the write‑data master — the Cyclone
   never drives DQ on write). The MAX V issues the SRAM write strobe (ADSC#/write). One sample lands
   per address as the counter sweeps. **[INFER + PATH A decode]**
2. **DRAIN (read):** proven non‑contending pattern — Cyclone drives **only D14** (the read clock),
   **tri‑states all 27 addr/ctrl/clk balls** so the MAX V holds/advances the read address, and the
   SRAM drives the DQ bus → Cyclone latches it each D14 edge → streams to GPMC deep‑frame ports.
   **[HW — `sramdump` "non‑contending SRAM read works"]**
3. **RE‑ARM:** frame‑tail strobes, back to capture.

**Data format:** 8‑bit unsigned codes, packed 16‑bit word **hi = CH1, lo = CH2**. Deep record
= **20480 samples** (`WAVE_ARRAY_COUNT`). **[PROVEN]**

---

## 3. SRAM interface — the exact Cyclone ball map

RE decode‑proven (`re_workflows/out/bringup/merged_sram_roles.json`): **18 ADDRESS + 6 CONTROL +
3 CLOCK = 27 FPGA→SRAM output balls**, all bidir/fabric‑OE outputs, 0 direction conflicts. **[PROVEN]**

| Group | Count | Balls |
|---|---|---|
| ADDRESS A0..A17 | 18 | L1 N2 P1 P2 R1 · J6 K5 · L3 N3 N6 P3 R7 · R3 R4 R5 T3 T4 T6 |
| CONTROL | 6 | **L2 N1 = CS1#/CS2#**, **M6 N5 R6 T5 = WEa#‑WEd#** (all idle‑HIGH, active‑low) |
| CLOCK | 3 | F2 J2 K2 (write sample clock) |
| READ CLOCK | 1 | **D14** — the one Cyclone‑controllable SRAM clock; the ONLY net driven during the drain read |
| DQ (data) | 5 bidir + wide | 5 Cyclone‑bidir lanes **F3 F5 G5 D3 F7**; the rest of the ×36 bus decodes as pure **input**, **bit‑identical to the ADC input lanes** (shared bus) → true width ∈ [5,36] **[REFUSED]** |
| A18 (19th addr) | 1 | off‑FPGA strap, not driven by the Cyclone |
| D2 = nCSO | 1 | Cyclone→MAX‑V **mode lever**, **STATIC‑LOW** during operation **[HW SAMPLE]** |
| P6 | 1 | MAX‑V→Cyclone status return **[HW]** |

**MAX V owns** SRAM pins 83‑88 = **ADV# / ADSP# / ADSC# / OE# / BW# / GW#** — it is a split command
sequencer with no address and no DQ (not a data master). It sequences the SRAM autonomously off the
Cyclone CLK/CS/WE + D2. **[INFER + board photos]** Its exact command protocol is **[REFUSED]** (TAP
unreachable, its FSM un‑decodable, transactions too fast for JTAG SAMPLE).

---

## 4. GPMC register protocol (factory, HW‑verified) — how the ARM drives capture+drain

CS1 = acquisition plane, CS3 = config/analog plane. 16‑bit words at `selector<<1`.

| Sel | Plane | Name / value | Meaning |
|---|---|---|---|
| 0x12 | CS1 R | VERSION = **0x0052** | addressing self‑check |
| 0x19 / 0x1a / 0x1b | CS1 W | CLASS / DIV_LO / DIV_HI | timebase divisor (**0x20 = native‑fast**; hi cleared FIRST) |
| 0x21 | CS1 W | ARM: **0xC0** reset‑head, **0xC3** go, **0xC8** halt | acquisition FSM opcode |
| 0x35 | CS1 W | RUN_WORD: **0x0001 AUTO**, **0x0003 NORM** | fill‑FSM enable + mode |
| 0x44 / 0x57 | CS1 W | RESET_HEAD / WR_PTR (1,0 pulse) | head + write‑pointer reset in arm |
| 0x39 | CS1 R | STATUS: **bit0 VALID, bit1 TRIG, bit2 DONE** | capture status |
| 0x46 | CS1 R | **FILL** (11‑bit, mask 0x07ff) | gate: halt when **≥ 0x200** |
| 0x3a / 0x3b | CS1 R | TRIGPOS lo/hi | interpolated trigger position |
| **0x30–0x34** | CS1 R | **SAMPLE PORTS** (round‑robin) | each read = 1 sample (hi=CH1/lo=CH2), auto‑increments the read ptr |
| 0x3c/0x3d/0x3e/0x58 + 0x16 | CS1 W | frame TAIL (2/8/0/0) + RETRIG=1 | frame completion / re‑arm |
| 0x2c | CS1 W | FORCE (0→1) | AUTO force‑trigger (exists; engine prefers saturated‑fill) |
| 0x07 | CS3 R | CONF_DONE (bit7) | config status — **NEVER write** |
| 0x14/0x34, 0x15/0x35 | CS3 W | trigger‑level DAC A/B | comparator threshold (3‑wire serial off Cyclone / on MAX‑V) |
| 0x10/0x30, 0x11/0x31 | CS3 W | offset DAC C1/C2 | vertical position |

**Per‑frame sequence (HW‑verified `engine_capture.go`):**
1. bring‑up: `0x44`=1,0 · `0x35`=runword · `0x36`=0 · `0x1b`=0 · `0x19`=class · `0x1a`=lo · `0x1b`=hi
2. arm: `0x21`=0xC0 ×2 · `0x57`=1,0 · ~2 ms settle · `0x21`=0xC3
3. wait: poll `0x39` (DONE/TRIG) + `0x46` (**FILL ≥ 0x200**)
4. halt: `0x21`=0xC8, re‑read `0x46` until two equal (record frozen)
5. drain: read `0x30→0x34` round‑robin `cols` times → samples
6. tail + re‑arm: `0x3c`=2 · `0x3d`=8 · `0x3e`=0 · `0x58`=0 · `0x16`=1

**Native‑fast:** class `0x20`, div lo=0 hi=0. **Half‑record caveat:** the factory fabric intermittently
freezes only the pre‑trigger half (~40% of native‑fast frames) — the vendor works around it with a
maturation floor + re‑capture (needs a real trigger). The owned M9K design fixes it structurally.

---

## 5. What is PROVEN on hardware

- **Our software reads the external SRAM the vendor way.** `draintest` drove the factory bitstream's
  arm→capture→drain over GPMC and pulled **real triangle data** out of the external SRAM — full‑depth
  20480‑sample records occur. **[HW]**
- **Non‑contending SRAM read** from our own fabric (`sramdump`): drive only D14, tri‑state the 27,
  MAX‑V holds address, latch DQ. **[HW]**
- The owned **acq.rbf (M9K)** and our **acq_sram.rbf** load + pass `iface.Verify` (build‑ID `0xc2f6eb5f`,
  VERSION `0x0052`) + present the CS1/CS3 register interface. **[HW]**
- ADC drive recipe cracked from boundary‑scan (ENCODE on K8..M8, held controls F1/L4/T2/T7=1,
  G1/G2/K1=0); git: "owned fabric CONVERTS + captures real data"; CH1 de‑interleave proven via
  `rawcap`: **CH1 bit7..0 = adc_lane[3,0,1,11,9,12,6,5]**, CH2 partial. **[PROVEN per git — but see §7]**

---

## 6. Our own external‑SRAM fabric (`capsram` / `acq_sram.rbf`) — status

Drop‑in for `standard/acq.rbf`: same register interface + build‑ID, swaps the M9K capture buffer for
the external SRAM. `capsram.v` never writes `mem[]` during fill — `mem[]` is filled ONLY by the D14
slurp reading the external SRAM, so a coherent triangle frame is by construction proof it traversed
the external part (`eng_enable=0` = negative control).

- **Built:** 368011 B, 0 fit errors, all 27 SRAM balls + D2 + D14 + DQ placed, Fmax 59 MHz. **[done]**
- **Loads + register interface works; SRAM read STRUCTURE gives full 20480 records.** **[HW]**
- Runtime write‑tuning debug regs (free CS1 sels, decoded in capsram, build‑ID untouched):
  - `0x48` DBG_WDIV (write clk div) · `0x4c` DBG_WPHASE (we/load phase) · `0x68` DBG_WSTROBE
    (load_sel/we_sel/low_mask) · `0x6c` DBG_WMISC (eng_enable/d2_wr/d2_rd/d2_idle) ·
    `0x0c` DBG_RDDIV · `0x08` DBG_MAP (addr order `amap` / lane_sel `lmap`)
  - reads: `0x00` DBG_ID=0x5CA0 · `0x04`/`0x1c` raw DQ vector · `0x7c` DBG_STATUS
- **Known bug:** the DBG *read* decode returns 0 (writes fine) → on‑fabric diagnosis is blind; RTL fix + rebuild pending.

---

## 7. CURRENT BLOCKERS (in order)

1. **ADC reads dead‑0x00 on our own fabric (both M9K and capsram).** With the deploy fixed
   (auto‑takeover on boot) and the front‑end confirmed emitted at 1 V/div both channels + offset
   zeroed (`SetVdiv`→`WriteRelay`+`WriteGain`, `ok:true`), the drained record is still all‑zero
   (`last_ptp=0`). `off_c1` moves with `offset1` so the offset path works, but no converted ADC data
   reaches the fabric — **contradicts §5's git‑proven de‑interleave/pinned lanes**. This blocks
   everything downstream (can't validate SRAM capture with no signal). Next FPGA diagnostic: load
   `rawcap` raw‑lane recorder to see if any `adc_lane` toggles → splits "ADC not converting" from
   "standard fabric capture/de‑interleave regression". `acq.rbf` is a non‑committed build artifact —
   possible build/state mismatch vs the proven one.
2. **MAX‑V SRAM write handshake for a standalone fabric** — untested (downstream of #1). May be a
   simple SPB drive the fixed MAX‑V translates, or need the exact vendor timing (**[REFUSED]** remotely).
3. **capsram DBG read decode** returns 0 → blind; RTL fix needed.

---

## 8. Deploy / operate (so this is reproducible)

- Device agent.env: **`OTA_AUTO_TAKEOVER=1`** at `/usr/bin/siglent/usr/media/U-disk0/ota/agent.env`
  → our app auto‑takes‑over on boot and loads the FPGA itself. (`untakeover` sets it false in state.)
- `make app-release` embeds `fpga/standard/acq.rbf` into `dist/app-arm`; deploy with
  `otactl -stage /usr/bin/siglent/usr/media/U-disk0 update-app dist/app-arm` (/tmp is read‑only).
- **Same build‑ID → fpgaload SKIPS reconfigure** unless CRAM is cleared first → **Shelly power‑cycle**
  (192.168.1.223), then the active slot's app loads the new rbf.
- Front‑end for the live triangle: **C1 500 mV–1 V/div, C2 50 mV/div, TDIV 25 µs (or native‑fast
  1e‑7 for the deep record), DC couple**. App API on `:8080` (`/api/set`, `/api/status`,
  `/api/frame.bin?raw=1`); NOT vendor VXI‑11 SCPI.
- Recovery: `otactl -shelly 192.168.1.223 power cycle`; the box wedges easily — cycle + re‑check each iter.

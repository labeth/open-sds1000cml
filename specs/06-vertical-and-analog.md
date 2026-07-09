# 06 — Vertical and Analog Front End

This document specifies the per-channel vertical path: the V/div gain ladder, the vertical offset
(position) DAC, and coupling. Read `00-overview.md` for the bus/plane vocabulary and `01-system-
architecture.md` for the single-owner GPMC discipline. Code→volts calibration values referenced
here are laid out in `10-calibration.md`.

## 1. Path summary and which bus each control lives on

The vertical front end is almost entirely **off the GPMC bus**. Only the offset DAC is a GPMC
(CS3) register; the gain ladder and coupling relays are on the SoC SPI bus.

| Control | Transport | Device / register | Owner |
|---|---|---|---|
| Coarse V/div range (per channel) | SPI | `/dev/spidev1.0` relay word, per-channel byte **bit 2** | Panel worker, direct |
| Fine V/div gain (per channel) | SPI | `/dev/spidev1.1` gain DAC, one byte per channel | Panel worker, direct |
| Coupling / BW-limit relays | SPI | `/dev/spidev1.0` relay word, per-channel byte | Panel worker, direct |
| Vertical offset / position (per channel) | GPMC | CS3 `0x10`(lo)/`0x30`(hi) C1, `0x11`(lo)/`0x31`(hi) C2 | Engine owner, at frame boundary |

Consequence of the split:

- **SPI writes are self-serializing and never touch the FPGA config.** The relay word and gain DAC
  are on a different physical bus from GPMC, so the panel worker drives them directly and
  concurrently with the acquisition loop. They cannot black-screen the engine (see §7).
- **The offset DAC is a GPMC CS3 write and is subject to the single-owner rule.** It must be staged
  as a command and flushed by the engine owner at a frame boundary, never written from the panel
  worker, and never during a capture-halt window. It also requires a trailing run-word re-assert
  (§5.3).

## 2. SPI transports

Two SPI character devices carry the analog front end. Both are opened once at start-up with
`O_RDWR` and configured with the ioctls below. Neither is ever closed while running.

The ioctl argument is a **pointer to** the value (`&v`), not the value itself; the kernel reads the
32-bit value through that pointer. Use the **write-direction** (`_IOW`, `0x40…`) request codes for
every configuration ioctl so the mode/bits/speed are actually programmed. The read-direction
(`_IOR`, `0x80…`) aliases of the same names do not program the bus — they copy the current setting
back into the argument.

### 2.1 `/dev/spidev1.0` — relay word (coarse range, coupling, BW-limit, trigger relays)

Configure on open (each is one `ioctl`, argument is a pointer to the value):

| ioctl | Request code | Value |
|---|---|---|
| `SPI_IOC_WR_MODE` | `0x40016b01` | `3` (mode 3, CPOL=1 CPHA=1) |
| `SPI_IOC_WR_BITS_PER_WORD` | `0x40016b03` | `0x18` (24 bits) |
| `SPI_IOC_WR_MAX_SPEED_HZ` | `0x40046b04` | `0x000493e0` (300 kHz) |

Each write is a single 24-bit word sent as one `spi_ioc_transfer` via `SPI_IOC_MESSAGE(1)`
(`0x40206b00`): `tx_buf = &word`, `rx_buf = 0`, `len = 4`, `bits_per_word = 0x18`, `speed_hz =
300000`. MSB-first (kernel default; do not set LSB-first). The full word is rebuilt and re-sent on
every change — never a read-modify-write of the hardware (§4.3).

**Trap — SPI mode.** The relay is a single-word shift-latch and tolerates the default SPI mode, so a
mode-config no-op does not by itself corrupt a relay write. Still program mode 3 with the
write-direction codes above; do not rely on the default, and do not use the `0x80…` read-direction
aliases (they leave mode/bits/speed at defaults).

### 2.2 `/dev/spidev1.1` — fine gain DAC

Configure on open:

| ioctl | Request code | Value |
|---|---|---|
| `SPI_IOC_WR_BITS_PER_WORD` | `0x40016b03` | `8` |
| `SPI_IOC_WR_MODE` | `0x40016b01` | `3` (mode 3, CPOL=1 CPHA=1) |
| `SPI_IOC_WR_MAX_SPEED_HZ` | `0x40046b04` | `300000` (300 kHz) |

MSB-first. Each gain update sends **two separate single-byte transfers** (not one 2-byte transfer),
each its own CS-framed `spi_ioc_transfer{tx_buf=&code, len=1, bits_per_word=8, speed_hz=0→fd
default}` via `SPI_IOC_MESSAGE(1)` (`0x40206b00`). There is **no address/command byte** — the raw
8-bit code is the whole payload; the two channels are distinguished only by transfer order:

1. **CH2 byte first**
2. **CH1 byte second**

On any gain change, **both** channel codes are re-sent in this order (§4.3).

**Trap — do not open the bitstream loader fd.** `/dev/spidev1.1` is the same physical node used by
the FPGA passive-serial bitstream loader (mode 0 / 8-bit / 24 MHz for bulk `.rbf` transfers). The
gain DAC must use only the mode-3 / 8-bit / 300 kHz single-byte path above. Sending single 8-bit
transfers at 300 kHz in mode 3, while `nCONFIG` is high and `CONF_DONE` is asserted (normal run
state), reaches only the DAC — it cannot reconfigure the FPGA.

**Hard rule.** FPGA reconfiguration is armed exclusively by the **GPMC config port `0x2010000e`**
(pulling `nCONFIG` low), which is on the GPMC bus, not SPI. The gain path must never touch
`0x2010000e` or the 24 MHz bitstream fd. With that rule held, all SPI front-end writes are safe:
`CONF_DONE` stays asserted through arbitrary relay + gain hammering.

## 3. The relay word (`/dev/spidev1.0`)

The 24-bit word packs, little-endian:

```
word = byte0 | (byte1 << 8) | (byte2 << 16)
  byte0 = CH1 control byte
  byte1 = CH2 control byte
  byte2 = (trigCoupling << 4) | (trigSrc << 2)
```

Per-channel control byte:

| Bit | Meaning | Notes |
|---|---|---|
| 0 | Bandwidth limit | **1 = BWL OFF (full BW)**, 0 = 20 MHz limit engaged |
| 1 | GND coupling select | **1 = GND** (AC baseline + bit 1, with bit 3 clear). Effective when the **absolute** word is emitted (CH1 byte `0x27`, word `0x70ad27`); an RMW that leaves bit 3 set is what makes GND look inert — see §6 |
| 2 | Coarse V/div range | **1 = attenuated/high range** (500 mV/div…10 V/div), 0 = sensitive/low range (2 mV/div…200 mV/div). HW-effective (§4). |
| 3 | DC coupling select | Selects the DC offset-cal entry, not a coupling cap — see §6 |
| 5 | Constant enable | Always 1 |
| 7 | CH2 channel-address bit | Set on the CH2 byte (byte1, base `0xA0` = bit7+bit5); clear on the CH1 byte (base `0x20`). It addresses the byte to the CH2 relay latch — **not** a coupling-relay polarity bit. |

`byte2`: `trigCoupling` high nibble = `0x7` for DC; `trigSrc` = 0 (C1) / 1 (C2) / 2 (EXT). See §6
and `05-triggering.md` for why the trigger-source nibble is not the runtime source selector on this
clone.

Reference words (DC, BWL off, both channels attenuated range): CH1 byte = `0x2d`, full word =
`0x70ad2d`. CH1 bytes at 2 V/div: DC=`0x2d`, AC=`0x25`, GND=`0x27`, DC+BWL-on=`0x2c`, DC+sensitive-
range=`0x29`.

CH2 byte bit 7 is the **CH2 channel-address bit**: the CH1 byte carries bit 7 = 0 (base `0x20`) and
the CH2 byte carries bit 7 = 1 (base `0xA0` = bit7+bit5). It selects which channel's relay latch the
byte lands in; it is **not** a coupling-relay polarity bit. (CH1 byte `0x2d` → bit7=0 vs CH2 byte
`0xad` → bit7=1, full DC word `0x70ad2d`.)

## 4. V/div gain ladder

The vertical range on this clone is a **real analog ladder**: a coarse relay range (bit 2) plus a
fine gain DAC per channel. Within a coarse range the DAC is linear in code; the two coarse ranges
give a ~34× step. There is **no per-detent digital post-scale at or above 10 mV/div** — the
displayed amplitude comes from the analog gain plus the calibration code→volts mapping, not from
multiplying raw ADC samples.

The two most sensitive detents (**2 mV/div** and **5 mV/div**) are the exception: they lie **below
the analog floor**. They drive the *same* finest analog code as 10 mV/div and are shown as a
**display-side digital magnification** of the 10 mV analog range (see §4.4).

### 4.1 Detent table

12 detents per channel. `bit2 = (idx >= 7)`. The fine gain-DAC code per detent is calibration
record `+0x00` (see `10-calibration.md`); the values below are the per-unit CH1 codes — CH2 shares
the same code table.

| idx | V/div | Coarse range (bit 2) | Gain-DAC code (CH1) | Note |
|---|---|---|---|---|
| 0 | 2 mV | 0 (sensitive) | **146** | below analog floor — reuses 10 mV code + ×5 digital zoom (§4.4) |
| 1 | 5 mV | 0 | **146** | below analog floor — reuses 10 mV code + ×2 digital zoom (§4.4) |
| 2 | 10 mV | 0 | 146 | finest reachable analog range |
| 3 | 20 mV | 0 | 63 | |
| 4 | 50 mV | 0 | 25 | |
| 5 | 100 mV | 0 | 12 | |
| 6 | 200 mV | 0 | 6 | |
| 7 | 500 mV | 1 (attenuated) | 115 | |
| 8 | 1 V | 1 | 57 | |
| 9 | 2 V | 1 | 28 | |
| 10 | 5 V | 1 | 11 | |
| 11 | 10 V | 1 (attenuated) | 6 | top detent; codes/volt ≈ 7 |

The inherited boot detent is **1 V/div (idx 8)**. At start-up, seed both channels' relay-range and
gain shadows to the boot detent but do **not** emit — leave the inherited analog range untouched
until the user changes V/div (§4.3 explains why the first emit must carry both channels).

### 4.2 Gain transfer (for a self-cal / range-check)

Against a fixed input, raw ADC peak-to-peak amplitude vs gain code is linear within each range:

- **bit 2 = 1 (attenuated):** ptp ≈ `1.32 · code`, linear to ~code 80, then rails.
- **bit 2 = 0 (sensitive):** ptp ≈ `45 · code`, linear to ~code 4.
- **bit 2 step:** ~34× coarse lever.

The gain change is immediate — the new code takes effect on the very next captured frame (no settle
lag at the ADC). The two channels are independent; the CH2 code does not affect CH1.

### 4.3 Applying a detent (deterministic recipe)

The gain is deterministic and monotonic **only** if each change writes the full absolute state, never
a partial/RMW update:

1. Rebuild the **full** relay word with bit 2 set for the target detent, and emit it over
   `/dev/spidev1.0`.
2. **Settle ~400 µs** after the relay emit (the coarse relay/attenuator needs physical settle time).
3. Set **both** channels' gain shadow bytes and emit both over `/dev/spidev1.1` (CH2 byte first,
   then CH1). Both must be sent because the gain Emit always transmits both; sending only the
   changed channel's byte from an unseeded (0) shadow collapses the other channel's gain.

With this recipe the analog gain is prior-, order-, and settle-independent: the first frame after
the change already shows the correct gain. No readback, retry, or double-write is needed.

**Trap — measure gain only with the offset centred.** A fixed gain looks history-dependent if the
offset DAC is off-centre, because an off-centre trace clips against the ADC rails and the measured
ptp shrinks. Any self-cal that measures codes/volt per detent must first centre the offset so the
signal is unrailed; otherwise the ladder reads non-monotonic. This clipping is a measurement
artifact, not a gain non-determinism.

### 4.4 Sub-10 mV digital zoom (2 mV, 5 mV/div)

Because the analog range floors at the 10 mV code (146) for idx 0/1/2, the 2 mV and 5 mV detents get
their extra sensitivity **on the display side**, not from the analog path:

- The analog gain code and relay range are those of **10 mV/div** (idx 2): gain code `146`, bit 2 =
  0.
- The display magnifies the captured samples by the ratio of the 10 mV analog range to the requested
  V/div: **×5 at 2 mV/div**, **×2 at 5 mV/div**.

This magnification requires **no extra step** in the render path: the display volts/div scale is
already the requested V/div (volts-per-code `Vdiv/25`, the 25-codes/div render scale of
`10-calibration.md` §7.1), while the code→volts mapping
uses the 10 mV analog range — so the trace is drawn ×5 / ×2 taller automatically. An implementer
must therefore *not* try to reach a finer analog code for these two detents (there is none; sending
219/164 or any code < 146 rails or inverts the gain), and must *not* suppress the requested-V/div
display scale for them.

## 5. Vertical offset (position) DAC

The offset DAC moves the captured window's DC centre across the ADC range. It is a **CS3 GPMC**
register — the one vertical control on the GPMC bus.

### 5.1 Registers

| Channel | Low byte | High byte |
|---|---|---|
| C1 | CS3 `0x10` | CS3 `0x30` |
| C2 | CS3 `0x11` | CS3 `0x31` |

The DAC code is 16-bit. Write **low byte first, then high byte**; the high-byte write self-latches
(there is no separate strobe). The transfer is **inverting**: a higher code produces a lower trace.

**Plane-explicit rule + write primitive.** These selectors (`0x10`/`0x30`/`0x11`/`0x31`) **alias live
acquisition ports on CS1**, so the CS3 plane MUST be forced explicitly on every offset write — a write
that lands on the default/CS1 plane corrupts the acquisition engine instead of the offset DAC. Use a
plane-explicit primitive `WriteRegCS(plane=3, sel, byte)`: low byte to `0x10` (CH1) / `0x11` (CH2)
first, then high byte to `0x30` / `0x31` (self-latches). See `00-overview.md` for the GPMC/CS
vocabulary.

### 5.2 Volts → code

The offset DAC injects a level shift **ahead of the fine gain stage** — the offset is
**input-referred**. Its code is **scaled by V/div**: the DAC runs at **50 codes per division of
offset**, so

```
code = clamp( round( zero − 50 · (V / VDIV) ) )
```

- `V` = requested offset in volts (input-referred).
- `VDIV` = the active volts/division detent.
- **50 DAC codes = 1 division = 25 on-screen ADC codes.** The offset (and trigger-level) DAC runs at
  **2× the display grid**: the on-screen graticule is 25 codes/div, the DAC is 50 codes/div.
  Codes-per-volt is therefore `50 / VDIV` — it **IS** scaled by V/div (a smaller V/div moves more
  DAC codes per input volt).
- `zero` = the calibrated per-tier offset-zero for the active channel (§5.2.1): one value for the ×1
  sensitive tier, one for the ×25 attenuated tier (≈ 10445 at 1 V/div; boot default `0x27ef` =
  10223).
- **Inverting:** a *positive* offset yields a *lower* code (the trace moves *up*); `0 V` programs
  exactly `zero`.

**Inverse** (for readback / WAVEDESC offset): `V = (zero − code) · VDIV / 50`.

**Trap — the slope is per-division, not a fixed codes-per-volt.** A form that uses one fixed
codes-per-input-volt (independent of V/div) under-drives the DAC at fine V/div and overshoots at
coarse V/div. The code scales as `50 / VDIV`; the attenuator sets only the tier and the clamp
(§5.2.1), it does not change the 50-codes-per-division slope.

#### 5.2.1 Tiered offset range and per-tier clamp

The offset range is **tiered**, set by the shared coarse-V/div input attenuator (relay-word **bit
2**, which engages at V/div ≥ 500 mV):

- **±1.6 V** on the sensitive ×1 tier (V/div ≤ 200 mV, bit 2 = 0).
- **±40 V** on the attenuated ×25 tier (V/div ≥ 500 mV, bit 2 = 1).

That is a **25× step exactly at the 200 mV ↔ 500 mV boundary** — the attenuator engage point. The
wide ±40 V range is produced by the attenuator **dividing the input** ahead of the injection point,
**not** by a wider or a second DAC: there is a **single** offset DAC, and the same DAC excursion
reaches ±1.6 V of input on the ×1 tier and ±40 V on the ×25 tier.

The code is clamped per tier:

```
clamp_codes = NUMERATOR / (1000 · VDIV)
```

with `NUMERATOR = 80000` on the sensitive tier (→ ±1.6 V) and `NUMERATOR = 2000000` on the
attenuated tier (→ ±40 V). `1000 · VDIV` for the 12 detents is
`[2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000]`. The final 16-bit code is additionally
clamped to `[0, 0xFFFF]`.

**Per-tier / per-channel centring is automatic.** Because the coarse range bit is itself a function
of the V/div index (`bit 2 = idx ≥ 7`), indexing the offset-zero by tier follows the V/div detent:
each channel picks its ×1-tier zero on V/div ≤ 200 mV and its ×25-tier zero on V/div ≥ 500 mV, so
both C1 and C2 are centred per tier with no separate range logic. Do **not** centre a channel with
one fixed code across both tiers — that mis-centres it by more than a division in one tier.

### 5.3 Applying an offset change

The offset write itself does **not** inherently wedge the engine and is robust at any *frame phase*
(mid-frame or between frames) **except during a capture-halt window**, when no GPMC write may occur
(see §7). But an engine that arms once per frame needs the front-end change re-anchored, so every
offset write on the owner is followed by a **trailing run-word re-assert**:

1. Write low byte to `0x10`/`0x11`.
2. Write high byte to `0x30`/`0x31` (latches).
3. Re-assert the CS1 run word `0x35` with the current run value (`0x0001` free-run/AUTO,
   `0x0003` armed/NORM). (This is CS1 `0x35`, the run word — **not** CS3 `0x35`, which is the
   trigger level lane-B high byte; different plane, do not conflate.)

**Staging / flush mechanism (panel → engine hand-off).** The offset write must run on the single
engine owner, never on the panel worker. Concrete mechanism: the owner exposes `SetOffsetDAC(ch,
code)`, which stores the code into a per-channel pending shadow (`offCode[ch]` + `offDirty[ch]`) under
a command mutex and returns immediately. At the top of each FSM iteration — the frame boundary, when
the engine is armed+filling and **not** in a `0xC8` halt window — the owner's `serviceCommands()`
drains the dirty shadows and calls `writeOffset`, which flushes low+high bytes (steps 1–2 above) then
re-asserts the run word (step 3). This is the only place a panel/offset GPMC access happens, which
preserves the single-owner discipline.

**Trap — offset-only changes must not re-emit gain.** The offset DAC and the SPI gain path are
independent. A control flush that re-emits the gain/relay word on an offset-only change will re-send
a stale/unseeded gain code (code 0), collapse the analog gain, and leave the offset with no signal
to move (the trace stays stuck at mid-scale). Gate the gain/relay emit on a "V/div changed" flag so
an offset-only change writes only the offset DAC + run-word re-assert.

## 6. Coupling and GND — what is real on this clone

The relay word carries per-channel coupling bits, but their electrical effect on this clone is
limited. Program them for parity, but do not assume hardware AC/GND behaviour:

- **DC coupling (bit 3):** set for DC. Coupling selection rides byte 0: **bit 3 = DC** (`0x2d`),
  **bit 1 = GND** (`0x27`), both clear = AC (`0x25`). It is not just a coupling capacitor select; it
  also selects a different offset-DAC calibration entry (the AC↔DC baseline differs by ~36 codes
  because the firmware loads a different offset-cal value per coupling state).
- **Coupling companion (CS3 config-plane write).** On any **coupling** change the front-end assembler
  emits a companion 16-bit preset over the CS3 config plane **alongside** the relay word (this is a
  coupling companion, **not** a BWL write). Per channel it is split low/high **`0x40` apart** (not
  adjacent): CH1 = low selector `0x12` (`0x20100024`) + high selector `0x32` (`0x20100064`); CH2 =
  low selector `0x13` (`0x20100026`) + high selector `0x33` (`0x20100066`). The 16-bit value encodes
  the coupling sense: **`0xb851` = DC** (captured `0x12`=`0x51`, `0x32`=`0xb8`), `0xced9` = the
  non-DC (AC/GND) sense. A factory-firmware-parity coupling change emits relay byte 0 (bit 3 / bit 1) **and**
  this companion pair. **Open:** which of AC vs GND maps to `0xced9` is not separately captured (only
  the DC value `0xb851` is observed on the bus); treat `0xced9` as "the non-DC sense".
- **AC coupling (bit 1 cleared / bit 3 cleared):** there is **no hardware high-pass** engaged on
  this clone. If AC (DC-blocking) behaviour is required, it must be a **software** DC-removal filter
  on the captured samples.
- **GND coupling (bit 1):** the GND relay is **populated and effective** when the **absolute** relay
  word is emitted — GND = the AC baseline **with bit 1 set and bit 3 clear** (CH1 byte `0x27`, full
  word `0x70ad27`), and the live ADC then reads a third distinct code band (DC / GND / AC give three
  separate code clusters). The earlier "inert / unpopulated" verdict was a **read-modify-write
  artifact** that left bit 3 set alongside bit 1; always emit the absolute word, never RMW. (There is
  separately no digital channel-ground in the GPMC plane: the PLANE-A mux CS1 `0x22`, values 0–3,
  does **not** zero the ADC — it is the engine-safe trigger-input mux, not a channel ground. A pure
  software GND display remains a valid fallback, but the relay bit itself works.)
- **Bandwidth limit (bit 0):** drives the BWL relay (1 = full BW / OFF, 0 = 20 MHz limit / ON), and
  is the **only** register it writes — the BWL handler has no direct FPGA/CS3 writer of its own.
  Toggling `C1:BWL ON`↔`OFF` flips CH1 byte 0 `0x2c`↔`0x2d`. BWL can also re-trim the V/div gain (the
  firmware re-runs the V/div gain apply on a BWL change via the force-BWL-on-by-V/div path), so
  re-emit the gain ladder after a BWL toggle.

**Open:** the on-clone *electrical* effectiveness of the BWL relay (whether the 20 MHz roll-off is
physically present on this clone) is not measured — only the relay-bit write (byte 0 bit 0) is
established. Treat the analog roll-off as best-effort until validated; if a hard 20 MHz limit is
required, apply a software low-pass as a fallback.

## 7. Load-bearing constraints

- **SPI front end is direct; offset DAC is not.** The relay word and gain DAC are off the GPMC bus,
  so the panel worker drives them directly. The offset DAC is a CS3 GPMC register: stage it as a
  command (`SetOffsetDAC` → pending shadow) and let the single engine owner flush it at a frame
  boundary via `serviceCommands()` → `writeOffset` (never during a capture-halt window, never from the
  panel worker — see §5.3). A second consumer touching GPMC during a halt black-screens the instrument.
- **Never write GPMC `0x2010000e`.** That config port (not SPI) is what arms an FPGA reconfigure.
  The gain path imports no GPMC dependency by construction; keep it that way.
- **Use write-direction SPI ioctls.** Configure both SPI fds with the `_IOW` (`0x40…`) request codes
  and a pointer argument, so mode/bits/speed are actually programmed. The `_IOR` (`0x80…`) aliases
  do not configure the bus.
- **Absolute writes only.** Rebuild the whole relay word and re-send both gain bytes on every
  change. RMW / single-byte gain writes make the gain look history-dependent and can collapse the
  untouched channel.
- **Relay settle.** Wait ~400 µs after a relay emit before the next front-end step; the coarse
  attenuator needs physical settle.
- **Offset slope is per-division and input-referred.** `code = clamp(zero − 50·(V/VDIV))` — 50 DAC
  codes per division of offset (= 25 on-screen codes). Take `zero` from the calibrated per-tier
  offset-zero (×1 sensitive tier vs ×25 attenuated tier) for the active channel; clamp per tier
  (±1.6 V on ×1, ±40 V on ×25).
- **Trailing run-word re-assert after an offset write** (CS1 `0x35`), so the once-armed engine stays
  coherent.
- **Centre the offset before any gain measurement.** Off-centre offsets clip the trace and corrupt a
  codes/volt self-cal.
- **2 mV/5 mV are digital zooms.** Drive the 10 mV analog code (146) at those detents and let the
  display magnify ×5 / ×2; do not seek a finer analog code (there is none).
- **Seed but do not emit at start-up.** Seed both channels' relay-range and gain shadows to the boot
  detent (1 V/div) without emitting, so the first real V/div change sends correct codes for both
  channels and the inherited boot analog range is left untouched until then.

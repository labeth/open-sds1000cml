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
| 1 | GND coupling select | **Inert on this clone** (relay coil unpopulated) — see §6 |
| 2 | Coarse V/div range | **1 = attenuated/high range** (500 mV/div…5 V/div), 0 = sensitive/low range (2 mV/div…200 mV/div). HW-effective (§4). |
| 3 | DC coupling select | Selects the DC offset-cal entry, not a coupling cap — see §6 |
| 5 | Constant enable | Always 1 |
| 7 | CH2 base preload | The CH2 byte (byte1) has bit 7 set (base `0xA0`); the CH1 byte base is `0x20` |

`byte2`: `trigCoupling` high nibble = `0x7` for DC; `trigSrc` = 0 (C1) / 1 (C2) / 2 (EXT). See §6
and `05-triggering.md` for why the trigger-source nibble is not the runtime source selector on this
clone.

Reference words (DC, BWL off, both channels attenuated range): CH1 byte = `0x2d`, full word =
`0x70ad2d`. CH1 bytes at 2 V/div: DC=`0x2d`, AC=`0x25`, GND=`0x27`, DC+BWL-on=`0x2c`, DC+sensitive-
range=`0x29`.

**Open:** the polarity/meaning of CH2 byte bit 7 beyond the `0xA0` base preload is not established.

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

11 detents per channel. `bit2 = (idx >= 7)`. The fine gain-DAC code per detent is calibration
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
already the requested V/div (`Vdiv/50` per `10-calibration.md` §7.1), while the code→volts mapping
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

### 5.2 Volts → code

The offset DAC is **input-referred**: it injects a level shift *ahead of* the gain stage, so the
code is a single fixed slope in **input volts**, **independent of V/div and of the per-detent gain
coefficient**:

```
code = clamp16( round( zero − V · K ) )
```

- `V` = requested offset in volts (input-referred).
- `K` = **262 DAC codes per input-volt** — a fixed constant. It is **not** scaled by V/div and
  **not** multiplied by the gain coefficient. (Env-tunable via `SCOPE_OFFSET_K` while calibrating a
  reference unit.)
- `zero` = the 0 V code for the **active channel and active V/div**, calibration RAM record `+0x12`
  (`rec = 0x32ced8 + ch*0xf0 + vd*0x14`, field `+0x12`; sourced from file Block A `+0x2` unless the
  Block-B override applies — see `10-calibration.md` §4). Boot default `0x27ef` = 10223; uncalibrated
  fixed fallback ≈ 10600.
- **Inverting:** a *positive* offset yields a *lower* code (trace moves *up*).
- `clamp16` clamps only the **final** 16-bit code to `[0, 0xFFFF]`. There is no intermediate
  divisions/±230 clamp.

**Trap — do not scale K by V/div or by the gain coefficient.** A form that scales the slope by
`50·V/VDIV·gainK` overshoots the DAC at fine V/div (the offset goes inert / rails) and under-drives
it at coarse V/div; it only tracks near ~1 V/div. `K` is a single fixed input-volts slope for all
detents.

**Inverse** (for readback / WAVEDESC offset): `V = (zero − stored_code) / K`.

**Per-range / per-channel centring is automatic.** Because `zero` is stored **per (channel, V/div)**
at record `+0x12`, both C1 and C2 are centred per detent simply by indexing `+0x12` with the active
channel and V/div — no separate range logic is needed. Since the coarse range bit is itself a
function of V/div index (`bit 2 = idx ≥ 7`), this yields the correct sensitive-range vs
attenuated-range zero for free. Example per-unit values: C2 ≈ 11150 (sensitive range) vs ≈ 10800
(attenuated range); C1 / single-code fallback ≈ 10600. Do **not** centre C2 with one fixed code
across both ranges — that mis-centres it by ~350 codes (>1.5 divisions) in one range.

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

**Trap — offset-only changes must not re-emit gain.** The offset DAC and the SPI gain path are
independent. A control flush that re-emits the gain/relay word on an offset-only change will re-send
a stale/unseeded gain code (code 0), collapse the analog gain, and leave the offset with no signal
to move (the trace stays stuck at mid-scale). Gate the gain/relay emit on a "V/div changed" flag so
an offset-only change writes only the offset DAC + run-word re-assert.

## 6. Coupling and GND — what is real on this clone

The relay word carries per-channel coupling bits, but their electrical effect on this clone is
limited. Program them for parity, but do not assume hardware AC/GND behaviour:

- **DC coupling (bit 3):** set for DC. It is not a coupling capacitor; it selects a different
  offset-DAC calibration entry (the AC↔DC baseline differs by ~36 codes because the firmware loads a
  different offset-cal value per coupling state).
- **AC coupling (bit 1 cleared / bit 3 cleared):** there is **no hardware high-pass** engaged on
  this clone. If AC (DC-blocking) behaviour is required, it must be a **software** DC-removal filter
  on the captured samples.
- **GND coupling (bit 1):** the GND relay bit is **electrically inert** (the relay coil is most
  likely unpopulated). There is also no digital channel-ground anywhere in the GPMC plane: the
  PLANE-A mux (CS1 `0x22`, values 0–3) does **not** zero the ADC (that register is the engine-safe
  trigger-input mux, not a channel ground). A GND display (flat zero trace) must therefore be done
  in **software** (render a flat baseline / suppress the channel).
- **Bandwidth limit (bit 0):** drives the BWL relay (1 = full BW, 0 = 20 MHz limit).

**Open:** the on-clone electrical effectiveness of the BWL relay (bit 0) is not established. It may
require a companion PLANE-B write; treat BWL as best-effort until validated.

## 7. Load-bearing constraints

- **SPI front end is direct; offset DAC is not.** The relay word and gain DAC are off the GPMC bus,
  so the panel worker drives them directly. The offset DAC is a CS3 GPMC register: stage it as a
  command and let the single engine owner flush it at a frame boundary (never during a capture-halt
  window, never from the panel worker). A second consumer touching GPMC during a halt black-screens
  the instrument.
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
- **Offset slope is fixed and input-referred.** `code = clamp16(zero − V·262)`; never scale by V/div
  or gain coefficient. Take `zero` from cal record `+0x12` for the active (channel, V/div).
- **Trailing run-word re-assert after an offset write** (CS1 `0x35`), so the once-armed engine stays
  coherent.
- **Centre the offset before any gain measurement.** Off-centre offsets clip the trace and corrupt a
  codes/volt self-cal.
- **2 mV/5 mV are digital zooms.** Drive the 10 mV analog code (146) at those detents and let the
  display magnify ×5 / ×2; do not seek a finer analog code (there is none).
- **Seed but do not emit at start-up.** Seed both channels' relay-range and gain shadows to the boot
  detent (1 V/div) without emitting, so the first real V/div change sends correct codes for both
  channels and the inherited boot analog range is left untouched until then.

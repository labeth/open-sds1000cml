# 10 — Calibration

Per-unit analog calibration: the on-flash `calibration.dat` blob, its on-disk
envelope (checksum + scramble), its record layout, and how those records are
unpacked into the in-RAM cal table that the vertical (gain/offset) and trigger
(level) paths consume. This spec covers the load path (file → RAM), the table
geometry, the compiled-default fallback, and the volts↔code formulae that turn a
cal record plus a user setting into the exact byte written to a DAC.

The calibration data is **per-unit**: the numeric gain/offset constants live only
on the unit's flash, not in the firmware image. The firmware supplies the
**envelope, layout, and formulae** (all documented here); it reads the **values**
at boot.

---

## 1. Storage

Calibration lives on the factory volume `firmdata0` (UBI volume `ubi2_0`, mounted
`ro,sync` ubifs at `/usr/bin/siglent/firmdata0/`).

| File | Purpose | Size |
|------|---------|------|
| `calibration.dat` | active per-unit calibration blob | 2752 B (`0xac0`) |
| `calibration_bak.dat` | redundant backup (identical format) | 2752 B |
| `bandwidth.txt` | bandwidth-option/limit setting | text |
| `bw_flag.dat` | bandwidth-flag state | — |

**Requirements**

- Read `calibration.dat` at boot. On any validation failure fall back to
  `calibration_bak.dat`. If both fail, keep the compiled defaults (§6) running.
- `firmdata0` is mounted **read-only**. Never write the live volume during normal
  operation. To pull the file for a read-only clone:
  `mount -o ro,sync -t ubifs ubi2_0 /mnt/firmdata0` and copy it off.
- `cali.bin` (on the `usr/` volume) is a **separate** transport/backup envelope
  wrapping a gzip-tar — it is **not** the live cal blob. Do not confuse the two:
  different volume, different writer, different format. To read it standalone,
  skip its `0x98`-byte header and `tar zxf` the remainder.

---

## 2. On-disk envelope

`calibration.dat` is 2752 (`0xac0`) bytes. Word-0 is a checksum target; the rest
is a scrambled payload.

```
+0x000  u32 (LE)  checksum target      (two's-complement additive checksum of the payload)
+0x004  0xabc B   scrambled payload    (2748 bytes: cal records + a trailing region)
```

### 2.1 Acceptance checksum (the ONLY accept/reject gate)

Two's-complement additive checksum over the payload byte span:

```
sum   = Σ payload bytes            (uint32, mod 2^32)
valid ⇔ ((~sum) + 1) == word0      (equivalently (sum + word0) == 0 mod 2^32)
```

- Span: the full `0xabc` (2748) payload bytes, starting at file `+0x04`.
- Target: word-0 of the file (per-unit data, **not** a firmware constant).

**This word-0 checksum is the entire acceptance test.** A file passes iff word-0
equals the two's-complement additive checksum over its 2748 payload bytes; on
mismatch, reject with *"Calibration data is not compatible!"* and fall back
(§5). There is **no** second checksum pass and **no** secondary guard test in the
accept/reject decision — do not add one, or otherwise-valid files are rejected.

> **Non-blocking secondary region.** The tail of the payload carries a second,
> self-describing sub-record with its **own** additive checksum and guard word,
> validated by a separate code path that is gated on a mode flag — it is
> **not** part of the main accept/reject gate. Its on-disk layout is fixed:
>
> ```
> +0xaa8  u32  secondary-block checksum target (two's-complement additive, over the 0x14-byte block)
> +0xaac  u32  guard word — the secondary validator rejects unless == 0x18
> +0xab0  16 B EXT-trig / probe-DAC sub-record (copied verbatim to a runtime preset buffer)
> ```
>
> The block is 20 (`0x14`) bytes and self-describing: its own additive-checksum
> target lives at `+0xaa8`, its `0x18` guard at `+0xaac`, the 16-byte sub-record
> at `+0xab0`. Do **not** fold this secondary checksum or the `0x18` guard into
> the main accept/reject decision (§2.1) — that over-rejects otherwise-valid
> files. **Open:** the field meanings of the 16-byte EXT-trig / probe-DAC
> sub-record (the length and `0x18` guard are fixed; the fields are not decoded).

### 2.2 Scramble

The payload (`0xabc` bytes) is de-scrambled by three byte-involutions applied
**in this order**:

1. **Reverse** the whole `0xabc`-byte buffer.
2. **NOT the entire back half:** for `i` in `[len - len/2, len)`, `buf[i] = ~buf[i]`
   (bitwise NOT of every byte in the back half).
3. **NOT every byte at a triangular index:** walk positions `1, 3, 6, 10, 15, …`
   (start at index 1, add an incrementing step each hop) and bitwise-NOT each byte
   in place, while `pos < len`. Index 0 is excluded.

This produces the `0xff…` runs visible in the raw file. **The transform is NOT
self-inverse** — applying it twice is not the identity. To **write** a cal file,
apply the inverse (the same three involutions in **reverse** order: NOT-triangular,
NOT-back-half, reverse), then recompute the word-0 checksum over the scrambled
payload.

After de-scramble, the 2748-byte payload holds the cal records (§3).

---

## 3. File record layout

The cal records are packed at **de-scrambled payload byte 0**, stride **8 bytes**,
24 records (2 channels × 12 V/div decades). Channel ordering is CH0 records
`[vd 0..11]` immediately followed by CH1 records `[vd 0..11]`.

| Field within record | type | meaning |
|---------------------|------|---------|
| `+0` | i16 | GAIN-DAC code (fine analog gain) |
| `+2` | i16 | OFFSET-zero code |
| `+4` | f32 | GAIN coefficient (per-decade) |

**Record base (de-scrambled payload byte offset):**

```
base(ch, vd) = (ch*12 + vd) * 8          // ch ∈ {0,1}, vd ∈ {0..11}
GAIN-DAC = i16 @ base+0
OFFSET   = i16 @ base+2
GAIN     = f32 @ base+4
```

**Worked offsets** (verifiable on a fresh build):
- CH0 vd0 → payload byte `0`.
- CH0 vd11 → payload byte `88` (`0x58`).
- CH1 vd0 → payload byte `96` (`0x60`).
- CH1 vd11 → payload byte `184` (`0xb8`).

There are **12 fully-populated per-V/div records per channel** — no PGA-stage
sub-table, no interpolation, no expansion. Do not attempt to reconstruct extra
stages.

### 3.1 Second record block (optional / not required for a first build)

The payload carries a second stride-8 block ("Block B": a range-2 / probe-×10
GAIN-B + offset term) after the primary block ("Block A"). **It is not
required** — a correct loader parses only Block A and copies the OFFSET-zero into
the live-zero field unconditionally (§4.2). A Block-A-only load is complete and
correct.

Both blocks are guard-stamped: the present-magic `0x0000dcba` sits one word
before each block's first record (the secondary tail region of §2.1 uses magic
`0x12345678`). In the guard-anchored view of the de-scrambled payload, Block A's
guard is at payload byte `+0x5c4` and Block B's at `+0x698`; a de-scramble whose
reversal pass re-orients the payload lands Block A's records at payload byte 0
instead (§3, worked offsets) — both frames describe the same record bytes and
values, so use whichever orientation your de-scramble produces.

**Block B layout** (24 records, stride 8, record base index `(ch*12+vd+0xb9)*8`):

| Field within record | type | → RAM (§4.1) | meaning |
|---------------------|------|--------------|---------|
| `+0` | i16 | `+0x10` | offset / range-2 term |
| `+2` | u16 | — | sanity word |
| `+4` | f32 | `+0x0c` | GAIN-B coefficient (≈35–41 on a representative unit) |

**Live-zero clamp.** The full loader writes RAM `+0x12` (live-zero) from Block B's
`+2` **only when that value exceeds `0x7e09`**; otherwise it copies `+0x08`. On a
representative unit every Block-B `+2 ≤ 0x7e09`, so `+0x12 == +0x08` for every
record and the simplified unconditional copy (§4.2) is exact.

**Open:** Block B's **analog role** — range-2 / probe-×10 offset vs a fine-offset
term — is not pinned from code alone (a bench check of which path a ×10 probe
selects would confirm it). Do not wire `+0x0c`/`+0x10` into the analog path
without a validated role; a Block-A-only load is correct without them.

---

## 4. In-RAM cal table

The unpacked records are overlaid onto the live cal table in `.data` at base
**`0x32ced8`**, one contiguous block per channel:

| Item | Value |
|------|-------|
| table base | `0x32ced8` |
| per-channel block | `0xf0` = 240 B (CH0 @`0x32ced8`, CH1 @`0x32cfc8`) |
| per-V/div record | `0x14` = 20 B, 12 records per channel |
| record address | `rec = 0x32ced8 + ch*0xf0 + vd*0x14` |

### 4.1 Record fields (20 bytes)

| off | type | field | meaning |
|-----|------|-------|---------|
| `+0x00` | i16 | GAIN-DAC code | fine analog gain, written to the gain DAC (§7.2) |
| `+0x04` | f32 | GAIN | per-decade gain coefficient (`codes/volt = analogVdivK/GAIN`); consumed by the volts diagnostic — **not** the render vertical-gain path (§7.1) |
| `+0x08` | i16 | OFFSET-zero code | boot default `0x27ef` = 10223 |
| `+0x12` | i16 | live-zero code | the zero the offset-DAC formula uses (§7.4); a copy of `+0x08` |

### 4.2 FILE → RAM mapping

| RAM field | ← file | note |
|-----------|--------|------|
| `+0x00` GAIN-DAC | record `+0` | |
| `+0x04` GAIN | record `+4` | |
| `+0x08` OFFSET-zero | record `+2` | |
| `+0x12` live-zero | record `+2` | **unconditional copy of `+0x08`** — no branch |

**Traps.**
- `+0x12` is always a direct copy of `+0x08`. There is **no** conditional
  overlay from a second block; on a representative unit `+0x12 == +0x08` and the
  loader never reads any other value into it.
- The RAM record fields at `+0x0c` (a Block-B gain) and `+0x10` (a Block-B
  offset/range-2 term) are **not populated** by the load path — the second file
  block is not loaded (§3.1). Leave them at their seeded/zero state; do not wire
  them from the file without a validated map.

### 4.3 Auxiliary scale table (no-op)

A parallel f32 scale table at `0x32d7ec` (indexed per channel × V/div) is **all
`1.0`** and only participates when a per-channel mode flag is set — a no-op in the
common path. It is the tell that per-detent vertical gain is done in HARDWARE (the
gain DAC), not by a render divisor: the render applies a **constant** scale (§7.1),
never the `+0x04` GAIN coefficient. A `+0.5` round bias (`0x3f000000`) is added
when computing an integer DAC code from a float.

---

## 5. Load path

The cal table is filled in two stages at boot:

1. **Seed compiled defaults** into every record (§6). This yields a working but
   uncalibrated instrument.
2. **Overlay the cal file:** read `calibration.dat`, validate the word-0 checksum
   (§2.1), de-scramble (§2.2), then unpack the records and overlay each per-V/div
   record per §4.2.

Boot sequence:

1. Seed `0x32ced8` with the compiled defaults.
2. Read `calibration.dat`; compute the word-0 checksum over its 2748 payload
   bytes (§2.1).
3. On checksum **PASS**, de-scramble and overlay the file's records.
4. On checksum **FAIL**, retry the whole step with `calibration_bak.dat`.
5. On both **FAIL**, keep the compiled defaults and emit *"Calibration memory
   lost"*.

The **only** accept/reject condition is the word-0 checksum (§2.1). Do not gate
on any secondary checksum or guard word.

The formulae (§7) are static firmware; only the record **values** come from the
file.

---

## 6. Compiled-default fallback

When no valid cal file is present, seed every record with generic constants. CH0
and CH1 share the same default ladder **shape**; CH1 carries small per-channel
trims (below). For each of the 12 V/div records per channel:

- **`+0x08` and `+0x12` OFFSET-zero code** — `0x27ef` = 10223 (write to both;
  `+0x12` mirrors `+0x08` via a `r[9] ← r[4]` copy loop).
- **`+0x00` GAIN-DAC code** — one per decade, seeded by the boot default loader:
  `0xe6, 0xa8, 0x94, 0x5e, 0x45, 0x20, 0x10, 0x08, 0x0d, 0x1c, 0x48, 0x05`
  (`230, 168, 148, 94, 69, 32, 16, 8, 13, 28, 72, 5`).
- **`+0x04` GAIN f32** — one per decade, read verbatim from the firmware literal
  pool @`0x1b208c…0x1b20cc`. The boot default loader seeds **both channels from
  this same 12-value ladder**, so the compiled-default CH1 is identical to CH0:
  CH0 = CH1 = `19.190, 8.845, 3.801, 1.841, 0.936, 16.495, 8.547, 4.206, 1.719,
  0.902, 0.426, 0.169` (endpoints `0x41998553` … `0x3e2d844d`; note the
  `0.936→16.495` per-range break at index 4→5 — a monotonic interpolation between
  the two pinned endpoints is **wrong**, it drops the break; the interior hex
  encodings are not independently pinned).
  **Open:** a real per-unit cal file carries a distinct CH1 sibling set with small
  per-decade trims (representative values near their CH0 siblings: `0.9364, 8.347,
  4.306, 0.852, 0.168`); the full 12 CH1 per-unit literals are not enumerated here
  and are per-unit file data, not compiled defaults.

These are **generic** firmware defaults, not any specific unit's calibration, and
give a working-but-uncalibrated scope until the cal file overlays real per-unit
values. A real unit's gain ladder differs and carries a per-range break — e.g.
CH0 `0.176, 0.439, 0.878, 1.757, 4.392, 8.784, 17.568, 0.977, 1.954, 3.907,
9.768, 19.536` with GAIN-DAC codes `219, 164, 146, 63, 25, 12, 6, 115, 57, 28,
11, 6`.

> **Note.** The `+0x00` GAIN-DAC codes above are firmware constants seeded by the
> boot default loader — not per-unit data. A clone may instead drive the
> per-detent codes from its own V/div ladder (§7.2/§7.3/§7.6); either source
> yields a working-but-uncalibrated front end until the cal file overlays real
> per-unit GAIN-DAC codes into `+0x00`.

---

## 7. Runtime consumption

The cal record feeds three DAC paths. Two vertical DACs (gain, offset) — the
fine-gain DAC on the SPI plane and the offset DAC on the CS3 config plane; the
trigger level DAC is also on the CS3 config plane. **None of these paths ever
writes the FPGA config port `0x2010000e`** — that is the only register that
reconfigures the FPGA, and it must never be written at runtime.

### 7.1 Display scale (code → volts)

The render vertical scale is **32 codes/div** (`256/8`): a calibrated
1-division signal occupies exactly 32 captured codes about screen centre.

Per-detent vertical gain is done in **HARDWARE** — the fine-gain DAC (record
`+0x00`, §7.2) plus the coarse-range relay (§7.3) — so a 1-division signal always
occupies the same raw codes/div regardless of V/div. The render therefore applies
a **constant** scale, not a per-detent one; this matches the boot firmware's own
auxiliary scale table (`0x32d7ec`, §4.3) being all `1.0`. Two regimes:

- **Analog regime** (cal present, real front-end driving the channel): the HW
  gain DAC has already normalised the detent, so the render passes codes straight
  through with the constant `analogRenderScale = 1.0`:
  `displayed = (code − 128)·1.0 + 128`.
- **Digital-zoom regime** (no cal / no relay, e.g. the bench harness): analog gain
  is fixed at the reference detent `vdivRef = 0.5 V/div` and V/div is a pure
  digital magnification about centre 128: `g = vdivRef / V`,
  `displayed = (code − 128)·g + 128`.

The `+0x04` GAIN coefficient is **not** the render vertical-gain term (applying it
per V/div double-scales: because the cal GAIN tracks V/div within a coarse range,
`GAIN/Vdiv` cancels and adjacent detents render at identical amplitude) and is
**not** the offset-DAC slope. Its only runtime consumer is a diagnostic — the
channel's true DC in volts:

```
analogVdivK = 110.0
dcVolts = (meanCode − 128) · GainCoeff(ch, vd) / analogVdivK
```

which is detent-invariant (`codes_per_volt = analogVdivK / GAIN`, so higher-gain
detents produce proportionally more codes per volt). `analogVdivK` is a
volts-diagnostic constant only; it is **not** in the render/display path.

### 7.2 Fine analog gain DAC (`/dev/spidev1.1`)

The per-detent fine analog gain uses record `+0x00` (GAIN-DAC code):

- **Device:** `/dev/spidev1.1`, opened **mode 3, 8-bit, 300 kHz**, MSB-first.
  (A second fd on the same node — mode 0, 24 MHz — is the bitstream-load fd; never
  use it for gain.)
- **Write:** store the 8-bit code into a 2-byte shadow (`buf[0]` = CH1,
  `buf[1]` = CH2), then emit **two consecutive single-byte `SPI_IOC_MESSAGE(1)`
  transfers: CH2 byte first, then CH1 byte**. No address/command byte; each
  transfer is `len=1, bits=8`.
- **Discipline:** re-emit **both** bytes on every change (an unseeded 0 for the
  other channel would collapse its gain). Seed both channels' shadows to the boot
  detent at open without emitting.

### 7.3 Coarse analog range (`/dev/spidev1.0`)

The coarse range doubles the analog ladder: relay-word `byte[ch] bit2`. bit2
rides the **full 24-bit relay word**, which must be rebuilt in full on every
detent change.

- **Device:** `/dev/spidev1.0`, opened **mode 3 (CPOL=1/CPHA=1), 24-bit,
  300 kHz**, MSB-first (`SPI_IOC_WR_MODE`=3, `SPI_IOC_WR_BITS_PER_WORD`=`0x18`,
  `SPI_IOC_WR_MAX_SPEED_HZ`=`0x493e0`). This is the relay carrier, distinct from
  the gain-DAC/bitstream node `/dev/spidev1.1`; it never touches the FPGA config
  port. Emit as a single 4-byte `SPI_IOC_MESSAGE(1)` transfer (`len=4`, `bits=24`).
- **Word (24-bit, little-endian):** `byte0 = CH1 control`, `byte1 = CH2 control`,
  `byte2 = (trigCoupling << 4) | (trigSrc << 2)`.
- **Per-channel control byte** (`byte0` = CH1, `byte1` = CH2):

  | bit | meaning | value |
  |-----|---------|-------|
  | 0 | bandwidth-limit | **1 = BWL off**, 0 = 20 MHz limit engaged |
  | 1 | GND coupling | 1 when coupling = GND |
  | 2 | **V/div coarse range** | 1 = attenuated high range (vd ≥ 7) |
  | 3 | DC coupling | 1 when coupling = DC |
  | 5 | enable | **always 1** |
  | 7 | CH2 base bit | preset only on `byte1` (CH2 base byte = `0xA0` = bit5+bit7) |

  Coupling is one field, not three bits: DC → bit3, AC → neither bit1 nor bit3,
  GND → bit1.
- **byte2:** `trigCoupling` high nibble = `0x7` (DC trigger coupling); `trigSrc` =
  0 (C1), 1 (C2), 2 (EXT). A DC-coupled C1-source word has `byte2 = 0x70`.
- **Open:** the CH2 `byte1` bit7 base-bit polarity/role is not decoded (it is set
  as part of the `0xA0` CH2 base but its function is uncaptured).

- Set bit2 for the attenuated high-V/div detents (V/div index ≥ 7, i.e.
  500 mV/div and up); clear it for the sensitive low detents (≤ 200 mV/div).
- Driving a detent = rebuild the **full** relay word (bit2 for the target channel)
  and re-send **both** gain-DAC bytes, so the gain is deterministic regardless of
  the prior detent. After the relay `Emit`, **settle ~400 µs** before writing the
  gain DAC.
- bit2 + the fine gain DAC together give a monotonic analog V/div ladder. This is
  the only coarse control; there is no second per-detent digital post-scale.

### 7.4 Vertical offset DAC (CS3)

The offset DAC moves the captured window's DC centre. It is on the CS3 config
plane and is flushed **only at the bus-idle frame boundary under the single-owner
engine** (never from a panel/render worker, never during a halt window). Like all
CS3/Gpmc writes it goes on the **inherited fd** — see the trap below.

**Registers** (CS3):

| Channel | low byte | high byte (latches) |
|---------|----------|---------------------|
| CH1 | `0x10` | `0x30` |
| CH2 | `0x11` | `0x31` |

**Write order:** low byte first, then high byte (the high byte self-latches; no
separate strobe), then re-assert the CS1 run word `0x35` so the front-end change
leaves the engine coherent.

**Volts → code.** The offset is **input-referred** (the DAC injects a level shift
ahead of the gain stage), so the slope is a **fixed constant in input volts** —
it is **not** scaled by V/div or the gain trim:

```
K     = 262                      DAC-codes per input-volt (fixed; env-tunable via SCOPE_OFFSET_K)
zero  = record +0x12             per-unit live-zero code (default 0x27ef = 10223)
code  = clamp16( round( zero − V·K ) )        clamp to [0, 0xFFFF]
```

The DAC is **inverting**: a positive offset yields a lower code (the trace moves
up). `0 V` programs exactly `zero`.

Inverse (code → volts): `V = (zero − code) / K`.

> **Trap — inherited fd.** The CS3/Gpmc device must **not** be freshly opened at
> runtime. A fresh `open()` of the node fails (`EFAULT`) post-takeover, because the
> driver's open path is opener-context-dependent. Reuse the fd inherited from the
> boot process tree and **never** close it.
>
> **Locating it:** scan `/proc/self/fd` and pick the descriptor whose `readlink`
> target equals the device node path — that number is the fd the boot chain
> (startup → agent → app, all direct children) already opened and chip-select
> initialised. Wrap that fd **number directly** (do **not** `dup` it — it must
> remain the same open file description as the boot holder) and clear any
> auto-close/finalizer so a stray `Close()` or GC can never drop the single shared
> fd for the whole process tree.

### 7.5 Trigger level DAC (CS3)

The HW trigger comparator threshold is one 16-bit DAC lane, mirrored to a sibling
lane so both comparator references match. Same plane, same frame-boundary
discipline, and same **inherited fd** as the offset DAC.

**Registers** (CS3):

| Lane | low byte | high byte (latches) |
|------|----------|---------------------|
| A | `0x14` | `0x34` |
| B | `0x15` | `0x35` |

> The CS3 `0x35` here is the **level lane-B high byte** — it is **not** the CS1
> run-word `0x35`. Different plane; do not conflate.
>
> The trigger-level DAC is these four CS3 lanes only (`0x14/0x34/0x15/0x35`).
> Registers `0x27–0x2a` are gain-cal (inert for trigger level on the free-running
> engine) and a `0x16` latch is a remote/SCPI path artefact — **neither** is the
> level DAC; do not drive the threshold through them.

**Write sequence** (at the frame boundary, single-owner, inherited fd):

1. `0x14 = lo`, `0x34 = hi`, `0x15 = lo`, `0x35 = hi` (both lanes equal; the high
   byte self-latches).
2. Re-arm the comparator: `CS1 0x00 = 0x80` (twice), then the normal engine
   arm (`0xC0` / `0x57` / `0xC3`). The frame loop does not self-heal a level
   change — the re-arm is required.

A bare `0x14/0x34` poke off this boundary wedges the display; the safety is
serialization + inherited fd + arm-boundary timing + the following re-arm, not a
latch strobe.

**Volts → code.** The level DAC rides the per-V/div cal ladder, so the exact map
is per-detent. A fit at 1 V/2 V-div: `code = 31434 (0x7aca) − 938·V` (higher level
→ lower code), rounded, clamped to 16 bits. For a HW sweep or other detents,
prefer the raw 16-bit code and/or the active cal record; treat the fit as exact
only at 1 V/2 V-div. Code `0` means "clear/none".

**Open:** the per-detent level-code map that rides the full cal ladder is not
tabulated; only the 1 V/2 V-div fit is pinned. Drive HW sweeps by raw code.

### 7.6 V/div ladder (detent → volts/div)

Both the coarse-range threshold (§7.3) and the cal-record index depend on the
V/div detent. The driven ladder has 11 detents:

| index | 0 | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 | 10 |
|-------|---|---|---|---|---|---|---|---|---|---|----|
| V/div | 2 mV | 5 mV | 10 mV | 20 mV | 50 mV | 100 mV | 200 mV | 500 mV | 1 V | 2 V | 5 V |

- Index **7 = 500 mV** is the coarse-range threshold (`≥ 7` sets relay bit2).
- Boot detent = index **8** (1 V/div); the engine does not drive the front-end at
  startup (it keeps the inherited boot analog range until the user turns the knob).
- The cal table holds 12 records per channel (indices 0..11); the ladder drives
  11 detents (2 mV..5 V). The 12th cal slot (10 V) exists in the record table but
  is not a driven detent on this ladder.

---

## 8. Load-bearing constraints

- **`firmdata0` is read-only.** Read the cal file; do not write the live volume in
  normal operation.
- **Per-unit data is not in the firmware.** The gain/offset/level constants exist
  only on the unit's flash. Ship the envelope, layout, and formulae; read the
  values at boot.
- **Accept on the word-0 checksum alone.** That single two's-complement additive
  checksum is the entire accept/reject gate (§2.1). Do not gate on any secondary
  checksum or guard word — that over-rejects valid files.
- **Never write the FPGA config port `0x2010000e`** from any cal/DAC path. The
  gain (spidev1.1), coarse-range (spidev1.0), and offset/level (CS3) writes are
  safe precisely because they never touch it.
- **CS3/Gpmc is inherited-fd only.** Never freshly `open()` the CS3/Gpmc node at
  runtime — a fresh open `EFAULT`s post-takeover. Reuse the fd inherited from
  the boot process tree and never close it. This applies to the offset DAC, the
  trigger-level DAC, and the panel-LED latch.
- **CS3 DACs are single-owner, frame-boundary only.** Flush the offset DAC and
  trigger-level DAC only from the engine owner at the bus-idle frame boundary
  (armed+filling, not in a `0xC8` halt), on the inherited fd. A bare poke off this
  boundary, or a second consumer touching the bus during a halt, wedges the
  display. Both DAC writes must be followed by the run-word re-assert / comparator
  re-arm noted above.
- **Re-send both gain-DAC bytes on every change**, and rebuild the full relay word
  for the target detent. Partial writes collapse the other channel's gain or leave
  a stale coarse-range bit.
- **Fall back on failure:** `calibration.dat` → `calibration_bak.dat` → compiled
  defaults, in that order, per §5.

---

## 9. Open items

- **Second file block — analog role** (§3.1): its payload location and field map
  are pinned (Block B guard `+0x698`, stride 8, `+0` offset/range-2 → RAM `+0x10`,
  `+4` GAIN-B → RAM `+0x0c`); its **analog role** (range-2 / probe-×10 offset vs
  fine offset) is not. Not loaded; not required for a correct first build.
- **Secondary tail-region field meanings** (§2.1): the layout is pinned (checksum
  `+0xaa8`, `0x18` guard `+0xaac`, 16-byte sub-record `+0xab0`); the 16-byte
  EXT-trig / probe-DAC field meanings are not decoded. It is **not** part of the
  main acceptance gate.
- **Trigger-level code map off 1 V/2 V-div** (§7.5): only the 1 V/2 V-div fit is
  pinned; the per-detent map that rides the full cal ladder is not tabulated.

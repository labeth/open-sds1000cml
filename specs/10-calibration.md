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

> **Non-blocking secondary region.** The tail of the payload carries an
> undecoded region (an EXT-trig / probe-DAC sub-record). Its field meanings are
> not decoded, and — critically — it is **not** part of the acceptance gate. Do
> not validate a secondary checksum or a guard word against it; that would
> over-reject. **Open:** the semantics of this tail region.

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

A representative unit's payload also contains a second stride-8 block (a
range-2 / probe-×10 GAIN-B + offset term). **It is not required.** A correct
loader parses only the block above and copies the OFFSET-zero into the live-zero
field unconditionally (§4.2). A Block-A-only load is complete and correct.

**Open:** the second block's payload location and its analog role (range-2 /
probe-×10 offset vs a fine-offset term; its f32 gain is ≈35–41 on a representative
unit) are not pinned. Do not wire it into the load path without a validated byte
map and role.

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
| `+0x04` | f32 | GAIN | per-decade gain coefficient; feeds the **display/render vertical-gain** path (§7.1) |
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
common path. The implementation needs no per-V/div scale divisor: the `+0x04`
GAIN coefficient is applied directly in the render vertical-gain path (§7.1). A
`+0.5` round bias (`0x3f000000`) is added when computing an integer DAC code from
a float.

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

When no valid cal file is present, seed every record with generic constants. The
same default arrays apply to **both channels** (CH0 and CH1 use one shared
ladder). For each of the 12 V/div records per channel:

- **`+0x08` and `+0x12` OFFSET-zero code** — `0x27ef` = 10223 (write to both;
  `+0x12` mirrors `+0x08`).
- **`+0x04` GAIN f32** — one per decade. Only the two endpoints are pinned:
  first decade ≈ `19.16` (`0x41998553`), last decade ≈ `0.169` (`0x3e2d844d`).
  The ten interior decade values are approximate seeds, not per-unit truth.

These are **generic** defaults, not any specific unit's calibration, and give a
working-but-uncalibrated scope until the cal file overlays real per-unit values.
A real unit's gain ladder differs and carries a per-range break — e.g. CH0
`0.176, 0.439, 0.878, 1.757, 4.392, 8.784, 17.568, 0.977, 1.954, 3.907, 9.768,
19.536` with GAIN-DAC codes `219, 164, 146, 63, 25, 12, 6, 115, 57, 28, 11, 6`.

**Open:** the compiled-default GAIN-DAC codes (`+0x00`) are not seeded by the load
path. An uncalibrated unit's analog gain comes from the driven V/div ladder
(§7.2/§7.3, the per-detent codes in §7.6), not from a cal-table default.

---

## 7. Runtime consumption

The cal record feeds three DAC paths. Two vertical DACs (gain, offset) — the
fine-gain DAC on the SPI plane and the offset DAC on the CS3 config plane; the
trigger level DAC is also on the CS3 config plane. **None of these paths ever
writes the FPGA config port `0x2010000e`** — that is the only register that
reconfigures the FPGA, and it must never be written at runtime.

### 7.1 Display scale (code → volts)

The render vertical scale is **32 codes/div** (`256/8`): a calibrated
1-division signal occupies exactly 32 captured codes about screen centre. The
`+0x04` GAIN coefficient is the **display/render vertical-gain** term, applied
per channel × V/div as

```
norm[i] = 32 * GainCoeff(ch,i) / (Vdiv[i] * analogVdivK)
displayed = (code − 128) * norm[i] + 128
```

so the per-detent analog-gain code jump across the range boundary is exactly
cancelled and the trace amplitude is continuous. The `+0x04` GAIN is **not** the
offset-DAC slope; the auxiliary scale table (§4.3) is `1.0`, so no extra per-V/div
divisor is applied.

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

The coarse range doubles the analog ladder: relay-word `byte[ch] bit2`.

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

- **Second file block** (§3.1): its payload location and analog role (range-2 /
  probe-×10 offset vs fine offset) are not pinned. Not loaded; not required for a
  correct first build.
- **Secondary tail region** (§2.1): the undecoded EXT-trig / probe-DAC
  sub-record's field meanings are not decoded. It is **not** part of the
  acceptance gate.
- **Trigger-level code map off 1 V/2 V-div** (§7.5): only the 1 V/2 V-div fit is
  pinned; the per-detent map that rides the full cal ladder is not tabulated.
- **Compiled-default GAIN-DAC codes** (§6): the `+0x00` default codes are not
  seeded by the load path; an uncalibrated unit's analog gain comes from the
  driven V/div ladder (§7.6).

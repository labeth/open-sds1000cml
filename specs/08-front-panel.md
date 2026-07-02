# 08 — Front Panel

The front panel is input-only from the CPU's point of view for rotation/press decode, plus a
CPU-driven LED latch. Physical events (button presses, knob rotation, knob presses) arrive as an
FPGA interrupt; the CPU reads the key/encoder matrix over the GPMC bus and decodes it in software.
Status LEDs are driven by writing a 16-bit shadow word to a CS3 latch, also over the GPMC bus.

Two hard facts shape everything below:

1. **The key-matrix read and the LED latch are on the GPMC bus.** The GPMC bus has exactly one
   owner — the acquisition engine goroutine — because the per-frame capture-halt is only safe if no
   other consumer touches the bus during the halt window. Therefore the panel worker never reads the
   matrix or writes the LED latch directly; it *requests* those operations and the engine performs
   them at the frame boundary (engine armed+filling, never mid-halt). See §2.
2. **The analog V/div front-end (spidev) is off the GPMC bus** and is driven directly by the panel
   worker, concurrently and safely. See §9.

This spec is self-contained enough to drive the panel and analog front-end. It has two declared
**hard prerequisites** whose numeric tables are reproduced here for convenience but owned elsewhere:
the timebase ladder (`04-timebase-and-bands.md`) and the trigger-level / offset-DAC write sequences
(`05-triggering.md`, `06-vertical-and-analog.md`). Where a value is reproduced, the owning spec is
the authority if they ever diverge.

---

## 1. Devices and file descriptors

| Purpose | Device | Access | Ownership |
|---|---|---|---|
| Key/encoder interrupt | `/dev/fpga_key` | SIGIO source only (never read for data) | **inherited fd** (reuse; do not open fresh) |
| Key-matrix + LED bus | `/dev/Gpmc` | ioctl register read/write | inherited fd, engine-owner only |
| Coarse V/div relay | `/dev/spidev1.0` | SPI write | panel worker, direct |
| Fine V/div gain DAC | `/dev/spidev1.1` | SPI write | panel worker, direct |

**Inherited-fd requirement (load-bearing).** A fresh `open("/dev/fpga_key")` returns **EFAULT**
("Bad address") in the post-boot state — that driver's `.open` is opener-context-dependent, exactly
like the `/dev/Gpmc` driver. The usable fd is the one opened at boot and passed down the process
tree. Locate it by scanning `/proc/self/fd` for the symlink target `/dev/fpga_key` and reuse it.
**Never `close()` it** — it is a shared boot fd. The driver's `.read` returns 0 with no
`copy_to_user`, so no key bytes are ever read from `fpga_key`; it exists only to deliver the
interrupt. All key/encoder *data* is read from the GPMC matrix registers.

---

## 2. Single-owner bus discipline and the engine command surface

The GPMC bus is owned solely by the acquisition-engine goroutine. A stray CS1/CS3 access during a
capture-halt window black-screens the instrument (engine collapse, recoverable only by reboot).
Every panel operation that touches the GPMC bus is therefore routed through the owner and applied at
the **frame boundary** — the top of the engine FSM loop, where the engine is armed and filling, not
halted.

The owner exposes exactly this command surface. The controller drives only these; it never issues a
GPMC access from its own goroutine.

| Command | Kind | Signature | Bus / plane | Applied |
|---|---|---|---|---|
| Read matrix | request/reply | `ReadMatrix() (snapshot [5]uint16, ok bool)` | GPMC CS1 (config-plane ioctl read) | owner reads at frame boundary, returns a snapshot |
| Set LEDs | dirty shadow | `SetLEDs(word uint16)` | GPMC CS3 latch (`0x09/0x0a/0x0b`) | owner flushes the 4-write strobe at the frame boundary (§8.1) |
| Set offset DAC | dirty shadow | `SetOffsetDAC(ch int, code uint16)` | GPMC CS3 (`0x10/0x30` C1, `0x11/0x31` C2) | owner flushes DAC **then re-asserts the CS1 run word `0x35`** at the frame boundary (see 06 §5.3) |
| Set trigger level | dirty shadow | `SetTrigLevel(code uint16)` | GPMC CS3 (`0x14/0x34` lane A, `0x15/0x35` lane B) | owner flushes the **4-write level quad + a full engine re-arm** at the bus-idle **Arm boundary** (see 05 §2.3) |
| Set timebase | staged flag | `SetTdiv(tdivS float64) (class, lo, hi uint16, envelope, ets, ok bool)` | engine state (band re-plan) | owner re-plans the acquisition band at the frame boundary |
| Set NORM mode | atomic flag | `SetNorm(bool)` | engine flag (not a GPMC write) | takes effect immediately |
| Set running | atomic flag | `SetRunning(bool)` | engine flag (not a GPMC write) | takes effect immediately |

- `SetTdiv` returns the routed band: `class`/`lo`/`hi` are the FPGA divisor-class words, `envelope`
  and `ets` flag the slow-envelope / equivalent-time paths, and `ok=false` means the requested tdiv
  is not in the divisor table (the caller leaves the band unchanged). The controller only needs
  `ok` and (for logging) `envelope`/`ets`.
- `SetNorm`/`SetRunning` are **atomic engine flags**, not frame-boundary-flushed GPMC writes: they
  set a boolean the FSM reads on its next iteration. STOP (`SetRunning(false)`) keeps the FSM armed
  and alive on the bus (so it never wedges) but publishes no frames (display holds); it does not
  stop servicing panel commands.

Implementation shape:

- Reads use a request/reply channel; writes set a "dirty" shadow word under a mutex. The owner
  drains all of these in one `serviceCommands()` call at the top of each FSM iteration. This is the
  **only** site where a panel/LED/offset/level GPMC access occurs.
- `ReadMatrix()` enqueues a reply channel (non-blocking; returns `ok=false` if the queue is full)
  and waits up to **200 ms** for the owner to answer. If the owner is in a long wait, the read fails
  and the caller retries on the next interrupt / re-sync tick.
- **Panel-response latency is bounded by the frame time.** Fast/deep bands (~15–32 fps) register a
  press in 30–66 ms; slow/roll bands can take up to ~250 ms. This is the correct cost of
  single-owner safety.

**A CS1 config-plane matrix read does NOT pop the sample FIFO** and is safe during fill, which is
why it can be serviced at the frame boundary without disturbing acquisition.

**Constructor contract.** The controller does not own the timebase ladder or the boot band — the
engine/timebase module injects them: `NewController(eng, fe, tdivs []float64, startTdiv float64)`.
`tdivs` is the ordered timebase ladder (ascending, from `04-timebase-and-bands.md`); `startTdiv` is
the boot band. `fe` may be nil (no analog front-end — V/div becomes inherited/digital only).

---

## 3. Interrupt input path

The panel worker arms an interrupt on the inherited `/dev/fpga_key` fd and decodes the matrix on
each interrupt, with a periodic safety re-sync.

1. Find the inherited `/dev/fpga_key` fd (§1).
2. Register a SIGIO handler / notification channel.
3. `fcntl(fd, F_SETOWN, getpid())` — direct this fd's SIGIO to this process (per-fd, opener-agnostic).
4. `fcntl(fd, F_SETFL, flags | O_ASYNC)` — enable async signalling.
5. Seed the decoder once (read the matrix, record the baseline) so the first real interrupt produces
   no spurious press.
6. Loop, selecting over:
   - **SIGIO** → request a matrix snapshot from the owner and decode it **with knob decode enabled**.
     Rate-cap these reads to ~150 Hz (≥6 ms gap): a fast knob spin fires interrupts far faster than
     detents and each read round-trips through the engine. Coalesce excess interrupts into one
     trailing read.
   - **~40 ms re-sync ticker** → request a matrix snapshot and decode it **with knob decode DISABLED**
     (buttons only).

**Degraded fallback (no SIGIO source).** If no inherited fd exists and a fresh open EFAULTs, SIGIO
cannot be armed. In that case a **40 ms poll loop is the ONLY event trigger available**, so the
fallback decodes **both buttons AND knobs** on each poll tick — a best-effort degraded mode that
accepts occasional mid-detent knob misreads because there is no interrupt to align to a detent. This
is the deliberate exception to the "never decode knobs on a timer" rule below, made only because no
better trigger exists. It is not the normal path.

**Why the 40 ms re-sync is mandatory (SIGIO path).** SIGIO is a **non-queued** signal. A knob-spin
burst fires interrupts faster than the worker drains them, so signals coalesce/drop. The button
decode is a 1→0 edge detector, so a dropped *release* edge leaves that button's shadow stuck "down"
and the button goes dead until a later full re-read. The 40 ms full re-read self-heals a dropped
edge within one tick.

**Why the SIGIO re-sync tick must NOT decode knobs.** A timer-driven read lands mid-detent and
misreads the quadrature phase. On the SIGIO path the interrupt *is* the per-detent step trigger (one
interrupt == one detent — see §5); knobs are decoded only on the SIGIO read, never on the 40 ms
re-sync tick. (This differs from the no-SIGIO fallback above, where the poll tick is the only
trigger and therefore must decode knobs.)

Benign residual: the very first press after boot is missed (the edge detector needs one read to seed
its baseline).

---

## 4. Key/encoder matrix — hardware mechanism

An 8×8 key matrix and 8 quadrature encoders are wired into four 16-bit **active-low** selector
registers, plus one shared magnitude counter. All are read on the GPMC CS1 config plane.

| Register (sel) | FPGA byte address | Contents |
|---|---|---|
| `0x64` | `0x202000C8` | matrix rows + encoder phase bits (group 0) |
| `0x65` | `0x202000CA` | matrix rows + encoder phase bits (group 1) |
| `0x66` | `0x202000CC` | matrix rows + encoder phase bits (group 2) |
| `0x67` | `0x202000CE` | matrix rows + encoder phase bits (group 3) |
| `0x69` | `0x202000D2` | **shared** knob step-magnitude counter (all knobs) |

- Byte address = CS1 window base `0x20200000` + `(sel << 1)`.
- **Idle = all ones (`0xFFFF`).** A held button or a low quadrature phase bit clears its bit (1→0).
- A **matrix snapshot** is the five words read in one pass, in order:
  `[0x64, 0x65, 0x66, 0x67, 0x69]`.
- **Quadrature is decoded and counted in HARDWARE.** There is no software quadrature state machine.
  The FPGA decodes phase and accumulates the sub-step count into `0x69`. A textbook 2-bit gray
  state machine is the wrong model — it cross-couples channels and rails values.
- `0x69` is a per-detent count (1 per detent when read once per interrupt), **not** read-to-clear and
  **not** a decaying pulse. Reading it once per SIGIO yields a stable count; dense polling produces a
  spurious "pulse/decay" artifact.

Within each selector word:

- Bit positions **6/7** and **14/15** are the two encoder quadrature phase pairs
  (`knobPhaseMask = 0xC0C0`). These bits are **never** dispatched as button edges.
- All other bit positions are matrix key bits, dispatched as buttons on a 1→0 edge.

---

## 5. Decode algorithm

Given a fresh snapshot and the previous snapshot:

### 5.1 Buttons (always decoded, both SIGIO and re-sync)

For each selector `0x64..0x67`, compute `pressed = prev &^ current` (newly cleared bits = new
presses). For each set bit in `pressed` **not** in `0xC0C0`, dispatch a button `(sel, bit)`. Then
store `current` as the new `prev`.

### 5.2 Knobs (decoded on the SIGIO read; also on the fallback poll — never on the SIGIO re-sync tick)

1. **Gate:** if `0x69 == 0`, this is a plain button interrupt — do nothing (no knob moved).
2. **Which knob:** walk the encoders in the **fixed FPGA priority order** below and take the FIRST one
   whose selector has a low phase bit. Exactly **one knob is serviced per interrupt** — this is the
   cross-coupling fix; do not accumulate per-selector deltas.
3. **Direction:** in that knob's selector word, `bitLo` low → **CW (+1)**; `bitHi` low → **CCW (−1)**.
   The encoder rests in the phase bit it last stepped to, so sustained one-way rotation holds that
   bit low every detent. Do NOT gate on phase *change* — that misses continued rotation.
4. **Magnitude:** `1` for the stepped 1-2-5 detented knobs (exactly one range step per detent);
   otherwise the accel-clamped `0x69` count (see §5.3).
5. Dispatch `(knobID, dir, steps)`. Stop — one knob per event.

Fixed priority order (highest first), with phase bits `(bitLo, bitHi)`:

| Priority | Knob | Selector | bitLo (CW) | bitHi (CCW) | Step type |
|---|---|---|---|---|---|
| 1 | HORIZ POSITION | `0x67` | 14 | 15 | continuous (accel) |
| 2 | CH2 POSITION | `0x67` | 6 | 7 | continuous (accel) |
| 3 | TIME/DIV | `0x66` | 14 | 15 | **stepped (1/detent)** |
| 4 | CH2 V/DIV | `0x66` | 6 | 7 | **stepped (1/detent)** |
| 5 | CH1 V/DIV | `0x65` | 14 | 15 | **stepped (1/detent)** |
| 6 | ADJUST | `0x65` | 6 | 7 | continuous (accel) |
| 7 | TRIG LEVEL | `0x64` | 14 | 15 | continuous (accel) |
| 8 | CH1 POSITION | `0x64` | 6 | 7 | continuous (accel) |

CW polarity reference: CH1 V/DIV rotated CW reads `0x65 = 0xBFFF` (bit14/bitLo low); CCW reads
`0x65 = 0x7FFF` (bit15/bitHi low).

### 5.3 Acceleration clamp

The continuous knobs map the shared `0x69` count through the velocity accel curve. Apply a **runaway
guard to the RAW `0x69` read first**, then map:

1. **Runaway guard (raw):** clamp the raw `0x69` count to a sane ceiling (**200**) to reject a
   glitched/absurd count before mapping. This guard sits **above** the legitimate maximum so it never
   caps a real fast spin.
2. **Accel map** (the mapped result is the final step count; `100` is the legitimate ceiling):

   | raw `0x69` | steps |
   |---|---|
   | ≤ 9 | the value itself |
   | 10–19 | 50 |
   | ≥ 20 | 100 |

Stepped knobs (TIME/DIV, CH1/CH2 V/DIV) ignore the magnitude entirely and always emit 1 step per
detent.

> The guard and the accel ceiling are distinct: the guard rejects glitch reads on the *input*; the
> accel map's `100` is the *output* ceiling. A guard set at or below 50 would silently make the
> "≥20 → 100" row unreachable — the guard must be ≥100.

---

## 6. Selector:bit map

Active-low; a press is a 1→0 edge on the listed bit of the listed selector. Codes are `sel<<8 | bit`.

### 6.1 Buttons

| Control | Selector | Bit |
|---|---|---|
| RUN/STOP | `0x65` | 2 |
| SINGLE | `0x65` | 10 |
| AUTO (Auto-Set) | `0x67` | 10 |
| CH1 | `0x66` | 4 |
| CH2 | `0x67` | 4 |
| PRINT / Hardcopy | `0x67` | 13 |

### 6.2 Softkeys (top → bottom, F1..F5)

| Softkey | Selector | Bit |
|---|---|---|
| F1 | `0x65` | 5 |
| F2 | `0x65` | 13 |
| F3 | `0x66` | 5 |
| F4 | `0x66` | 13 |
| F5 | `0x67` | 5 |

### 6.3 Knob rotation + push

Every knob has a push switch in addition to rotation.

| Knob | Rotate selector : bits (lo·hi) | Push selector : bit |
|---|---|---|
| CH1 V/DIV | `0x65` : 14·15 | `0x65` : 9 |
| CH1 POSITION | `0x64` : 6·7 | `0x64` : 1 |
| CH2 V/DIV | `0x66` : 6·7 | `0x66` : 1 |
| CH2 POSITION | `0x67` : 6·7 | `0x67` : 1 |
| TIME/DIV | `0x66` : 14·15 | `0x66` : 9 |
| HORIZ POSITION | `0x67` : 14·15 | `0x67` : 9 |
| TRIG LEVEL | `0x64` : 14·15 | `0x64` : 9 |
| ADJUST | `0x65` : 6·7 | `0x65` : 1 |

### 6.4 Menu / mode buttons — unmapped (gap)

The `(sel,bit)` matrix codes for the menu/mode buttons (CURSORS, ACQUIRE, DISPLAY, MEASURE,
SAVE/RECALL, UTILITY, REF, MATH) are **not established**. Their LED bits are likewise not fully known
(§8.2). The button and LED for each are on the matrix/latch, but the specific assignments must be
captured **one control at a time** on hardware (the same scan procedure that produced §6.1/§6.2):

- **Scan procedure:** with the decoder running and logging every raw `(sel,bit)` 1→0 edge, press one
  physical button in isolation and record the reported `(sel,bit)`; repeat per control. For the LED,
  drive one bit at a time (write a single-bit word through `SetLEDs`) and observe which lamp lights.
- **Until captured:** the decoder still emits these as generic `(sel,bit)` button events. The
  controller must **claim and ignore** any unmapped non-phase bit so it cannot cross-drive a mapped
  control. Do not wire a specific action to a guessed code.

Placeholder (fill from the scan):

| Control | Selector | Bit | LED bit |
|---|---|---|---|
| CURSORS | TBD | TBD | TBD |
| ACQUIRE | TBD | TBD | TBD |
| DISPLAY | TBD | TBD | TBD |
| MEASURE | TBD | TBD | TBD |
| SAVE/RECALL | TBD | TBD | TBD |
| UTILITY | TBD | TBD | TBD |
| REF | TBD | TBD | TBD |
| MATH | TBD | TBD | TBD |

**Open:** the `(sel,bit)` and LED bit for every §6.4 control.

---

## 7. Control wiring (event → instrument action)

| Event | Source | Action |
|---|---|---|
| TIME/DIV knob | `0x66`:14/15 | step the injected tdiv ladder one detent → `SetTdiv` (routes ETS / native-fast / deep / envelope / roll automatically) |
| CH1 V/DIV knob | `0x65`:14/15 | step `vIdx` → analog front-end `SetVdiv` (§9), off-bus |
| CH2 V/DIV knob | `0x66`:6/7 | step `vIdx` → analog front-end `SetVdiv` (§9), off-bus |
| CH1 POSITION knob | `0x64`:6/7 | step CH1 offset DAC code → `SetOffsetDAC(0, code)` (CS3, owner-flushed **with run-word re-assert**, see 06 §5.3) |
| CH2 POSITION knob | `0x67`:6/7 | step CH2 offset DAC code → `SetOffsetDAC(1, code)` (CS3, owner-flushed **with run-word re-assert**, see 06 §5.3) |
| TRIG LEVEL knob | `0x64`:14/15 | step level DAC code → `SetTrigLevel(code)` (CS3, owner-flushed as the **4-write quad + re-arm**, see 05 §2.3) |
| RUN/STOP button | `0x65`:2 | toggle running → `SetRunning`; update LEDs |
| SINGLE button | `0x65`:10 | enter NORM/arm (wait for a comparator edge) + running → `SetNorm(true)`+`SetRunning(true)`; update LEDs |
| AUTO button | `0x67`:10 | return to AUTO free-run trigger mode + running → `SetNorm(false)`+`SetRunning(true)`; update LEDs |

Stepping conventions and constants:

- **TIME/DIV / V/DIV are stepped knobs**: one detent = one ladder index regardless of magnitude. CW
  (dir=+1) → slower tdiv / larger V/div. The tdiv ladder + boot band are injected via the
  constructor (§2); the V/div ladder is in §9.
- **POSITION / TRIG LEVEL are continuous**: apply `dir * step * magnitude`, clamp to the DAC's linear
  region.

  **Offset (position) DAC** — the centre (0 V) seed is the **calibrated per-(channel, V/div) zero** from
  cal RAM record `+0x12` (spec 10 §7.4 / 06 §5.2); `10600` is only the **uncalibrated fallback** centre.
  `20` codes/accel-step, clamp `[9600, 11600]`; higher code → lower mean.
  `nc = offCode[ch] + dir*20*steps`, clamped.

  **Trigger level DAC** — code assembled and written by the owner (05 §2). Constants (valid at
  1 V/div and 2 V/div):

  | Constant | Value | Meaning |
  |---|---|---|
  | `trigCenter` | `31434` (`0x7aca`) | 0 V threshold |
  | `trigStep` | `40` codes / accel-step | ≈ 0.043 V/step @1 V/div |
  | `trigMin` | `27000` (`0x6978`) | ≈ +4.7 V @1 V/div |
  | `trigMax` | `35000` (`0x88b8`) | ≈ −3.8 V @1 V/div |
  | slope | `−938` codes/V | **higher threshold → lower code** |

  Sign: CW (dir=+1) **raises** the level, which **lowers** the code:
  `nc = trigCode − dir*40*steps`, clamped to `[trigMin, trigMax]`.

- **Startup discipline:** seed all shadow state to the inherited boot detents but drive **nothing** at
  startup (no front-end / offset / level write) — the inherited boot analog range and engine band
  stay untouched until the user turns a knob. The initial LED word (§8) is the one exception and is
  pushed at startup.

**Partial / unwired bindings:** HORIZ POSITION, ADJUST, softkeys, §6.4 menu buttons, and CH1/CH2
on/off are decoded but not all wired to engine state (no horizontal-position / menu plumbing yet).
The AUTO button is bound to "return to AUTO free-run trigger mode", not a full autoscale. Unwired
controls must be **claimed and ignored** so they cannot cross-drive another control.

---

## 8. Panel LEDs — CS3 latch

The status LEDs are driven by writing a single 16-bit shadow word to a CS3 latch over the GPMC bus.
This is CPU-driven; it must be flushed by the bus owner at the frame boundary (§2), never from the
panel goroutine.

### 8.1 Strobe sequence (order is mandatory; single-owner, one indivisible burst)

§8.1 is the sequence the **bus owner** performs when it services a `SetLEDs(word)` request during its
frame-boundary flush. Emit it as **one indivisible 4-write burst** — do not split it across FSM
iterations, and do not interleave it with the offset (`0x10/0x30`) or level (`0x14/0x34`) CS3
flushes that share the same flush site. A concurrent or interleaved strobe corrupts the latch; a
stray CS3 access during a halt window collapses the engine.

Write, in this exact order, on CS3:

1. `WriteRegCS(3, 0x0b, 0)` — strobe low (FPGA `0x20100016`)
2. `WriteRegCS(3, 0x0a, word >> 8)` — high byte (FPGA `0x20100014`)
3. `WriteRegCS(3, 0x09, word & 0xff)` — low byte (FPGA `0x20100012`)
4. `WriteRegCS(3, 0x0b, 1)` — strobe high → latches the word

The `0x0b=0` leading strobe is required; omitting it means the latch never captures the word.

### 8.2 Bit → LED map

Only these bits are established on this clone:

| Bit | LED |
|---|---|
| `0x0004` | TRIG'd |
| `0x0010` | CH2 |
| `0x0020` | CH1 |
| `0x2000` | RUN (green element) |
| `0x4000` | STOP (red element) |
| `0x8000` | SINGLE |

- RUN/STOP is one bicolor LED: `0x2000` = green, `0x4000` = red; both set = amber.
- Writing `0xFFFF` is a lamp-test (every LED on).
- Typical state word: `CH1 | CH2` always, plus `RUN(0x2000)` while running or `STOP(0x4000)` while
  stopped, plus `SINGLE(0x8000)` in NORM/arm mode. The idle state is STOP (`0x4000`).
- This clone's low-byte wiring does **not** follow any presumed internal LED index order — use the
  bits above, not an index guess.

**Open:** the bit positions for the §6.4 menu/mode LEDs (CURSORS, ACQUIRE, DISPLAY, MEASURE,
SAVE/RECALL, UTILITY, REF, MATH) and any low-bit lamps beyond the six above are **not established**.
Capture them one bit at a time (§6.4 scan). Do not populate them from an assumed index table — that
order does not hold on this board.

**Trap:** this must be a single-owner flush like every other CS3 write. Treat a non-effect as harmless
(on a differently-wired board the latch may be MCU-owned); either way it must never disturb the
engine.

---

## 9. Analog V/div front-end (off-bus)

The analog vertical range is set over spidev, which is **not** on the GPMC bus, so the panel drives
it directly and concurrently (no owner round-trip). The full calibration and coupling story is in
`06-vertical-and-analog.md`; the panel-relevant transport, layout, and constants are reproduced here
so the front-end can be driven from this spec.

### 9.1 SPI transports and configuration

Open both **fresh** with `O_RDWR` (they are not the GPMC/fpga_key inherited-fd class), configure once,
never close while running.

**`/dev/spidev1.0` — coarse relay word.** Config ioctls (each: request code, value):

| ioctl | Request code | Value |
|---|---|---|
| `SPI_IOC_WR_MODE` | `0x40016b01` | `3` (mode 3, CPOL=1 CPHA=1) |
| `SPI_IOC_WR_BITS_PER_WORD` | `0x40016b03` | `0x18` (24 bits) |
| `SPI_IOC_WR_MAX_SPEED_HZ` | `0x40046b04` | `0x000493e0` (300 kHz) |

Each write is a single 24-bit word sent as one `spi_ioc_transfer` via `SPI_IOC_MESSAGE(1)`
(`0x40206b00`): `tx_buf=&word, rx_buf=0, len=4, bits_per_word=0x18, speed_hz=300000`, MSB-first.

**`/dev/spidev1.1` — fine gain DAC.** Config ioctls:

| ioctl | Request code | Value |
|---|---|---|
| `SPI_IOC_WR_BITS_PER_WORD` | `0x40016b03` | `8` |
| `SPI_IOC_WR_MODE` | `0x40016b01` | `3` (mode 3) |
| `SPI_IOC_WR_MAX_SPEED_HZ` | `0x40046b04` | `300000` |

Each gain update sends **two separate single-byte transfers** (not one 2-byte transfer), each its own
CS-framed `spi_ioc_transfer{tx_buf=&code, len=1, bits_per_word=8}` via `SPI_IOC_MESSAGE(1)`. There is
**no address/command byte** — the raw 8-bit code is the whole payload; the channels are distinguished
only by order: **CH2 byte first, CH1 byte second.** Both channel codes are re-sent on every change.

### 9.2 Relay word layout

24-bit, little-endian: `word = byte0 | (byte1<<8) | (byte2<<16)`, where `byte0` = CH1 control byte,
`byte1` = CH2 control byte, `byte2 = (trigCoupling<<4) | (trigSrc<<2)`.

Per-channel control byte:

| Bit | Meaning | Notes |
|---|---|---|
| 0 | Bandwidth limit | **1 = full BW**, 0 = 20 MHz limit |
| 1 | GND coupling select | inert on this clone (relay unpopulated) |
| 2 | **Coarse V/div range** | **1 = attenuated/high range** (500 mV/div…5 V/div), 0 = sensitive/low range |
| 3 | DC coupling select | selects a DC offset-cal entry, not a coupling cap |
| 5 | Constant enable | always 1 |
| 7 | CH2 base preload | CH2 byte base `0xA0`; CH1 byte base `0x20` |

`byte2`: `trigCoupling` high nibble = `0x7` for DC; `trigSrc` = 0 (C1) / 1 (C2) / 2 (EXT).

### 9.3 V/div ladder (constants)

11 detents per channel, 1-2-5:

| idx | V/div | Coarse range bit2 (`idx≥7`) | Gain-DAC code |
|---|---|---|---|
| 0 | 2 mV | 0 | 146 |
| 1 | 5 mV | 0 | 146 |
| 2 | 10 mV | 0 | 146 |
| 3 | 20 mV | 0 | 63 |
| 4 | 50 mV | 0 | 25 |
| 5 | 100 mV | 0 | 12 |
| 6 | 200 mV | 0 | 6 |
| 7 | 500 mV | 1 | 115 |
| 8 | 1 V | 1 | 57 |
| 9 | 2 V | 1 | 28 |
| 10 | 5 V | 1 | 11 |

- `rangeHi = (vIdx ≥ 7)` — the coarse relay bit2 is set for the attenuated high-V/div detents.
- `gainCode` per detent is the table above (CH2 shares it). idx 0/1 are below the analog floor and
  reuse idx 2's code (146); the display digital-zooms below 10 mV.
- **`BootVdivIdx = 8` (1 V/div).** Seed both channels' `vIdx` here; do **not** drive the front-end at
  startup (leave the inherited boot analog range until the user turns the knob).

### 9.4 Drive rules

- **Absolute writes only.** For every detent change, rebuild the *full* relay word (bit2 for the
  target detent) and re-send *both* gain-DAC bytes, so the gain is deterministic regardless of the
  prior detent. A relative/partial write makes the gain history-dependent and can collapse the
  untouched channel's gain.
- **Order per `SetVdiv`:** set + emit the relay word → **settle ~400 µs** → set + emit both gain bytes
  (CH2 then CH1).
- **Seed both channels' shadows to the boot detent at open without emitting.** The first user V/div
  change then re-emits both channels' correct codes. An unseeded 0 code on the untouched channel would
  collapse that channel's gain the first time the DAC is emitted.
- A relay-only front-end (gain DAC unavailable) is acceptable — coarse range works, fine trim off.

Because these writes never touch the FPGA config port (`0x2010000e` / nCONFIG), they cannot disturb
CONF_DONE or the engine — that is why they are safe off the bus.

---

## 10. Load-bearing constraints (summary)

1. **Reuse the inherited `/dev/fpga_key` fd**; a fresh open EFAULTs. Never close it. Never read data
   from it (it carries none) — it is a SIGIO source only.
2. **Matrix read and every CS3 latch/DAC write are GPMC operations** and must go through the single
   bus owner at the frame boundary — never a concurrent goroutine, never during a capture-halt window.
3. **The LED strobe is one indivisible 4-write burst** at the flush site, not interleaved with the
   offset/level CS3 flushes and not split across FSM iterations (§8.1).
4. **The offset DAC write must be followed by a CS1 run-word `0x35` re-assert** (06 §5.3); the trigger
   level write is a **4-write quad + full engine re-arm at the bus-idle Arm boundary** (05 §2.3). A
   bare write of either black-screens the display.
5. **On the SIGIO path, decode knobs only on the interrupt read**, never on the 40 ms re-sync tick (a
   timer read lands mid-detent and corrupts the quadrature phase). The only exception is the no-SIGIO
   **fallback poll**, where the tick is the sole trigger and must decode knobs (accepting misreads).
6. **The 40 ms re-sync is mandatory** on the SIGIO path because SIGIO is non-queued; a coalesced
   dropped release edge otherwise leaves a button stuck down.
7. **One knob per interrupt**, chosen by fixed priority — accumulating per-selector deltas
   cross-couples channels.
8. **Read `0x69` once per interrupt**; it is a stable per-detent count, not a decaying pulse. Apply the
   runaway guard (≥100, e.g. 200) to the raw count *before* the accel map (§5.3).
9. **Analog V/div writes are absolute** (full relay word + both gain bytes) and off-bus; seed shadows
   without emitting at open; settle the relay ~400 µs before the gain DAC.
10. **Never write the FPGA config port** (`0x2010000e`) at runtime from any panel path.
11. **Clear/seed reused decoder shadow state** (the baseline matrix) at startup so the first interrupt
    produces no spurious press.

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

### 1.1 `/dev/Gpmc` register transport (ioctl ABI)

Every GPMC register access — the matrix read, the LED latch, the offset/level DAC flushes — is one
`ioctl(gpmcFd, req, &buf)` where `buf` is a **6-byte struct** and `req` selects read vs write:

| Direction | Request code |
|---|---|
| Read | `0x80026700` |
| Write | `0x40026701` |

The 6-byte argument is:

```
buf[0] = chip-select plane   (1 = CS1 acquisition window, 3 = CS3 config selectors)
buf[1] = 0
buf[2] = sel & 0xff          selector, UN-shifted
buf[3] = (sel >> 8) & 0xff   (the kernel does the <<1 to form the byte address; do NOT pre-shift)
buf[4] = val & 0xff          value, little-endian
buf[5] = (val >> 8) & 0xff
```

- **`buf[0]` is load-bearing.** The kernel computes `index = buf[0] − 1` and picks the ioremap base
  from that index. `buf[0] = 0` yields `index = 0xFF` → a garbage base → the access **stalls for
  seconds** (this is the root of every "writes don't work / post-kill stall" symptom). Always set the
  plane: `1` for CS1, `3` for CS3.
- **Read vs write is the request code, not `buf[0]`.** A read reuses the same struct with `val = 0`
  and returns the register value little-endian in `buf[4..5]`.
- `WriteReg(sel,val)` / `ReadReg(sel)` are the CS1 shorthands (`buf[0]=1`); `WriteRegCS(3,sel,val)` /
  `ReadRegCS(3,sel)` target CS3. A selector means different things per plane (e.g. `0x09` is a reject
  byte on CS1 but the LED-latch low byte on CS3), so config actuators must name the plane; a CS3 write
  that lands on CS1 silently fails.

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
| Set offset DAC | dirty shadow | `SetOffsetDAC(ch int, code uint16)` | GPMC CS3 (`0x10/0x30` C1, `0x11/0x31` C2) | owner flushes DAC **then re-asserts the CS1 run word (selector `0x35`)** at the frame boundary (§8.3) |
| Set trigger level | dirty shadow | `SetTrigLevel(code uint16)` | GPMC CS3 (`0x14/0x34` lane A, `0x15/0x35` lane B) | owner flushes the **4-write level quad + a full engine re-arm** at the bus-idle **Arm boundary** (§8.4) |
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

### 6.4 Menu / mode buttons

Active-low `(sel,bit)`; press is a 1→0 edge. Each printed menu key opens its tab of the settings
sheet (§7.2) and, pressed again on that tab, closes it. The lamp column is the CS3-latch bit
(§8.2, the map is spec 02 §7.5) the key's lamp is driven on; "none" marks a key without a lamp.

| Control | Selector | Bit | Opens | Lamp |
|---|---|---|---|---|
| TRIGGER MENU | `0x64` | 10 | TRIG tab | none |
| ACQUIRE | `0x64` | 11 | ACQ tab | `0x0200` — acquisition not Normal, or super-res on |
| CURSORS | `0x64` | 12 | CURS tab | `0x0002` — the cursors are on |
| MENU (on/off) | `0x64` | 13 | the workspace's sheet (§7.2) | none |
| DISPLAY | `0x65` | 3 | VIEW tab | `0x0400` — a view other than Y-T, or persistence |
| MEASURE | `0x65` | 4 | MEAS tab | `0x0100` — readouts on, one or more chosen |
| SAVE/RECALL | `0x65` | 11 | FILE tab | `0x0800` — the settings are those last saved or recalled |
| HORIZONTAL MENU | `0x65` | 12 | TIME tab | none |
| UTILITY | `0x66` | 3 | BUS tab | `0x1000` — a bus is decoded |
| MATH | `0x66` | 12 | MATH/REF tab, Math row | `0x0020` — a math trace is on |
| REF | `0x67` | 12 | MATH/REF tab, Store Ref A row | `0x0080` — REF A or REF B is shown |
| HELP | `0x67` | 11 | SYS tab, the guide to the workspace | none |
| CH1 | `0x66` | 4 | CH1 tab (switches an off channel on) | `0x0010` — CH1 shown |
| CH2 | `0x67` | 4 | CH2 tab (switches an off channel on) | `0x0040` — CH2 shown |

### 6.5 Remaining control buttons

| Control | Selector | Bit | Action |
|---|---|---|---|
| SET TO 50% | `0x66` | 2 | trigger level at the midpoint of the source channel's frame; without a signal there a notice says so and the level stays |
| DEFAULT SETUP | `0x66` | 10 | factory settings, the mask and the serial trigger's match bytes cleared; any menu closes; the workspace stays (§7.1) |
| FORCE | `0x67` | 2 | one untriggered capture (`ForceTrigger`) |
| PRINT | `0x67` | 13 | the screen as `SDSnnnn.png` on the U-disk, named in a notice |

- **Scan procedure (for any future board revision):** with the decoder running and logging every raw
  `(sel,bit)` 1→0 edge, press one physical button in isolation and record the reported `(sel,bit)`;
  repeat per control. For an LED, drive one bit at a time (write a single-bit word through `SetLEDs`)
  and observe which lamp lights.

---

## 7. Control wiring (event → instrument action)

The panel is driven by **task workspaces** (ADR-TASK-WORKSPACES): five screens for the jobs the
instrument is used for, each re-laying the LCD (spec 07 §10.3) and giving the two tool knobs (Adjust
and Horizontal Position) a job named on the screen, over one declarative settings model whose rows
the sheets, the knobs, the F-keys and the remote all change through the same setter. Every key is
wired; none is claim-and-ignore.

### 7.1 Workspaces (no menu open)

F1..F5 select the workspaces on a pictogram rail beside the keys; the active workspace's key again
shows its next view. Entering a workspace shows a short label beside its key for 1.6 s
(`F3 SPECTRUM   again: FFT / Spectrogram / Bode`).

| Key | Workspace | Pane | Key again |
|---|---|---|---|
| F1 | SCOPE | none: the full trace | Y-T ↔ X-Y |
| F2 | BUS | the decoded packets: a fixed-column hex dump, a separate string box, the match | Packets ↔ eye diagram |
| F3 | SPECTRUM | the FFT large, a small time trace, a peak table, marker, THD | FFT → spectrogram → Bode |
| F4 | MEASURE | statistics (now, mean, min, max, sdev, N) of the chosen readouts, cursors | statistics ↔ all values |
| F5 | COMPARE | MASTER, REF A, REF B, MASK, MATH rows, live difference | stack a master (again: cancel) |

Entry behaviour: BUS with decode off auto-detects the bus on CH1/CH2 and decodes it, with a notice
(decoding stays on after leaving BUS). The search reads a decimated record first (a few thousand
filtered samples a screen, tens of milliseconds) and keeps a bus whose shortest feature spans 8 of
its samples (6 for CAN and FlexRay, whose CRCs a misread record does not pass; a UART only at a rate
UARTs use); else a full-rate record of the timebase on screen, unless the decimated record was the
full-rate one already, resolves every edge it holds, or the timebase is slower than the short record
below (a megasample at 200 us/div takes seconds). Each lease ends with its phase (the web's Detect
alike). A reading of one byte, a UART with a fifth of its bytes misframed or an I2C clock faster than
5 MHz is no bus. With no bus, or only a UART of under 4 bytes or at a rate no UART uses (as likely part
of another bus's frame), the search looks again at the slowest timebase sampled at the full rate: its
decimated record, then a full-rate record a quarter as long (a fast bus's short frames, in under a
second), then its whole record. The timebase the bus is found at stays (found on the short record,
BUS steps to the slowest timebase up to the full-rate one whose live view reads it); with nothing
found the timebase goes back and the scant UART, if any, is named. SPECTRUM starts on the FFT; MEASURE with nothing chosen picks
Vpp and frequency of the shown channels. Each workspace remembers its view. A master (COMPARE again)
averages N captures of the trigger source (the engine's Average, N = 64 by default; under a serial
trigger aligned on the match's own edge, as the live captures are), stores the result as REF A,
restores the acquisition so live captures are tested raw, builds the mask from the master in the
geometry of the live captures once three in a row share it (the averaged record is not that geometry:
on the SRAM images a filtered record at another sample interval than the live peak-detect readouts),
and turns the test on
with zeroed counters; it is refused, saying how to fix the trigger, while the trigger source does not
repeat from capture to capture. COMPARE's MASK row push, once a mask
exists: a mask built with its test off starts counting, counting stops on a fail, and after stop on
fail the push clears the mask (the sheet's Mask test turns a test off and keeps the mask; its Clear
mask row removes it at any time, as does a web page's Clear mask). Clearing turns the test off, zeroes
its counts and captured failures, and stops a mask build or master under way. AUTO and DEFAULT SETUP
close any menu and keep the workspace, which re-applies its entry behaviour; DEFAULT SETUP also clears
the mask and the serial trigger's match bytes (no bytes, any I2C address and direction), puts the mask
build's frames and tolerance, super-res's channel, grid, stop and target, the averaging count (16),
ERES length (1), precision rate (31.25 MSa/s), roll mode (chunked), the FFT source (C1), the master
depth (64) and the stream's stop (on an error) back, chooses no measurement (MEASURE then measures Vpp
and Freq), and turns the frequency response off on C1 / C2 with its points forgotten. The screen saver,
screen off and remote notices are the instrument's preferences and stay. The VIEW tab chooses the frequency
response's reference and DUT channels (picking the other's channel swaps them) and clears its points.

### 7.2 Settings sheets (a menu open)

MENU opens the workspace's sheet where it was left (the first time on its own first tab); the printed
keys (§6.4) open their tabs. A sheet is full screen with every option written out; the workspace's
own tabs come first. While a sheet (or the BUS byte picker) is open, F1..F5 pick the choices written
beside them in the 116 px F-key column; actions run only from their labelled F-key.

| Control | In a sheet |
|---|---|
| TIME/DIV | the previous / next tab (wrapping); leaves an edit or a sub-mode |
| ADJUST | the row (wrapping); in an edit: the value; in the match editor: the key |
| ADJUST push | edit the row (again: done); on Match bytes: the match editor; in the editor: type the key |
| HORIZ POSITION | the row's value: the next option, or a knob step; in the editor: the cursor |
| HORIZ POSITION push | the row's factory value; in the editor: clear |
| F1..F5 | the row's choices; a number row: Down, Up and presets; an action row: its actions |
| MENU / the tab's key | close the sheet (a sub-mode: discard it) |

The BUS tab's Match bytes row opens the match editor (a full-screen keyboard of Hex, Text and Special
keys, 32 units at most: F1 key set, F2 type, F3 delete, F4 OK, F5 cancel) or the byte picker (the
bytes decoded on screen, the first complete message's first byte highlighted; F1..F4 trigger on 1..4
bytes from it and set the trigger type Serial; F5 cancel).

### 7.3 Knobs and pushes

V/DIV, vertical POSITION, TRIG LEVEL and TIME/DIV keep their printed jobs in every workspace, and the
pushes theirs (Variable, Zero, Zoom). ADJUST and HORIZ POSITION are each workspace's tools.

| Control | Source | Action |
|---|---|---|
| TIME/DIV knob | `0x66`:14/15 | step the injected tdiv ladder → `SetTdiv`; zoomed: the zoom factor; a sheet: its tab |
| TIME/DIV push (Zoom) | `0x66`:9 | zoom on (the last factor, 10× the first time) / off; BUS Packets: zoom onto the selected packet |
| CH1 / CH2 V/DIV knob | `0x65`:14/15, `0x66`:6/7 | step `vIdx` → analog front-end `SetVdiv` (§9), off-bus |
| CH1 / CH2 V/DIV push (Variable) | `0x65`:9, `0x66`:1 | a notice: the front end has only its 12 calibrated steps, no fine V/div |
| CH1 / CH2 POSITION knob | `0x64`:6/7, `0x67`:6/7 | step the offset → `SetOffsetDAC(ch, code)` (owner-flushed **with run-word re-assert**, §8.3) |
| CH1 / CH2 POSITION push (Zero) | `0x64`:1, `0x67`:1 | offset back to 0 V |
| TRIG LEVEL knob | `0x64`:14/15 | step the level code → `SetTrigLevel(code)` (the **4-write quad + re-arm** at the Arm boundary, §8.4) |
| TRIG LEVEL push (Zero) | `0x64`:9 | the level to 0 V |
| RUN/STOP | `0x65`:2 | toggle running → `SetRunning` (a decoded stream: stop / restart it; a web page's decoded capture: end it first) |
| SINGLE | `0x65`:10 | Normal, one triggered capture, then stop (a web page's decoded capture ends first) |
| AUTO | `0x67`:10 | autoset (a second press cancels the sweep; a web page's decoded capture ends first) |

Workspace tools (no menu open):

| Workspace | ADJUST | ADJUST push | HORIZ POSITION | HORIZ POSITION push |
|---|---|---|---|---|
| SCOPE | the last number set in a sheet (tagged in the bottom row), else the active cursor | open the sheet at that row | trigger position (zoomed: the zoom window) | centre |
| BUS Packets | select a packet (zoomed: pan to it) | the byte picker (no bus: detect again) | the next / previous packet holding the match | back to the trigger |
| BUS eye | the threshold cursor | clear the eye | the sample-point cursor | both cursors to the crossing and the eye centre |
| SPECTRUM | the marker peak to peak (Bode: point to point) | a delta reference at the marker (again: off) | the marker bin by bin | the largest peak (Bode: follow the sweep) |
| MEASURE | cursor A | cursors off / time / volts | cursor B | zero the statistics |
| COMPARE | the row | the row's action (written at its right end) | trigger position | centre |

Stepping conventions and constants:

- **TIME/DIV / V/DIV are stepped knobs**: one detent = one ladder index regardless of magnitude. CW
  (dir=+1) → faster tdiv / smaller V/div, as the printed arcs show ("S ... nS", "V ... mV"; the
  opposite was a bug). The tdiv ladder + boot band are injected via the constructor (§2); the V/div
  ladder is in §9.
- **POSITION / TRIG LEVEL / ADJUST / HORIZ POSITION are continuous**: apply `dir * step * magnitude`
  (the acceleration of §5.3), clamped to the setting's range.

  **Offset (position) DAC** — the centre (0 V) seed is the **calibrated per-(channel, V/div) zero**: the
  cal RAM record field `+0x12` (i16 live offset-zero code, boot-default `0x27ef` = `10223`; with the
  boot-default cal table `+0x12` equals `+0x08` for every record — see calibration spec 10 / 06 §5.2). `10600` is only the
  **uncalibrated fallback** centre (the reference implementation still hardcodes `10600`; cal-RAM
  wiring is an open item). The knob steps 0.2 displayed divisions an accel-step at every V/div;
  higher code → lower mean.

  **Trigger level DAC** — code assembled and written by the owner (§8.4). Constants (the measured
  global fit, constant across the V/div ladder; the front end's per-detent calibration replaces it
  once pushed):

  | Constant | Value | Meaning |
  |---|---|---|
  | `trigCenter` | `31437` | 0 V threshold |
  | `trigStep` | `40` codes / accel-step at 1 V/div, scaled by the source's V/div (at least 1 code) | ≈ 0.044 V/step @1 V/div |
  | `TrigCodeMin` | `1` | ≈ +34.5 V (the full 16 bits: the SRAM images trigger on sample codes) |
  | `TrigCodeMax` | `65535` | ≈ −37.4 V |
  | slope | `−911` codes/V | **higher threshold → lower code** |

  Sign: CW (dir=+1) **raises** the level, which **lowers** the code:
  `nc = trigCode − dir*step*steps`, clamped to `[TrigCodeMin, TrigCodeMax]`.

- **Startup discipline:** seed all shadow state to the inherited boot detents but drive **nothing** at
  startup (no front-end / offset / level write) — the inherited boot analog range and engine band
  stay untouched until the user turns a knob. The initial LED word (§8) is the one exception and is
  pushed at startup.

### 7.4 Remote control

`/api/panel` injects a button or knob event by its physical name (§6, unchanged) and also takes typed
entry into the LCD's current context (`{"type":{"text":"1.25V"}}`: the highlighted row, the byte
picker or the match editor) or into a row by id (`{"set":{"row":"trig.level","text":"1.25V"}}`).
`GET /api/sheets` is the whole settings model (tabs, rows with stable ids, kinds, options or ranges,
values, availability, help); `POST /api/sheets/set` and `/api/sheets/action` change a row or run an
action through the same setter as the knobs and F-keys, validated and clamped alike, answering
`{ok, value, text, error}` with a readable error. A remote change never opens, moves or closes
anything on the LCD; a one-line `REMOTE` note confirms it for 1.5 s (the SYS tab turns these off).
`/api/status` carries the panel snapshot (workspace, view, sheet, F-key labels as drawn, tool line,
notice, picker, lamp word, `Seq`) and `GET /api/panel/wait?seq=N&timeout_ms=T` long-polls it (at most
10 s). `{"workspace":"bus"}` and `{"again":true}` on `/api/panel` are the workspace keys for the phone
remote: they switch the workspace or run its again whatever the LCD shows, a sheet open there staying as
it is (spec 11 §8). A number row's presets run through `/api/sheets/action` by their labels.

A remote's change runs on the panel goroutine, ordered with the knobs and keys, and its answer says what it
did, measured there: the ids of the rows whose value it changed (a timebase counts at once, though the
engine applies it at its next capture; `workspace` when it switched the workspace), the notice it raised and
the sheet values' count after it (`SheetsSeq`). An Autoset it starts is named; when the sweep ends the snapshot names it as the last one done
with the rows it changed. A key or knob sent with `"remote":true` runs the same way and is answered once it
has run; it waits while Autoset sweeps (as the panel's own keys do), and it never works the LCD's menus: the
menu keys and F-keys are refused, the S/DIV, Adjust and Position turns and the Adjust and Position pushes
(any key or knob with `"nomenu":true`) wait while a menu is open there (the S/DIV push only zooms, so it
runs), and the Adjust push is refused where it would open one
(SCOPE's row sheet, BUS's byte picker; the phone does that job itself). The snapshot leaves out
live texts, so a remote is not woken on every frame: the highlighted row of an action is sent without its
status text (a mask test's counts), and COMPARE's again names the key's job, not the trigger's stability.

### 7.5 Screen saver

Two SYS rows set it (ADR-SCREEN-SAVER), both counted from the last input: **Screen saver** (Off, 1, 2,
5, 10, 15 or 30 min, 1 h; factory 15 min; F1..F5 offer Off, 5, 15, 30 min and 1 h) and **Screen off**
(Off, 5, 10, 15 or 30 min, 1, 2 or 4 h; factory 1 h; F1..F5 offer Off, 15, 30 min, 1 and 2 h). After
the Screen saver time the LCD shows only a small live scope drifting over black (spec 07 §10.5); after
the Screen off time it is black with gpio7 low (§10.1). A Screen off shorter than the saver goes
straight to black.

Input is anything run on the panel goroutine: the scope's keys and knobs, and for the API every
`/api/panel` and `/api/sheets` change (the phone remote, the web's keys), the web's Clear mask and SCPI
`*RST`. A key or knob on the scope that finds the screen asleep only wakes it, and so does any key or
knob in the 0.5 s after it, so a knob turned to wake the screen does not also change a setting. A
remote input wakes the screen and also does its job. The web page's own controls (`/api/set`) are not
input: they change the engine directly, and the page renews its leases on timers. While the screen
sleeps the scope keeps acquiring, analysing and driving its lamps. `/api/status` names the state
(`Screen`: `on`, `saver`, `off`). The rows belong to the instrument: the settings file keeps them,
while the setup memories leave them out and their recall and Default Setup leave them as they are.

---

## 8. Panel LEDs — CS3 latch

The status LEDs are driven by writing a single 16-bit shadow word to a CS3 latch over the GPMC bus.
This is CPU-driven; it must be flushed by the bus owner at the frame boundary (§2), never from the
panel goroutine.

### 8.1 Strobe sequence (order is mandatory; single-owner, one indivisible burst)

§8.1 is the sequence the **bus owner** performs when it services a `SetLEDs(word)` request during its
frame-boundary flush. Emit it as **one indivisible 4-write burst** — do not split it across FSM
iterations, and do not collide the strobe with any other CS3 write. In particular the LED strobe and
the offset-DAC flush (`0x10/0x30`) both happen at the **frame boundary**, so serialize them: complete
one before starting the other; never interleave their individual CS3 writes. The **trigger-level**
flush is *not* at this site — it is applied at the bus-idle **Arm boundary** (§8.4), a separate flush
point — so it never shares the strobe window. A concurrent or interleaved strobe corrupts the latch;
a stray CS3 access during a halt window collapses the engine.

Write, in this exact order, on CS3:

1. `WriteRegCS(3, 0x0b, 0)` — strobe low (FPGA `0x20100016`)
2. `WriteRegCS(3, 0x0a, word >> 8)` — high byte (FPGA `0x20100014`)
3. `WriteRegCS(3, 0x09, word & 0xff)` — low byte (FPGA `0x20100012`)
4. `WriteRegCS(3, 0x0b, 1)` — strobe high → latches the word

The `0x0b=0` leading strobe is required; omitting it means the latch never captures the word.

### 8.2 Bit → LED map

The bit map is spec 02 §7.5 (the corroborated PCB wiring: `0x0010` CH1, `0x0020` MATH, `0x0040` CH2,
`0x0080` REF, `0x0002` CURSORS, `0x0004` INTENSITY/ADJUST, the mid-byte menu lamps and the high-byte
RUN/STOP/SINGLE bits). There is no TRIG'd lamp: trigger-armed is an on-screen indicator.

The panel drives twelve lamps, each with one meaning — the key's function is in use — whether a sheet
is open or not:

| Bit | Lamp | Lit while |
|---|---|---|
| `0x0010` | CH1 | CH1's trace is shown |
| `0x0040` | CH2 | CH2's trace is shown |
| `0x0020` | MATH | a math trace is on |
| `0x0080` | REF | REF A or REF B is shown |
| `0x0002` | CURSORS | the cursors are on |
| `0x0100` | MEASURE | readouts are on (one or more chosen) |
| `0x0200` | ACQUIRE | the acquisition is not Normal, or super-res is on |
| `0x0400` | DISPLAY | a view other than Y-T, or persistence |
| `0x0800` | SAVE/RECALL | the settings are those last saved to or recalled from a setup slot |
| `0x1000` | UTILITY | a bus is decoded |
| `0x2000`/`0x4000` | RUN/STOP | green running, amber (both) waiting for a trigger (Normal or Single), red stopped |
| `0x8000` | SINGLE | a single capture is armed |

- The softkey firmware lit MATH, REF and CURSORS on this board from these bits. INTENSITY/ADJUST stays
  **dark**: no firmware has lit it, so its lamp is not confirmed.
- RUN/STOP is one bicolor LED: `0x2000` = green, `0x4000` = red; both set (`0x6000`) = amber.
- Writing `0xFFFF` is a lamp-test (every LED on).
- The panel computes the word from the state, latches it only when it changes, and re-syncs it on its
  40 ms tick, so the lamps follow engine-side changes (a single self-stopping, SCPI or the web
  toggling run) without a key press. Set/clear the bit(s) in the shadow and flush once per change
  (not per bit) to avoid intermediate visible states — the strobe latches the whole word.
- This clone's wiring does **not** follow any presumed internal LED index order — use spec 02 §7.5,
  not an index guess.

**Trap:** this must be a single-owner flush like every other CS3 write. Treat a non-effect as harmless
(on a differently-wired board the latch may be MCU-owned); either way it must never disturb the
engine.

### 8.3 Offset-DAC flush sequence

When the owner services a dirty `SetOffsetDAC(ch, code)` at the frame boundary, it writes the 16-bit
code low byte then high byte (the high byte self-latches — no separate strobe), then re-asserts the
CS1 run word so the front-end change leaves the engine coherent:

1. `WriteRegCS(3, lo, code & 0xff)` — CS3, `lo` = `0x10` (CH1) or `0x11` (CH2)
2. `WriteRegCS(3, hi, code >> 8)` — CS3, `hi` = `0x30` (CH1) or `0x31` (CH2); latches
3. `WriteReg(0x35, runWord)` — CS1 selector `0x35` = the **run word**, `runWord` = `0x0001`
   (free-run/AUTO) or `0x0003` (armed/NORM)

Note: the CS1 selector `0x35` (run word) is a **different register** from the CS3 level lane-B high
byte, also numbered `0x35` (§8.4) — different plane, do not conflate. Higher code → lower mean; a bare
offset write without the run-word re-assert can leave the engine incoherent.

### 8.4 Trigger-level flush sequence (Arm-boundary)

Unlike the LED/offset flushes, the trigger-level flush is applied at the **bus-idle Arm boundary** (not
armed+filling). When the owner services a dirty `SetTrigLevel(code)`:

1. **Level quad** — write the **same** 16-bit code to both comparator lanes, low byte then high byte
   (the high byte self-latches):
   - `WriteRegCS(3, 0x14, lo)` — lane A low
   - `WriteRegCS(3, 0x34, hi)` — lane A high (latches lane A)
   - `WriteRegCS(3, 0x15, lo)` — lane B low
   - `WriteRegCS(3, 0x35, hi)` — lane B high (latches lane B)
2. **Full engine re-arm** — re-anchor the comparator to the new reference (the frame loop does not
   self-heal a bare level poke):
   - `WriteReg(0x00, 0x80)` twice
   - then arm: `WriteReg(0x21, 0xC0)` twice → `WriteReg(0x57, 0x0001)` → `WriteReg(0x57, 0x0000)` →
     arm-settle delay → `WriteReg(0x21, 0xC3)`

A bare `0x14/0x34` poke off this boundary **black-screens** the display. The safety is
serialization + the inherited fd + Arm-boundary timing + the following re-arm — not a latch strobe.

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
| 1 | GND coupling select | the relay works when the absolute word is emitted (spec 06 §6); the firmware keeps coupling a display transform |
| 2 | **Coarse V/div range** | **1 = attenuated/high range** (500 mV/div…10 V/div), 0 = sensitive/low range |
| 3 | DC coupling select | selects a DC offset-cal entry, not a coupling cap |
| 5 | Constant enable | always 1 |
| 7 | CH2 base preload | CH2 byte base `0xA0`; CH1 byte base `0x20` |

`byte2`: `trigCoupling` high nibble = `0x7` for DC; `trigSrc` = 0 (C1) / 1 (C2) / 2 (EXT).

### 9.3 V/div ladder (constants)

12 detents per channel, 1-2-5:

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
| 11 | 10 V | 1 | 6 |

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

## 10. Panel outputs — discrete GPIO & eHRPWM (off the GPMC bus)

Besides the FPGA LED latch (§8) the panel drives three non-FPGA outputs: two AM335x GPIO lines and one
eHRPWM channel, all through `sysfs` (write-file / `system()` echo). They are **not** FPGA registers and
never touch the acquisition register space or the `nCONFIG` config port — do not model them as such.

### 10.1 `gpio7` — LCD display / backlight enable (load-bearing)

Kernel global GPIO **7** is the **LCD display-enable / backlight** line: with it low the screen stays
dark, so the firmware **must drive it high to light the panel**. It is already exported and set at boot,
so a `echo 7 > /sys/class/gpio/export` a second time returns **EBUSY** — do **not** re-export. The
boot sequence (reproduce only the parts not already done) is:

```
echo 7   > /sys/class/gpio/export          # already done at boot → skip (EBUSY if repeated)
echo out > /sys/class/gpio/gpio7/direction
echo 1   > /sys/class/gpio/gpio7/value     # 1 = display ON; 0 = dark
```

It is the panel's only light control. The screen saver (§7.5) drives it low after the Screen off time,
once a black frame is up, and high again on the next input, once the picture is; the app also drives it
high when it stops. Measured with the Shelly plug, the scope draws 22.3 W with it high and 20.8 W with it
low. The kernel's `tlc59108-bl` backlight device (`/sys/class/backlight`, brightness 0..255) is not
wired to this panel: brightness 0 leaves the draw at 22.3 W.

### 10.2 `gpio113` — trigger-event pulse (no panel indicator)

Kernel global GPIO **113** is a pulsed notification line (rear trigger-out / internal), **not** a
front-panel lamp. It has **no export call** in the boot firmware, so a clean-room app must export and
set direction itself once before writing a value:

```
echo 113 > /sys/class/gpio/export
echo out > /sys/class/gpio/gpio113/direction
```

A trigger "event" pulse is a back-to-back clear then set (a 0→1 edge):
`echo 0 > .../gpio113/value` then `echo 1 > .../gpio113/value`.

### 10.3 eHRPWM `run` pulse — acquisition repetition gate

The eHRPWM module `ehrpwm.0` channel 0 is driven via `sysfs`. Its role is most likely the **live/roll
acquisition repetition gate** (killing the boot firmware freezes the live ADC because nobody re-issues
`echo 1 > run`), not a beeper. A clone that drives its own acquisition engine (§2) does not
need it.

```
sysfs base: /sys/devices/platform/omap/ehrpwm.0/pwm/ehrpwm.0:0/{request,period_freq,duty_percent,run}
init:  echo 1 > request ; echo 10000 > period_freq ; echo 50 > duty_percent   # 10 kHz, 50% duty
pulse: echo 1 > run   (then echo 0 > run after the interval)
```

`period_freq` is a target frequency in Hz taken verbatim (`10000` = exactly 10.000 kHz, independent of
TBCLK); `duty_percent` is 0..100.

### 10.4 Demo/simulation gate

The boot firmware **skips** the `gpio113` pulse and the eHRPWM `run` pulse when a demo/sim flag is set
(`[state+4] == 1`). A faithful clean-room app / emulator must replicate this gate, or it emits pulses in
contexts where the stock firmware suppresses them.

---

## 11. Load-bearing constraints (summary)

1. **Reuse the inherited `/dev/fpga_key` fd**; a fresh open EFAULTs. Never close it. Never read data
   from it (it carries none) — it is a SIGIO source only.
2. **Matrix read and every CS3 latch/DAC write are GPMC operations** and must go through the single
   bus owner at the frame boundary — never a concurrent goroutine, never during a capture-halt window.
3. **The LED strobe is one indivisible 4-write burst** at the flush site, not interleaved with the
   offset/level CS3 flushes and not split across FSM iterations (§8.1).
4. **The offset DAC write must be followed by a CS1 run-word (selector `0x35`) re-assert** (§8.3); the
   trigger level write is a **4-write quad + full engine re-arm at the bus-idle Arm boundary** (§8.4). A
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

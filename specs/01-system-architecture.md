# System Architecture

This spec defines the process/boot model, the single-owner GPMC bus discipline, file-descriptor
inheritance, the GPMC chip-select planes, and the health/supervision model. Every constraint here is
load-bearing: the acquisition engine (spec 03), triggering (05), front panel (08), and control plane
(09) are all shaped by the rules stated here. Read this before any of them.

The firmware is a **single process** with several concurrent workers (goroutines/threads). Exactly one
of those workers owns the acquisition hardware; the rest are producers of commands and consumers of
frames. This is not an optimization — it is the correctness invariant that keeps the display from
wedging to a black screen.

---

## 1. Hardware access surfaces

The device exposes the FPGA and front end through these kernel devices:

| Device | Purpose | Access |
|---|---|---|
| `/dev/Gpmc` | FPGA register + sample-port access over the GPMC bus (both chip-select planes) | 6-byte ioctl (read/write), single-open |
| `/dev/mem` | direct MMIO mapping of a GPMC chip-select window (syscall-free reads) | `mmap` of a physical CS base |
| `/dev/fpga_key` | front-panel key/knob interrupt source (SIGIO) | inherited fd, `F_SETOWN`+`O_ASYNC` |
| `/dev/fb0` | LCD framebuffer (RGB565) | `mmap`, write-only from the app's view |
| `/dev/spidev1.0`, `/dev/spidev1.1` | analog front end: V/div relay word and fine-gain DAC | SPI; **OFF the GPMC bus** |

`/dev/Gpmc` and `/dev/fb0` are the two devices that must never be touched by more than one code path
at a time in a way that violates the disciplines below. The spidev front end is off the GPMC bus and
may be driven directly and concurrently.

### 1.1 GPMC chip-select planes

The FPGA is mapped behind two GPMC chip selects. Every GPMC access names a plane; a selector means
different things on different planes, and a write aimed at the wrong plane silently does nothing.

| Plane | Role | Physical base (mmap) | Selectors used |
|---|---|---|---|
| **CS1** | acquisition / read plane: arm, divisor, status, fill counter, sample drain ports, key-matrix read | `0x01000000` | arm `0x21`, run-word `0x35`, reset-head `0x44`, divisor `0x19/0x1a/0x1b`, write-ptr `0x57`, status `0x39`, trig-pos `0x3a/0x3b`, fill `0x46`, version `0x12`, deep drain `0x30–0x34`, roll FIFO `0x41/0x59`, matrix `0x64–0x69` |
| **CS3** | config / control plane: vertical offset DAC, trigger level DAC, panel-LED latch, source mux, config-status | `0x03000000` | offset DAC `0x10/0x30` (CH1) `0x11/0x31` (CH2), trigger level DAC `0x14/0x34` (lane A) `0x15/0x35` (lane B), LED latch `0x09/0x0a/0x0b`, source mux `0x22`, config-status `0x07` |

**Register width and addressing.** Every FPGA register is **16-bit**. A register selector `sel`
addresses the 16-bit word at **byte offset `sel<<1`** in its chip-select window (equivalently:
`uint16`-array index `sel`).

- On the **ioctl** path the driver performs the `<<1` scaling itself — pass the raw selector, never a
  pre-shifted address (see §1.2).
- On the **mmap** path there is no driver: read a register as a single aligned `uint16` load at byte
  offset `sel<<1`. **Do NOT apply `<<1` on top of a `uint16` index** — that double-shifts to byte
  offset `sel<<2` and reads the wrong register. Confirm addressing before trusting a fresh map:
  selector `0x12` (FPGA version) must read `0x0052`.

### 1.2 ioctl vs mmap access

Two access methods exist for the GPMC bus; they are not interchangeable.

- **ioctl (`/dev/Gpmc`)** — one 6-byte struct per access. This is the path for **all writes** (arm,
  divisor, run-word, config-plane writes): the driver has **no mmap-write path**, so every write goes
  through ioctl. It is also the fallback read path when `/dev/mem` is unavailable, and the **required**
  read path for the **roll FIFO** ports `0x41/0x59` (an mmap load does not pop the FIFO read pointer;
  only an ioctl read advances it).
- **mmap (`/dev/mem`)** — one CPU load per register read, ~50× faster than an ioctl. It is safe for
  **any plain, always-complete register** — status `0x39`, fill `0x46`, trig-pos `0x3a/0x3b`, version
  `0x12` — and is the preferred path for those reads in the hot trigger-poll (it removes a syscall per
  ~150 µs iteration). It is also the path for the **deep sample drain** `0x30–0x34`, but only *after*
  the capture-halt has frozen those ports.

**The sample-port hazard is the whole reason mmap is constrained.** Only the sample ports (deep drain
`0x30–0x34` and roll FIFO `0x41/0x59`) can be read while a transaction is incomplete, and a **raw mmap
load of an incomplete sample port hangs the CPU uninterruptibly** (an L3 non-completion on the GPMC
WAIT line) — it cannot be abandoned. A software-deadline poll loop cannot rescue it; only a physical
power-cycle recovers. Therefore:

- Never mmap-read a sample port outside the frozen (post-halt) window. The deep drain runs only after
  `0x21=0xC8` freezes the ports; the roll FIFO is not read via mmap at all (ioctl only).
- Plain status/fill/trig-pos/version registers have no WAIT-line hang risk and are read either way; use
  mmap for the hot path.

Note that the WAIT-line hang is what makes the sample-port rule absolute; it is **not** the rationale
for keeping plain-register reads on ioctl. The wait-for-trigger loop bounds itself with a
software deadline (`time.Now().Before(deadline)`), not a syscall timeout, so it can poll `0x39`/`0x46`/
`0x3a/0x3b` via mmap freely.

**The 6-byte ioctl encoding.** `b[0]` is the chip-select plane: **`1` = CS1, `3` = CS3**, for both
reads and writes. The kernel computes `index = b[0]-1` to pick the ioremap base. `b[0]=0` yields index
`0xFF`, selecting a garbage ioremap base; the access then **stalls for seconds**. Always set `b[0]` to
`1` (CS1) or `3` (CS3). Byte layout:

```
b[0] = plane (1=CS1, 3=CS3)
b[1] = 0
b[2] = sel & 0xff          (low byte of selector; NOT pre-shifted)
b[3] = (sel >> 8) & 0xff
b[4] = val & 0xff          (little-endian value, writes only)
b[5] = (val >> 8) & 0xff
```

Read vs write is the **ioctl request code**, not `b[0]`:

| Op | Request code |
|---|---|
| read | `0x80026700` |
| write | `0x40026701` |

For the mmap path, verify the window is addressed correctly before trusting it (selector `0x12` must
read `0x0052`, per §1.1). If it does not verify, fall back to the ioctl read path.

---

## 2. Boot and launch model

The device boots into factory firmware that performs the one-time hardware bring-up, then the clean-room
app inherits the configured fabric and drives it.

### 2.1 One-time bring-up (factory firmware, at boot)

1. The factory firmware loads the FPGA bitstream (`CONF_DONE` asserts, CS3 `0x07 & 0x80`).
2. It applies the per-unit analog **calibration** (front-end gain/offset trims from the on-disk
   calibration file).
3. It opens `/dev/Gpmc` — this establishes the single-open fd **and** the boot-time chip-select
   initialisation that a fresh open lacks.

The bitstream and the calibrated front end are **inherited**; the app does **not** reconfigure the
FPGA and does **not** re-run bring-up. `CONF_DONE` stays high for the life of the session.

### 2.2 Takeover (inherit-then-kill)

The runtime is **standalone**: the factory app is not kept alive. Takeover happens at an **idle
landing** so the acquisition engine is never killed mid-frame (a mid-frame kill freezes the engine
holding the GPMC WAIT line — unrecoverable in software, power-cycle only):

1. A boot-tree supervisor (the OTA **agent**) holds the inherited `/dev/Gpmc` fd open across the kill
   so the fd — and therefore the FPGA chip-select validity — survives. (`release()` frees the CS only
   on the *last* close of the fd.)
2. The engine is driven into a stoppable state and the factory app is stopped only when confirmed idle
   (fill counter `0x46` frozen), never mid-capture.
3. The clean-room app is `exec`'d as a descendant of the agent, so it **inherits** the agent's open
   fds (see §2.3).

### 2.3 The launch chain and fd inheritance

The app is launched as a direct descendant of the agent (`boot → agent → app`), all via `exec`, so
open fds propagate down the tree. On the running unit the app inherits, among others:

- **`/dev/Gpmc`** — the acquisition bus fd (already chip-select-initialised).
- **`/dev/fpga_key`** — the front-panel interrupt source.
- **`/dev/mem`** and **`/dev/fb0`** as available.

The app also inherits its **runtime contract via environment variables** exported by the slot script.
These are normative agent↔app names; a mismatch silently breaks the feature:

| Variable | Meaning |
|---|---|
| `OTA_HEALTH_PATH` | path of the health file the app writes and the agent watches (§4.2). |
| `SCOPE_GPMC` | `/dev/Gpmc` path (default `/dev/Gpmc`). |
| `SCOPE_LCD` | framebuffer path (default `/dev/fb0`). |
| `SCOPE_MMAP_DRAIN` | `0` disables the `/dev/mem` fast path (forces ioctl reads); any other value enables it. |

**The agent is the permanent fd holder; the app is a tenant.** Each app launch (including a restart
after a crash or an OTA update) inherits the same live fds. This is why a wedge can be recovered by
re-launching the app on the still-live inherited fd without a reboot.

---

## 3. The single-owner GPMC bus discipline (the core invariant)

**Exactly one worker — the EngineOwner — owns the inherited `/dev/Gpmc` fd (and the `/dev/mem` drain
map) and is the only code that ever touches the GPMC bus.** It runs the per-frame acquisition FSM
(arm → wait → capture-halt → drain → re-arm) itself and hands *frozen frame copies* to consumers. No
renderer, panel, control-plane, or network worker ever issues a GPMC access directly.

```
 EngineOwner  (single goroutine, sole owner of /dev/Gpmc + the /dev/mem drain map)
   per-frame FSM  →  drains a frozen frame  →  publishes it to a 3-slot arena
        ▲ staged commands (drained at the frame boundary)          │ frozen frame copy
        │                                                          ▼
   Panel / Control / Network                                 Renderer → /dev/fb0
   (producers of commands, never bus writers)                (consumer, never a bus reader)
```

**FSM register values.** This spec defines the *ownership and staging discipline*; the full per-frame
FSM register semantics live in **spec 03 (Acquisition Engine)**. The minimal opcode set needed to
follow §3.1 and to make the loop run at all:

| Selector | Plane | Values |
|---|---|---|
| arm `0x21` | CS1 | `0xC0` reset-head, `0xC3` go, `0xC8` capture-halt, `0xCB` latch-without-halt (roll) |
| run-word `0x35` | CS1 | `0x0001` AUTO (free-run), `0x0003` NORM (armed) |
| write-ptr `0x57` | CS1 | pulse `0x0001` → `0x0000` |
| reset-head `0x44` | CS1 | strobe `0x0001` → `0x0000` (part of enable+divisor bring-up) |
| divisor `0x19/0x1a/0x1b` | CS1 | class / lo / hi (per band, spec 04) |
| status `0x39` | CS1 | bit1 (`0x02`) trig edge seen, bit2 (`0x04`) capture done |
| fill `0x46` | CS1 | 11-bit sample-write counter (mask `0x07ff`) |

The arm sequence is `0x21=0xC0` (×2) → `0x57` pulse → arm-settle → `0x21=0xC3`. See spec 03 for the
AUTO/NORM publish policy, the fill latch target, ETS/roll/envelope variants, and settle timings.

### 3.1 Why this makes the capture-halt safe

Each frame the engine issues a **capture-halt** (`0x21=0xC8`) that freezes the sample buffer so it can
be drained coherently, then **re-arms immediately** so the engine is filling again *before* the frame
is rendered. The halt window is only the ~1 ms drain. The renderer works on a **copy** and never reads
the bus, so the halt window is never concurrent with any other GPMC access — because no other GPMC
access exists.

**Violating this wedges the device to a black screen.** A stray GPMC access (a config-plane write, a
matrix read, a sample read) that lands *inside* the ~1 ms halt window collapses the engine: the display
goes black and does not recover in software. This is not a hardware limit on the halt — the halt is
safe *by construction* under single ownership. It is a concurrency hazard that single ownership
eliminates. Any design that lets a second worker touch the bus reintroduces it.

### 3.2 How non-owner workers reach the bus

Producers that need a GPMC action **stage a command**; the owner applies it at the **frame boundary** —
the top of the FSM loop, where the engine is armed+filling and *not* in a halt window. The staging
mechanism is a **coalescing desired-state shadow** (last-write-wins per control) plus a small request
channel for reads, **not a FIFO** (a FIFO would replay dozens of stale knob deltas one per frame
instead of applying only the latest state).

Every GPMC-bound control is routed this way. The concrete byte sequences are in the referenced specs;
the owner is the only code that emits them:

| Control | Bus | Selector(s) | How it is driven | Sequence in |
|---|---|---|---|---|
| key-matrix read | CS1 | `0x64–0x69` | request channel → owner reads a snapshot at the frame boundary, replies | spec 08 |
| panel-LED latch | CS3 | `0x09/0x0a/0x0b` | shadow word → owner flushes the strobe sequence when it changes | spec 08 |
| vertical offset DAC | CS3 | `0x10/0x30`, `0x11/0x31` | per-channel shadow code → owner flushes lo/hi + re-asserts the run word | spec 06 |
| trigger level DAC | CS3 | `0x14/0x34`, `0x15/0x35` | shadow code → owner writes the level quad + full re-arm | spec 05 |
| timebase / run-stop / mode | CS1 | `0x19/0x1a/0x1b`, `0x35`, `0x21` | staged desired-state, applied at the boundary (band change re-inits) | spec 03/04 |
| **analog V/div (relay + gain DAC)** | **OFF bus (spidev)** | — | driven **directly** by the producer; safe concurrently | spec 06 |

The frame boundary is the only place a matrix read, an LED latch, an offset write, or a trigger-level
write happens. The single owner applies them between halts, on its own goroutine. A wrong CS3 write
order emitted off the boundary is exactly the black-screen wedge §3.1/§5 warns about — always use the
sequence from the referenced spec.

### 3.3 STOP keeps the engine alive

RUN/STOP is a policy flag, not a bus state. **STOP keeps the FSM armed and cycling on the bus** (so it
never wedges) but **publishes nothing** — the display holds the last frame. Commands (including RUN to
resume, and band changes) are still serviced at the frame boundary while stopped. Never stop the FSM by
leaving the engine halted or by ceasing to touch the bus in a way that abandons an outstanding
transaction.

### 3.4 Frame handoff and pacing

Frames are handed off through a **3-slot arena** (triple buffer) with **drop-newest** backpressure:

- The producer (owner) drains straight into its private `write` slot with no lock held, then swaps it
  into the `ready` slot under a microsecond-scale critical section.
- The consumer (renderer) swaps `ready` into its private `read` slot the same way and works from that.
- The mutex guards only a **RAM pointer swap** — it is **not on the GPMC bus**, so single-ownership is
  intact and the producer never blocks on the ~50 ms render.

Triple-buffering is required (double-buffering tears against the immediate-re-arm invariant — the next
drain would land in the slot the renderer still holds). If the consumer has not taken the previous
frame, it is simply overwritten (drop-newest); the consumer re-presents its held frame, which is a
legitimately-quiet display, not a wedge.

**Frame pacing is a correctness/quality constraint, not a tuning knob.** The render/consume loop is
deliberately capped at a **~50 ms / ~20 fps** budget. The CPU is a single shared ARM core; driving the
consume/publish cadence faster (e.g. shortening the frame period below ~50 ms) starves the SoC, which
*both* degrades acquisition frame uniformity *and* paradoxically lowers the delivered fps. Do not
uncap the loop to "go faster" — hold the ~20 fps budget.

**Reused-buffer metadata trap.** The three frame buffers are allocated once and reused in place; the
drain writes samples into the existing arrays. **Every producer path MUST set (or clear) all of a
frame's metadata every frame** — `Valid`, `WinCols`, `EdgeX`, `Ptp`, the interpolation flag, and the
envelope flags (`IsEnv`, `EnvCols`). A path that leaves a stale flag from a previous band (e.g. an
envelope flag left set after switching from a slow band to a fast band) makes the renderer draw the
wrong thing from correct data. Clear what you do not set.

---

## 4. Process, health, and supervision

### 4.1 The supervision chain

The OTA agent supervises the app. It launches the app slot, watches a **health signal**, and rolls
back on failure. Two slots exist (A/B) plus a known-good emergency binary for crash-loop safety. The
agent — not the app — owns the fds and the hardware watchdog.

### 4.2 The health signal

Health is reported by the app writing/updating a **health file** at `OTA_HEALTH_PATH` (§2.3) that the
agent watches. **The health signal must not be a proxy for process-alive.** A wedged-but-running
process that never advances a frame is invisible to an exit-shaped rollback, and on this unit `reboot`
is effectively a no-op — an undetected wedge means a physical power-cycle. The signal therefore encodes
*frame-advance liveness*, in three requirements:

1. **First report is gated on genuine capture.** Report healthy only after **N genuinely-advancing
   coherent frames** (≥3 frames that both latched and drained a coherent record — the engine's
   `Coherent` counter reaching 3). A working engine reaches this in well under a second; a wedged boot
   never does, so the agent sees it unhealthy and rolls back. **Do not** write the health token at
   launch or before the first capture.

2. **Liveness is a re-written token driven by frame advance, not process life.** The owner exports a
   **monotonic heartbeat** — the FSM-cycle counter (`Frames`), incremented once per FSM loop iteration
   whether or not a frame publishes, plus the `Coherent`/`Published` counters. After the first healthy
   report, the health writer **re-writes (touches) the token whenever the heartbeat advances**,
   throttled to at most once per ~500 ms. The agent treats the token as stale — **unhealthy → relaunch
   the app on the still-live inherited fd** — if it has not changed for **> ~3 s** while the process is
   still alive. (A write-once token that freezes after the first report is a bug: it makes the signal
   the process-alive proxy this section forbids.)

3. **Discriminate NORM-quiet from wedged.** A legitimately quiet NORM display advances no *published*
   frames — a real trigger may simply not have arrived — but the engine is still armed and cycling, so
   the **heartbeat (`Frames`) keeps advancing**. A wedge is the heartbeat frozen (owner blocked / WAIT
   line held), plus a frozen fill counter `0x46`, a never-latching status, a flat drain, and/or
   `CONF_DONE` dropping. Key liveness off the **heartbeat/`Frames`-advance**, *not* off publish-
   generation, so the watchdog does not false-positive a quiet NORM screen as a wedge. `Frames` and
   `Coherent`/`Published` are all owner-internal (only the single bus owner reads `0x46`/`0x39`); they
   are surfaced to the health writer through the owner's exported stats snapshot, never by a second bus
   reader.

### 4.3 Recovery ladder

Detect a wedge each frame (heartbeat/fill never advances, never latches, drain flat, `CONF_DONE`
cleared) and escalate: re-arm → full register re-assert (divisor + run word + arm) → declare unhealthy
so the agent re-launches the app **on the still-live inherited fd**. A dropped `/dev/Gpmc` fd is
unrecoverable (power-cycle), so the fd must never be closed and a panic in the owner must not become an
fd loss.

### 4.4 Panic containment

The owner goroutine has a **top-level recover**: a panic becomes a logged, observable wedge event that
stops the owner cleanly, **not** a process exit. A fast-exit crash-loop would make the supervisor roll
back to the other (unknown) slot, and the inherited fd survives either way — recovering in place keeps
the failure diagnosable (e.g. over the status interface) and the fd intact.

---

## 5. Load-bearing constraints and traps

These are requirements. Each is why the design is shaped the way it is.

1. **Single owner of the GPMC bus.** Exactly one worker touches `/dev/Gpmc` (and the `/dev/mem` drain
   map). All other GPMC-bound actions are staged commands applied by the owner at the frame boundary.
   Any second consumer touching the bus during a halt window black-screens the device.

2. **Inherited-fd-only for `/dev/Gpmc`.** Reuse the fd inherited from the agent (scan `/proc/self/fd`
   for the descriptor pointing at `/dev/Gpmc`). A **fresh `open()` is a hard fault**: it hits the
   single-open guard (EPERM while the inherited fd is held) and lacks the boot chip-select init (reads
   can wedge). If no inherited fd is found, **refuse to drive and report unhealthy** — do not attempt a
   fresh open that could wedge the driver.

3. **Never close the inherited `/dev/Gpmc` fd.** It is the *same* open file description as the boot
   holder's; closing it frees the chip select for the whole process tree. Clear any runtime finalizer
   so an accidental GC of the wrapper cannot close it either.

4. **Inherited-fd-only for `/dev/fpga_key`.** Reuse the inherited key-device fd for SIGIO; a fresh open
   of it faults post-takeover. Arm it with `F_SETOWN`+`O_ASYNC` and never close it. If it cannot be
   armed, fall back to a periodic matrix poll (spec 08) — never a fresh open loop.

5. **Never write the FPGA config port at runtime.** The nCONFIG / config port (`0x2010000e`) collapses
   the acquisition engine when written while running. Runtime source/level/mode changes go through the
   normal CS3 config selectors, never the config port. `CONF_DONE` must stay asserted.

6. **Never let a GPMC access land inside a capture-halt window.** The engine is halted only for the
   ~1 ms drain; drain + re-arm complete before the frame is handed off. No other GPMC access — matrix
   read, CS3 write, sample read — may occur during that window. Single ownership guarantees this; do
   not add a code path that breaks it.

7. **mmap is legal on any plain register but NEVER on an incomplete sample port.** Status `0x39`, fill
   `0x46`, trig-pos `0x3a/0x3b`, and version `0x12` are plain always-complete registers and are read
   via mmap on the hot path (or ioctl). The **deep drain `0x30–0x34`** is mmap-read *only after* the
   capture-halt froze the ports. The **roll FIFO `0x41/0x59`** is read by ioctl only (mmap does not pop
   the FIFO). A raw mmap load of an incomplete sample port hangs the CPU uninterruptibly and cannot be
   timed out. **All writes stay on ioctl** — the driver has no mmap-write path.

8. **`b[0]` is the chip-select plane (1 or 3), not a read/write flag.** `b[0]=0` yields index `0xFF`, a
   garbage base, and stalls the bus for seconds. Selector goes in bytes 2–3 un-shifted; value LE in
   bytes 4–5; read vs write is the request code.

9. **Correct mmap register scaling.** Registers are 16-bit; read one as a `uint16` load at byte offset
   `sel<<1`. Do NOT apply `<<1` on top of a `uint16` array index (double-shift → wrong register).
   Verify with selector `0x12` == `0x0052` before trusting the map.

10. **Single-writer for ALL CS3 writes** (trigger level, offset DAC, panel LEDs, source mux). These are
    staged and flushed by the owner at the frame boundary using the exact sequences in specs 05/06/08.
    A bare CS3 write off that boundary collides with the engine's in-flight burst and wedges — the
    safety is serialization + inherited fd + arm-boundary timing (+ the following re-arm for the
    trigger level), not any latch strobe.

11. **Clear reused frame-buffer metadata every frame** (see §3.4): a stale flag on a recycled buffer
    renders wrong output from correct data.

12. **Cap the frame rate at ~20 fps / ~50 ms** (see §3.4). The single shared ARM core is starved by a
    faster publish/consume cadence, which lowers both frame uniformity and delivered fps. Pacing is a
    correctness constraint, not a knob to raise.

13. **Bounded, preemptible waits.** The wait-for-trigger poll is a bounded, non-blocking loop (a
    software deadline of tens of ms, self-healing), never a blocking syscall. A single owner that blocks
    forever in NORM would hang STOP, timebase changes, mode changes, and panel-matrix reads behind a
    trigger that may never arrive. Each poll iteration checks for an abort/urgent-command flag.

14. **Kill/takeover only at an idle landing.** The factory app is stopped only when the engine is confirmed
    idle (fill counter frozen), never mid-frame; a mid-frame kill freezes the engine on the GPMC WAIT
    line and is unrecoverable in software.

15. **Allocation-free hot loop.** The per-frame drain writes into the preallocated arena buffers; no
    per-frame heap allocation. On this class of CPU, GC in the hot loop would steal the drain's time
    budget and widen the halt window.

16. **The spidev analog front end is off the GPMC bus** and is driven directly by producers. Do not
    route it through the owner; do not treat it as a GPMC access.

---

## 6. Open

- **Kill-timing observation channel.** The idle-landing detection during takeover reads engine state
  while the factory app is still alive; the exact status polled (`0x12`/`0x38`/`0x46`) and the guaranteed-
  stoppable timebase depend on the factory app's dispatcher and are established in the acquisition spec, not
  here.
- **Coarse HW trigger source mux (CS3 `0x22`) code values** for C1/C2 are not pinned; source selection
  is currently done in software downstream (spec 05). This does not affect the bus discipline.
- **Health re-write cadence / staleness threshold.** The ~500 ms re-write throttle and ~3 s agent
  staleness window in §4.2 are the required *shape* of the liveness contract; the exact numbers are a
  design parameter that must satisfy `staleness_window > max legitimate quiet interval > re-write
  throttle`. The agent and app must agree on the concrete values.

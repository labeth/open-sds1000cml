# 02 — Register Map

The firmware talks to the FPGA over the OMAP **GPMC** bus through the `/dev/Gpmc` character
driver. Every acquisition, trigger, front-panel and calibration action reduces to reads and writes
of the registers below. This document is the complete register reference the firmware uses.

---

## 1. Bus & addressing model

### 1.1 Chip-select planes

The FPGA presents **two** register windows, selected by the chip-select (CS) field of the ioctl:

| Plane | Register/bus base | mmap physical base | Purpose | Access |
|---|---|---|---|---|
| **CS1** | `0x20200000` | `0x01000000` | Acquisition / read plane: arm FSM, status, sample ports, timebase, panel matrix, trigger source mux, on-bus calibration | ioctl (read + write); post-halt sample drain may use mmap |
| **CS3** | `0x20100000` | `0x03000000` | Config / control plane: config port, CONF_DONE, trigger-level DAC, offset DAC, LED latch | ioctl (read + write); CONF_DONE also mmap-readable |

A third chip-select (CS2) is allocated by the kernel bring-up but is **not used** by the acquisition
firmware (see `00-overview.md` §2); the LCD framebuffer is `/dev/fb0` (spec 07), not a GPMC plane.

**Two addressing axes are in play; do not conflate them.**

- The **register/bus-address base** is what a register's byte address is computed from:
  `byte_addr = register_base + (sel << 1)`. CS1 registers are at `0x20200000 + sel·2`, CS3 registers at
  `0x20100000 + sel·2`. e.g. CS3 sel `0x14` → `0x20100028`; CS3 sel `0x34` → `0x20100068`; CS3 sel `0x07`
  → `0x2010000e` (the config port); CS1 sel `0x22` → `0x20200044` (the trigger source mux).
- The **mmap physical base** is the `/dev/mem` mapping address for the syscall-free drain/read fast path
  (§1.4): CS1 maps at `0x01000000`; CS3's CONF_DONE is mmap-readable at `0x03000000`.

A **register selector** `sel` addresses the 16-bit FPGA **word** `sel << 1` within its plane. On the
ioctl path the driver shifts `<<1` internally — pass the **selector**, never the pre-shifted byte
address.

### 1.2 Obtaining the `/dev/Gpmc` fd (inherited, do not open fresh)

The `/dev/Gpmc` driver enforces a **single-open** guard and performs its chip-select ioremap init
only in the boot process context. A fresh `open("/dev/Gpmc", O_RDWR)` therefore either fails with
`EPERM` while the boot holder's fd is live, or succeeds but lacks the boot-time chip-select init so
the first reads **wedge the bus for seconds**. The firmware MUST instead reuse the descriptor the
boot process already opened and passed down the exec tree:

1. Scan `/proc/self/fd`; for each entry read the symlink target and match it against the string
   `"/dev/Gpmc"`. The matching entry's numeric name is the inherited fd.
2. Wrap that fd **number directly** without duplicating it (`os.NewFile(fd, …)` — same open file
   description as the boot holder). Do **not** `dup()`.
3. **Never `Close()` it**, and **clear any GC finalizer** on the wrapper so it is never auto-closed.
   Closing this fd closes the single-open device for the whole process tree and kills acquisition.

Reference: `app/device/internal/gpmc/gpmc.go` `FindInheritedFD` / `OpenInherited`. Every other
section assumes you already hold this working inherited fd.

### 1.3 ioctl encoding

Register access uses a 6-byte ioctl struct on the inherited `/dev/Gpmc` fd:

```
b[0] = plane (chip-select): 1 = CS1, 3 = CS3      # index into the kernel's ioremap base table
b[1] = 0
b[2] = sel & 0xff                                  # low byte of selector (NOT pre-shifted)
b[3] = (sel >> 8) & 0xff
b[4] = val & 0xff                                  # 16-bit value, little-endian
b[5] = (val >> 8) & 0xff
```

ioctl request codes:

| Direction | Request code |
|---|---|
| Read | `0x80026700` |
| Write | `0x40026701` |

Read vs write is selected by the **request code**, not by `b[0]`. `b[0]` MUST be a valid plane:
the kernel computes `index = b[0] − 1` to pick the ioremap base, so `b[0] = 0` yields index `0xFF`
→ a garbage base → the access **stalls for seconds**. Use `1` or `3` explicitly, never `0`.

### 1.4 mmap fast path (drain only)

After a capture-halt the CS1 sample ports are frozen, so the drain reads them via an mmap of the
CS1 window — a syscall-free aligned load, ~50× faster than ioctl, which keeps the halt window near
1 ms. Mapping parameters (all load-bearing):

- Open **`/dev/mem`** with `O_RDWR | O_SYNC`. `O_SYNC` is required: it makes the mapping
  uncached/device memory. Without it you get cached loads and stale/incoherent samples.
- `mmap(fd, phys = 0x01000000, len = 4096, PROT_READ | PROT_WRITE, MAP_SHARED)` — one page maps the
  whole CS1 register window.
- Read a register as **exactly one 16-bit aligned load**: `*(uint16*)(base + (sel << 1))`. Mark it
  no-inline / volatile so the compiler cannot split it into two byte loads, hoist it, or CSE it out
  of the drain loop. A sample port **auto-increments its read pointer once per bus transaction**, so
  two byte loads pop the FIFO twice and corrupt the drain.

mmap reads are **only** valid for the frozen post-halt sample ports; all control writes and all reads
of a live (non-halted) register go through the ioctl path. Reference: `internal/gpmc/mmap.go`
(`OpenMMap` / `Read`).

Addressing self-check: on CS1, selector `0x12` (byte `+0x24`) reads `0x0052`. Use this to identify
the acquisition plane at bring-up.

### 1.5 Single-owner requirement (load-bearing)

**Exactly one thread may touch the GPMC bus.** The per-frame capture-halt (`0x21 = 0xC8`) freezes
the acquisition engine for the ~1 ms drain; any *other* GPMC access (a second sample read, a panel
matrix read, a CS3 LED/DAC write) landing inside that halt window collides with the engine's
in-flight burst — the HW bus is serialized with no software lock — and the acquisition/render path
stalls, producing a **black framebuffer with the backlight still on**. Therefore:

- The acquisition owner goroutine is the sole reader/writer of the bus.
- Panel matrix reads, LED-latch writes, offset-DAC writes and trigger-level writes are **staged**
  by their producers and flushed by the owner at the **frame boundary** (engine armed + filling,
  never mid-halt).
- The analog V/div front-end (relay word on spidev1.0, fine gain DAC on spidev1.1) is **off** the
  GPMC bus and is driven directly by its producer — it is not subject to this rule.

---

## 2. CS1 — acquisition / read plane

All selectors below are on plane 1. `sel` is the register selector; access is ioctl unless noted.

| sel | Name | Access | Value / format | Purpose |
|---|---|---|---|---|
| `0x00` | Reset / head | W | `0x0080` preamble | Head/reset strobe; written `0x80` (×2) as the preamble of the trigger-level recommit re-arm |
| `0x19` | Divisor class | W | `0x20` / `0x01` / `0x80` | Sample-clock class: `0x20` = 500 MSa/s (2 ns/sample), `0x01` = 250 MSa/s (4 ns), `0x80` = 100 MSa/s base, decimated by the divisor word |
| `0x1a` | Divisor low | W | 16-bit low word | Decimation divisor low half (class-`0x80` sample interval = divisor·10 ns) |
| `0x1b` | Divisor high | W | 16-bit high word | Decimation divisor high half; write `0` before writing `0x19`/`0x1a`, then the real high word |
| `0x21` | **Arm / halt opcode** | W | see §4.1 | Acquisition FSM control: reset-head / go / capture-halt / latch-no-halt |
| `0x22` | Trigger source mux (CH1) | W | `code & 3` | Engine-safe internal/EXT select, CH1: `0x03` = internal channel, `0x00` = EXT (EXT also sets `0x35` bit3). Byte `0x20200044`; commit strobe `0x53` = 1→0. CONF_DONE-safe; shifts the coarse trigger anchor. Does NOT distinguish C1-vs-C2 (both internal = `0x03`) — the source **channel** is the SPI relay-word byte2 (`0x70 \| src<<2`, off-bus, spec 06) + software (spec 05 §6) |
| `0x42` | Trigger source mux (CH2) | W | `code & 3` | Per-channel int/EXT mux for CH2, same encoding as `0x22`; companion `0x43` |
| `0x35` | **Run word** | W | `0x0001` / `0x0003` | Run mode: `0x0001` = free-run (AUTO), `0x0003` = armed (NORM). Re-asserted after any front-end change |
| `0x36` | Reset | W | `0x0000` | Cleared during bring-up |
| `0x38` | **Status B** | R | bitfield, see §3.2 | Native-fast done gate (bit5) + idle/hold state |
| `0x39` | **Status A** | R | bitfield, see §3.1 | Primary acquisition status: capture DONE (bit2), trigger edge (bit1) |
| `0x3a` | Trigger position low | R | low byte | HW trigger-position latch low byte (trustworthy ONLY at decimated/deep bands; see §3.3) |
| `0x3b` | Trigger position high | R | high byte | HW trigger-position latch high byte; `trigPos = (0x3b << 8) | (0x3a & 0xff)` |
| `0x3c` | Trigger delay/position low | W | `count & 0xff` | Trigger delay/position sample-count, low byte. Steady-loop value `0x02` (count word `0x0802`). One of the mode-multiplexed position alternates (`0x17/0x18`, `0x24/0x25`, `0x3e/0x58` carry the same value in other modes) |
| `0x3d` | Trigger delay/position high | W | `(count >> 8) & 0xff` | Trigger delay/position sample-count, high byte. Steady-loop value `0x08`. `count = round(t/Tsample)/div` (`div = 2` at TB-idx `0x0b`, else `4`). On the single-owner capture-halt engine the edge lands mid-record regardless of this value (§11), so the firmware does not rely on it |
| `0x44` | Reset-head strobe | W | `0x0001` then `0x0000` | Reset-head pulse issued first in the bring-up sequence |
| `0x46` | Fill counter | R | 11-bit (`& 0x07ff`) | Samples written into the deep record since arm; used as the latch gate (`>= LatchAt`, default `0x0200`) and to confirm the halt froze the fill |
| `0x57` | Write-pointer reset | W | `0x0001` then `0x0000` | Write-pointer reset pulse inside each arm; also phase-aligns the drain frame-to-frame |
| `0x30`–`0x34` | **Deep sample ports** | R (ioctl or mmap) | packed word: hi byte = C1, lo byte = C2 | Round-robin deep-record read ports. Read `0x30,0x31,0x32,0x33,0x34,0x30,…` for successive samples. Coherent only after a `0xC8` halt |
| `0x41` | **Roll port C1** | R (ioctl only) | hi byte = C1 sample | Free-running roll FIFO, channel 1. Each ioctl read pops one live sample; mmap does **not** advance the FIFO |
| `0x59` | **Roll port C2** | R (ioctl only) | hi byte = C2 sample | Free-running roll FIFO, channel 2 |

### 2.1 Panel key-matrix selectors (CS1, read-only)

Read as one 5-word snapshot in this order: `0x64, 0x65, 0x66, 0x67, 0x69`.

| sel | Purpose |
|---|---|
| `0x64` | Key-matrix row (active-low) — lowest-priority knob group + keys |
| `0x65` | Key-matrix row; idle level `0xbf` (edge-select masking) |
| `0x66` | Key-matrix row |
| `0x67` | Key-matrix row — highest priority; first-active row by fixed priority `[0x67 hi … 0x64 lo]` identifies which knob generated the event |
| `0x69` | Shared knob step-magnitude counter (read-to-clear); bits also carry direction (bit6/bit7) |

The 8×8 matrix is active-low. The FPGA decodes and counts quadrature in hardware; software reads
which row fired (knob identity by priority), the direction bit, and the shared magnitude in `0x69`.
See the front-panel decode spec for the full selector:bit button/knob map.

---

## 3. CS1 status registers

### 3.1 `0x39` — primary status

| Bit | Mask | Name | Meaning |
|---|---|---|---|
| 1 | `0x0002` | TRIG | A trigger edge was seen this acquisition |
| 2 | `0x0004` | DONE | Capture complete — the deep record is coherent for a halt+drain |

At the **decimated/deep bands (≥ 50 µs/div, class `0x80` divisor ≥ 8)** the wait gate is: poll
`0x39`; on bit2 (DONE) anchor the trigger position from `0x3a/0x3b`, then wait until
`0x46 & 0x07ff >= LatchAt`. In NORM (`0x35 = 0x0003`) bit2 asserts only on a real comparator edge,
so a completed frame is phase-locked. **This gate does NOT apply at native-fast bands** — see §3.2,
§3.3, §5 and §7.8.

### 3.2 `0x38` — secondary status

| Bit | Mask | Name | Meaning |
|---|---|---|---|
| 5 | `0x0020` | NFDONE | Native-fast capture-done gate; used as the fast-band fill/done indicator where `0x39` bit2 is unreliable |

`0x38` also reflects the engine's run/hold state (a settled, held-idle value versus a momentary
capture value). At the **native-fast bands (§5)** neither `0x39` bit2 nor `0x38` bit5 discriminates
a real trigger edge from an untriggered free-run fill — **both assert on the untriggered fill too**.
The shipping FSM therefore does **not** poll `0x38` in the native-fast path (`0x38` bit5 = NFDONE is a
diagnostic-only capture-done indicator); it waits a bounded fill budget on `0x39`/`0x46`, halts
unconditionally when the budget expires, and decides *whether a real edge was captured* purely from
**captured content** (peak-to-peak + valid same-slope crossing, §9). It never gates the fast-band
publish on a status bit.

### 3.3 Trigger-position latch trustworthiness (`0x3a/0x3b`)

`0x3a/0x3b` hold a usable trigger position **only at the decimated/deep and slow bands**, where the
comparator crossing lands in-window. At the **native-fast bands (100 ns – 20 µs/div)** the register
is **not trustworthy**: the real edge lands mid-record and the latch does not give a usable position
(it jitters, std ≈ 89 codes). At native-fast the firmware MUST NOT anchor the render to `0x3a/0x3b`;
instead it **locates the edge in the drained sample content and re-centres in software** (find edge →
shift to record centre), and sets the render trigger position from that software-found index. Keep
the `0x3a/0x3b` anchor only for the decimated/deep bands.

---

## 4. CS1 value formats

### 4.1 `0x21` arm / halt opcodes

| Value | Name | Effect |
|---|---|---|
| `0x00C0` | RESET-HEAD | Reset read/write head. Issued ×2 at the start of every arm; also phase-aligns the drain |
| `0x00C3` | GO | Arm / start the capture |
| `0x00C8` | **CAPTURE-HALT** | Latch the coherent frozen deep record and stop filling — the per-frame halt the drain reads. Freezes `0x30`–`0x34` |
| `0x00CB` | LATCH-NO-HALT | Snapshot the read pointer **without** halting; the FIFO keeps producing. Used only by the roll path (`0x41`/`0x59`) |

### 4.2 `0x35` run word

| Value | Mode |
|---|---|
| `0x0001` | AUTO (free-run) |
| `0x0003` | NORM (armed — hold for the HW comparator edge, decimated/deep bands only) |

### 4.3 Sample-port packing

Each read of a `0x30`–`0x34` or roll port returns one 16-bit word: **high byte = channel 1**,
**low byte = channel 2**. ADC codes are unsigned 8-bit per channel.

### 4.4 Divisor / sample interval

| Class (`0x19`) | Sample rate | Per-sample interval |
|---|---|---|
| `0x20` | 500 MSa/s | 2 ns (displayed as 1 ns/sample; see spec 04) |
| `0x01` | 250 MSa/s | 4 ns |
| `0x80` | 100 MSa/s ÷ divisor | `divisor · 10 ns`, where `divisor = 0x1a | (0x1b << 16)` |

**The physical class-`0x80` count period is 10 ns (100 MSa/s base).** This one value governs all
deep-record window sizing and the envelope divisor computation (which targets ~0.23 ms/sample and is
validated at that interval). The roll **read-pacing** constant in §7.7 is expressed as
`divisor · 50 ns`; that is a software read cadence (deliberately slower than the physical count so
every ioctl read pops a fresh, non-dwell sample), **not** a second physical count period. Do not use
the 50 ns figure for window/timebase sizing.

The `0x19` divisor **class + 32-bit divisor for a given s/div is NOT derivable from this document**;
it is computed by the timebase planner (spec 04, `PlanTdiv`). §5 gives only
the coarse band boundaries. Spec 02 alone is insufficient to select a specific timebase.

---

## 5. Band classification (which registers a timebase uses)

The timebase decides the register path. The concrete `(class, 32-bit divisor)` for each s/div is a
**normative cross-reference to spec 04** (`PlanTdiv`); the ranges below are the routing boundaries.

| Band | Timebase | Class / divisor | Path |
|---|---|---|---|
| Native-fast | 100 ns – 20 µs/div | class `0x20`, `0x01`, or `0x80` with divisor ≤ 4 | Deep capture-halt + drain `0x30`–`0x34`; **content-discriminated, software-centred** (§7.8) — do NOT gate on `0x39` bit2, do NOT anchor on `0x3a/0x3b` |
| Decimated (deep) | ≥ 50 µs/div | class `0x80`, divisor ≥ 8 | Deep capture-halt + drain, gated by `0x39` bit2 + `0x46` fill; anchor on `0x3a/0x3b` (§7.3) |
| Envelope | ≥ 5 ms/div | class `0x80`, moderate divisor | Deep drain reduced to per-column min/max |
| Roll | ≥ 100 ms/div | class `0x80`, large divisor | Free-run roll ports `0x41`/`0x59` (arm-once + per-update `0xCB`, §7.7) |
| ETS | ≤ 50 ns/div (opt-in) | class `0x20` | Many halted sub-acquisitions interleaved by a **software** sub-sample crossing phase (§5.1) |

The **physical deep-record depth is 20480 samples** — the drain stays a real captured waveform to
~20480 then reads a flat dead tail; there is no memory wrap below that. Drains are clamped to it.

### 5.1 ETS interleave phase source

ETS reconstructs a dense equivalent-time record from many halted sub-acquisitions. The sub-sample
phase for the interleave is a **software** measurement: in each drained record, take the source
channel's own mid-level rising crossing, sub-sample-interpolate its fractional position `xref`, and
map `frac = xref − floor(xref)` to one of `factor` phase bins. Each real sample `k` is placed on the
equivalent-time grid at `tEq = (k − xref)·Ts + W/2` (`Ts` = per-sample interval, `W` = the
10-division window in ns). Captures land at different sub-sample phases, so the grid densifies as
bins fill; gaps are linear-interpolated (never a fabricated edge). `0x3a/0x3b` is read for
**telemetry only** and is NOT used for the interleave — the FPGA comparator cannot HW-lock a fast
repetitive source from the register plane, so the register is unusable as the phase key.

---

## 6. CS3 — config / control plane

All selectors below are on plane 3. Access is ioctl (`WriteRegCS(3, sel, val)` /
`ReadRegCS(3, sel)`).

| sel | Byte addr | Name | Access | Value / format | Purpose |
|---|---|---|---|---|---|
| `0x07` | `0x2010000e` | **Config port / CONF_DONE** | R only at runtime | read: `& 0x80` = CONF_DONE | Read for FPGA-configured status. **NEVER WRITE at runtime** — writing this port is nCONFIG and reconfigures the FPGA, collapsing the acquisition engine |
| `0x09` | `0x20100012` | LED latch low | W | byte = `word & 0xff` | Panel-LED shadow low byte (part of the strobe sequence, §7.5) |
| `0x0a` | `0x20100014` | LED latch high | W | byte = `word >> 8` | Panel-LED shadow high byte |
| `0x0b` | `0x20100016` | LED latch strobe | W | `0` then `1` | Strobe/commit line for the LED latch |
| `0x10` | `0x20100020` | Offset DAC C1 low | W | `code & 0xff` | Vertical offset DAC, channel 1, low byte |
| `0x30` | `0x20100060` | Offset DAC C1 high | W | `code >> 8` | Vertical offset DAC, channel 1, high byte (self-latches) |
| `0x11` | `0x20100022` | Offset DAC C2 low | W | `code & 0xff` | Vertical offset DAC, channel 2, low byte |
| `0x31` | `0x20100062` | Offset DAC C2 high | W | `code >> 8` | Vertical offset DAC, channel 2, high byte (self-latches) |
| `0x14` | `0x20100028` | **Trigger LEVEL DAC lane A low** | W | `code & 0xff` | HW trigger comparator threshold, lane A, low byte |
| `0x34` | `0x20100068` | **Trigger LEVEL DAC lane A high** | W | `code >> 8` | Lane A high byte (self-latches lane A) |
| `0x15` | `0x2010002a` | Trigger LEVEL DAC lane B low | W | `code & 0xff` | Mirror lane B, low byte (write the SAME code as lane A) |
| `0x35` | `0x2010006a` | Trigger LEVEL DAC lane B high | W | `code >> 8` | Mirror lane B high byte (self-latches lane B). **NOT** the CS1 run word `0x35` — different plane |

The **trigger source mux is NOT on this plane** — it is CS1 `0x22` (byte `0x20200044`), listed in §2.
Do not write it as a CS3 register.

### 6.1 DAC value computation (calibration dependency)

The DAC **register formats** are fixed (below), but the **code value** to write for a meaningful
volts target is a normative dependency on the active per-V/div calibration record (spec 06/10) and
is out of scope for this document. For HW bring-up and sweeps, write **raw codes**.

**Trigger LEVEL DAC** — one 16-bit code, `code16 = (hi << 8) | lo`, written low-then-high per lane
and mirrored to both lanes. Higher level = lower code. The volts fit

```
code16 ≈ 31434 − 938 · V        # 0 V = 0x7aca = 31434; slope −938 codes/V
```

is exact only at 1 V/2 V-div; every other V/div rides the per-V/div cal ladder and needs the active
cal record. Boot-inherited anchor ≈ `0x754c`. The comparator responds only within a code window
(crossings latch into `0x3a/0x3b` only when the level sits on the signal); outside it `0x3a/0x3b`
read 0. For an unambiguous HW sweep, use the raw code.

**Offset DAC** — 16-bit code per channel; low byte then high byte, the high byte self-latches (no
strobe). The code moves the captured window's DC centre (the render reflects it with no render-side
change). Higher code → lower mean. The centre (0 V) code is the **calibrated per-(channel, V/div) zero**
from cal RAM record `+0x12` (spec 10 §7.4; boot default `0x27ef` = 10223); the fixed `~10600` is only the
**uncalibrated fallback** centre, not a per-detent constant. The usable linear span is ~9600–11600. A
general volts→code transfer function across all V/div requires the cal record and is **Open** (§11); use
raw codes for centring and sweeps.

---

## 7. Register sequences (write order matters)

### 7.1 Engine bring-up (enable + divisor)

Run once per band change, on the owner goroutine:

1. `0x44 = 0x0001`, `0x44 = 0x0000`  (reset-head strobe)
2. `0x35 = run`  (`0x0001` AUTO / `0x0003` NORM)
3. `0x36 = 0x0000`
4. `0x1b = 0x0000`  (clear divisor high first)
5. `0x19 = class`
6. `0x1a = divisor_lo`
7. `0x1b = divisor_hi`

Writes NO CS3 level/mask/slope — the boot comparator is inherited so `0x39` bit2 keeps asserting.
Clobbering CS3 during bring-up is what stops bit2 from ever firing.

### 7.2 Per-frame arm

1. `0x21 = 0x00C0`  (reset head)
2. `0x21 = 0x00C0`  (again)
3. `0x57 = 0x0001`, `0x57 = 0x0000`  (write-pointer reset pulse)
4. wait `ArmSettle` (default 2 ms)
5. `0x21 = 0x00C3`  (go)

### 7.3 Capture → halt → drain → re-arm (decimated/deep bands ≥ 50 µs/div)

1. Arm (§7.2).
2. Poll `0x39`: on bit2 (DONE) anchor `trigPos = (0x3b<<8)|(0x3a&0xff)`; then wait
   `0x46 & 0x07ff >= LatchAt`. Bounded budget (40–80 ms), poll interval ~150 µs.
3. `0x21 = 0x00C8`  (capture-halt). Confirm the halt froze: read `0x46` twice, expect equal.
4. Drain `drainCols` samples round-robin from `0x30`–`0x34` (mmap fast path). hi = C1, lo = C2.
5. Re-arm immediately (§7.2) — the engine refills before the frame is rendered.

The engine is halted only for the ~1 ms drain (steps 3–4); no other bus access may occur in that
window (§1.5). In NORM this path may **hold** (publish nothing) when `0x39` bit2 + `0x46` fill do
not both assert within the budget — that is the intended comparator gate. **This holding behaviour
applies ONLY to the decimated/deep bands.** For native-fast use §7.8.

### 7.4 Trigger LEVEL recommit (safe write)

Fired ONLY at the bus-idle frame boundary, once per change:

1. `WriteRegCS(3, 0x14, lo)`  `WriteRegCS(3, 0x34, hi)`  (lane A, hi self-latches)
2. `WriteRegCS(3, 0x15, lo)`  `WriteRegCS(3, 0x35, hi)`  (lane B mirror, same code)
3. `WriteReg(0x00, 0x80)` ×2  (reset/head preamble)
4. Full re-arm (§7.2)

A bare `0x14/0x34` poke off this boundary collides with the engine's in-flight burst and black-LCDs
the unit. Safety is serialization + inherited fd + frame-boundary timing + the following re-arm —
not a latch strobe. Cadence: once-on-change, never re-pushed per frame.

### 7.5 Panel-LED latch (best-effort)

Strobe order (all CS3):

1. `WriteRegCS(3, 0x0b, 0)`  (strobe low)
2. `WriteRegCS(3, 0x0a, word >> 8)`
3. `WriteRegCS(3, 0x09, word & 0xff)`
4. `WriteRegCS(3, 0x0b, 1)`  (latch)

LED shadow-word bit map (this clone):

| Bit | LED |
|---|---|
| `0x2000` | RUN (green) |
| `0x4000` | STOP (red) |
| `0x8000` | SINGLE |
| `0x0020` | CH1 |
| `0x0010` | CH2 |
| `0x0004` | TRIG'd |

`0xffff` lights every LED.

### 7.6 Offset DAC write (safe)

1. `WriteRegCS(3, lo_sel, code & 0xff)`  (CH1 `0x10` / CH2 `0x11`)
2. `WriteRegCS(3, hi_sel, code >> 8 & 0xff)`  (CH1 `0x30` / CH2 `0x31`, latches)
3. `WriteReg(0x35, run)`  (re-assert the run word so the front-end change leaves the engine coherent)

### 7.7 Roll bring-up (≥ 100 ms/div, ports `0x41`/`0x59`)

1. Bring-up (§7.1).
2. `0x21 = 0x00C0`; `0x57 = 1`, `0x57 = 0`; `0x21 = 0x00C3`  (arm ONCE).
3. wait ~3 ms.
4. Per update: `0x21 = 0x00CB` (latch-no-halt), then ioctl-read a paced batch from `0x41`/`0x59`.

**Never arm/halt on the roll path** — `0xC0/0xC3/0xC8` freeze the free-run. Pace the reads at a
software cadence of `divisor · 50 ns` (clamped ~50 µs … 40 ms); this is a read-pacing heuristic, not
the physical sample clock (§4.4). Rapid un-paced `0x41` reads wedge the FIFO and also re-read a
dwell value instead of a fresh sample.

### 7.8 Native-fast capture (100 ns – 20 µs/div)

At native-fast bands neither status bit discriminates a real edge, so this path **always** drains
and decides from content, in **both AUTO and NORM** (do NOT hold on `0x39` bit2 / `0x38` bit5):

1. Arm (§7.2).
2. Wait a bounded fill budget, polling `0x39`/`0x46` (not `0x38`); halt when the budget expires.
   `0x38` bit5 (NFDONE) documents the native-fast capture-done bit but is a diagnostic-only indicator —
   the shipping FSM does not poll it. Do NOT gate publish on any status bit.
3. `0x21 = 0x00C8`  (capture-halt). Confirm the halt froze: read `0x46` twice, expect equal.
4. Drain the record round-robin from `0x30`–`0x34` (mmap fast path). hi = C1, lo = C2.
5. **Content-discriminate** on the selected trigger-source channel (§9):
   `edgeFrame = (ptp ≥ nativeEdgeMinPtp) AND (a valid same-slope mid-level crossing exists)`.
6. If `edgeFrame`: locate the crossing, **software-centre** it to record centre, set the render
   trigger position from that index (`0x3a/0x3b` is ignored here, §3.3), publish.
   If not `edgeFrame`: HOLD the last edge frame, **except** after `nativeFlatFallback` consecutive
   no-edge frames publish one honest flat capture (liveness).
7. Re-arm immediately (§7.2).

The `norm && !nativeFast` early-hold of §7.3 is **bypassed** here — the edge is selected by content,
not the done-gate. The engine is halted only for the ~1 ms drain; §1.5 applies.

---

## 8. On-bus calibration registers (CS1)

These carry the per-V/div and per-acq-mode gain/offset calibration. The firmware writes them from
the calibration table (see spec 10); they are not part of the runtime acquisition loop.

| sel | Name | Purpose |
|---|---|---|
| `0x16` | Cal latch | Latch strobe for the `0x27`–`0x2a` gain-cal words |
| `0x27`–`0x2a` | Gain-cal ("cal32") | Per-acq-mode / per-V/div digital gain-cal coefficients (32-bit across 4 words) |
| `0x5a`–`0x7f` | Cal-coefficient bank | Calibration coefficient store |
| `0x01`–`0x0f` | Cal-coefficient bank (low) | Calibration coefficient store (low selectors) |
| `0x2c` | Bandwidth limit (BWL) | `0` = BWL on, `1` = BWL off |

Analog V/div gain and channel/trigger coupling are NOT GPMC registers — they are on spidev1.0 (relay
word: coarse V/div range bit + coupling) and spidev1.1 (fine gain DAC). See spec 06.

---

## 9. Native-fast content discrimination (thresholds)

At native-fast bands the publish decision is purely content-based (§3.2, §7.8). A frame passes iff
**BOTH** hold on the selected trigger-source channel's drained samples:

- **Amplitude:** `peak-to-peak ≥ nativeEdgeMinPtp = 40` codes. A flat capture is ~5 codes of ADC
  noise; a real cal edge is ~150. The threshold is amplitude-independent (flat-vs-edge separation).
- **Shape:** a valid mid-level crossing on the configured slope exists in the record.

**Liveness fallback:** after `nativeFlatFallback = 60` consecutive no-edge frames, publish one real
flat capture (a genuinely flat band must not freeze forever on a stale edge). Otherwise HOLD the last
edge frame between real edges (so noisy flat frames do not flash the display).

---

## 10. Load-bearing constraints (traps)

- **Single owner (§1.5).** Only the acquisition owner touches the bus. Panel reads, LED, offset and
  trigger-level writes are staged and flushed at the frame boundary. A stray access during a
  `0x21 = 0xC8` halt black-LCDs the unit.
- **Inherited `/dev/Gpmc` fd (§1.2).** Reuse the boot-inherited fd from `/proc/self/fd`; never open
  fresh, never `dup`, never `Close`, clear its finalizer. A fresh open fails the single-open guard or
  wedges the bus for seconds.
- **mmap needs `O_SYNC` and single 16-bit loads (§1.4).** `/dev/mem` mapped without `O_SYNC` returns
  cached/stale samples; a byte-pair load double-pops the auto-incrementing sample port and corrupts
  the drain.
- **Never write CS3 `0x07` at runtime.** It is the nCONFIG config port; a write reconfigures the
  FPGA and collapses the engine. Read it for CONF_DONE only.
- **`b[0]` (plane) must be 1 or 3.** `b[0] = 0` → garbage ioremap base → multi-second stall.
- **Pass the selector, not the byte address** — the driver shifts `<<1`.
- **Selectors are plane-specific.** `0x09` is a reject byte on CS1 but the LED-latch low byte on
  CS3; a CS3 write mis-issued on CS1 silently fails. Name the plane on every config actuator.
- **CS3 `0x35` ≠ CS1 `0x35`.** CS3 `0x35` = trigger LEVEL lane-B high byte; CS1 `0x35` = the run
  word. Different plane, do not conflate.
- **Do not clobber the inherited comparator during bring-up.** Bring-up writes no CS3 level/mask —
  writing them stops `0x39` bit2 from asserting.
- **Native-fast: no status gate, no `0x3a/0x3b` anchor.** At 100 ns – 20 µs/div both status bits
  assert on the untriggered fill and the position latch is junk; discriminate on content and centre
  in software (§3.2, §3.3, §7.8, §9). Gating on `0x39` bit2 there starves the display to ~2 fps.
- **Roll path never arms.** Arm/halt freezes the free-run; pace `0x41`/`0x59` reads.
- **Clamp drains to 20480.** Beyond the physical deep-record depth the ports read a flat dead tail
  (harmless but not signal).
- **Reused frame buffers carry stale metadata.** A real-time frame reusing a buffer last used by an
  envelope band must clear the envelope flag/columns, or the renderer draws the stale min/max band.
- **DAC volts→code needs the cal record (§6.1).** Only raw-code writes are self-contained; a
  meaningful volts target across all V/div depends on spec 06/10.

---

## 11. Open

- **Pre-trigger split `0x3c`/`0x3d`.** These registers set a pre/post-trigger record-depth split.
  On the single-owner capture-halt engine the trigger edge lands mid-record regardless of the split
  value, so the firmware does not rely on them; the exact field encoding is not established.
- **Trigger source mux CS1 `0x22`** (byte `0x20200044`, + `0x53` strobe). Engine-safe (CONF_DONE-safe
  coarse mux), but the concrete C1/C2/EXT code values are not pinned; runtime source selection is done
  in software on the drained samples (spec 05 §6).
- **Panel LED controllability.** The LED-latch write path (§7.5) commits the shadow word on CS3;
  whether every RUN/STOP/CH LED on a given clone is CPU-drivable or owned by the front-panel MCU is
  clone-dependent. The write is best-effort and a harmless no-op where the latch is MCU-owned.
- **Offset DAC volts→code, and trigger-level across all V/div.** Only the 1 V/2 V-div trigger-level
  fit and the raw-code offset centre (~10600) / linear span (~9600–11600) are established. A general
  volts→code map for either DAC needs the active per-V/div cal record (spec 06/10).
- **Class-`0x80` roll pacing constant.** Roll read pacing is expressed as `divisor · 50 ns` while the
  physical count period is 10 ns (§4.4). The 50 ns is a read-cadence multiplier (chosen to guarantee
  fresh non-dwell samples), not a second physical rate; the exact relationship of the pacing
  multiplier to the FIFO dwell time is not formally characterised.

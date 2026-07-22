# 03 — Acquisition Engine

The acquisition engine is a single goroutine (`Engine.Run`) that **exclusively owns the
inherited `/dev/Gpmc` fd** and is the only thing in the process that touches the GPMC
acquisition bus. It runs the per-frame capture FSM, drains a frozen sample record into a
private buffer, re-arms, then publishes a **copy** of the frame through a triple-buffer
arena. The renderer, the panel, the control plane, and the remote interface all consume
from the arena or submit commands; none of them ever issues a bus access.

This is a **clean-sheet owned design**: the app drives our owned acquisition FPGA through
the **generated** `iface` bindings (`app/internal/iface`, spec 02). Every selector, field
mask, access rule, and behavioral-semantics flag comes from the schema — the engine holds
no hand-packed masks or magic ranges. The FSM is a small, trustworthy cycle: **program →
arm → wait-on-real-`DONE` → capture-halt → burst-drain → re-arm → publish**. The fabric
provides a real trigger, an exact pre/post capture window, a live-stream min/max envelope,
and a static-freeze record (`fpga/standard/docs/DESIGN.md` §4–§7); the engine relies on
those behaviors rather than working around them.

Two properties are load-bearing for the whole design:

1. **Single bus owner.** The per-frame capture-halt (`OPCODE = OP_HALT`) latches a coherent
   frozen record, and the owner drains and re-arms it before handing the frame off, so no
   second bus consumer can overlap the halt→drain window or fire a re-arm mid-drain.
2. **Owned-fabric identity gate.** Before trusting the bus the engine verifies the
   build-ID handshake (`iface.Verify`, §11); a mispaired or wedged fabric is refused, never
   half-driven.

---

## 1. Driver interface

The engine reaches the fabric through the `bus.Bus` interface (`app/internal/bus`); every
method is called **only** on the owner goroutine. Two register planes are addressed by the
`iface.Plane` argument:

- **CS1 (acquisition plane).** `Read(iface.CS1, sel)` / `Write(iface.CS1, sel, val)`. The
  FSM uses this plane for the whole capture cycle. Engine helpers: `e.r(sel)` = read CS1,
  `e.w(sel,val)` = write CS1.
- **CS3 (config plane).** `Write(iface.CS3, sel, val)` (helper `e.w3(sel,val)`). Reaches the
  front-end command registers (LED latch, offset DACs, trigger-level DAC) and the read-only
  `CONF_DONE` config-status. Used only in `serviceCommands` (§9) and the wedge check (§11).

**Bus interface methods:**

| method | role |
|---|---|
| `Read(plane, sel) (uint16, error)` | one 16-bit register read through the plane window |
| `Write(plane, sel, val) error` | one 16-bit register write; refused unless `iface.Writable(plane,sel)` (schema write guard) |
| `BurstInto(c1, c2 []uint8, n int)` | drain `n` frozen record words from the single auto-inc `BURST` port in one tight pass — **hi byte → C1, lo byte → C2** |
| `ChannelInto(sel, dst []uint16, n int)` | read `n` packed words from a result-channel auto-inc port (`ENV_DATA`) |
| `MmapDrain() bool` | whether the drain uses the `/dev/mem` fast path |

**ioctl ABI, `/dev/Gpmc` inheritance, and the mmap fast path** are specified in spec 02
(§1.2–§1.4) and are not restated here. The two facts the FSM depends on:

- The drain may use a syscall-free mmap read over the CS1 window (`/dev/mem`, physical base
  `0x01000000`), a single un-inlined aligned 16-bit load (`load16`, `//go:noinline`) per
  bus transaction. It is trusted only after the mmap addressing re-reads `VERSION == 0x0052`
  (the double-shift trap); any failure falls back to the ioctl drain. `MmapDrain()` reports
  which path is live.
- A read of an **auto-inc** port (`BURST`, `ENV_DATA`) is a **mutation**: each read pops one
  word. It must never be deduplicated, speculated, or CSE'd — that is why the drain uses the
  single-load path and reads exactly `n` words.

---

## 2. Register surface

The register table is **generated** (spec 02 §3; `fpga/standard/docs/REGISTER-MAP.md`) and is
the single source of truth. The selectors below are the `iface.Sel*` constants the engine
binds; names, planes, and field masks track the schema.

### CS1 (acquisition plane)

| block | sel | name | role |
|---|---|---|---|
| meta | `0x10`/`0x11` | `BUILDID_LO`/`HI` | 32-bit schema build-ID; `(HI<<16)\|LO` must equal `iface.BuildID` (§11) |
| meta | `0x12` | `VERSION` | reads `0x0052` — addressing self-check |
| capture | `0x20` | `OPCODE` | capture strobe: `OP_GO`=`0x0001` (arm/re-arm) · `OP_HALT`=`0x0002` (freeze) · `OP_RESET`=`0x0000` (idle). Write-only, strobe semantics |
| capture | `0x21` | `RUN` | `MODE[1:0]` (0=AUTO, 1=NORM) + `RUN[2:2]`. Composed via `iface.RunWithMode(m) \| iface.RunWithRun(true)`: AUTO=`0x0004`, NORM=`0x0005` |
| capture | `0x22`/`0x23` | `DECIM_LO`/`HI` | 32-bit stream decimation factor (`cap_tick` once per `DECIM` base samples, §3) |
| capture | `0x24`/`0x25` | `PRETRIG_LO`/`HI` | 32-bit pre-trigger depth |
| capture | `0x26`/`0x27` | `POSTTRIG_LO`/`HI` | 32-bit post-trigger depth |
| drain | `0x30` | `BURST` | single fixed-address auto-inc frozen-record port (hi byte C1, lo byte C2). Read-after-halt |
| drain | `0x3e` | `BURST_REMAIN` | `READY[15]` + `REMAIN[14:0]` words-remaining / DMA-ready. Reserved for a flow-controlled DMA drain; the current FSM drains a known column count and does not poll it |
| status | `0x41` | `STATUS_A` | clean live level: `VALID[0]` (coherent record present / AUTO free-run timeout) · `TRIG[1]` (a comparator crossing was accepted) · `DONE[2]` (post-trigger record complete, drain open) |
| status | `0x42`/`0x43` | `TRIGPOS_LO`/`HI` | interpolating HW trigger position: `LO.FRAC[15:0]` (Q16 sub-sample), `HI.IDX[14:0]` (physical index). Read-after-halt. **Telemetry only** (§8) — the FSM reads only `HI.IDX` for `Frame.TrigPos`; centring is done in software |
| status | `0x44` | `FILL` | `COUNT[10:0]` fill progress (11-bit, mask `0x07ff`) |
| spine | `0x50` | `XFORM_CTRL` | `BYPASS0/1` transform-stage bypass. Left at the fabric default (stage-0 decimator active); the FSM does not write it |
| spine | `0x51` | `ENV_COLS` | envelope reducer column count (min/max folded on the live stream) |
| channels | `0x60` | `ENV_DATA` | envelope channel: successive packed record words, auto-inc, read-after-halt |
| channels | `0x61` | `ENV_COUNT` | `COUNT[14:0]` record count + `OVERFLOW[15]`. Live level |
| channels | `0x62` | `ENV_RESET` | clears the envelope FIFO (strobe) |

### CS3 (config / analog front end plane)

| block | sel | name | role |
|---|---|---|---|
| config | `0x07` | `CONF_DONE` | `DONE[7]` config/`CONF_DONE` status. **Read only** — writing collapses the config engine and is refused by the write guard. Read only in the wedge check (§11) |
| frontend | `0x09`/`0x0a`/`0x0b` | `LED_LO`/`LED_HI`/`LED_STROBE` | panel LED latch (§9) |
| frontend | `0x10`/`0x30` | `OFF_C1_LO`/`OFF_C1_HI` | C1 vertical-offset DAC (high byte self-latches) |
| frontend | `0x11`/`0x31` | `OFF_C2_LO`/`OFF_C2_HI` | C2 vertical-offset DAC |
| frontend | `0x14`/`0x34` | `LVL_A_LO`/`LVL_A_HI` | trigger-level DAC lane A; the `HI` write self-latches + loads the 3-wire serializer (strobe) |
| frontend | `0x15`/`0x35` | `LVL_B_LO`/`LVL_B_HI` | trigger-level DAC lane B (mirror of lane A) |

**Write guard is schema-derived.** `bus.Write` permits a write iff `iface.Writable(plane,
sel)` — false for every read-only register (`CONF_DONE`, all status/drain/channel ports)
and any undefined selector. The guard cannot drift from the fabric because both are generated
from the one schema. There are no hand-maintained forbidden ranges.

**Inherit the boot comparator.** The engine writes **no** CS3 registers in the frame loop; the
trigger-level and offset DACs are driven only through the command path (§9). Raw ADC code is
**polarity-inverted** (signal-rising = code-falling); every software slope test honours the
configured slope rather than assuming a code direction.

---

## 3. Band model

A `Band` (`bands.go`) is one ladder detent: `TdivS` (nominal s/div) plus a nominal
`(Class, Lo, Hi)` triple used **for reporting and native-fast classification only**. What
bring-up actually programs is the **stream decimation factor `DECIM`**, derived from the
band's real per-sample interval (§3.1); the app's timebase module maps each detent to a band
and the FSM consumes it — `SetTdiv` / `SetBand` stage the change and it is applied at the
frame boundary with a full bring-up.

`Band.Kind()` routes the frame path (predicates resolved in order):

| Kind | timebase | frame path |
|---|---|---|
| `KindNativeFast` | `≤ 20 µs/div` (class `0x20`/`0x01`, or class `0x80` divisor ≤ 4) | core real-time FSM (`oneFrame`), full deep-record drain |
| `KindDecimated` | `50 µs – 2 ms/div` (class `0x80` divisor ≥ 8) | core FSM, decimated drain; optional STREAM sub-path (§6) |
| `KindEnvelope` | `5 – 50 ms/div` | live-stream min/max band (`envFrame`, §4) |
| `KindRoll` | `≥ 100 ms/div` | scrolled halt/re-arm capture (`rollUpdate`, §4) |

`NativeFast()` is true for **every** class `0x20`, **every** class `0x01`, or class `0x80`
with divisor ≤ 4 (the ladder has no divisor 5–7, so the split is gap-free). The core FSM
therefore handles the entire fast band set down to `1 ns/div` — the fastest timebase is
native-fast, not a special engine.

**Envelope, roll, and ETS are separate frame paths on the same owner + arena**, specified in
full in spec 04:

- **envelope** (`5–50 ms/div`): arm → modest fill wait → halt → burst-drain the window →
  re-arm → publish. A repetitive, well-sampled signal publishes as a normal edge-centred
  trace; otherwise a per-display-column **MIN/MAX band** is taken from the fabric's envelope
  channel (`ENV_DATA`/`ENV_COUNT`, primary) or the software reducer over ~24 phase-scattered
  frames (fallback). Publishes every frame in AUTO and NORM.
- **roll** (`≥ 100 ms/div`): a slow, scrolled capture band. Each update arms, paces a
  fill, halts, burst-drains a batch (`rollBatch` = 1600), re-arms, and scrolls it into a ring
  reduced to the 800-column MIN/MAX band. It is an ordinary halt/re-arm band — the owned
  fabric halts and re-arms cleanly, so roll needs no free-run latch and no never-halt rule.
- **equivalent-time (ETS)** (`≤ 50 ns/div`, AUTO only, opt-in): interleaves many triggered
  sub-acquisitions by their software-measured sub-sample phase (`etsFrame`, `ets.go`).
  **Never auto-routed** — `SetETS(true)` opts in; every fast band otherwise renders the
  real-time trace by default.

### 3.1 Timebase → DECIM (input to bring-up)

Bring-up programs `DECIM = round(CaptureIntervalNs / baseTickNs)` (`Band.Decim`), where
`baseTickNs = 2.0` is the spine's base interval at `DECIM = 1`. `CaptureIntervalNs` is the
real per-sample interval of the captured record: class `0x20` = 2 ns, class `0x01` = 4 ns,
class `0x80` = `divisor · 10 ns`. Envelope/roll fold their **phase-scatter** divisor into
`DECIM` the same way.

| TdivS | Kind | capture interval | `DECIM` |
|---|---|---|---|
| 1 ns – 200 ns | native-fast | 2 ns | 1 |
| 500 ns, 1 µs | native-fast | 4 ns | 2 |
| 2, 5, 10 µs | native-fast | 10 ns | 5 |
| 20 µs | native-fast | 40 ns | 20 |
| 50 µs | decimated | 80 ns | 40 |
| 100 µs | decimated | 200 ns | 100 |
| 200 µs | decimated | 400 ns | 200 |
| 500 µs | decimated | 800 ns | 400 |
| 1 ms | decimated | 2 µs | 1000 |
| 2 ms | decimated | 4 µs | 2000 |
| 5 – 50 ms | envelope | `EnvPlan` divisor · 10 ns (per band) | derived per band |
| ≥ 100 ms | roll | `rollDivisor` (37000) · 10 ns = 370 µs | 185000 |

**Display window vs capture.** The **display** window (`WinCols`) sizes the 10-division
screen at the display interval. Class-`0x20` native-fast bands are captured at 2 ns/sample but
*displayed* at the 1 ns nominal (`displayIntervalNs`), so 10 divisions match the labelled
tdiv. `DisplayedSdivS` reports the s/div actually delivered after the window clamp.

**Drain depth** (`DrainCols`): native-fast drains the full `deepRecord` = **20480** samples
(the edge lands mid-record, §7); decimated drains `decimDrain` = **6144** by default
(`decimWin` = 2048 display window + centring margin), configurable to `deepRecord` via
`SetMemDepth`; envelope drains its window + centring margin; roll fills `rollWin` = 4096.

**Exact pre/post window.** `Band.capWindow = min(DrainCols, deepRecord − 2)`;
`PreTrig = capWindow/2`, `PostTrig = capWindow − capWindow/2`. So `pre + post ≤ REC_DEPTH − 2`
always — the arm-time clamp the fabric enforces (`fpga` doc §5): the drained array holds
exactly `pre` pre-trigger samples, the trigger sample at index `pre`, then `post − 1`
post-trigger samples, with the oldest pre-trigger cell never clobbered.

---

## 4. Engine bring-up (per band/mode change, not per frame)

`bringUp()` programs the capture block for the current band. Run once at start and again on
every band, `DECIM`, or trigger-mode change — **never per frame**. Writes are CS1, in order:

1. `OPCODE = OP_RESET` — idle the capture FSM before reprogramming.
2. `RUN = runWord()` — `MODE` (AUTO/NORM) + `RUN` bit (`0x0004` AUTO / `0x0005` NORM).
3. `DECIM_LO`, `DECIM_HI` — the stream decimation factor.
4. `PRETRIG_LO`, `PRETRIG_HI` — pre-trigger depth.
5. `POSTTRIG_LO`, `POSTTRIG_HI` — post-trigger depth.
6. Envelope/roll bands only: `ENV_COLS = 800`, then `ENV_RESET = 1` (clear the envelope FIFO).

No CS3 write, no config-port write, no per-frame reprogram. On a band change the reused arena
state and cross-frame accumulators are cleared first (§8, §10). When leaving an
envelope/roll band for a real-time band, `OPCODE = OP_RESET` is issued twice before bring-up
so the next armed capture starts clean.

`doReinit(level)` is a staged debug/recovery re-init run on the owner at a loop boundary: level
1 re-programs (identical to a band change); level 2 additionally issues `OP_HALT` → `OP_RESET`
+ 2 ms settle first. There are no "untried lever" pulses — a mispaired build is refused at
bring-up, and a healthy fabric always re-programs cleanly.

---

## 5. Per-frame FSM

Each iteration of the owner loop (`Run`):

```
serviceCommands()                    (§9 — the only place panel/LED/offset/level touch the bus)
bumpFrames()                         (heartbeat: +1 every iteration, stopped or not)
if not RUNNING: sleep 50 ms, publish nothing, continue
apply pending band/mode/ETS change at this boundary (§8, transition)
dispatch by Band.Kind():
    decimated + stream  → stitchFrame   (§6)
    roll                → rollUpdate     (§4, spec 04)
    envelope            → envFrame       (§4, spec 04)
    native-fast + ETS   → etsFrame       (§4, spec 04)
    otherwise           → oneFrame       (the core real-time FSM, below)
```

`oneFrame`:

```
ARM     OPCODE = OP_GO        (armEngineQuiet: arm-settle, then GO; engine begins filling)
WAIT    waitCapture(): bounded, paced poll of STATUS_A + FILL   (§5.2)
        if not ready (decimated NORM without a filled trigger): holdFrame; pace; return
HALT    OPCODE = OP_HALT; read FILL twice — equal = froze (halt-confirm telemetry)
DRAIN   BurstInto(C1, C2, cols)   (single auto-inc BURST port, §7)
RE-ARM  OPCODE = OP_GO            (engine fills again BEFORE the frame is discriminated)
        [ERES boxcar → software centring → publish decision — §7, §8]
PUBLISH arena.Publish()           (only if this frame is to be shown — §8)
pace    (§5.3: never publish faster than the ~50 ms floor; holdoff extends it)
```

The drain and re-arm both complete before `Publish`, so the engine is halted only for the
drain and is filling during the whole render.

**Constants** (`engine.go`, `bands.go`): `ArmSettle` = 2 ms (`tuneArmSettleUs`); poll pace
`PollEvery` = 150 µs; publish floor `FramePeriod` = 50 ms; deep record = 20480; `latchAt`
(FILL gate) = `0x200`; `fillFull` (AUTO saturate) = `0x7f0`; `fillMask` = `0x07ff`;
native-fast signal threshold `nativeEdgeMinPtp` = 40 codes ptp; flat-hold fallback
`nativeFlatFallbck` = 60 held frames; AUTO liveness wall-clock ceiling `autoLivenessMaxWait`
= 1500 ms.

### 5.1 Arm

`armEngine()` / `armEngineQuiet(quietHeld)`: hold the quiet gate (§7), sleep (or busy-spin)
the arm-settle, then `OPCODE = OP_GO`. The arm-settle holds the single core so no competing
goroutine perturbs the capture-setup window. Re-arm after the drain uses the same call.

### 5.2 Wait gate

`waitCapture(norm)` polls `STATUS_A` + `FILL` every `PollEvery` within the band budget and
returns `(anchored, sawTrig, filled, fillMoved, trigPos)`:

- **Budget** = `Band.WaitBudgetNs` = `clamp(3 · CaptureIntervalNs · latchAt, 40 ms, 80 ms)`.
- Each iteration:
  - `STATUS_A.TRIG` (bit1) → mark `sawTrig`, timestamp, latch `TRIGPOS.idx` into `trigPos`.
  - `STATUS_A.DONE` (bit2), or in AUTO `STATUS_A.VALID` (bit0, the free-run timeout) →
    `completed`; on first `completed` mark `anchored` (latch `trigPos` if no trig yet).
  - `FILL & fillMask`: if it changed since the first read, mark `fillMoved`; if `≥ latchAt`,
    mark `filled`.
- **Native-fast** returns as soon as `filled && (anchored || sawTrig || AUTO)`. The deep
  record fills in ~µs; an untriggered AUTO frame free-runs its live view rather than burning
  the whole budget; NORM without a trigger waits the full budget, then holds.
- **Decimated** returns on `anchored && filled` **plus**: a triggered capture waits out the
  post-trigger record time from the edge (`postNs = denseNs · (1 − trigPosFrac) · 1.15`), and
  decimated NORM additionally waits `denseNs` (the time to clock a full `drainCols` record) so
  software centring has a dense record, not the sparse triggered gate. `denseNs =
  drainCols · CaptureIntervalNs`. An AUTO decimated frame with no trigger returns early once
  `FILL ≥ fillFull` (the record saturated — drain it now).
- Returns on `stopReq` (abandon armed+filling — safe) or budget expiry (AUTO free-runs a
  refresh; NORM holds). The heartbeat (`beatN`) advances every poll.

`STATUS_A` bits are **clean live levels** (`DONE` means done); the gate reads them directly
and never content-discriminates as the primary readiness signal. `FILL` is a fullness check
*after* `anchored`, never a trigger-ready gate on its own.

### 5.3 Loop pacing floor

`pace` / `paceHold` cap the RUN loop so the **published** rate never exceeds ~20 fps (~50 ms
minimum period, `FramePeriod`). This ARM SoC is shared between the engine, the renderer, and
the panel; pushing the loop past the floor starves the SoC and *drops* served fps and
cross-frame uniformity. A trigger **holdoff** (`SetHoldoff`, 0–10 s) raises the floor after a
genuinely triggered publish so a bursty waveform re-triggers on the same event. Long paces
`sleepBeating` in ≤ 500 ms slices, beating each slice so the liveness supervisor sees a healthy
scope, not a wedge.

---

## 6. Halt, drain, and channel layout

**Halt.** `OPCODE = OP_HALT`. This freezes the record and opens the drain. Immediately read
`FILL` twice (up to 5 tries, accept the first equal pair) — equal reads confirm the fill froze
(`halt()` returns `HaltOK`, the final read is `FillAtHalt` telemetry). The frozen record is a
**static freeze**: fill has stopped, nothing writes the record M9K, and the single read port is
CPU-paced. It is coherent to the full `deepRecord` = 20480 samples.

**Drain.** `BurstInto(f.C1[:cols], f.C2[:cols], cols)` reads `cols` words from the **single
fixed auto-inc `BURST` port** — one bus transaction per read, the port auto-increments through
samples `0 … cols−1`, **hi byte = C1, lo byte = C2**. There is one drain port and one pass; no
port cycling, no per-channel ports, no modulo. `cols = effDrainCols()`: the configured memory
depth on decimated bands (full `deepRecord` when a single-shot is armed), the band's own
`DrainCols` elsewhere. The drain uses the mmap fast path when available (§1) and falls back to
ioctl reads otherwise.

**Re-arm** immediately (`OP_GO`) so the engine is filling again before the frame is
discriminated and published.

**Static-freeze / quiet gate.** Because the frozen record is immutable after halt, a concurrent
LCD framebuffer blit on the shared memory bus cannot corrupt the drain. The engine currently
still holds the `quiet` gate — pausing the LCD render / web serialize / panel — across the
load-sensitive windows (arm-settle + drain, and for native-fast the whole arm→fill→halt→drain,
since a competing CPU/memory burst *during the fill* was proven to freeze the capture). This
gate is **retired once the fabric's static-freeze byte-identity test passes on the bench**
(`fpga` doc §7/§9); the single-owner property, not the gate, is what makes the drain safe.

**Envelope/roll drain** uses `ChannelInto(ENV_DATA, …)` for the fabric envelope band (results,
O(columns)) or `BurstInto` for the raw window; see §4 and spec 04.

**STREAM sub-path** (`stitchFrame`, decimated + `streamMode`): arm → **pure timed wait** of
exactly `cols · CaptureIntervalNs` (no trigger/saturation poll) → halt → burst-drain → publish
**every** window raw and contiguous with continuity metadata (`StreamSeq`, `WindowNs`, `GapNs`),
arming once and un-pacing publishing. The client stitches consecutive windows on one axis.

---

## 7. Software centring and the publish decision

The HW `TRIGPOS` latch is carried as telemetry (`Frame.TrigPos`, from `TRIGPOS_HI.IDX`); the
displayed edge is **centred in software** off the drained samples. Centring primitives
(`discern.go`), all judging slope in code space (honouring the polarity-inverted ADC and the
configured slope):

1. Select the discrimination channel `disc`: C1 by default, C2 when the trigger source is
   channel 2. `lo, hi, p = ptp(disc)`.
2. **Signal-present evidence.** `sigPresent = p ≥ nativeEdgeMinPtp` (40). On decimated bands a
   small signal also qualifies when `signalPresent(disc, sigK)` — `p ≥ sigK · noiseFloor(disc)`
   — which separates a real sub-division signal from a noisy flat rail at any timebase
   (`sigK` = 8 default). Native-fast keeps raw ptp only (its record spans < 1 period).
3. **EDGE anchor (WYSIWYG).** `td = trigDispLevel(src)` maps the user's trigger-level DAC code
   (plus the channel's applied offset) to a display code 0–255 — the same level the on-screen
   line uses. `edgeX = centerCross(disc, td, rising)`. If the level is set but sits **off the
   signal band** (`td < lo − (hi−lo)/16` or `td > hi + …`), no lock is possible. When no level
   is set (`code 0` = boot comparator inherited), `td = −1` and the anchor falls back to a
   mid-level crossing `centerCross(disc, (lo+hi)/2, rising)` (`= midLevel`) to keep the first
   frames stable.
4. For PULSE/SLOPE/VIDEO trigger types the type's software qualifier replaces the EDGE anchor
   (§7.2); its returned sub-sample position is `edgeX`.
5. `Frame.EdgeX = edgeX`, or `−1` for a flat rail / no lock (a held frame carries `−1`).

### 7.1 `centerCross(sig, lvl, rising) → float64`

Finds the qualifying level crossing nearest the frame centre and returns its sub-sample
position, or `−1` if none qualifies. A crossing at index `c` qualifies when rising:
`sig[c−1] < lvl && sig[c] ≥ lvl` (falling mirrored). It anchors only a **CONFIRMED** crossing,
using noise-scaled hysteresis: going outward from the crossing the trace must reach the far
state (`≥ lvl+h` after / `< lvl−h` before, for rising) **without bouncing back first**, where
`h = clamp(round(hystK · noiseFloor), hystMinCodes, …)` (`hystK` = 4, `hystMinCodes` = 2,
search bounded by `hystMaxReach` = 2048). This rejects single-sample noise blips and the
opposite-slope dither that a fixed-count majority window confirmed on a slow ramp — the residual
rising⇄falling flip. Among confirmed crossings it picks the one nearest the reference (frame
centre by default; a phase-continuity `hint` parameter exists but the shipping EDGE path passes
none). The sub-sample fraction is `(lvl − sig[c−1]) / (sig[c] − sig[c−1])` clamped to `[0,1]`.
The confirmed-crossing hysteresis **is** the shipping slope validation; `windowSlopeMatches`
(the adjacent-plateau comparator) is retained in `discern.go` as a helper but is not on the
EDGE path.

Supporting helpers: `ptp` (min/max/peak-to-peak); `midLevel` = `(min+max)/2`; `noiseFloor` =
median `|s[i+1] − 2·s[i] + s[i−1]|` (a period-independent second-difference noise estimate,
floored at 0.5); `validDepthP` estimates how many leading samples carry real signal before a
flat dead tail (drain-sizing / telemetry).

### 7.2 Non-EDGE trigger qualifiers (PULSE / SLOPE / VIDEO)

There are four trigger **types**: `TrigEdge`, `TrigPulse` (GLIT), `TrigSlope` (SLEW),
`TrigVideo` (TV). The three qualifiers (`qualify.go`) run a **software qualifier** on the
drained record that **replaces** the EDGE pipeline; a qualifying event returns `edgeX` and
anchors + publishes the frame, no event holds the display. All level thresholds are fractions
of the frame's own `[min, max]` span (band- and V/div-independent); a span under
`flatRejectSpan` (40 codes) returns no event.

- **`qualifySlope`** (SLEW): a monotone `lo→hi` (rising) / `hi→lo` (falling) traversal
  between `slopeLoFrac`/`slopeHiFrac` (default 0.2 / 0.8), bailing if the level reverses past
  the first threshold by more than `span/10`. Qualify the traversal time against
  `[tMin,tMax]`/`cond`; anchor at the **second-threshold** crossing nearest centre.
- **`qualifyPulse`** (GLIT): a `high` pulse (default) enters on a rising crossing of
  `pulseLvlFrac` (0.5) and exits on the next falling crossing (`low` mirrored). Qualify the
  width against `[wMin,wMax]`/`cond`; anchor at the **completing** edge nearest centre.
- **`qualifyVideo`** (TV): sync-separate at 30 % up from the sync tip (low rail for negative
  sync, high for positive); collect crossings *into* the sync region as line boundaries. For
  line `N` (clamped to the standard's max — PAL ≤ 625 / NTSC ≤ 525) anchor at the `N`-th sync
  edge; for any-line (`line 0`) anchor at the sync edge nearest centre. Field/odd-even
  discrimination is not implementable on a partial record and never silently mis-triggers.

### 7.3 Publish policy

Only frames with a **lock** are displayed; a lock is a validated triggered event on the
captured **content**: `edgeX ≥ 0` and (`qualifier` or `sigPresent`), plus (decimated only) a
`coherent` capture. `oneFrame` decides:

| case | behaviour |
|---|---|
| **lock** | publish, centred on `edgeX`; reset the flat-hold counter |
| **AUTO EDGE, level off signal, coherent** | free-run an unlocked live capture at the record centre (`EdgeX = −1`, `Trigd = false`) — never claim a trig, never freeze |
| **AUTO native-fast, comparator did not fire** | free-run a live refresh at the record centre (`EdgeX = −1`) instead of holding |
| **native-fast flat / AUTO decimated flat, no signal** | HOLD, but publish one honest flat capture (`EdgeX = −1`, never a fabricated edge) every `nativeFlatFallbck` (60) holds |
| **NORM quiet / un-fired qualifier / AUTO not-locked** | HOLD the last locked frame. AUTO liveness: an honest unlocked refresh every 60 holds **or** after `autoLivenessMaxWait` (1.5 s), so AUTO never freezes on a live signal; NORM holds strictly |

In NORM a quiet screen is a legitimate held display, never a random untriggered frame. Content
discrimination is not synthesis: every displayed frame is real captured samples; a flat capture
is dropped or shown flat, never fabricated into an edge. `Frame.Degraded` is always `false` —
the owned fabric drains a clean full record, never a half-capture.

**Post-processing on the drained record** (real samples, no synthesis; `modes.go`):

- **ERES** (`AcqEres`): a symmetric odd-length **boxcar** (`eresBoxcar`) applied in place to
  the whole record for **both** channels **before** discrimination, so anchor and display see
  the enhanced samples. Length `L = clampEresLen(round(4^bits))` ∈ `[1,64]`, forced odd
  (`L ≤ 1` disables). Ends shrink the kernel — no wrap, no fabricated tail.
- **AVERAGE** (`AcqAverage`): a published, coherent, edge-aligned frame is replaced by the
  per-column **mean of the last N** edge-aligned frames (`avgRing`). `N = avgCount`
  (`SetAvgCount`, default 16, menu {4,16,32,64,128,256}, clamped `[1,256]`). Each frame is
  aligned so its crossing lands at the window centre before accumulation; only published
  coherent captures with a real edge enter the ring; the ring clears on
  acq-mode/depth/band/NORM changes (`avgKey`). `AcqPeak` is accepted but is a no-op at
  real-time bands (envelope/roll are peak-detect by construction).

**Observe-only consumers** run on a locked frame and observe, they do not gate acquisition
(their own specs): serial-protocol trigger (`serialtrig.go`, re-anchors and may hold non-matching
frames), zone trigger + mask test (`zonemask.go`), and FRA/Bode (`bode.go`). The cross-frame
uniformity ring (`uniRing`) is pushed on every published frame for the `WinColStd/Raw/Max`
telemetry.

---

## 8. Frame metadata — set and clear on the reused buffer

The arena's three frame buffers are reused in place. Every producer path **must** set the
frame's metadata and **must clear** any state a previously-visited band left, or the renderer
takes the wrong draw path.

Set on every real-time frame (`oneFrame`): `Valid = cols` (consumers slice `C1[:Valid]`),
`WinCols`, `EdgeX`, `Interp = NativeFast()` (request linear interpolation — native-fast windows
fewer real samples than the panel is wide), `Ptp`, `Trigd` (comparator fired), `TrigPos`
(telemetry), `Coherent`, `HaltOK`, `Degraded = false`, `RollCodes = false`, `TdivS`,
`DisplayedS`, `SampleS`, `Norm`. `Seq` advances only on a real publish.

**Mandatory clears (real-time producer):** `IsEnv = false`, `EnvCols = 0`. A slow/roll band
leaves `IsEnv = true` plus envelope min/max pairs in the buffer; if a real-time producer does
not clear them the renderer takes its envelope-fill branch and paints the stale min/max band
after a slow→fast timebase change. Every real-time path (the core FSM, STREAM, ETS) clears them.

Band/mode/ETS changes are staged (`pendBand`/`pendSet`, `norm`, `etsWant`) and applied at the
frame boundary by `transition`: idle the FSM when leaving envelope/roll, adopt the new band,
`clearCrossFrame` (flat-hold, envelope ring, roll snapshots, ETS accumulator, uniformity ring,
average ring), re-run bring-up, and (roll) `rollBringUp`.

---

## 9. Command boundary — single-owner bus discipline

The panel, control plane, and front-end are **command producers**, not bus writers. Every GPMC
access other than the frame FSM is serviced by the owner in `serviceCommands()`, called at the
**top of each loop iteration** — the frame boundary, where the engine is armed and filling,
never inside a halt window. A stray CS3 or matrix access during the halt window is exactly what
wedges the bus. Commands are staged into coalescing last-wins shadows with dirty flags, so a
burst of knob deltas applies as one write per frame.

`serviceCommands()` drains, in order:

1. **Key-matrix reads** (CS1): drain every pending request with **one** snapshot — read
   selectors `0x64,0x65,0x66,0x67,0x69` once and reply to all. These config-plane reads do not
   pop the sample FIFO.
2. **LED latch** (CS3): the indivisible 4-write strobe `LED_STROBE=0 → LED_HI=word>>8 →
   LED_LO=word&0xff → LED_STROBE=1`.
3. **Offset DAC** per channel (CS3): low byte then self-latching high byte
   (`OFF_C1_LO`/`OFF_C1_HI`, `OFF_C2_LO`/`OFF_C2_HI`); then re-assert `RUN` (CS1) so the
   front-end change leaves the engine coherent.
4. **Trigger level DAC** (CS3) — the safe recommit: write the level quad both lanes equal, the
   high byte self-latching + loading the serializer (`LVL_A_LO=lo, LVL_A_HI=hi, LVL_B_LO=lo,
   LVL_B_HI=hi`), then a full re-arm (`armEngine()`, `OP_GO`). A bare level poke off this
   boundary wedges the display; serialization on the single owner + the bus-idle arm boundary +
   the following re-arm is what makes it safe.

Envelope/roll frame paths, whose fill waits span many hundred ms, pump `serviceCommands()`
mid-fill (never a halt window) so a knob step does not wait out the whole frame. Analog V/div
(spidev) is off the GPMC bus and is driven directly by the front end, not queued here.

**STOP** keeps the FSM armed and alive on the bus (so it never wedges) but publishes nothing;
the display holds. Commands — including RUN to resume, and band changes — are still serviced
every ~50 ms. `Stop(timeout)` exits the owner at the next boundary (never mid-halt), leaving the
engine in a safe armed+filling state.

---

## 10. Frame hand-off — the triple-buffer arena

The arena (`arena.go`) is a lock-based triple buffer of three preallocated frames with
**drop-newest** backpressure: `write` (the producer's private fill slot the drain writes into),
`ready` (the most-recent completed frame), `read` (the consumer's private slot).

**Sizing.** All three frames are preallocated to the **deepest drain any band can reach — the
`deepRecord` = 20480-sample native-fast record — not the initial band's `Cols`.** A runtime
band change can switch to a native-fast band that drains the full record; a smaller buffer would
overrun. `newArena(deepRecord)`; envelope buffers are sized to `envDisplayCols` = 800.

**Publish** swaps `write ⇄ ready` under a microsecond critical section and marks `dirty`.
**Consume** returns `(ready→read, true)` if a new frame arrived, else `(read, false)` so the
renderer re-presents the held frame (a legitimately quiet NORM display, not a wedge). The mutex
guards only the RAM pointer swap; it is **not** on the GPMC bus, so single-bus ownership is
preserved. The producer never blocks on the render, the consumer never tears, and there is zero
steady-state allocation — the in-place drain is the copy. Each frame carries its own config
snapshot (`TdivS`, `SampleS`, …) so a mid-flight band change cannot tear the display.

**Two-channel capture.** Every `BURST` word carries **both** channels — hi byte = C1, lo byte =
C2 — so both channels de-interleave from the one drain on every band. The producer fills C2 only
when the configured channel count is > 1. On decimated bands C2 is a faithful independent
channel; on the fast classes independent per-sample C2 fidelity is not guaranteed and the
native-fast content discrimination is single-channel.

---

## 11. Telemetry and health

The owner exposes an un-fakeable live snapshot (`Stats`, `Snapshot()`): `Frames` (FSM
heartbeat), `Coherent`, `Published`, `Held`, `HaltConfirm` (fill froze after halt), `DeadRuns`,
`Wedged`, `FPS` (publishes in the trailing second), `LastPtp`, `LastTrigPos`, `ArmToLatch`,
`DrainMs`, `ValidDepth`, `WinCols`, `MemDepth`, and the cross-frame uniformity metrics
(`WinColStd`/`WinColRaw`/`WinColMax`) over the software-centred window, plus mode/band flags. A
per-frame `AcqSample` ring records every halt+drain frame (instrumentation only; it never
influences the FSM).

**Health / recovery:**

- **Build-ID handshake.** `iface.Verify(read)` runs at bus construction (`bus.New`) **and** at
  the top of `Run` before the first arm: `VERSION` must read `0x0052`, and `(BUILDID_HI<<16) |
  BUILDID_LO` must equal the compiled `iface.BuildID`. Any mismatch → mark `Wedged`, refuse to
  drive, and park (no dual mode, no fallback). Re-checking at the run-loop top catches a
  re-flashed or wedged fabric.
- **Liveness heartbeat.** `Beats()` (`beatN`) advances on every loop iteration **and** inside
  every legitimate long wait (holdoff pacing, budget polls, envelope/roll fills, recovery
  bring-up). The OTA health token keys on this, not on frame count — a 10 s holdoff between
  frames is a healthy scope, and a frame-keyed token would read it as a wedge.
- **Panic containment.** A top-level `recover()` in the owner goroutine turns a panic into a
  logged wedge event and parks the goroutine (health stops advancing, the agent relaunches on
  the still-live fd) — never fd loss or a fast-exit crash-loop.
- **Wedge ladder** (`deadEvidence`): a frame whose `FILL` never advanced and whose drain is flat
  is dead evidence. Every 10 dead frames re-assert bring-up; at 50, mark `Wedged`. On the
  drain path a healthy-but-flat native-fast input is indistinguishable from a wedge by
  fill+ptp alone (the 11-bit counter can sit saturated between polls), so `Wedged` there
  additionally requires a dead fabric — `CONF_DONE` (`CS3 0x07` bit7) reading clear; otherwise
  the engine keeps re-asserting bring-up and surfaces `DeadRuns` instead of crash-looping a
  healthy app. A decimated dead frame (a small frozen fill that could not be counter saturation)
  is certain wedge evidence and marks `Wedged` directly.
- **`/dev/Gpmc` is single-open.** It is inherited from the boot process and **never
  `Close()`d** (closing frees the chip select for the whole process tree); a fresh `open()` is a
  hard fault. See spec 01 §3 / spec 02 §1.2 for the inheritance and single-open rules — the
  engine assumes it already holds the working inherited fd.

---

## 12. Load-bearing constraints (why the design is shaped this way)

1. **Single bus owner.** Exactly one goroutine touches the GPMC bus. The capture-halt latches a
   coherent record only if no other access overlaps the halt→drain window, and a re-arm can
   never fire mid-drain. All panel/LED/offset/level access is queued and serviced at the arm
   boundary (§9).
2. **Owned-fabric identity gate.** Verify the build-ID before trusting the bus, and re-verify at
   the run-loop top; refuse a mispaired or re-flashed fabric outright (§11).
3. **Drain and re-arm before hand-off.** The engine is halted for the drain only, never across
   the render (§6).
4. **Program on change, not per frame.** `RUN`/`DECIM`/`PRETRIG`/`POSTTRIG` are written by
   bring-up on a band/mode change; the frame loop never reprograms them (§4).
5. **Never write the config port.** `CONF_DONE` (CS3 `0x07`) is read-only; the schema write
   guard refuses it. Write no CS3 comparator registers in the frame loop (§2).
6. **Exact pre/post window.** `pre + post ≤ REC_DEPTH − 2`; the drained trigger sample sits at
   index `pre` (§3).
7. **Gate on `STATUS_A`, a clean level.** `DONE` means done; `FILL` is a fullness check after
   `anchored`, never a trigger-ready gate alone (§5.2).
8. **Auto-inc reads are mutations.** `BURST`/`ENV_DATA` pop one word per single, un-inlined bus
   transaction — never dedup/speculate/CSE, and drain exactly `cols` words (§1, §6).
9. **The HW `TRIGPOS` does not index the displayed edge — centre in software** (§7). `TRIGPOS`
   is telemetry; `centerCross` with confirmed-crossing hysteresis is the anchor.
10. **Clear reused frame metadata**, above all `IsEnv`/`EnvCols`, on every real-time frame (§8).
11. **Pace the status poll** (150 µs) and **cap the published rate** (~50 ms floor); exceeding it
    starves the shared ARM SoC and lowers delivered fps and uniformity (§5).
12. **Size the arena to the deepest drain** (`deepRecord` = 20480), not the initial band's
    `Cols`, so a runtime switch to a native-fast band has somewhere to land (§10).
13. **Retire the quiet gate only on the static-freeze bench gate.** Until the fabric's
    byte-identity test passes, keep the render/web/panel pause across the load-sensitive windows;
    the single-owner property is what actually makes the drain safe (§6).

**Open:** the tightest cross-frame uniformity at the sub-cycle native-fast bands is a genuine
ceiling of software centring on a short record; it does not affect correctness or liveness.

# 03 — Acquisition Engine

The acquisition engine is a single goroutine (`EngineOwner`) that **exclusively owns the
inherited `/dev/Gpmc` fd** and is the only thing in the process that touches the GPMC
acquisition bus. It runs the per-frame capture FSM, drains a frozen sample record into a
private buffer, re-arms, then publishes a **copy** of the frame through a triple-buffer
arena. The renderer, the panel, the control plane, and the remote interface all consume
from the arena or submit commands; none of them ever issues a bus access.

This single-owner discipline is the load-bearing property of the whole design: the
per-frame capture-halt (`0x21=0xC8`) latches a coherent frozen record only if **no other
bus access overlaps the halt window**. Because the owner drains and re-arms before it hands
the frame off, the engine is halted for only the ~1 ms drain and is filling again for the
entire render. There is no second bus consumer to overlap that window.

---

## 1. Driver interface

Two register planes are reached through the driver:

- **CS1 (acquisition plane).** `WriteReg(sel, val)` / `ReadReg(sel)`. The driver maps the
  *selector* (not the byte address) to `0x20200000 + sel·2` — it applies the `<<1` shift.
  The FSM uses this plane for everything in the frame loop.
- **CS3 (config plane).** `WriteRegCS(plane=3, sel, val)`. Reaches the front-end command
  registers (LED latch, offset DAC, trigger-level DAC) through the config-plane ioctl.
  This capability is **optional**: if the driver does not expose it, all LED / offset-DAC /
  level-DAC writes are silently skipped and the inherited boot state is kept.

A syscall-free mmap read path over the CS1 window is used for the sample drain (§6); it is
`~50×` faster than the ioctl read and is safe **only** on ports frozen by a preceding
`0x21=0xC8` halt.

---

## 2. Register surface

### CS1 (acquisition plane — `WriteReg`/`ReadReg`)

| sel | name | role |
|---|---|---|
| `0x00` | re-anchor preamble | written `0x80` **twice** immediately before the full re-arm in the level-DAC recommit (§9). Not written anywhere else |
| `0x19` | divisor **class** | `0x20`=500 MSa/s (≤200 ns/div) · `0x01`=250 MSa/s (500 ns–1 µs) · `0x80`=100 MSa/s base (≥2 µs) |
| `0x1a` / `0x1b` | divisor **lo** / **hi** | 32-bit sample-clock divisor = `lo \| hi<<16` (class `0x80` only; class `0x20`/`0x01` use divisor 0) |
| `0x21` | **arm FSM opcode** | `0xC0` clear+reset-head · `0xC3` arm/fire · `0xC8` latch+**HALT**+reset-read-ptr · `0xCB` latch **without** halt (roll only) |
| `0x35` | **run word** | bit0 RUN, bit1 TRIGGER-ARMED. `0x0001`=AUTO free-run, `0x0003`=NORM armed |
| `0x36` | reset (secondary) | written `0` during bring-up |
| `0x44` | reset-head strobe | pulsed `1→0` bracketing the run-word write in bring-up |
| `0x57` | write-pointer reset **pulse** | pulsed `1→0` inside every arm |
| `0x38` | secondary status | bit5 (`0x20`) = comparator-fired. **Read only by the diagnostic native-fast probe; the shipping FSM never polls it** (§5) |
| `0x39` | primary status | bit1 (`0x02`) = **HW-triggered** (edge discriminated, `0x3a/0x3b` valid) · bit2 (`0x04`) = **frame FILLED/DONE** (the decimated frame-ready gate) |
| `0x3a` / `0x3b` | HW trigger position | 16-bit `= 0x3b<<8 \| (0x3a & 0xff)`. Jittery; telemetry only — it does **not** index the drained edge (centre in software, §7) |
| `0x46` | sample-write counter | 11-bit (`& 0x07ff`); the fill counter. Used as a FULL check **after** `0x39` bit2, never alone |
| `0x30`–`0x34` | **deep sample drain** | round-robin 5-port; **hi byte = C1, lo byte = C2**. mmap auto-increments; frozen and safe to read after `0xC8` |
| `0x64`–`0x67`, `0x69` | key-matrix reads | 8×8 active-low matrix + quadrature phase bits + shared step-magnitude counter; read as one snapshot in `serviceCommands` (§9). Config-plane reads — they do **not** pop the sample FIFO |

### CS3 (config plane — `WriteRegCS(3, sel, val)`)

| sel | name | role |
|---|---|---|
| `0x09` / `0x0a` / `0x0b` | **LED latch** | strobe sequence `0x0b=0 → 0x0a=word>>8 → 0x09=word&0xff → 0x0b=1` (§9) |
| `0x10` / `0x30` | **offset DAC C1** | low byte `0x10`, high byte `0x30` (high self-latches) |
| `0x11` / `0x31` | **offset DAC C2** | low byte `0x11`, high byte `0x31` (high self-latches) |
| `0x14` / `0x34` | **level DAC lane A** | comparator threshold, low byte `0x14`, high byte `0x34` (high self-latches). 16-bit `code = hi<<8 \| lo` |
| `0x15` / `0x35` | **level DAC lane B** | mirror of lane A, low `0x15`, high `0x35` (high self-latches) |
| `0x07` (`0x2010000e`) | FPGA config port | **never written at runtime** — any write reconfigures the fabric and wedges the engine |

**Plane-collision trap.** Selector numbers overlap between planes and mean entirely
different things:

- CS1 `0x30`–`0x34` (deep sample drain) vs CS3 `0x30`/`0x31` (offset-DAC high byte).
- CS1 `0x35` (run word) vs CS3 `0x35` (level-DAC lane-B high byte).

Always route by plane (`WriteReg` = CS1, `WriteRegCS(3,…)` = CS3). Crossing them corrupts
either the divisor/run state or the front-end DACs.

Raw ADC code is **polarity-inverted** (signal-rising = code-falling); every edge detector
must honour the configured slope rather than assuming a code direction.

**Inherit the boot comparator.** The engine writes **no** CS3 comparator registers
(`0x09`/`0x0a`/`0x0b`, `0x14`/`0x34`, `0x15`/`0x35`) during the frame loop. Clobbering the
inherited comparator is what makes the `0x39` bit2 done-gate stop asserting. The level DAC
is written only through the command path (§9), never in the frame loop.

---

## 3. Band model

Each band is fully described by a `(class, lo, hi)` sample-clock divisor plus a `TdivS`
(seconds/div). **The engine does not resolve a timebase itself** — `(class, lo, hi, TdivS)`
is a **required input** supplied to `Config` / `SetBand` / `SetTdiv` by the timebase module
(§3.1). Given a band, the engine computes drain depth, display window, and frame path.

Bands split into frame paths. This spec covers the **real-time** path (the core FSM);
envelope/roll (`≥5 ms/div`) and equivalent-time (ETS, opt-in) are separate frame paths
layered on the same owner and arena.

| path | timebase | class / divisor | done gate | drain depth |
|---|---|---|---|---|
| **native-fast** | ≤50 ns – 20 µs | `0x20` (all) · `0x01` (all) · `0x80` div ≤ 4 | **content only** (§6) — no status gate | `20480` (full deep record) |
| **decimated** | 50 µs – 2 ms | `0x80` div ≥ 8 | `0x39` bit2 DONE **and** `0x46` ≥ `LatchAt` | configured `Cols` (≤ `20480`) |
| envelope / roll | ≥ 5 ms | `0x80` moderate/large div | phase-independent | separate path |
| ETS / RIS (opt-in) | any class-`0x20` band | `0x20` | `0x39` bit2 via the wait gate (§5) | modest deep record per sub-acquisition |

`nativeFast(class, lo, hi)` is true for **every** class `0x20`, **every** class `0x01`, or
class `0x80` with divisor ≤ 4. Consequently the core real-time FSM handles the **entire**
fast band set down to the fastest timebase — `≤50 ns/div` is native-fast, not a special
case and not a separate engine.

**ETS is never auto-routed.** The timebase router returns `ets=false` for every timebase;
the fast bands render the real-time catch/hold trace by default. ETS is switched on only by
an explicit `SetETS(true)` opt-in for a genuinely fast repetitive source (e.g. a multi-MHz
sine), where it captures many triggered sub-acquisitions and interleaves their real samples
by measured sub-sample phase. Its per-sub-acquisition capture uses the same wait gate
(`0x39`, §5), **not** `0x38`.

**Why native-fast drains the full record.** At the fast classes the trigger edge lands near
the *middle* of the physical deep record (~sample 10190 of 20480), not in the first samples.
A shallow drain reads only the flat pre-trigger rail. Draining the full `20480`-sample record
captures the mid-record edge, which software-centring then floors. At 100–200 ns/div the
record spans far less than one signal period, so a faithful capture is a **flat rail** (ptp
~5) with no edge — this is correct, not a fault.

**Sample interval** (to size the display window to 10 divisions): class `0x20` = 2 ns,
class `0x01` = 4 ns, class `0x80` = `divisor · 10 ns`. The class-`0x20` bands are *displayed*
at the nominal 1 ns/sample window (`winCols = round(10·TdivS / 1 ns)`) even though captured at
2 ns/sample, so 10 divisions occupy the same screen fraction as the reference instrument.

### 3.1 Timebase → divisor table (input to the engine)

This is the required `(class, lo, hi)` per timebase step. The engine consumes it; it does
not compute it. `lo`/`hi` are the 16-bit halves of the class-`0x80` sample-clock divisor
(`div = lo | hi<<16`); class `0x20`/`0x01` use divisor 0.

| TdivS | class | lo | hi | | TdivS | class | lo | hi |
|---|---|---|---|---|---|---|---|---|
| 1 ns | `0x20` | `0x0000` | `0` | | 500 µs | `0x80` | `0x0050` | `0` |
| 2 ns | `0x20` | `0x0000` | `0` | | 1 ms | `0x80` | `0x00c8` | `0` |
| 5 ns | `0x20` | `0x0000` | `0` | | 2 ms | `0x80` | `0x0190` | `0` |
| 10 ns | `0x20` | `0x0000` | `0` | | 5 ms | `0x80` | `0x0320` | `0` |
| 25 ns | `0x20` | `0x0000` | `0` | | 10 ms | `0x80` | `0x07d0` | `0` |
| 50 ns | `0x20` | `0x0000` | `0` | | 20 ms | `0x80` | `0x0fa0` | `0` |
| 100 ns | `0x20` | `0x0000` | `0` | | 50 ms | `0x80` | `0x1f40` | `0` |
| 200 ns | `0x20` | `0x0000` | `0` | | 100 ms | `0x80` | `0x4e20` | `0` |
| 500 ns | `0x01` | `0x0000` | `0` | | 200 ms | `0x80` | `0x9c40` | `0` |
| 1 µs | `0x01` | `0x0000` | `0` | | 500 ms | `0x80` | `0x3880` | `0x001` |
| 2 µs | `0x80` | `0x0001` | `0` | | 1 s | `0x80` | `0x0640` | `0x003` |
| 5 µs | `0x80` | `0x0001` | `0` | | 2 s | `0x80` | `0x1a80` | `0x006` |
| 10 µs | `0x80` | `0x0001` | `0` | | 5 s | `0x80` | `0x3500` | `0x00c` |
| 20 µs | `0x80` | `0x0004` | `0` | | 10 s | `0x80` | `0x8480` | `0x01e` |
| 50 µs | `0x80` | `0x0008` | `0` | | 20 s | `0x80` | `0x0900` | `0x03d` |
| 100 µs | `0x80` | `0x0014` | `0` | | 50 s | `0x80` | `0x1200` | `0x07a` |
| 200 µs | `0x80` | `0x0028` | `0` | | | | | |

---

## 4. Engine bring-up (per timebase change, not per frame)

Run once at start and again whenever the band, the divisor, or the trigger mode changes.
Writes are on the CS1 plane, in this exact order:

1. `0x44 = 0x0001`, then `0x44 = 0x0000` — reset-head strobe
2. `0x35 = runWord` — `0x0001` (AUTO) or `0x0003` (NORM)
3. `0x36 = 0x0000`
4. `0x1b = 0x0000` (**clear divisor hi first**), then `0x19 = class`, `0x1a = lo`,
   `0x1b = hi` — the hi register is zeroed **before** the class/lo latch, then loaded with the
   real hi. Skipping the initial clear leaves a stale hi for any band whose divisor is ≤ 16
   bits (`hi = 0`) after a slow band, mis-programming the sample clock.

No config-port write, no per-frame reprogram. Bring-up writes no CS3 comparator registers
(§2). On a band change, additionally clear the reused arena state (§8).

---

## 5. Per-frame FSM

Each iteration of the owner loop:

```
serviceCommands()                    (§9 — the only place panel/LED/offset/level touch the bus)
if STOP: sleep 50 ms, publish nothing, continue
apply pending band/mode change at this boundary (§8)
ARM     0x21=0xC0 ×2 → 0x57=1 → 0x57=0 → sleep ArmSettle → 0x21=0xC3
WAIT    bounded, paced poll of 0x39 (bit1 trig, bit2 done) + 0x46 fill (§5.2)
HALT    0x21=0xC8                     (latch the coherent frozen record; ~1 ms window opens)
DRAIN   0x30-0x34 round-robin into the producer slot (§6)
RE-ARM  0x21=0xC0 ×2 → 0x57 → 0x21=0xC3   (engine fills again BEFORE the frame is rendered)
        [ERES / edge-detect / publish decision — §7]
PUBLISH arena.Publish()  (only if this frame is to be shown — §7)
pace    (§5.3: never publish faster than the ~50 ms floor)
```

The `0xC0` reset also phase-aligns the read pointer frame-to-frame. The drain and re-arm both
complete before `Publish`, so the engine is halted only for the drain and filling during the
whole render.

**Constants:** `ArmSettle` = 2 ms; `LatchAt` (0x46 fill target) = `0x200`; coherent frozen
record depth = `20480` samples; native-fast edge threshold `nativeEdgeMinPtp` = `40` codes
ptp; native-fast flat-hold fallback `nativeFlatFallback` = `60` frames; poll pace = 150 µs.

### 5.1 Arm

`armEngine()` (CS1):

1. `0x21 = 0xC0`  (twice)
2. `0x57 = 0x0001`, then `0x57 = 0x0000`  (write-pointer reset pulse)
3. sleep `ArmSettle` (2 ms)
4. `0x21 = 0xC3`  (go — engine begins filling)

### 5.2 Wait gate

`waitTriggered()` arms then polls `0x39` with a **bounded, paced** loop:

- **Budget** = `clamp(3 · interval · LatchAt, 40 ms, 80 ms)`, where `interval = 10 ns · divisor`.
- **Poll pace** = 150 µs between reads. The status bits are persistent levels, not
  transients, so a busy-spin here starves the renderer and the remote interface and drops the
  display below 1 fps.
- Each iteration:
  - if `0x39 & 0x02` (bit1): mark `sawTrig`, latch trigger position `0x3b<<8 | (0x3a & 0xff)`.
  - if `0x39 & 0x04` (bit2) and not yet anchored: mark `anchored`; if no trig yet, latch position.
  - if `anchored` and `(0x46 & 0x07ff) ≥ LatchAt`: mark `filled`.
  - break when `anchored && filled`.

**Decimated bands** require `anchored && filled` — bit2 can assert on the edge before the
post-trigger record has finished filling, so a bare bit2 break grabs a half-empty frame at
large divisors.

**Native-fast bands do NOT gate on the wait outcome.** `0x39` bit2 does not reliably assert
for a real native-fast edge capture (edge frames commonly hit the wait deadline un-`filled`),
so a status early-hold would reject nearly every edge frame and starve the display. Native-fast
therefore **always** proceeds to halt+drain and discriminates purely on captured-sample
**content** (§6). No `0x38` poll is performed anywhere in the shipping FSM.

### 5.3 Loop pacing floor

Cap the RUN loop so the **published** rate never exceeds ~20 fps (~50 ms minimum period). At
native-fast free-run bands the trigger fires immediately, so ARM→DRAIN→RE-ARM can otherwise
loop far faster. This hardware shares one ARM SoC between the engine, the renderer, and the
panel; pushing the loop past the ~50 ms floor **starves the SoC**, which paradoxically *drops*
the served fps and wrecks cross-frame uniformity. The ~40 ms wait-budget floor supplies most
of this pacing for bands that must wait; the fast free-run bands must be paced explicitly to
the same floor.

---

## 6. Halt, drain, and channel layout

**Halt.** Write `0x21 = 0xC8`. This latches the read pointer of the already-complete frame
and stops the fill. Immediately read `0x46` twice; equal reads confirm the fill froze
(halt-confirm telemetry). The frozen record is coherent to `20480` samples — the same depth as
the reference instrument's record — so draining the full record after the halt reads real
captured samples, not aliased memory re-reads.

**Drain.** Read `drainCols` words round-robin from ports `0x30`–`0x34` into the producer's
private buffer:

- word bit layout: **hi byte = C1, lo byte = C2**.
- port for sample `i` is `0x30 + (i mod 5)`.
- `drainCols` = `20480` for native-fast bands, configured `Cols` (clamped ≤ `20480`) for
  decimated bands.
- Use the **mmap** read path (syscall-free, ~50× faster than ioctl). It is safe here only
  because the ports are frozen after `0xC8`; the drain must be fast so the halt window stays
  ~1 ms. Fall back to ioctl reads if mmap is unavailable.

**Re-arm** immediately (`armEngine`) so the engine is filling again before the frame is
published.

**Native-fast content discrimination.** At native-fast bands the short record rarely contains
the edge, so most captures are a flat rail at whichever square level the free-running engine
froze. The status gates do not discriminate (they assert on the untriggered fill too), and on
a flat rail a crossing search finds a spurious noise crossing. Therefore discriminate on the
**content** of the real captured samples:

- an **edge frame** = `ptp ≥ nativeEdgeMinPtp (40)` **and** a valid slope-correct crossing (§7).
- publish an edge frame; **hold** the last edge frame otherwise (do not flash the flat
  capture).
- after `nativeFlatFallback (60)` consecutive held no-edge frames, publish one honest flat
  capture (set `EdgeX = -1`) so a genuinely flat band (100–200 ns) still shows a live, real
  frame. A band whose edge arrives often never reaches this fallback.

Content discrimination is not synthesis: every displayed native-fast frame is a real captured
edge; a flat capture is dropped, never fabricated into an edge.

---

## 7. Software centring and the publish decision

The HW trigger position (`0x3a/0x3b`) is jittery and does not index the drained edge; **centre
in software** off the drained samples. The three centring primitives are specified below; the
whole fast-band uniformity floor rests on them.

1. Select the discrimination channel: C1 by default, C2 if the trigger source is set to
   channel 2 and two channels are captured.
2. `lvl = midLevel(disc)`.
3. For an EDGE trigger: `xc = centerCross(disc, lvl, slope)`. `slopeOK = xc ≥ 0 &&
   windowSlopeMatches(disc, xc, lvl, winCols, slope)`. For PULSE/SLOPE/VIDEO types, run the
   type's software qualifier instead; a qualifying event sets `xc`, none holds the display.
4. `EdgeX = xc`, or `-1` for a flat rail (no edge).

### 7.1 `midLevel(sig) → int`

`(min + max)/2` over the drained samples (`128` for an empty slice). This is the crossing
threshold; it floats with the captured amplitude so it works at any V/div.

### 7.2 `centerCross(sig, lvl, edge) → float64`

Slope is judged in **code space** (honouring the configured slope, since ADC code is
polarity-inverted). A crossing at index `c` (1 ≤ c < n) qualifies when:

- rising (`edge ≥ 0`): `sig[c-1] < lvl && sig[c] ≥ lvl`
- falling (`edge < 0`): `sig[c-1] > lvl && sig[c] ≤ lvl`

Among all qualifying crossings pick the one with minimum `|c − n/2|` (nearest the frame
centre — for a periodic wave every same-slope crossing shows identical downstream content, so
nearest-to-centre is phase-stable frame to frame). Return the sub-sample position
`(c−1) + frac`, where `frac = (lvl − sig[c-1]) / (sig[c] − sig[c-1])` clamped to `[0,1)` (0 if
the denominator is 0 or out of range). Return `-1` if no crossing qualifies.

### 7.3 `windowSlopeMatches(sig, xc, lvl, winCols, edge) → bool`

Validates that the anchored crossing really is the requested slope, by comparing the plateaus
**immediately adjacent** to the crossing — never the far outer window edges. Let `c = int(xc)`,
`skip = max(winCols/16, 1)` (the transition band to exclude), `span = winCols/4`:

- `leftMean` = mean of `sig[c-span : c-skip]`
- `rightMean` = mean of `sig[c+skip : c+span]`
- `margin = (max − min of sig) / 8`
- rising: return `rightMean − leftMean ≥ −margin`
- falling: return `leftMean − rightMean ≥ −margin`

Return **true** (do not veto) if `n < 8`, `winCols < 8`, or either adjacent range is empty —
insufficient context must never reject a frame.

This deliberately **passes** a dense multi-period window: when the window spans ~2 signal
periods (e.g. 200 µs/div = 2 cycles) both adjacent plateaus average toward mid, so `sep → 0`
and the frame passes. It **rejects** only a genuine wrong-side pair — a spurious opposite-slope
micro-crossing buried in the other edge's junk, whose adjacent plateaus sit clearly on the
wrong sides (`sep < −margin`). Judging from the outer eighths instead would put the two window
ends on opposite plateaus of a 2-cycle window and false-reject every correctly-centred edge —
silently freezing the display. Do not do that.

### 7.4 Publish policy

| mode | publish when |
|---|---|
| **AUTO** (`0x35=0x0001`) | every coherent frame (edge frames at native-fast; every drained frame at decimated) — free-run, random-phase |
| **NORM** (`0x35=0x0003`) | only a comparator-anchored, slope-valid frame (`Coherent && slopeOK`); otherwise **HOLD** (publish nothing → the display re-presents the last good frame) |
| **native-fast** (any mode) | only an edge frame, else hold (with the 60-frame flat fallback, §6) |
| **non-EDGE type** (any mode) | only a frame with a qualifying event, else hold |

In NORM a quiet screen (no trigger) is a legitimate held display, never a random untriggered
frame. Software-centring a comparator-locked NORM frame floors the cross-frame uniformity.

Optional post-processing on the drained record, before publish: **ERES** boxcar low-pass
applied to the whole record before edge detection; **AVERAGE** replaces a published,
edge-aligned frame with the mean of the last N edge-aligned frames (only published coherent
captures enter the average ring).

---

## 8. Frame metadata — set and clear on the reused buffer

The arena's three frame buffers are reused round-robin. A real-time producer **must** set the
frame's metadata and **must clear** any state a previously-visited band left in the buffer, or
the renderer takes the wrong draw path.

Set on every real-time frame:

- `Seq` — monotonic capture sequence (advance only on a real drain).
- `Valid` = `drainCols` — consumers slice `C1[:Valid]`/`C2[:Valid]`; the tail beyond `Valid`
  is stale.
- `WinCols` — the display window (samples spanning the 10-division screen at this band's
  sample interval).
- `EdgeX` — the software crossing, or `-1` for a flat rail.
- `Interp` = `nativeFast(...)` — request linear interpolation of the windowed real samples
  (native-fast windows fewer real samples than the panel is wide).
- `Triggered`, `TrigPos`, `Coherent`, `HaltOK`, `Post46`, `Ptp` — coherence telemetry.

**Mandatory clears (real-time producer):**

- `IsEnv = false`
- `EnvCols = 0`

A slow/roll band leaves `IsEnv = true` plus envelope min/max pairs in the buffer. If a
real-time producer does not clear these, the renderer takes its envelope-fill branch first and
fills the screen with the stale min/max band after any slow→fast timebase change. Clearing
them forces the trace-draw path. Every real-time frame path (the main FSM, the depth probe,
etc.) must clear these.

---

## 9. Command boundary — single-owner bus discipline

The panel, control plane, and front-end are **command producers**, not bus writers. Every GPMC
access other than the frame FSM is serviced by the owner in `serviceCommands()`, called at the
**top of each loop iteration** — the frame boundary, where the engine is armed and filling,
never inside a `0xC8` halt window. A stray CS3 or matrix access during the halt window is
exactly what wedges the engine.

`serviceCommands()` drains, in order:

1. **Key-matrix reads** (CS1) — for each pending request, read selectors
   `0x64,0x65,0x66,0x67,0x69` once and reply. These config-plane reads do not pop the sample
   FIFO.
2. **LED latch** (CS3, if available): `0x0b=0 → 0x0a=word>>8 → 0x09=word&0xff → 0x0b=1`.
3. **Offset DAC** per channel (CS3): low byte to `0x10`/`0x11`, high byte to `0x30`/`0x31`
   (high latches); then re-assert the run word `0x35` (CS1) so the front-end change leaves the
   engine coherent.
4. **Trigger level DAC** (CS3) — the safe recommit:
   1. write the level quad lo→hi per lane, both lanes equal (the high byte self-latches):
      `CS3 0x14=lo, CS3 0x34=hi, CS3 0x15=lo, CS3 0x35=hi`.
   2. full re-arm: `CS1 0x00 = 0x80` (twice, the comparator re-anchor preamble), then
      `armEngine()` (`0xC0/0x57/0xC3`).

The level-DAC recommit is why the write is safe: serialization on the single owner + the
inherited fd + the bus-idle Arm-boundary timing + the `0x00=0x80` re-anchor + the following
full re-arm. A bare level write off this boundary wedges the display. Analog V/div (spidev) is
off the GPMC bus and is driven directly by the front-end, not queued here.

Commands are staged into a coalescing desired-state shadow (last-wins) with dirty flags, so a
burst of knob deltas applies as one write per frame rather than one-per-frame stale replays.

**Band / mode changes** are staged (`pendBand`) and applied at the frame boundary: adopt the
new `(class, lo, hi, TdivS)`, recompute drain depth and window, clear the uniformity rings and
reused envelope state, and re-run bring-up (§4). When leaving an envelope/roll band for a
real-time band, first issue `0x21=0xC0` twice to drop the latched free-run state before
bring-up.

**STOP** keeps the FSM armed and alive on the bus (so it never wedges) but publishes nothing;
the display holds. Commands, including RUN to resume and band changes, are still serviced every
~50 ms.

---

## 10. Frame hand-off — the triple-buffer arena

The arena is a lock-based triple buffer of three preallocated frames with **drop-newest**
backpressure:

- `write` — the producer's private fill slot (the drain writes straight into it).
- `ready` — the most-recent completed frame.
- `read` — the consumer's private slot.

**Sizing.** All three frames are preallocated to the **maximum drain depth any band can
reach — the `20480`-sample native-fast record — not to the initial band's `Cols`.** A runtime
band change can switch to a native-fast band that drains the full `20480`; if the buffers were
sized to a smaller decimated `Cols` (e.g. 2048), that drain would overrun them. Size to
`max(cfg.Cols, 20480)` (the probe modes size to their own larger probe depth).

**Publish** swaps `write ⇄ ready` under a microsecond critical section and marks `dirty`.
**Consume** returns `(ready→read, true)` if a new frame arrived, else `(read, false)` so the
renderer re-presents the held frame (a legitimately quiet NORM display, not a wedge). **Peek**
copies `ready` into a caller buffer without consuming (diagnostic tap).

The mutex guards only the RAM pointer swap; it is **not** on the GPMC bus, so single-bus
ownership is preserved. The producer never blocks on the ~50 ms render, the consumer always
sees a whole frame (no tearing), and there is zero steady-state allocation — the in-place drain
is the copy. `copyInto` deep-copies samples plus all metadata so a consumer never aliases a
producer buffer; each frame carries its own config snapshot so a mid-flight band change cannot
tear the display. A `gen` counter advances only on a real publish (a liveness token).

---

## 11. Telemetry and health

The owner exposes an un-fakeable live snapshot: `Frames`, `Published`, `Held`, `Coherent`
(bit2 asserted + `0x46` full), `HaltConfirm` (`0x46` froze after `0xC8`), `Wedged` (fill never
advanced / flat frame), `FPS` (published frames/sec), `LastPtp`, `LastTrigPos`, `ArmToLatchMs`,
`DrainMs`, and the cross-frame uniformity metrics over the software-centred display window.

**Health / recovery requirements:**

- The fd is anchored in the supervising agent; each app launch inherits it. A fresh `open()`
  in the app is a hard fault — refuse to drive and report unhealthy, never re-open the driver.
- A liveness token must be re-written **only** while frames genuinely advance (capture seq /
  `0x46` progressing), not merely "process alive"; report healthy only after several genuinely
  coherent frames, so a wedged boot is not rubber-stamped.
- A top-level `recover()` in the owner goroutine turns a panic into a logged wedge event, not
  fd loss or a fast-exit crash-loop.
- Distinguish NORM-quiet (`0x46` advancing = alive) from wedged (`0x46` frozen + flat drain +
  CONF_DONE `CS3 0x07 & 0x80` clear) so the watchdog does not false-positive a quiet NORM
  display.

---

## 12. Load-bearing constraints (why the design is shaped this way)

1. **Single bus owner.** Exactly one goroutine touches the GPMC bus. The `0xC8` halt latches a
   coherent record only if no other access overlaps the ~1 ms halt window. All panel/LED/offset/
   level access is queued and serviced at the Arm boundary (§9).
2. **Drain and re-arm before hand-off.** The engine is halted for the drain only, never across
   the render — this is what makes the per-frame halt safe.
3. **Inherit the boot comparator.** Write no CS3 comparator registers in the frame loop, or the
   `0x39` bit2 done-gate stops firing.
4. **Never write the config port** (`CS3 0x07` / `0x2010000e`) at runtime — it reconfigures the
   fabric.
5. **Clear reused frame metadata**, above all `IsEnv`/`EnvCols`, on every real-time frame.
6. **Pace the status poll** (150 µs); the status bits are levels, not transients.
7. **Fill (0x46) is a FULL check after bit2, never a trigger-ready gate alone** — it latches
   before the post-trigger record fills.
8. **Native-fast gates on content, not status.** `0x39` bit2 does not reliably fire for a fast
   edge capture, and `0x38` is not polled by the FSM; discriminate on captured-sample ptp+slope.
9. **The HW trigger position does not index the edge** — centre in software (§7). Judge the
   slope from the plateaus *adjacent* to the crossing, not the outer window edges, or a
   multi-period window silently freezes the display.
10. **Fast drain (mmap) is safe only after `0xC8`** freezes the ports.
11. **Cap the published rate at ~20 fps (~50 ms floor).** Exceeding it starves the shared ARM
    SoC and *reduces* both served fps and cross-frame uniformity (§5.3).
12. **Route by plane.** CS1 `WriteReg` vs CS3 `WriteRegCS(3,…)` share selector numbers
    (`0x30`–`0x35`) that mean different things; crossing planes corrupts the divisor/run state or
    the front-end DACs.
13. **Size the arena to the deepest drain (`20480`)**, not the initial band's `Cols`, so a
    runtime switch to a native-fast band has somewhere to land.

**Open:** the tightest cross-frame uniformity at the sub-cycle native-fast bands (~1–2 codes)
is a genuine ceiling of software centring on a short record; it does not affect correctness or
liveness. Two-channel capture at native-fast timescales is not established with this register
set (the fast path resolves a single muxed source).

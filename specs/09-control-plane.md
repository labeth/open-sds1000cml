# Control Plane

This spec defines how commands — set timebase, V/div, vertical offset, trigger level/slope/source/type,
run/stop/single, acquisition mode — reach the acquisition hardware. The governing rule (spec 01) is that
exactly one worker, the **bus owner**, may touch the GPMC bus, and only at a **frame boundary** (never
during a capture-halt window). Every command that needs a GPMC write is therefore *staged* by its
producer and *applied* by the owner in its per-frame command-service step. This spec gives the staging
model, the register-access API, the coalescing rules, the abort-long-frame requirement, the exact
register write sequences the owner performs, and the line-protocol network interface that maps onto the
same staged commands.

Read spec 01 (single-owner discipline, fd inheritance, CS planes), spec 03 (the acquisition FSM and
frame boundary), spec 04 (timebase divisor table + per-band drain/window formulas), and spec 05 (trigger
level DAC + type qualifiers) first — this spec assumes them.

**Inherited-fd requirement (whole plane).** Every GPMC access in this plane — the CS1 acquisition
registers, the CS1 key-matrix read, the CS3 config-plane writes (LED / offset / trigger level), and the
`/dev/fpga_key` interrupt source — runs on the **inherited boot file descriptors**, never a freshly
opened one. A fresh `open()` of `/dev/Gpmc` or `/dev/fpga_key` after inheritance EFAULTs (the driver's
mapping is opener-context dependent; spec 01). The owner therefore holds and reuses the inherited CS1/CS3
descriptors for all of §5/§6, and the panel producer reuses the inherited `/dev/fpga_key` fd (spec 08).

---

## 1. Model: stage, coalesce, apply at the boundary

There are three classes of control, distinguished by which hardware surface they touch:

| Class | Surface | How applied |
|---|---|---|
| **GPMC-bus commands** | CS1 acquisition plane, CS3 config plane, key-matrix read | **Staged** by the producer; **applied by the bus owner** at the frame boundary. |
| **Software-refinement commands** | none (CPU-only discrimination/post-processing) | Stored in an atomic/lock-guarded field; **read as a value snapshot** by the owner each frame. No bus access. |
| **Analog front-end commands** | `/dev/spidev1.0`, `/dev/spidev1.1` | **Driven directly** by the producer. SPI is OFF the GPMC bus, so it needs no staging. |

A producer (physical panel controller, network handler, host command) **never** issues a GPMC access
itself. It calls a setter on the bus owner. The setter records the desired state (staging) and returns
immediately. The owner drains and applies the staged state once per FSM iteration, at the top of the
loop, in a step called `serviceCommands`, which runs while the engine is armed and filling — i.e. NOT
inside a `0x21=0xC8` capture-halt window. This is the primary place a GPMC command write, an LED-latch
write, an offset-DAC write, a trigger-level write, or a key-matrix read occurs. **The long roll/envelope
frame loops additionally re-invoke `serviceCommands` periodically (§3.2)** — safe because those loops are
not halt windows — so control stays responsive during multi-hundred-millisecond frames.

**Why staging is mandatory (load-bearing):** the per-frame capture-halt is only safe because no second
bus access ever overlaps the halt window. A producer poking a CS3 register (LED, offset, trigger level)
or reading the key matrix off the frame boundary re-introduces exactly the concurrent-bus condition that
black-screens the display. Staging + single-owner apply is not an optimization; it is the correctness
invariant.

### 1.1 The register-access API

The owner reaches the bus through two narrow interfaces (spec 01/02 give the concrete `/dev/Gpmc`
driver behind them):

```
// CS1 acquisition plane. WriteReg/ReadReg operate on the CS1 window implicitly (no plane arg).
type Reg interface {
    ReadReg(sel uint16) (uint16, error)
    WriteReg(sel, val uint16) error
}

// Optional CS3 config plane. A CS3 write is plane-qualified: plane == 3 selects the config plane.
type regCS interface {
    WriteRegCS(plane uint8, sel, val uint16) error
}
```

- `sel` is the register **selector**, not the byte address (the driver shifts `<<1`).
- The concrete driver implements both interfaces on the same inherited fd. A unit-test fake may
  implement only `Reg`. The owner detects the CS3 capability **once at construction** via a type
  assertion (`Reg` → `regCS`); if it fails, the CS3 config-plane writes (LED, offset, trigger level) are
  silently skipped and only CS1 acquisition + matrix reads run. This is the interface the §6 step-2 guard
  tests.
- **The CS1-vs-CS3 plane distinction is load-bearing.** Selector `0x35` names *two different registers*:
  on CS1 it is the run word; on CS3 it is the trigger-level lane-B high byte. Writing a CS3 value via the
  CS1 `WriteReg` (or vice-versa) corrupts the wrong register. Every acquisition write in §6 uses
  `WriteReg`/`ReadReg` (CS1); every LED/offset/trigger-level byte uses `WriteRegCS(3, …)` (CS3).

### 1.2 Coalescing — latest value wins per control

Staging shadows are **level-triggered, not queued**. Each control has one shadow slot plus a dirty flag.
Setting a control overwrites the slot and sets the flag; the owner clears the flag when it applies the
value. Consequences the implementation must honor:

- Multiple sets between two frame boundaries collapse to the **last** value. A knob spun quickly issues
  many `SetOffsetDAC` calls; only the final code is written to the DAC. This bounds bus work to at most
  one write per control per frame regardless of input rate.
- **LED and trigger-level shadows compare-on-change:** a set whose value equals the current shadow does
  **not** mark the control dirty (no redundant bus write). Each carries an `init` flag (`ledInit`,
  `trigInit`) so the very first set applies even when it equals the default. **The offset-DAC shadow does
  NOT compare-on-change and has no init flag** — every `SetOffsetDAC(ch,code)` sets `offCode[ch]` and
  `offDirty[ch]=true` unconditionally, so a repeated identical offset re-writes the DAC and re-asserts
  the run word. Redundant offset traffic is instead suppressed at the *producer* (the offset knob handler
  only calls `SetOffsetDAC` when its own accumulated code changes). An implementer may add an equality
  compare + `offInit` flag to the offset shadow to match LED/trigger, but the plane's correctness does
  not depend on it — the run-word re-assert after an offset write is idempotent.
- The dirty read + clear happens under the command mutex; the actual bus write happens **after**
  releasing the mutex (the write may sleep, e.g. the arm settle, and must not hold off producers).

### 1.3 The key-matrix read is a request/reply, not a shadow

The panel key-matrix lives on the CS1 plane, so reading it is a GPMC access and must run on the owner.
It is modeled as a bounded request channel: the producer posts a reply channel, the owner reads the five
matrix selectors in `serviceCommands` and sends the snapshot back. If the request queue is full, or the
owner does not answer within a timeout (it may be blocked in a long wait), the read returns `ok=false`
and the producer simply retries on the next interrupt / re-sync tick. See §5.

---

## 2. The staged controls

### 2.1 GPMC-bus controls (applied at the boundary)

| Control | Setter | Shadow | Applied by | Registers |
|---|---|---|---|---|
| Run / Stop | `SetRunning(bool)` | `running` (atomic bool) | loop top (gates the whole frame) | none directly — gates arm/publish |
| Trigger mode AUTO/NORM | `SetNorm(bool)` | `norm` (mutex) | loop (re-runs bring-up on change) | run word CS1 `0x35` = `0x0001`/`0x0003` |
| Timebase / band | `SetTdiv` / `SetBand` | `pend*` + `pendBand` (mutex) | loop (re-runs bring-up) | divisor `0x19/0x1a/0x1b` (§3) |
| Panel LEDs | `SetLEDs(word)` | `ledWord`+`ledDirty`+`ledInit` | `serviceCommands` | CS3 `0x0b/0x0a/0x09` latch |
| Vertical offset DAC | `SetOffsetDAC(ch,code)` | `offCode[ch]`+`offDirty[ch]` (no init, no compare) | `serviceCommands` | CS3 `0x10/0x30` (C1), `0x11/0x31` (C2) |
| HW trigger level | `SetTrigLevel(code)` | `trigCode`+`trigDirty`+`trigInit` | `serviceCommands` | CS3 `0x14/0x34`+`0x15/0x35` + re-arm |
| ETS on/off | `SetETS(bool)` | `pendETS`+`pendBand` | loop (re-runs band bring-up) | divisor/mode re-plan |

**Run/Stop and Single are producer-composed, not dedicated engine controls.** RUN/STOP toggles
`SetRunning`. SINGLE and AUTO are composed from the existing setters (§4). There is no `SetSingle`
control and no network `set run`/`set single` verb — the physical panel and UI drive them through
`SetRunning`/`SetNorm`.

### 2.2 Software-refinement controls (no bus access)

These change how the owner *interprets* the captured samples; they take effect on the next frame with no
register write. Store them in atomics or under the command mutex and snapshot them once per frame.

| Control | Setter | Field | Effect |
|---|---|---|---|
| Trigger slope | `SetTrigSlope(rising bool)` | `trigRising` (atomic) | direction (+1/−1) the software edge-centering discriminates. |
| Trigger source | `SetTrigSource(ch int)` | `trigSrc` (atomic) | which captured channel (0=C1, 1=C2) the discrimination/centering locks onto. |
| Trigger type | `SetTrigType(t int)` | `tp.typ` (mutex) | EDGE / PULSE / SLOPE / VIDEO software qualifier that gates publish. |
| Slope params | `SetSlopeParams(loFrac, hiFrac, tMinNs, tMaxNs float64, cond int)` | `tp.slope*` (mutex) | two threshold fractions of the frame span, traversal-time window (ns), condition. |
| Pulse params | `SetPulseParams(lvlFrac, wMinNs, wMaxNs float64, cond int)` | `tp.pulse*` (mutex) | level fraction of the frame span, width window (ns), condition. |
| Video params | `SetVideoParams(std, line int, neg bool)` | `tp.video*` (mutex) | standard (0=PAL/1=NTSC), line (0=any), sync polarity (true=negative). |
| Acq mode | `SetAcqMode(mode int)` | `acqMode` (atomic) | NORMAL / AVERAGE / ERES post-processing selection. |
| Average depth N | `SetAvgCount(n int)` | `avgCount` (atomic) | mean over N edge-aligned frames; clamp [1,256]. |
| ERES boxcar length | `SetEresLen(l int)` | `eresLen` (atomic) | intra-frame low-pass length; clamp [1,64], forced odd. |

The `cond` argument is `0`=any, `1`=less-than-min, `2`=greater-than-max, `3`=inside-[min,max]. Levels are
fractions of each frame's own `[min,max]` span (band/gain-independent); times are nanoseconds, converted
to samples via the band sample interval. The per-type discrimination algorithms (edge/slope/pulse/video
qualifiers) that gate publish live in spec 05; this plane only stages their parameters and snapshots them
per frame.

Changing the acq mode, average depth, or band **clears the average ring** so a new setting does not
average across incompatible frames.

### 2.3 Analog front-end controls (direct, off-bus)

V/div (coarse relay word on `/dev/spidev1.0`, fine gain DAC on `/dev/spidev1.1`) is driven directly by
the panel controller through the front-end object — no engine staging. See spec 06. The producer must
still serialize its own SPI writes, but they never contend with the GPMC bus.

---

## 3. Timebase / band changes

A timebase change is staged as a *pending band* and applied by the loop (not `serviceCommands`), because
it re-runs the engine bring-up (divisor program + arm) and resets per-band state.

**Band resolution (spec 04).** The seconds/division → band plan resolver lives in spec 04 and is used
verbatim here. Its contract:

```
PlanTdiv(tdivS float64) (class, lo, hi uint16, envelope, ets, ok bool)
```

returns the divisor triple plus the frame-path flags. `ok=false` means the timebase is not in the divisor
table. The loop's frame-path switch (§4 / spec 03) consumes two predicates from the same source:
`isEnvelopeTdiv(tdivS)` (≥5 ms/div → MIN/MAX envelope) and `isRollTdiv(tdivS)` (≥100 ms/div → free-run
roll). ETS routing is opt-in only (`isETSTdiv` returns false); the ETS flag is set explicitly via
`SetETS`. The exact divisor table and these predicates are owned by spec 04 — this plane treats
`PlanTdiv`/`isEnvelopeTdiv`/`isRollTdiv` as its resolver boundary.

**Staging.** `SetTdiv(tdivS)` calls `PlanTdiv`, stores the plan into the `pend*` fields under the band
mutex, and sets `pendBand`; it returns the plan and `ok`. `SetBand(class, lo, hi, tdivS)` stages a raw
divisor directly (used for measurement sweeps). `SetETS(on)` re-stages the current band with the ETS flag
toggled. All of these only set fields; nothing touches the bus.

**Apply (loop top, under the mutex):**
1. Copy `pend*` into the live config; clear `pendBand`.
2. `recomputeBand()` — recompute the band's **drain depth** (`drainCols`) and **display window**
   (`winCols`). The formulas (native-fast full-deep drain vs decimated `cfg.Cols`; 10-division window at
   the band sample interval; envelope sample window) are owned by the acquisition-engine spec (spec 04);
   this plane owns only its call ordering. Native-fast bands drain the full deep record; decimated bands
   drain `cfg.Cols`; envelope bands set `drainCols = winCols` from the envelope plan.
3. `resetUniformityLocked()` — clear all cross-frame rings (uniformity, envelope, roll, ETS phase,
   average) so the new band's metrics/rendering are not polluted by the previous band's windows. **Reused
   frame buffers must have their envelope metadata cleared on the real-time path** (a buffer previously
   used for a slow/roll envelope band still carries `IsEnv=true` + stale min/max; if not cleared the
   renderer draws the stale envelope — see spec 07).
4. If leaving a slow/roll (free-run, latch-no-halt) band for a real-time band, issue a full head reset
   (`0x21=0xC0` twice) before re-programming, to drop the latched free-run state cleanly.
5. `enableAndDivisor()` — program the run word + divisor:
   `0x44=1, 0x44=0, 0x35=runWord, 0x36=0, 0x1b=0, 0x19=class, 0x1a=lo, 0x1b=hi`.

The next frame arms into the new band.

### 3.1 A pending timebase change MUST abort an in-progress long real-time-ish frame

Slow/roll and envelope frames take a long time to build (a roll/envelope frame reads samples over
hundreds of milliseconds). If the owner blocked in such a read while a band change (or STOP, or
shutdown) was staged, a TIME/DIV knob turn out of the roll bands would not take effect until the whole
roll frame finished.

**Requirement:** the **roll and envelope** read loops must poll an abort predicate every iteration and
**bail early** when it is true. The predicate `bandChangePending()` is true when any of: `stopped` is
set, `running` is false (STOP), or `pendBand` is set. On early bail the frame produces nothing and
returns to the loop top, where the pending band change (or STOP hold) is serviced within one short read
interval instead of after the full frame. This keeps timebase changes responsive at the slow bands.

**ETS is exempt and bounded by budget, not by abort-poll.** The ETS build loop (≤50 ns/div, opt-in)
captures many `0xC8`-halt sub-acquisitions and does **not** poll the abort predicate. It is bounded
instead by a fixed per-frame budget — at most 40 sub-acquisitions or ~650 ms wall time — after which it
returns to the loop top where the band change is applied. Worst-case latency leaving an ETS band is
therefore one ETS frame budget (~650 ms). This is acceptable because ETS is an opt-in refinement on a
narrow band set; a matrix read (§5) posted during that window simply times out and the panel retries.

### 3.2 Long frames must pump `serviceCommands` mid-loop

The roll and envelope frames run for ~0.2–0.25 s. If `serviceCommands` only ran at the loop top, a
`ReadMatrix` request (§5, 200 ms timeout) posted during a roll frame would be starved for the whole
frame — the panel would drop the TIME/DIV knob step, so a band change would never even get staged and the
knob could not leave the roll band (§3.1's abort would then never fire, because `pendBand` is never set).

**Requirement:** inside the roll read loop and the envelope fill-wait loop, call `serviceCommands`
periodically (every ~16 reads / waits, ~6 ms at the roll interval). This is single-owner-safe: the roll
port free-runs and the envelope fill wait is pre-halt — neither is a `0x21=0xC8` halt window — so a
matrix read / LED flush / offset write there does not overlap a halt. The pump is near-free when idle
(empty request queue, no dirty shadow). This mid-frame pump plus §3.1's abort are complementary: the
pump lets the knob step reach the engine and stage `pendBand`; the abort then bails the frame so the loop
top applies it.

---

## 4. Run / Stop / Single and trigger mode

- **RUN** (`SetRunning(true)`): the FSM arms, captures, drains, re-arms, and **publishes** frames.
- **STOP** (`SetRunning(false)`): the FSM stays *armed and alive on the bus* but publishes nothing; the
  display holds the last frame. STOP must **not** stop issuing arms — an idle, unarmed engine can wedge.
  The loop, when `running` is false, sleeps ~50 ms and continues, but still calls `serviceCommands`
  every iteration so that RUN (to resume), band changes, LED/offset/trigger updates, and matrix reads
  are all serviced while stopped.
- **SINGLE**: realized by the producer as exactly `SetNorm(true)` then `SetRunning(true)` — there is no
  dedicated engine control. The engine enters NORM (comparator-gated hold) and runs: it holds the display
  until a real trigger edge completes a frame, then continues holding on subsequent quiet frames. This is
  NORM-arm; it re-publishes each fresh comparator-fired frame rather than latching after exactly one
  capture. A one-shot that disarms after a single capture would be a strict superset of this behavior.
- **AUTO** (`SetNorm(false)` + `SetRunning(true)`): free-run trigger mode.
- **AUTO** vs **NORM** (`SetNorm`): AUTO writes run word `0x35=0x0001` (free-run, publish every coherent
  frame, random phase); NORM writes `0x35=0x0003` (armed, publish only comparator-fired + slope-valid
  frames, HOLD otherwise). A change re-runs the bring-up (new arm bit) and clears the uniformity rings.

`SetRunning` and the run/stop gate use an atomic bool so RUN/STOP is observed at the loop top without a
mutex. `SetNorm` uses the command mutex because the loop reads `norm` together with the band fields.

---

## 5. Key-matrix read path

The physical panel is interrupt-driven (spec 08): an interrupt (or a periodic re-sync tick) asks for a
matrix snapshot, decodes it, and issues the resulting commands. Because the matrix lives on CS1, the read
must run on the owner, on the inherited fd (§preamble).

`ReadMatrix()` (called by the panel producer):
1. Allocate a buffered reply channel; post it to the owner's `matrixReq` channel. If that channel is
   full, return `ok=false` immediately (the producer retries next interrupt).
2. Wait for the reply with a timeout (200 ms). If the owner is blocked in a long wait and does not
   answer, return `ok=false`.

In `serviceCommands` (and in the §3.2 mid-frame pumps) the owner drains **every** pending request, and
for each reads the five selectors `0x64, 0x65, 0x66, 0x67, 0x69` into a `[5]uint16` snapshot via
`ReadReg` (CS1) and replies. `0x64–0x67` are the 8×8 active-low key matrix + quadrature phase bits;
`0x69` is the shared knob step-magnitude counter (spec 08). This is a CS1 read: it does not pop the
sample FIFO and never runs during a halt, so it is safe during fill.

---

## 6. The owner's `serviceCommands` step (exact sequence)

Called at the top of every FSM iteration (before the arm/capture) and periodically inside the long
roll/envelope loops (§3.2). All accesses use the inherited CS1/CS3 fds (§preamble). Order:

1. **Drain matrix requests.** For each queued reply channel: `ReadReg` `0x64,0x65,0x66,0x67,0x69`, send
   the snapshot. Loop until the request channel is empty.
2. **If no CS3 writer is available, return.** (On a unit-test fake, or a build where the `regCS`
   interface is not implemented, the config-plane writes are skipped. Detected once at construction by
   the `Reg`→`regCS` type assertion; the result is cached as a nil/non-nil CS3 handle.)
3. **Snapshot + clear the shadows under the command mutex:** read `ledDirty/ledWord`,
   `offDirty[0..1]/offCode[0..1]`, `trigDirty/trigCode`; clear each dirty flag. Release the mutex.
4. **Apply the dirty writes (mutex released):** LEDs, offset C1, offset C2, trigger level — each only if
   its snapshot was dirty.

**CS3 config-port trap (load-bearing).** Only the enumerated CS3 config-plane offsets below —
`0x09/0x0a/0x0b` (LED latch), `0x10/0x30/0x11/0x31` (offset DAC), `0x14/0x34/0x15/0x35` (trigger level
DAC) — are written at runtime. The CS3 **config/nCONFIG strobe port must NEVER be written by the control
plane.** Touching it re-initializes the FPGA and tears down the inherited bitstream, collapsing the
engine to a black screen. The bring-up (`enableAndDivisor`, §3 step 5) deliberately writes **no** CS3
level/mask/slope either, so the inherited boot comparator's done-gate keeps asserting. See the CS-plane
address map in spec 01/04 for the exact config-port address.

### 6.1 LED latch write

CS3 latch strobe (best-effort; a harmless no-op on a clone whose LED latch is MCU-owned):
```
WriteRegCS(3, 0x0b, 0)          ; strobe low
WriteRegCS(3, 0x0a, word>>8)    ; high byte
WriteRegCS(3, 0x09, word&0xff)  ; low byte
WriteRegCS(3, 0x0b, 1)          ; latch
```
LED bitmap for this clone (`word`): `0x2000` RUN green, `0x4000` STOP red, `0x8000` SINGLE/armed,
`0x0020` CH1, `0x0010` CH2, `0x0004` TRIG'd. The producer composes the word from UI state
and stages it via `SetLEDs`; the shadow only flushes on change (§1.2).

### 6.2 Vertical offset DAC write

Per channel, low byte then high byte (the high byte latches; no separate strobe):
```
C1: WriteRegCS(3, 0x10, code&0xff) ; WriteRegCS(3, 0x30, (code>>8)&0xff)
C2: WriteRegCS(3, 0x11, code&0xff) ; WriteRegCS(3, 0x31, (code>>8)&0xff)
```
Then re-assert the run word (`WriteReg(0x35, runWord)`, CS1) so the front-end change leaves the engine
coherent. The offset DAC moves the captured window's DC centre; the renderer reflects it with no
render-side change. Codes are clamped by the producer to the DAC's linear region (~9600–11600, ~10600
centres). The offset shadow does not compare-on-change (§1.2), so an unchanged code re-runs this
idempotent sequence — harmless.

### 6.3 HW trigger level write + safe recommit

The trigger comparator threshold is a **real HW register** — a 16-bit LEVEL DAC. A bare poke off the
frame boundary wedges the display. The safe recommit (issued only here, at the bus-idle boundary, on the
inherited fd, under single-owner discipline) is, with `lo = code&0xff`, `hi = (code>>8)&0xff`:

```
(1) LEVEL QUAD — write lo then hi per lane; the high byte self-latches:
    WriteRegCS(3, 0x14, lo)   ; lane A low
    WriteRegCS(3, 0x34, hi)   ; lane A high  (latches lane A)
    WriteRegCS(3, 0x15, lo)   ; lane B low
    WriteRegCS(3, 0x35, hi)   ; lane B high  (latches lane B)
(2) FULL RE-ARM — re-anchor the comparator to the new reference:
    WriteReg(0x00, 0x80)      ; CS1
    WriteReg(0x00, 0x80)      ; CS1
    armEngine()               ; 0x21=0xC0 x2, 0x57 pulse (0x57=1,0x57=0), arm settle, 0x21=0xC3
```

The **arm settle** in `armEngine` is a fixed sleep between the write-pointer pulse and `0x21=0xC3`, of
duration `Config.ArmSettle` (default **2 ms**). Too short a settle mis-anchors the comparator on the
level-write re-arm.

Both comparator lanes (A = `0x14/0x34`, B = `0x15/0x35`) get the same code. **CS3 `0x35` here is the
level lane-B high byte — it is NOT the CS1 run word `0x35`; different plane (§1.1), do not conflate.** The
re-arm is required: the free-running frame loop does not self-heal the comparator anchor after a level
change. `code == 0` means "clear/none" (keep the inherited boot comparator). The inherited boot
comparator is kept until the first `SetTrigLevel` call.

`TrigLevelCode(volts)` maps a level in volts to a code: `code = 31434 − 938·volts` (0 V = `0x7aca`,
−938 codes/V, higher level = lower code), clamped to 16 bits. This fit is exact at 1 V/2 V-div; the level
DAC rides the per-V/div cal ladder, so other V/div need the active cal record. For an unambiguous HW
sweep, set the raw code directly rather than via volts. Slope and source are **software** refinements
over this HW threshold (§2.2): the HW level sets *where* the edge is; slope/source select *which*
crossing of it is locked.

---

## 7. Network / status interface

The bus owner exposes a TCP line protocol (default `:5610`) for live stats + staged commands. It lets the
band/mode/trigger set be driven and the un-fakeable proof numbers read without the physical panel or a
reboot. **The handler never touches GPMC** — every `set` maps onto a staging setter, which the single
owner applies at the next frame boundary; every query maps onto a lock-guarded snapshot/peek. It runs in
its own goroutine, one request per connection.

### 7.1 Request handling

Accept a connection, set a ~200 ms read deadline, read one line, trim it. If it begins `set `, parse
three whitespace fields `set <control> <value>` and dispatch (§7.2), replying `ok ...` or `err ...`.
Otherwise dispatch a query keyword (§7.3). A bare connect (empty/other input) returns the stats JSON.

### 7.2 Set commands → staged setters

| Line | Setter | Notes |
|---|---|---|
| `set tdiv <seconds>` | `SetTdiv(v)` | e.g. `set tdiv 5e-4`. Replies with resolved `class/lo/hi/envelope/ets`; `err` if not in the divisor table. |
| `set norm <0\|1>` | `SetNorm(v=="1")` | AUTO / NORM. |
| `set triglevel <volts>` | `SetTrigLevel(TrigLevelCode(v))` | volts via the 1 V/2 V-div fit. |
| `set triglevelcode <n>` | `SetTrigLevel(uint16(n))` | raw 16-bit DAC code — the clean HW sweep. |
| `set trigslope <1\|rising\|pos\|up \| else>` | `SetTrigSlope(rising)` | anything else = falling. |
| `set trigsource <1\|2\|c1\|c2>` | `SetTrigSource(ch)` | `2`/`c2` → C2 (ch 1), else C1 (ch 0). |
| `set trigtype <edge\|pulse\|slope\|video>` | `SetTrigType(t)` | `glit`→pulse, `slew`→slope, `tv`→video, else edge. |
| `set acqmode <normal\|average\|avg\|eres\|peak>` | `SetAcqMode(m)` | `peak` maps to normal at real-time bands (realised by the slow/roll envelope path). |
| `set avgcount <n>` | `SetAvgCount(n)` | AVERAGE depth, clamped [1,256]. |
| `set eres <n>` | `SetEresLen(n)` | ERES boxcar length in samples (odd, ≤64). |
| `set eresbits <b>` | `SetEresLen(EresLenForBits(b))` | enhancement-bit form, `L=round(4^b)`. |
| `set ets <0\|1\|on\|off>` | `SetETS(on)` | equivalent-time on the current band. |

There is no network verb for RUN/STOP, SINGLE, or the per-type trigger params
(`SetSlopeParams`/`SetPulseParams`/`SetVideoParams`); those are driven by the panel/UI producer.
Unrecognized control, wrong field count, or an unparseable value → `err bad command "<line>"` (or a
control-specific `err`). Each successful set replies with a short `ok ...` echo of the applied value.

### 7.3 Query keywords

| Line | Returns |
|---|---|
| *(bare / anything else)* | One JSON line of the live `Stats` snapshot (§8) plus `halt_mode`. |
| `dump` | JSON of the most-recent published frame's `C1[:Valid]` samples + `win`/`edge`/`ptp`/`seq`. |
| `dumpenv` | JSON of the most-recent envelope band's per-column `(min,max)` pairs + `ptp`/`seq`. |
| `nfdump` | JSON of the latest native-fast probe's raw deep + roll arrays. |

Queries read a copy (`Snapshot`, `DumpFrame`, `DumpEnv`, probe result) — read-only taps that never
touch the bus and never fake data.

---

## 8. Status snapshot (`Stats`)

`Snapshot()` returns a mutex-guarded copy of the live stats. These are proof telemetry — real measured
numbers, never synthesized. Fields (partial; see spec 03/04 for the acquisition detail):

| Field | Meaning |
|---|---|
| `Frames` / `Published` / `Held` | FSM cycles / frames handed to the arena / cycles that held the display. |
| `Coherent` / `HaltConfirm` / `Wedged` | done-gate+filled frames / fill froze after halt / never-advanced frames. |
| `FPS` | measured PUBLISHED-frame rate over a 1 s window. |
| `Norm` | current trigger mode (true = NORM comparator-gated). |
| `WinColStd` / `WinColStdRaw` / `WinColMax` | cross-frame per-column uniformity (centred / fixed-position / worst-column). |
| `LastPtp` / `LastTrigPos` / `ArmToLatchMs` / `DrainMs` | last frame's ptp / HW trigger position / wait / drain (= halt-window) time. |
| ETS / probe fields | populated only in the corresponding band/probe mode. |

Health reporting (spec 01) reads this snapshot: report healthy only after genuinely advancing coherent
frames (`Coherent >= 3 && Frames >= 3`), so a wedged boot is never rubber-stamped healthy.

---

## 9. Load-bearing constraints (recap)

1. **Single owner, frame boundary only.** No producer issues a GPMC access. Every bus command (CS1, CS3,
   matrix read) is staged and applied by the owner in `serviceCommands`/loop, never during a halt window.
2. **Whole plane on the inherited fds.** All CS1/CS3/`fpga_key` accesses use the inherited boot
   descriptors; a fresh `open()` EFAULTs (spec 01).
3. **CS1 vs CS3 planes are distinct.** Selector `0x35` is the CS1 run word AND the CS3 level lane-B high
   byte; acquisition writes use `WriteReg`/`ReadReg` (CS1), config writes use `WriteRegCS(3,…)` (CS3).
4. **Never write the CS3 config/nCONFIG port.** Only the enumerated LED/offset/level offsets are written
   at runtime; the config strobe tears down the inherited bitstream and black-screens the display.
5. **Coalesce, latest-wins.** One shadow per control; no command queue backlog. LED and trigger-level
   shadows suppress redundant writes via compare-on-change + an init flag; the offset shadow does not (its
   redundancy is suppressed at the producer, and its write is idempotent).
6. **Apply writes with the mutex released.** Snapshot+clear dirty under the mutex; do the (possibly
   sleeping) bus write after unlocking.
7. **A pending timebase change / STOP / shutdown aborts an in-progress roll/envelope frame** (§3.1), and
   those long frames pump `serviceCommands` mid-loop (§3.2), so control stays responsive within one short
   read interval. ETS is exempt and bounded by a ~650 ms frame budget instead.
8. **Clear reused frame-buffer envelope metadata on the real-time path** after a band change, or the
   renderer draws a stale envelope.
9. **Trigger level = real HW register with a mandatory safe recommit** (level quad → CS1 `0x00=0x80` ×2 →
   full re-arm with a `Config.ArmSettle` sleep, default 2 ms), issued only at the bus-idle boundary. A
   bare poke wedges the display.
10. **STOP keeps arming.** STOP publishes nothing but must keep the engine armed/alive and keep servicing
    commands, or it wedges.
11. **The network handler never touches GPMC** — it only calls staging setters and read-only snapshots.

---

## 10. Scope and limitations

Trigger source is selected in software (§2.2). The coarse HW source mux at CS3 `0x22` is engine-safe, but
its C1/C2 code values are not pinned, so this plane does not use it for source select. External trigger
(EXT) has no readback lane and is unsupported. SINGLE is realized as NORM-arm (§4); the plane provides no
disarm-after-one-capture latch. The per-type trigger qualifier parameters have setters (§2.2) but no
network verbs; they are panel/UI-driven.

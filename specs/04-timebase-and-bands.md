# 04 — Timebase and Bands

The seconds/division setting (`tdiv`) selects one of a fixed ladder of detents. Each detent
resolves to a **sample-clock class + divisor** written to the FPGA, and to one of five
**acquisition band classes** that determine how a frame is captured, sized, and rendered. This
spec defines the step table, the divisor-register programming, the band classification and
routing, the per-band sample rates and record/display sizing, and the edge-catch behaviour of
each band.

The engine is a single goroutine that solely owns the GPMC bus and runs a per-frame
capture-halt FSM (see spec 03). All register sequences below run on that owner goroutine; no
other worker touches the bus.

---

## 1. Sample-clock classes

The sample clock is programmed by these registers, in this write order (a divisor change forces
a full re-enable + re-arm):

| step | register | sel | meaning |
|---|---|---|---|
| 1 | reset-head strobe | `0x44` | write `0x0001` then `0x0000` |
| 2 | run word | `0x35` | `0x0001` free-run (AUTO) / `0x0003` armed (NORM) |
| 3 | reset | `0x36` | `0x0000` |
| 4 | divisor-high (clear first) | `0x1b` | `0x0000` |
| 5 | class | `0x19` | class byte (below) |
| 6 | divisor-low | `0x1a` | low 16 bits of divisor |
| 7 | divisor-high | `0x1b` | high bits of divisor |

The divisor is a 32-bit value split `lo = divisor & 0xffff`, `hi = divisor >> 16`.

| class byte | sample rate | deep sample interval | used by |
|---|---|---|---|
| `0x20` | 500 MSa/s | **2 ns/sample** | fast bands ≤ 200 ns/div |
| `0x01` | 250 MSa/s | 4 ns/sample | 500 ns, 1 µs/div |
| `0x80` | 100 MSa/s base ÷ divisor | **divisor × 10 ns** | all decimated bands ≥ 2 µs/div |

Class `0x02` exists in the FPGA but is never used and must not be programmed.

**Fast-band rate is 500 MSa/s (2 ns/sample), not 1 GSa/s.** The nominal single-channel
1 GSa/s figure is a marketing max, not the per-band record density: the captured fast-band
record is 2 ns/sample. Do not attempt to double the fast-band capture rate; no reachable
register produces a denser record. (The display *window* is sized at a 1 ns/sample nominal —
see §6 — so 10 divisions occupy the full screen width at the labelled tdiv; the captured
samples remain 2 ns apart.)

**The class-`0x80` deep sample interval is `divisor × 10 ns` (100 MSa/s base).** This is the
per-sample spacing of the captured deep record and is the ONLY clock period that sizes the deep
capture bands and their display windows. Examples: 1 ms/div (divisor 200) → 2 µs/sample;
100 ms/div (divisor 20000) → 200 µs/sample.

> **Two distinct cadences — do not conflate.** The deep-record sample interval above
> (`divisor × 10 ns`) is the physical FPGA sample clock. The **roll-port read pacing** (§5.2)
> uses a *separate* empirical cadence, `divisor × 50 ns` (`rollClockNs = 50`). `rollClockNs` is
> a read-pace heuristic that produces good phase-scatter on the free-running roll FIFO; it is
> **not** the FPGA count period and must not be used to size deep records or display windows.
> The roll phase-scatter math in §5.2 lives entirely in the `× 50 ns` pacing domain; every
> other timing in this spec uses the `× 10 ns` deep interval.

---

## 2. Timebase step table

The complete detent ladder (33 steps, 1 ns/div … 50 s/div). The `1-2.5-5` fast decade uses a
**25 ns** step (not 20 ns) — the `… 10, 25, 50, 100 ns …` sequence is the real detent ladder.
Matching a requested `tdiv` to a row is done within a small relative tolerance (≈ 1e-6) so float
round-trips still hit the row.

| tdiv | class | lo | hi | divisor | deep interval |
|---|---|---|---|---|---|
| 1 ns | `0x20` | `0x0000` | 0 | 0 | 2 ns |
| 2 ns | `0x20` | `0x0000` | 0 | 0 | 2 ns |
| 5 ns | `0x20` | `0x0000` | 0 | 0 | 2 ns |
| 10 ns | `0x20` | `0x0000` | 0 | 0 | 2 ns |
| **25 ns** | `0x20` | `0x0000` | 0 | 0 | 2 ns |
| 50 ns | `0x20` | `0x0000` | 0 | 0 | 2 ns |
| 100 ns | `0x20` | `0x0000` | 0 | 0 | 2 ns |
| 200 ns | `0x20` | `0x0000` | 0 | 0 | 2 ns |
| 500 ns | `0x01` | `0x0000` | 0 | 0 | 4 ns |
| 1 µs | `0x01` | `0x0000` | 0 | 0 | 4 ns |
| 2 µs | `0x80` | `0x0001` | 0 | 1 | 10 ns |
| 5 µs | `0x80` | `0x0001` | 0 | 1 | 10 ns |
| 10 µs | `0x80` | `0x0001` | 0 | 1 | 10 ns |
| 20 µs | `0x80` | `0x0004` | 0 | 4 | 40 ns |
| 50 µs | `0x80` | `0x0008` | 0 | 8 | 80 ns |
| 100 µs | `0x80` | `0x0014` | 0 | 20 | 200 ns |
| 200 µs | `0x80` | `0x0028` | 0 | 40 | 400 ns |
| 500 µs | `0x80` | `0x0050` | 0 | 80 | 800 ns |
| 1 ms | `0x80` | `0x00c8` | 0 | 200 | 2 µs |
| 2 ms | `0x80` | `0x0190` | 0 | 400 | 4 µs |
| 5 ms | `0x80` | `0x0320` | 0 | 800 | 8 µs |
| 10 ms | `0x80` | `0x07d0` | 0 | 2000 | 20 µs |
| 20 ms | `0x80` | `0x0fa0` | 0 | 4000 | 40 µs |
| 50 ms | `0x80` | `0x1f40` | 0 | 8000 | 80 µs |
| 100 ms | `0x80` | `0x4e20` | 0 | 20000 | 200 µs |
| 200 ms | `0x80` | `0x9c40` | 0 | 40000 | 400 µs |
| 500 ms | `0x80` | `0x3880` | `0x001` | 80000 | 800 µs |
| 1 s | `0x80` | `0x0640` | `0x003` | 198208 | ~1.98 ms |
| 2 s | `0x80` | `0x1a80` | `0x006` | 400000 | 4 ms |
| 5 s | `0x80` | `0x3500` | `0x00c` | 800000 | 8 ms |
| 10 s | `0x80` | `0x8480` | `0x01e` | 2000000 | 20 ms |
| 20 s | `0x80` | `0x0900` | `0x03d` | 4000000 | 40 ms |
| 50 s | `0x80` | `0x1200` | `0x07a` | 8000000 | 80 ms |

Each `divisor` column equals `lo | (hi << 16)`; the `deep interval` column equals
`divisor × 10 ns`. All three columns are self-consistent per row — program from any one and you
get the same clock.

**Note:** the divisor written to the engine is the faithful sample-clock divisor. Because the
deep record's per-sample interval (`divisor × 10 ns`) is finer than a nominal time/div at
several bands, the *displayed* seconds/division of a `cols`-sample record is
`cols × interval / 10` and can differ from the nominal `tdiv` label. Report the timebase to the
UI as the displayed per-div so "set tdiv" equals the on-screen time/div while the HW divisor
stays the faithful one.

**Note (envelope/roll bands, tdiv ≥ 5 ms):** the divisor columns in the rows for
tdiv ≥ 5 ms are the *faithful nominal* divisor only — they are **not** the divisor actually
programmed. The slow-envelope band (5–50 ms) recomputes a phase-scatter divisor targeting
`envIntervalS` (§5.1; e.g. ~0.23 ms/sample, not the table's `divisor × 10 ns`), and the roll
band (≥ 100 ms) programs the fixed `rollDivisor = 7400` (§5.2), not the table divisor.
Programming the table divisor for these rows yields a thin/wandering band (the failure §5.1
warns against). Use §5.1/§5.2 for what is written at tdiv ≥ 5 ms.

---

## 3. Band classes and routing

`tdiv` routes to exactly one band class. Routing is resolved once per band change and drives
the drain depth, the display window, and which frame builder runs.

| band class | tdiv range | clock | frame builder | display |
|---|---|---|---|---|
| **ETS** (opt-in only) | 1–50 ns | class `0x20` | phase-bin interleave of many triggered captures | dense equivalent-time record |
| **Native-fast real-time** | 100 ns – 20 µs | class `0x20`/`0x01`, or `0x80` divisor ≤ 4 | deep capture-halt, catch + discriminate + HOLD + zoom | single-valued windowed trace |
| **Decimated deep** | 50 µs – 2 ms | class `0x80` divisor ≥ 8 | deep capture-halt, software-centre | single-valued windowed trace |
| **Slow envelope** | 5 ms – 50 ms | class `0x80` moderate divisor | deep capture-halt, MIN/MAX peak-detect | rail-to-rail min/max band |
| **Roll** | ≥ 100 ms | class `0x80` scatter divisor | free-running roll port, MIN/MAX | rail-to-rail min/max band |

### 3.1 Routing predicates

Resolve in this order:

1. **Envelope band** iff `tdiv ≥ 5 ms`. Of these:
   - **Roll band** iff `tdiv ≥ 100 ms`.
   - otherwise **slow envelope** (5–50 ms).
2. **ETS band** iff `tdiv ≤ 50 ns` **and** ETS is explicitly enabled in config. ETS is an
   opt-in density refinement for a genuinely fast repetitive source; it is **not** auto-routed.
   With ETS disabled (the default), the 1–50 ns bands fall through to native-fast.
3. **Native-fast** iff the resolved class is `0x20` or `0x01`, **or** class `0x80` with
   divisor ≤ 4. This covers 100 ns through 20 µs (20 µs = divisor 4).
4. **Decimated deep** otherwise (class `0x80` divisor ≥ 8): 50 µs through 2 ms.

Because ETS is off by default, the 1–50 ns detents take the native-fast catch+hold+zoom path
(§8.1), which windows the same deep record they would otherwise interleave.

---

## 4. The deep record (capture record vs. display record)

The **capture record** is the physical FPGA sample buffer drained after a `0x21=0xC8`
capture-halt. Its physical depth is **≥ 20480 samples**; there is no 2048 ceiling. It drains
coherently (a real captured waveform) to ~20480 samples, then reads a flat dead tail. Drain
from ports `0x30–0x34` (mmap, see §10.1). Each drained 16-bit word is **hi byte = C1, lo byte =
C2**; samples are **8-bit UNSIGNED codes** (`COMM_TYPE = 0`; the remote `C1:WF? DAT2` payload is
raw 8-bit codes, 20480/frame). Code orientation: **higher code = more positive = higher on
screen** — code 255 = graticule top, code 0 = bottom, the 256-code span across the graticule
height. The drain applies **no** polarity inversion (`C1[i] = byte(word>>8)`,
`C2[i] = byte(word&0xff)`); the same orientation governs the ptp/level and centring math.

The **display record** (`winCols`) is the windowed slice actually rendered — 10 divisions wide
at the band's sample interval (§6), centred on the software-detected edge (§8.5).

**Drain depth per band class:**

| band class | drain depth (`drainCols`) | rationale |
|---|---|---|
| native-fast | **20480** (full physical record) | the trigger edge lands near record centre (~sample 10190); a shallow drain reads only the pre-trigger rail |
| decimated deep | configured record length (default **2048**) | the edge and useful record fit within the shallow drain |
| ETS (per sub-acquisition) | 2048 (`etsDrainCols`) | enough to hold the trigger region near record/2 plus the interleave reach; drains in ~1 ms |
| slow envelope | the per-frame sample window (§5.1) | drained then min/max-reduced |
| roll | n/a (roll port, not a halt drain) | see §5.2 |

Drain depth is always clamped to the arena buffer capacity.

**Native-fast edge position:** the edge is HW-phase-stable near record/2 (cross-frame position
std 1–2), far tighter than the jittery HW trigger-position register (`0x3a/0x3b`, std ≈ 89).
Software-centring only refines an already-centred edge. The HW trigger-position register is
**not** a usable real-time anchor at these bands; software-centre (§8.5) on the captured content.

---

## 5. Envelope and roll bands (MIN/MAX peak-detect)

A slow/roll trace of the 1 kHz cal is a solid rail-to-rail band, because the signal is far
faster than the timebase. Reproduce it as a software MIN/MAX reduction over **real captured
samples** — every displayed pixel is a real ADC min or max; nothing is synthesized. A min/max
band is solid **iff each display column's samples scatter across a full signal period**, which
requires a per-sample interval that is a good *fraction* of the period, **not** dense sampling.

Constants:

| constant | value | meaning |
|---|---|---|
| `envIntervalS` | 2.3e-4 s | target per-sample interval for 5–50 ms (≈ 0.23 period of the kHz cal — the phase-scatter point) |
| `envDisplayCols` | 800 | display columns; each carries one (min,max) pair |
| `envMinWin` / `envMaxWin` | 200 / 2048 | bounds on the per-frame sample window |
| `envRingN` | 24 | recent frames the min/max accumulates over (cross-frame scatter) |

### 5.1 Slow envelope (5–50 ms)

Use a **moderate divisor** targeting `envIntervalS` (≈ 0.23 ms/sample), **not** the standard
tdiv divisor (which samples at ≪ 1 period → a thin, wandering band).

Per-frame sample window and divisor:
```
span    = 10 * tdiv
winCols = round(span / envIntervalS)     clamped to [envMinWin, envMaxWin]
divisor = round(span / winCols / 10ns)   (>= 1)      # deep interval domain (× 10 ns)
class   = 0x80
```

Per frame:
1. `armEngine` (§10).
2. wait a **modest** deep fill: poll `0x46 & fillMask ≥ fillTarget`, where
   `fillTarget = min(winCols, 0x0600)` (the 11-bit fill counter is capped), budget 250 ms,
   poll every 200 µs.
3. `0x21=0xC8` halt.
4. drain `winCols` samples from `0x30–0x34`.
5. re-arm (`armEngine`).
6. accumulate the drained samples into the min/max ring; reduce the ring to the 800-column
   (min,max) band (§5.3).

A partial fill is sufficient; the ring accumulates the scatter across frames.
Phase-independent (no trigger discrimination); publish every frame. Poll a pending band
change / STOP each wait iteration and bail without publishing if set (§9).

### 5.2 Roll (≥ 100 ms)

At these timebases a per-frame deep capture cannot fill at a usable rate (≥ 20 ms/sample), so
capture from the **free-running roll port** instead:

| constant | value | meaning |
|---|---|---|
| roll port C1 / C2 | `0x41` / `0x59` | free-running sample FIFO; an IOCTL read pops one live sample (its **high byte** is C1, §10.1) |
| `rollDivisor` | 7400 | scatter divisor (paces at ≈ 0.37 ms — a non-period fraction) |
| `rollClockNs` | 50 | roll read-pace constant (empirical); **not** the FPGA count period |
| `rollWin` | 4096 | length of the scrolling raw-sample ring; one **scroll snapshot** = a full 4096-sample copy of this ring taken at the end of each roll update |
| `envRingN` | 24 | roll min/max reduces over the last `envRingN` scroll snapshots (each `rollWin`=4096 samples), same constant as the envelope band |
| `rollBatch` / `rollPace` | 1600 / 64 | batch size / pace interval |

The read pacing **must phase-scatter, not track the timebase**. In the read-pace domain
(`divisor × rollClockNs = divisor × 50 ns`), the standard 100 ms tdiv divisor (20000) paces at
exactly one 1 kHz period (20000 × 50 ns = 1 ms) → every read lands on the same phase → a thin
band. Use `rollDivisor = 7400` instead (7400 × 50 ns ≈ 0.37 ms ≈ 0.37 period), so each read
lands on a fresh phase.

Roll bring-up (once, on entering the band):
1. `enableAndDivisor` (§1) with `rollDivisor`.
2. `0x21=0xC0` (reset head).
3. write-pointer pulse: `0x57=0x0001` then `0x57=0x0000`.
4. `0x21=0xC3` (arm/go — **once**).
5. sleep ~3 ms, then `0x21=0xCB` (latch read pointer **without** halting), read `0x41` once and
   pre-fill the ring with that real sample (so unpopulated columns don't draw a false 0-rail
   bar).

Per update (budget ~220 ms):
1. `0x21=0xCB` (re-snapshot so the FIFO advances) then read `0x41` (and `0x59` if 2-channel).
2. scroll the sample into the raw ring; advance `rollPos`.
3. **sleep the paced interval** = `divisor × rollClockNs` = `divisor × 50 ns`, clamped to
   `[50 µs, 40 ms]`.
4. at the end of the update, push a full 4096-sample copy of the scrolling ring as one **scroll
   snapshot** and reduce over the last `envRingN` = 24 snapshots: every one of the 4096 ring
   samples in each snapshot bins into its 800-column display slot (`col = i × 800 / rollWin`),
   recording per-column (min,max). The reduction therefore spans `envRingN × rollWin` real
   samples (24 × 4096), each snapshot captured at a different scroll phase → a solid, stable
   rail-to-rail band (§5.3).

**Roll traps (load-bearing):**
- **Never arm with `0x21=0xC8`** on a roll band — the halt freezes the free-run. Roll uses
  arm-once `0xC3` + per-update `0xCB` only.
- **Never read `0x41`/`0x59` un-armed.** A roll read requires the port to have been armed
  (`0xC3` at bring-up); reading it un-armed holds the GPMC WAIT line and hard-wedges the bus
  (uninterruptible D-state, power-cycle only — see §10.1).
- **Reads must be paced.** Rapid un-paced `0x41` reads re-read a dwell value (a thin band) and
  can wedge the port. Pace to `divisor × 50 ns` so each read pops a fresh sample.
- **Harden against a wedging port:** bail the read loop after ~8 consecutive read errors.
- Poll a pending band change / STOP each read iteration and bail without publishing (§9).

### 5.3 Building the band

Reduce the ring of captured windows to per-display-column (min,max): every column bins all
samples (across all ring frames) whose window index maps to it and records the min and max.
Cross-frame + within-frame phase scatter fills the rails → a gapless solid band. Fill any
never-seen column from its nearest seen neighbour (copies a real neighbouring min/max — never
invents amplitude). Render fills each display column from min → max.

---

## 6. Display window sizing (`winCols`)

The display window spans **10 divisions** at the band's sample interval:
```
winCols = round(10 * tdiv / sampleInterval)
```
clamped to `[1, drainCols]`.

`sampleInterval` per class:
- class `0x20`: **use 1 ns** (nominal display rate) — not the real 2 ns capture rate. The
  fast-band record is captured at 2 ns/sample but *displayed* at the 1 ns nominal so 10
  divisions occupy the full screen width at the labelled tdiv (a ~250-sample nominal window; a
  caught ~122-sample cal edge fills ~47 % of the 800-px display). Sizing with the real 2 ns rate
  would zoom in 2× and fill the whole screen with the edge.
- class `0x01`: 4 ns.
- class `0x80`: `divisor × 10 ns`.

Resulting fast-band windows (class `0x20`, 1 ns nominal): 25 ns → 250, 50 ns → 500,
100 ns → 1000, 200 ns → 2000. Native-fast records fewer real samples than the panel is wide
(e.g. 125 real samples at 25 ns over 800 px), so the renderer **linear-interpolates** the real
samples into a smooth vector — connect adjacent real samples with straight segments — never
sample-hold stair-steps and never a fabricated square.

For envelope/roll bands, `winCols` is the *sample window* (not a centred display window); the
display is the 800-column min/max band. For ETS, `winCols` is the equivalent-time column count
(§7).

---

## 7. Equivalent-time sampling (ETS, ≤ 50 ns, opt-in)

At ≤ 50 ns/div the 10-division screen spans 20–500 ns — far finer than the 2 ns real interval,
so a single capture has only ~10–250 window samples. ETS reconstructs a dense record by
capturing many triggered sub-acquisitions, measuring each one's sub-sample trigger phase, and
interleaving its real samples onto a fine equivalent-time grid shifted by that phase.

Phase-bin table (nearest row to `tdiv`; defaults to the 2 ns row):

| tdiv | factor (phase bins) | nCols (= 0xA000/factor + 10) |
|---|---|---|
| 1 ns | 500 | 91 |
| 2 ns | 500 | 91 |
| 5 ns | 500 | 91 |
| 10 ns | 250 | 173 |
| 20 ns | 100 | 419 |
| 50 ns | 50 | 829 |

**Placement symbols** (all times in ns):
- `Ts` = the deep sample interval of the sub-acquisition (§1): 2 ns at class `0x20`.
- `W` = the equivalent-time window = `10 * tdiv` (ns). Fall back to `nCols * Ts` if `tdiv ≤ 0`.
- `etColTime` = ns per equivalent-time column = `W / nCols`.
- `xref` = the source channel's sub-sample-interpolated rising crossing (§8.5) nearest record
  centre in the drained sub-acquisition; its fractional part is that capture's sub-sample phase.

Per equivalent-time frame (bounded by `etsMaxAcqPerFrame = 40` sub-acquisitions or
`etsFrameBudget = 650 ms`, giving ~1.5 fps):
1. capture one real sub-acquisition on the `0xC8`-halt engine (drain `etsDrainCols = 2048`).
2. take `xref` as the sub-sample phase reference. (The FPGA comparator cannot HW-lock a fast
   repetitive source from the register plane, so do not use the `0x3a/0x3b` register as the phase
   source here.)
3. skip the capture if there is no resolvable crossing or `ptp < etsEdgeMinPtp` (= 40; a
   flat/slow source).
4. interleave both channels' real samples relative to `xref`: for each real sample `k` in
   `[⌊xref⌋ − nCols, ⌊xref⌋ + nCols]` that lies within the drained record, place it at
   equivalent-time column
   `round(((k − xref)·Ts + W/2) / etColTime)`, accumulating a running average per column.
5. stop early once every phase bin is covered.

The accumulator persists across frames and rebuilds once `≥ 9/10` of the bins are covered or
after `etsMaxAccFrames = 8` frames (liveness), so the reconstruction densifies over a few
frames and re-tracks a changing source.

Reduce: each filled column = the mean of its real samples; interior gaps between filled columns
are **linear-interpolated**; the ends extend the nearest filled column. Every value is a real
average or an interpolation between two real averages — **no fabricated samples**.

**Faithful flat fallback:** a slow source (e.g. the 1 kHz cal, whose 500 µs-period edge almost
never lands in the ≤ 41 µs record) yields too few anchors and the reconstruction stays a flat
rail. When phase-bin coverage `< factor/4` or the reconstructed `ptp < etsEdgeMinPtp`, display a
**real single-capture window** (the drained record's centre `nCols` samples, real ADC noise),
not a patchwork. When the captured record carries no resolvable edge, display that flat rail —
do not invent an edge. A genuinely fast repetitive edge (≥ ~1 MHz) reconstructs a dense waveform
through this same interleave.

---

## 8. Edge-catch behaviour per band

| band | shape of the cal (1 kHz) | catch behaviour |
|---|---|---|
| ETS ≤ 50 ns (opt-in) | flat rail | edge too rare in ≤ 41 µs record → faithful flat rail; a fast repetitive source densifies |
| 100–200 ns (native-fast) | flat rail | record spans ≪ the period → mostly a real flat rail (per-frame ptp ~5) |
| 500 ns – 20 µs (native-fast) | real square edge | edge lands at record/2; caught, discriminated, centred, zoomed |
| 50 µs – 2 ms (decimated) | real square edge | edge within the shallow record; software-centred |
| 5 – 50 ms (slow envelope) | solid rail-to-rail band | MIN/MAX over phase-scattered deep captures |
| ≥ 100 ms (roll) | solid rail-to-rail band | MIN/MAX over the free-running roll port |

### 8.1 Native-fast per-frame sequence

Native-fast (100 ns – 20 µs) runs the same ARM → WAIT → HALT → DRAIN → RE-ARM cycle as the
decimated band, but the halt is issued **unconditionally** when the wait returns (the done bit
does not reliably assert on a real edge here, §8.2), and the frame is then gated on **content**,
not on the status bits. Per frame:

1. `armEngine` (§10).
2. **Wait** on the real-time budget (§8.4): poll `0x39` and `0x46` every ~150 µs until
   `bit2(done)` is set **and** `0x46 & fillMask ≥ LatchAt`, or the budget expires. Native-fast
   does **not** require this condition — it always proceeds to halt when the wait returns.
3. `0x21=0xC8` halt (unconditional). Read `0x46` twice to confirm the fill froze (halt OK).
4. **Drain 20480** samples from `0x30–0x34` (mmap, hi=C1, lo=C2).
5. `armEngine` (re-arm immediately — the engine refills during the render).
6. **Content-discriminate** (§8.2 + §8.5): compute `ptp`, `midLevel`, the nearest-centre
   `centerCross`, and `windowSlopeMatches`. An `edgeFrame` requires `ptp ≥ nativeEdgeMinPtp`
   (= 40) **and** a valid slope crossing.
7. **Software-centre** on the crossing (`EdgeX`); publish an edge frame, or **HOLD** the last
   edge frame (publish nothing) if this frame carries no edge. After `nativeFlatFallback = 60`
   consecutive held frames, publish one honest flat capture (`EdgeX = -1`) so a genuinely-flat
   band stays live.

### 8.2 Content discrimination (native-fast)

The `0x39` bit2 done / `0x38` bit5 status gate does **not** discriminate an edge at native-fast
— it asserts on the untriggered free-run fill too. Discriminate on **content**: publish a
native-fast frame only if it carries a real edge (`ptp ≥ nativeEdgeMinPtp = 40` AND a valid
slope crossing, §8.5); otherwise HOLD the last edge frame. Publish only frames carrying a real
edge; otherwise hold the last edge frame — every displayed frame is a real captured edge; a flat
capture is dropped, never fabricated into an edge.

In NORM, the early "no comparator done → hold" gate is **skipped for native-fast** (the done bit
does not reliably assert for a real edge at these bands and would starve NORM to ~2 fps);
native-fast always drains and content-discriminates, identical to AUTO. In NORM at decimated
bands, a frame is published only if it is coherent (§8.3) **and** slope-valid.

### 8.3 Decimated-deep wait gate

Decimated deep bands wait on `0x39` **bit2 (done)** AND `0x46` fill `≥ LatchAt` before the
halt. `bit1 (trig)` is recorded for telemetry (`sawTrig`/`trigPos`) but is **not** a gating
condition — do not add a bit1 precondition (it can lag or never assert and would over-hold or
hang NORM). Once `bit2 && fill≥LatchAt` (or the budget expires), issue `0x21=0xC8`, drain
`drainCols`, re-arm, then software-centre and publish.

`LatchAt` = the `0x46` fill-counter threshold that must be reached before the halt. Reference
default **`0x200` = 512**, clamped to `≤ fillMask (0x7ff)`. (This is distinct from the decimated
**record length** default of 2048, §4.)

### 8.4 Real-time wait budget and cadence

The real-time (native-fast + decimated) ARM → WAIT loop is bounded — at native-fast the done
bit is unreliable, so the loop must never spin forever:

- **Budget** = `clamp(3 × interval × LatchAt, 40 ms, 80 ms)`, where
  `interval = divisor × 10 ns` (the deep sample interval; `interval = 0` at class `0x20`/`0x01`
  fast bands, so their budget floors to 40 ms).
- **Poll** `0x39` (status) and `0x46` (fill) every **150 µs**.
- On **native-fast**: when the budget expires (or the gate condition is met), halt + drain
  **unconditionally**, then content-discriminate (§8.2).
- On **decimated**: require `bit2 (done)` + `fill ≥ LatchAt` within the budget; otherwise (in
  NORM) HOLD.

### 8.5 Anchor and slope primitives

The content-discrimination and software-centring above use three primitives over a captured
channel `sig` (the selected trigger source, default C1):

- **mid-level** `lvl = (min(sig) + max(sig)) / 2` — the crossing threshold, derived from the
  frame's own extremes (not a fixed level, not the mean).
- **centre crossing** `centerCross(sig, lvl, edge)` — the `edge`-slope crossing of `lvl`
  **nearest the record centre** (`len(sig)/2`), returned as a **sub-sample** position by linear
  interpolation between the two straddling samples: `pos = (c-1) + (lvl - sig[c-1]) /
  (sig[c] - sig[c-1])`. Returns `-1` if there is no such crossing (a flat rail → no fabricated
  edge). `edge = +1` rising / `-1` falling, per the selected trigger slope. For a periodic wave,
  the nearest-to-centre crossing shows identical downstream content frame-to-frame → a still
  trace.
- **slope-valid** `windowSlopeMatches(sig, xc, lvl, winCols, edge)` — validates that the
  crossing at `xc` is a true `edge`-slope transition by comparing the plateaus **immediately
  adjacent** to the crossing, not the outer eighths (a window spanning ~2 periods has its ends
  on opposite plateaus). Read the mean of `[xc − winCols/4, xc − winCols/16)` (left) and
  `[xc + winCols/16, xc + winCols/4)` (right), skipping the `±winCols/16` transition band. For a
  rising edge, accept iff `rightMean − leftMean ≥ −margin`; for falling, iff
  `leftMean − rightMean ≥ −margin`, where `margin = (max−min)/8` (amplitude-scaled so ADC noise
  never flips the verdict). A dense multi-period window averages both sides to mid (separation
  → 0, PASSES — no slow-band regression); a spurious opposite-slope micro-crossing shows
  wrong-side plateaus (REJECTED — polarity-flip protection).

---

## 9. Band-change handling and load-bearing constraints

- **Single-owner GPMC bus.** Only the engine goroutine issues GPMC accesses. The `0xC8` halt
  window (drain, ~1–10.5 ms) must never overlap another bus operation — this is the condition
  that keeps the per-frame halt from wedging. The full 20480 deep drain is ~10.5 ms; the engine
  re-arms and refills during the ~50 ms render, so it is halted only for the drain.

- **Clear reused frame metadata on real-time frames.** The arena's frame buffers are reused
  round-robin. A buffer may still carry a stale envelope flag + min/max band from a previously
  visited slow/roll band. On every real-time frame, clear the envelope flag (`IsEnv`) and
  envelope column count (`EnvCols`) before publishing, or the renderer takes its envelope branch
  and fills the screen with the stale MIN/MAX band after a slow→fast transition.

- **Service commands mid-frame in slow/roll loops.** The slow-fill wait (~250 ms) and the roll
  read batch (~220 ms) block the owner loop for the whole frame. Service panel/control commands
  every ~16 iterations (~6 ms — well under the 200 ms matrix-read timeout) so a TIME/DIV knob
  step is not starved and dropped. This is single-owner-safe: the roll port is free-running (no
  halt window) and the env fill wait is *before* the halt. Poll for a pending band change /
  STOP every iteration and **bail immediately** (return without publishing) so the change
  applies within one read interval instead of after the whole frame.

- **Clean free-run → armed transition.** When leaving an envelope/roll band for a real-time
  band, issue an explicit head reset (`0x21=0xC0` ×2) before `enableAndDivisor`, dropping the
  latched roll free-run state so the next `armEngine` re-inits the armed capture path cleanly.

- **Never write the config port at runtime.** The trigger-source mux / nCONFIG config port must
  not be written during acquisition; only band divisor + arm/halt registers change.

- **Apply band changes at the frame boundary.** A band or mode change is staged (`pendBand`) and
  applied by the run loop at the next frame boundary: re-run `enableAndDivisor` and clear the
  cross-frame uniformity rings so the new band's metric is not polluted by the old band's
  windows.

---

## 10. Register reference (this domain)

| sel | name | role |
|---|---|---|
| `0x19` | divisor class | `0x20`/`0x01`/`0x80` |
| `0x1a` | divisor low | low 16 bits |
| `0x1b` | divisor high | high bits (clear first) |
| `0x21` | arm/halt opcode | `0xC0` reset-head, `0xC3` go, `0xC8` capture-halt, `0xCB` latch-no-halt |
| `0x35` | run word | `0x0001` free-run (AUTO), `0x0003` armed (NORM) |
| `0x36` | reset | held `0x0000` |
| `0x39` | status | bit0 (`0x01`) valid — AUTO untriggered-timeout (the boot firmware takes an untriggered refresh frame on this bit when bit1 has not fired); bit1 (`0x02`) trig (HW comparator fired, `0x3a/0x3b` valid); bit2 (`0x04`) frame filled/capture done |
| `0x3a`/`0x3b` | HW trigger position | lo/hi byte; jittery (std ≈ 89) — not a real-time anchor |
| `0x44` | reset-head strobe | `0x0001` then `0x0000` |
| `0x46` | fill counter | 11-bit (`fillMask = 0x7ff`) sample-write count; halt when `≥ LatchAt` |
| `0x57` | write-pointer pulse | `0x0001` then `0x0000` |
| `0x30`–`0x34` | deep drain ports | mmap, round-robin, hi byte = C1, lo byte = C2 |
| `0x41`/`0x59` | roll ports | free-running FIFO, IOCTL pop, C1/C2 |

Constants: `LatchAt = 0x200` (512, ≤ `fillMask`), decimated record length default `2048`,
`nativeDeepCols = 20480`, `nativeEdgeMinPtp = 40`, `nativeFlatFallback = 60`, real-time poll
`150 µs`, real-time budget `clamp(3 × divisor × 10 ns × LatchAt, 40 ms, 80 ms)`.

Arm sequence (`armEngine`): `0x21=0xC0` ×2 → `0x57=0x0001` → `0x57=0x0000` → arm-settle sleep
(default 2 ms) → `0x21=0xC3`.

### 10.1 Physical register access

**Device node and ioctl path (status/arm/latch/roll/fill — everything except the deep drain).**
Register access is a `/dev/Gpmc` ioctl. A selector `sel` addresses FPGA word `sel<<1`; the
kernel performs the `<<1` shift, so pass the **raw** selector (do **not** pre-shift). Request
codes: `fpgaRead = 0x80026700`, `fpgaWrite = 0x40026701` (read vs write is the request code, not
a struct field). The argument is a **6-byte little-endian struct**:

| byte | meaning |
|---|---|
| `b0` | chip-select plane: **1 = CS1** (the acquisition window — all registers in this spec). The kernel computes `index = b0 − 1` to pick the ioremap base; **`b0 = 0` selects a garbage base and stalls the access for seconds** — always set `b0 = 1`. |
| `b1` | `0` |
| `b2` | `sel & 0xff` |
| `b3` | `sel >> 8` |
| `b4` | `val & 0xff` (write value low) |
| `b5` | `val >> 8` (write value high) |

On a read the returned struct carries the value LE at `b4/b5`.

**Obtaining the handle — reuse the inherited fd, never fresh-open.** `/dev/Gpmc` has a
single-open guard and a boot-time chip-select init: a fresh `open()` hits `EPERM` while another
holder is open, and even when it succeeds it lacks the init so reads can wedge; **closing** the
handle kills the shared fd for the whole process tree. Instead **reuse the inherited fd**: scan
`/proc/self/fd` for the descriptor whose `readlink` == `"/dev/Gpmc"` (opened by the boot tree,
already chip-select-initialised), wrap it **without dup**, clear its GC finalizer, and **never
`Close()` it**. A fresh open is the sim/fallback path only.

**Deep drain — mmap (`0x30–0x34`).** The deep drain reads through a direct `/dev/mem` mapping
for speed (one CPU load per sample, no syscall). Map `/dev/mem` (`O_RDWR | O_SYNC`) at the CS1
physical base **`CS1Phys = 0x01000000`**, length **4096** bytes. A register read is **one
aligned 16-bit load at byte offset `sel<<1`** — a single bus transaction, so a sample port's
read-pointer auto-increment fires **exactly once** (two byte loads would double-advance it).
Validate the window by `Read(0x12) == 0x0052` (FPGA version). **Fatal hazard:** an mmap load of a
**not-yet-complete** sample port hangs the CPU **uninterruptibly** (no goroutine or timeout can
abandon a raw load, unlike an ioctl) — therefore drain `0x30–0x34` **only AFTER** the
`0x21=0xC8` capture-halt, and keep status / arm / latch / fill / roll on the ioctl path (which
is timeout-guardable).

**Roll ports (`0x41`/`0x59`) — arm-first, or the bus wedges.** A roll read is an ioctl
`ReadReg(0x41)` whose **high byte is the C1 sample** (`byte(word>>8)`); `0x59` is C2. The port
must be **ARMED first** — roll uses arm-once `0x21=0xC3` at bring-up + per-update `0x21=0xCB`
(latch-no-halt), never `0xC8`. **Reading `0x41`/`0x59` while UN-ARMED holds the GPMC WAIT line
and hard-wedges the bus** (uninterruptible D-state, power-cycle only) — stronger than the
un-paced-read hazard in §5.2. Pace the reads to `divisor × rollClockNs` (= `divisor × 50 ns`)
and **bail the read loop after 8 consecutive read errors** so a wedging port cannot hang the
owner loop.

### 10.2 Frame structure and publish contract

The engine hands frozen frame COPIES to consumers through a **lock-based triple buffer** (three
preallocated `Frame`s: `write` / `ready` / `read`) with drop-newest backpressure. The producer
(engine owner) fills its private `write` slot with **no lock** during the ~1 ms drain, then
`Publish()` swaps `write ↔ ready` under a microsecond critical section; a consumer `Consume()`
swaps `ready ↔ read`. Producer and consumer never touch each other's private slot (no tearing,
producer never blocks on the ~50 ms render); the mutex guards only the RAM pointer swap, never
the GPMC bus. If the consumer has not taken the previous `ready`, it is simply overwritten
(drop-newest).

`Frame` fields:

| field | meaning |
|---|---|
| `C1`, `C2` `[]byte` | 8-bit sample columns (capacity = arena cols); only `[:Valid]` is this frame's data — the tail is stale |
| `Valid int` | number of samples actually drained this frame (band-dependent: native-fast drains the deep record, decimated the shorter configured record) |
| `Seq uint64` | monotonic capture sequence (advances only on a real drain) |
| `Triggered bool` | `0x39` bit1/bit2 asserted this frame |
| `TrigPos uint16` | `0x3a/0x3b` HW trigger index latched with the frame |
| `Coherent bool` | done gate `0x39` bit2 asserted AND `0x46` reached `LatchAt` |
| `HaltOK bool` | `0x46` froze low after the `0x21=0xC8` halt (engine really stopped filling) |
| `Post46 uint16` | `0x46` sampled right after the halt (should be small/frozen) |
| `Ptp int` | `C1[:Valid]` peak-to-peak (flat rail ~2–5; real edge ~150) |
| `WinCols int` | display window width (samples spanning the 10-division screen, §6) |
| `EdgeX float64` | software-centred slope crossing over `C1[:Valid]`, or **-1** = flat rail (no edge) |
| `Interp bool` | request the renderer linear-interpolate the windowed real samples across the panel columns (set for class-`0x20` native-fast, §6) |
| `IsEnv bool` | select the MIN/MAX envelope renderer (slow/roll bands) |
| `EnvCols int` | number of (min,max) display columns when `IsEnv` |
| `EnvMin`, `EnvMax` `[]byte` | the (min,max) pairs (len ≥ `EnvCols` when `IsEnv`) |

The §9 "clear stale envelope metadata" rule = on every real-time (non-envelope) frame set
`IsEnv = false` and `EnvCols = 0` **before** `Publish()`, or the renderer takes its envelope
branch and fills the screen with the previous band's stale MIN/MAX band after a slow→fast
transition.

---

## 11. Open items

- **Fast-band AUTO edge/flat ratio (500 ns – 2 µs).** The content-discrimination gate (§8.2)
  publishes a frame whenever the capture carries a real edge, so at these bands it shows the edge
  on most AUTO frames. The boot firmware's AUTO instead publishes a **mostly-flat** stream: its
  real-time FSM waits for `0x39` bit1 (trigger) and, when bit1 has not fired, takes an
  *untriggered* refresh frame (state 4) gated on `0x39` **bit0** (valid). The concrete fix is to
  implement that AUTO untriggered-timeout path: in AUTO, if bit1 does not assert within the wait
  budget but bit0 (valid) is set, publish an untriggered (flat) frame instead of holding, so the
  displayed AUTO edge/flat mix matches the boot firmware's timeout-refresh cadence rather than
  showing the edge every frame. NORM waits bit1 indefinitely (FSM watchdog only); SINGLE
  single-shots. (Both `centerCross`/content discrimination and the bit0 path stay as specified;
  this only adds the AUTO bit0 timeout branch.)
- **Roll solidity ≥ 1 s.** The divisor-decimated roll port leaves a small fraction (~3 %) of
  display columns thin because its phase coverage is imperfect; target is a fully solid
  rail-to-rail band (no thin columns) at every roll timebase. Closing it would need reading the
  full-rate ADC min/max rather than the divisor-decimated roll port.
- **Higher-band 2→2.5 relabelling.** The 25 ns fast step is anchored; the analogous `2→2.5`
  relabelling at higher decades (250 ns, 2.5 µs, …) is **not** applied — no evidence beyond the
  fast bands.

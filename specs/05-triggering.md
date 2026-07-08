# 05 — Triggering

The trigger is a **hybrid**: a real hardware comparator sets the threshold, but all
discrimination, slope, type qualification and display-hold are performed in **software** on the
drained sample record. This split is mandatory — the app owns only the inherited `/dev/Gpmc`
register plane, not the full bitstream/DDR pipeline, so the comparator cannot be driven into a
signal-locked capture-and-halt. The achievable design is: program the HW level DAC to place the
comparator threshold, let the armed engine complete frames, then software-select which channel,
which crossing and which qualifying event becomes the trigger, and **hold the last good frame**
whenever the current frame carries no qualifying event.

Everything here runs inside the single GPMC-bus owner (the acquisition-engine goroutine). No other
worker touches the bus. See §9 for why this is load-bearing.

---

## 0. Register access — GPMC wire encoding and the inherited fd

Every trigger/acquisition register access goes through the `/dev/Gpmc` driver. The primitives named
throughout this spec are:

- `WriteReg(sel, val)` — write `val` to selector `sel` on **CS1** (the acquisition window). Equal to
  `WriteRegCS(1, sel, val)`.
- `WriteRegCS(plane, sel, val)` — write on an explicit chip-select plane: `plane = 1` = CS1
  (acquisition window), `plane = 3` = CS3 (config selectors: trigger level DAC, offset DAC, LED
  latch). The selector means different things per plane (e.g. `0x09` is a reject byte on CS1 but the
  LED-latch low byte on CS3), so config actuators MUST name the plane.
- `ReadReg(sel)` == `ReadRegCS(1, sel)` — the read counterpart.

**Wire encoding (6-byte ioctl struct):**

```
b = [6]byte{ plane, 0, byte(sel), byte(sel>>8), byte(val), byte(val>>8) }
```

- `b[0]` = the chip-select plane (**1** = CS1, **3** = CS3). `b[0] = 0` is fatal: the kernel
  computes `index = b[0] − 1 = 0xFF`, selects a garbage ioremap base, and the access STALLS the bus
  for seconds. Always pass 1 or 3.
- `b[1] = 0`.
- Bytes 2..3 = the selector `sel`, **raw**, little-endian. The kernel shifts it `<<1` to form the
  FPGA word address — do **NOT** pre-shift the selector.
- Bytes 4..5 = the value, little-endian. A read zeroes bytes 4..5, issues the read ioctl, then
  decodes the result LE from bytes 4..5.

**ioctl request codes:** read = `0x80026700`, write = `0x40026701`. Read vs write is chosen by the
request code, not by `b[0]`.

**The inherited fd (mandatory).** All GPMC access uses the **boot-inherited** `/dev/Gpmc` fd —
freshly opening the device faults (a single-open guard returns EPERM while any inherited fd is open,
and a fresh open also lacks the boot-time chip-select init, so its reads can wedge the bus). Obtain
the fd the boot process tree already holds:

- `FindInheritedFD("/dev/Gpmc")` scans `/proc/self/fd` for the entry whose `readlink` target equals
  `/dev/Gpmc` and returns that fd number.
- `OpenInherited` wraps that number with `os.NewFile(fd, "/dev/Gpmc")` — this does NOT dup it; it is
  the SAME open file description as the boot holder — and clears the GC finalizer
  (`runtime.SetFinalizer(f, nil)`).
- Callers must **NEVER** `Close()` it — that would close the single-open `/dev/Gpmc` fd for the
  whole process tree.
- The fresh `Open(path)` path is the sim / fallback only.

---

## 1. Trigger model

| Element | Where it lives | Register / mechanism |
|---|---|---|
| Level (comparator threshold) | **Hardware** | CS3 level DAC `0x14/0x34` (+ mirror `0x15/0x35`) |
| Source (coarse) | Hardware | CS1 source mux `0x22` (+ `0x53` strobe), engine-safe |
| Source (fine select) | Software | pick which drained channel to discriminate |
| Slope (rising/falling) | Software | edge sign in the centring/qualifier |
| Trigger type (edge/slope/pulse/video) | Software | qualifier over the drained record |
| Mode (AUTO/NORM/SINGLE) | Hardware run word + software publish policy | CS1 run word `0x35` + publish gate |
| Coupling (DC/AC/HFREJ/LFREJ) | Off-bus analog + software filter | spidev1.0 relay byte 2 high nibble |
| Holdoff | Software | re-arm timing (no FPGA holdoff register) |
| Position / delay | **Software** | centre the display window on `EdgeX`; NO register written |

The comparator level DAC and the coarse source mux are the ONLY trigger registers that both reach
the hardware and are safe to write at runtime. Position/delay is done **purely in software** by
centring the display window on the software-detected trigger position `EdgeX`; there is no hardware
trigger-position or pre-trigger-window register in the write path — the CS1 pre-trigger split
`0x3c/0x3d` and window-count `0x17/0x18` are **not written**. The config port `0x2010000e`
(nCONFIG) must **never** be written at runtime — it clears `CONF_DONE` and collapses the engine
(recoverable only by reboot).

---

## 2. Trigger LEVEL DAC (CS3) — the hardware comparator threshold

The trigger level is one 16-bit DAC lane, mirrored to a sibling lane so both comparator references
match. It is written on the CS3 config plane.

| Lane | Low byte (CS3 sel) | High byte (CS3 sel) | Byte address |
|---|---|---|---|
| A | `0x14` | `0x34` | `0x20100028` / `0x68` |
| B (mirror) | `0x15` | `0x35` | `0x2010002a` / `0x6a` |

- Code assembly: `code16 = (hi << 8) | lo`. Both lanes carry the **same** code.
- The **high byte self-latches** the lane; there is no separate strobe.
- ⚠ CS3 `0x35` (level lane-B high byte) is a different plane from CS1 `0x35` (the run word).
  Do not conflate them.

### 2.1 Volts → code (scope: linear fit + raw code)

`TrigLevelCode(volts)` implements ONLY the linear fit valid at 1 V/div and 2 V/div:

```
code16 = round(31434 − 938·volts)      # 0 V = 0x7aca = 31434; higher level = LOWER code
                                       # clamp to [0, 65535]
```

This fit is exact only at 1 V/2 V-div — the DAC rides the per-V/div calibration ladder, so other
V/div settings need the active per-channel cal record. For an unambiguous hardware sweep at any
V/div, drive the raw 16-bit code directly (`SetTrigLevel`) rather than volts.

**General cal-ladder form (documented; the app implements only the §2.1 fit).** The arbitrary-V/div
volts→code is fully pinned — formula and field addresses:

```
code = round((V − Vcenter)/Vref · 50)          # clamp to ±6·Vref about Vcenter
```

- `Vref` (the level-code unit; ±50 codes = one V/div half-span): for C1/C2 it is the per-channel
  **VDIV** field at cal byte address `0x410bf0 + src·0x10 + 8`; the EXT input uses fixed
  `Vref = 200`, the EXT/5 input fixed `Vref = 1000`.
- `Vcenter` is the per-channel zero from the active RAM cal record at
  `0x32ced8 + ch·0xf0 + vd·0x14` (the stride-`0x14` per-V/div record the offset DAC also reads).

Only the §2.1 linear fit is implemented; the general form is documented here (and in the
calibration spec, spec 10 §7.5) for completeness — it is fully pinned, not open. Drive the
raw code (`SetTrigLevel`) for an exact hardware level at any other V/div.

### 2.2 Gated crossing window and out-of-window behaviour

Reference values: boot-inherited comparator level ≈ `0x754c`; `0 V ≈ 0x7aca`. The HW comparator
registers a valid crossing only when the code lands inside a signal-dependent window (≈ `0x7000`–
`0x7900` = 28672–30976 at 1 V/div with the cal square). Outside that window the HW
trigger-position latch (`0x3a/0x3b`) reads 0 and **no crossing is gated**.

Operational clamp: the level is clamped to the panel range **[27000, 35000]** (`0x6978`–`0x88B8`,
≈ +4.7 V … −3.8 V @1 V/div). This range deliberately extends past the narrower gated crossing
window so the user can park the level on either rail. **Behaviour outside the gated window:** the
HW comparator no longer asserts a gated crossing, so discrimination degrades gracefully to the
pure **software content path** — the publish gate (§4) still runs on the drained record content
(edge ptp + slope), and if no edge is present the display HOLDs and eventually shows the honest
flat capture via the flat-fallback. There is no wedge and no infinite hold: liveness is guaranteed
by the software gate, not the HW latch.

### 2.3 The SAFE write sequence (a bare write wedges)

A bare `WriteRegCS(3, 0x14, …)` fired off the frame boundary **wedges the display to a black
framebuffer** (backlight still on). The cause is not the value and not a missing latch strobe: an
unsynchronized GPMC access — of ANY register, read or write — collides with the acquisition
engine's in-flight burst on the HW-serialized bus (there is no software bus lock), stalling the
engine/render path. The safety is **serialization + inherited fd + arm-boundary timing + a
following re-arm**.

Issue the following, in order, **only at the bus-idle Arm boundary, from the single bus owner**:

```
# (1) LEVEL QUAD — lo then hi per lane, both lanes the SAME code; the HIGH byte self-latches.
WriteRegCS(3, 0x14) = lo        # lane A low
WriteRegCS(3, 0x34) = hi        # lane A high  (latches lane A)
WriteRegCS(3, 0x15) = lo        # lane B low
WriteRegCS(3, 0x35) = hi        # lane B high  (latches lane B)

# (2) FULL ENGINE RE-ARM — re-anchor the comparator (REQUIRED; the frame loop does not self-heal).
WriteReg(0x00) = 0x80           # preamble
WriteReg(0x00) = 0x80           # preamble (x2)
WriteReg(0x21) = 0xC0           # arm reset head
WriteReg(0x21) = 0xC0           # (x2)
WriteReg(0x57) = 0x0001         # write-pointer reset pulse high
WriteReg(0x57) = 0x0000         # write-pointer reset pulse low
<arm-settle dwell>              # configured settle (default 2 ms)
WriteReg(0x21) = 0xC3           # go
```

Steps (2) are exactly the engine's normal re-arm (`0xC0 / 0x57 pulse / settle / 0xC3`) with the
`0x00=0x80` preamble prepended.

### 2.4 Cadence and constraints

- **Once-on-change only.** Never re-push the level per frame. Stage a dirty flag when the code
  changes; flush it (the §2.3 sequence) at the next frame boundary.
- **The re-arm is required** after the quad — the running frame loop does not re-anchor the
  comparator to the new reference on its own.
- Setting the comparator via this safe path **keeps the engine alive**: the done-gate keeps firing
  at every level (unlike the config port, which collapses it).
- The vertical **offset DAC** (CS3 `0x10/0x30` CH1, `0x11/0x31` CH2) uses the identical bus-idle
  frame-boundary slot; the offset DAC and the trigger level DAC never write concurrently because
  both are drained from the same command-flush point on the owner goroutine.

---

## 3. Trigger modes and the arm FSM

The run word (CS1 `0x35`) selects the engine's arming policy; a software publish policy layers
STOP on top.

| Mode | Run word `0x35` | Engine behaviour | Publish policy |
|---|---|---|---|
| AUTO | `0x0001` (free-run) | free-runs, completes every budget | publish every coherent frame |
| NORM | `0x0003` (armed) | completes on a comparator edge | publish only qualified frames, else HOLD |
| SINGLE | `0x0003` (armed) | armed, identical to NORM | enter NORM and HOLD the display until an edge (see note) |
| STOP | (unchanged) | FSM stays armed+alive on the bus | publish nothing; display holds last frame |

**SINGLE note:** SINGLE is implemented as plain NORM (armed run word `0x0003`, publish qualified
frames + hold otherwise). Automatic STOP-after-first-frame is **not implemented** — there is no
engine one-shot latch and no `SetSingle`/arm-single command. The display holds on a quiet armed
screen but the engine keeps re-arming and will publish subsequent qualifying frames.
True single-shot (auto-STOP after the first qualified frame) is a `SetSingle`/arm-single control
command plus an engine latch that flips `SetRunning(false)` on the first `publish==true` frame. Until
that command is wired, SINGLE behaves as plain NORM (armed, holds a quiet screen, keeps re-arming).

Arm-opcode port CS1 `0x21`: `0xC0` = reset read/write head, `0xC3` = go (arm), `0xC8` =
capture-halt (latch the coherent frozen frame), `0xCB` = latch-without-halt (roll snapshot; the
FIFO keeps producing). Status port CS1 `0x39`: bit1 (`0x02`) = HW-triggered (comparator fired,
`0x3a/0x3b` valid), bit2 (`0x04`) = frame filled/done. Fill counter CS1 `0x46` (`& 0x07ff`) =
sample-write counter. HW trigger position: CS1 `0x3a` (low) / `0x3b` (high).

### 3.1 Band classification (the ONE authoritative rule)

Every branch below keys off `nativeFast(class, lo, hi)`:

```
nativeFast(class, lo, hi):
    class == 0x20 (500 MSa/s, ≤200 ns/div)   -> true    (native-fast)
    class == 0x01 (250 MSa/s, 500 ns–1 µs)   -> true    (native-fast)
    class == 0x80: divisor = lo | (hi<<16)
                   divisor ≤ 4  (2–20 µs)     -> true    (native-fast)
                   divisor ≥ 8  (≥50 µs)      -> false   (decimated)
    otherwise                                 -> false
```

Native-fast = 100 ns–20 µs/div; decimated = 50 µs–2 ms/div. Slow/roll (≥5 ms/div) uses the
envelope/roll path (§4.4). The ≤50 ns/div equivalent-time interleave is a separate opt-in engine
(`SetETS`) that is **not auto-routed** and does not use the comparator trigger — out of scope here.

The `(class, lo, hi)` fed to `nativeFast` are the sample-clock divisor registers written at band
setup: **class → CS1 `0x19`**, **lo → CS1 `0x1a`**, **hi → CS1 `0x1b`**, with `0x36 = 0` throughout.
`divisor = lo | (hi<<16)`. The complete per-timebase table:

| tdiv/div | class `0x19` | lo `0x1a` | hi `0x1b` | path |
|---|---|---|---|---|
| 1 ns … 200 ns (incl. 25 ns) | `0x20` | `0x0000` | `0x0000` | native-fast |
| 500 ns, 1 µs | `0x01` | `0x0000` | `0x0000` | native-fast |
| 2 µs, 5 µs, 10 µs | `0x80` | `0x0001` | `0x0000` | native-fast |
| 20 µs | `0x80` | `0x0004` | `0x0000` | native-fast |
| 50 µs | `0x80` | `0x0008` | `0x0000` | decimated |
| 100 µs | `0x80` | `0x0014` | `0x0000` | decimated |
| 200 µs | `0x80` | `0x0028` | `0x0000` | decimated |
| 500 µs | `0x80` | `0x0050` | `0x0000` | decimated |
| 1 ms | `0x80` | `0x00c8` | `0x0000` | decimated |
| 2 ms | `0x80` | `0x0190` | `0x0000` | decimated |
| 5 ms | `0x80` | `0x0320` | `0x0000` | envelope |
| 10 ms | `0x80` | `0x07d0` | `0x0000` | envelope |
| 20 ms | `0x80` | `0x0fa0` | `0x0000` | envelope |
| 50 ms | `0x80` | `0x1f40` | `0x0000` | envelope |
| 100 ms | `0x80` | `0x4e20` | `0x0000` | roll |
| 200 ms | `0x80` | `0x9c40` | `0x0000` | roll |
| 500 ms | `0x80` | `0x3880` | `0x0001` | roll |
| 1 s | `0x80` | `0x0640` | `0x0003` | roll |
| 2 s | `0x80` | `0x1a80` | `0x0006` | roll |
| 5 s | `0x80` | `0x3500` | `0x000c` | roll |
| 10 s | `0x80` | `0x8480` | `0x001e` | roll |
| 20 s | `0x80` | `0x0900` | `0x003d` | roll |
| 50 s | `0x80` | `0x1200` | `0x007a` | roll |

(The envelope/roll rows use a moderate phase-scatter divisor instead of the raw table value at
runtime — see §5.5; the table above is the raw real-time divisor set.)

### 3.2 Per-frame FSM (the bus owner)

Native-fast and decimated bands share ONE capture path; they differ only in drain depth and in the
publish gate (§4). NEITHER writes CS3 comparator registers — both **inherit the boot comparator**
(the level DAC is touched only by the explicit `SetTrigLevel` path, §2.3).

1. Service control commands at the frame boundary (including the staged level-DAC flush, §2.3).
2. **ARM:** `0xC0 / 0xC0 / 0x57=1 / 0x57=0 / settle / 0xC3`.
3. **WAIT** (bounded, §3.3): poll `0x39` bit2 (done) then `0x46` fill `& 0x07ff` reaching the
   band's fill target. A frame is **coherent** = done bit2 asserted **AND** fill ≥ target.
   - NORM, **decimated** bands: if not coherent this budget → **re-arm and HOLD** (publish
     nothing). Never publish an untriggered/half-empty decimated NORM frame.
   - NORM, **native-fast** bands: the `0x39` bit2 done-gate does NOT reliably assert for a real
     edge here, so do NOT early-hold — always drain and let the software content gate (§4)
     discriminate (identical to AUTO; the edge is selected by content, not the done-gate).
4. **HALT:** `0x21 = 0xC8` — latch the coherent frozen frame.
5. **DRAIN** the frozen record from `0x30–0x34` (§3.4). This is the ONLY window the engine is
   halted (~1 ms).
6. **RE-ARM immediately** (`0xC0/0x57/0xC3`) so the engine fills again before the frame is
   rendered.
7. **Software-discriminate + publish decision** (§4, §5).

Drain depth per band (`drainCols`):
- Native-fast: `nativeDeepCols = 20480` (the full physical deep record). The trigger edge lands
  near the MIDDLE of the deep record (~sample 10190 of 20480), so a shallow 2048 drain reads only
  the pre-trigger rail; drain the full deep record so software-centring can floor uniformity. The
  roll FIFO (`0x41/0x59`) is NOT used on the halt engine.
- Decimated: `cfg.Cols` (default 2048), clamped to `20480`.

STOP keeps the FSM armed and alive (so the bus never wedges) but publishes nothing; RUN, band
changes and level changes are still serviced ~every 50 ms.

### 3.3 Wait budget, poll cadence, fill target

- **Fill target** (`LatchAt`): default `0x200` = **512** words. A frame is coherent when done
  bit2 is set AND `0x46 & 0x07ff ≥ LatchAt`.
- **Sample interval:** `intervalNs = 10 · (lo | hi<<16)` ns (the divisor tick).
- **Wait budget:** `budget = 3 · intervalNs · LatchAt`, **clamped to [40 ms, 80 ms]**.
- **Poll cadence:** 150 µs between status polls until (done AND filled) or the budget expires.

(Cross-reference: spec 03 defines `LatchAt` and the drain-depth per band.)

### 3.4 Drained-record encoding

The five ports `0x30…0x34` are read **round-robin**: sample `i` reads port `0x30 + (i % 5)`. Each
read returns one 16-bit word:

```
word = (C1_code << 8) | C2_code       # hi byte = CH1, low byte = CH2
```

Samples are **8-bit codes, 0–255**. The code convention is **higher code = higher signal =
higher on screen** (`sampleToY` maps `0x00`→bottom, `0xff`→top); the drained codes are used
directly with **no software inversion**. Therefore "rising" in §5/§7 means **increasing raw code**
(increasing signal amplitude). CH2 is populated only when the second channel is enabled.
(Cross-reference: spec 03 for the byte layout / round-robin.)

---

## 4. Software discrimination-HOLD (the publish gate)

The hardware status gate (`0x39` bit2 / `0x38` bit5) asserts on the untriggered free-run fill too
and does **not** by itself indicate a real caught edge. Discrimination is therefore done on the
**content** of the real captured samples, and the display **holds the last good frame** whenever
the current frame carries no qualifying event. Nothing is ever synthesised — a flat capture is
dropped, never fabricated into an edge.

Per drained frame:

1. Choose the discrimination channel = the selected trigger source (§6): C1 by default, C2 when
   `SetTrigSource(1)` and two channels are enabled.
2. `lvl = (min+max)/2` of that channel over the drained record; `edge = +1` rising / `−1` falling.
3. Run the type qualifier (§5). For EDGE this is the nearest-centre level crossing with the right
   slope; for SLOPE/PULSE/VIDEO it is that type's qualifier.
4. Compute `ptp` (peak-to-peak) of the record.
5. Publish decision by band class:

**4.1 Native-fast bands (`nativeFast==true`, 100 ns–20 µs):**
- A frame carries a real edge iff `ptp ≥ 40` (`nativeEdgeMinPtp`) **AND** a valid right-slope
  crossing exists. `40` cleanly separates a real square edge (~150 codes) from a flat rail
  (~5 codes of ADC noise) at any V/div (the ratio is amplitude-independent).
- Publish only edge frames; otherwise HOLD the last edge frame (do NOT flash the flat capture).
- After `60` consecutive no-edge frames (`nativeFlatFallback`), publish one honest flat capture
  (`EdgeX = −1`, no fabricated centre) so a band whose edge is genuinely absent still shows a live
  real frame. Edgey bands never reach the fallback (the held square stays stable).

**4.2 NORM decimated bands (`nativeFast==false`, NORM):** publish iff the frame is coherent AND
the slope is valid (rejects a spurious opposite-slope micro-crossing = polarity-flip protection).

**4.3 AUTO decimated bands:** publish every coherent frame.

**4.4 Slow / roll bands (≥5 ms/div, envelope):** see §5.5 — the min/max envelope is the display;
there is no per-frame edge discrimination and no HOLD (every frame publishes the scrolling/accumulated
band).

**SLOPE / PULSE / VIDEO types (any real-time band):** the software qualifier IS the trigger —
publish only a frame with a qualifying event, HOLD otherwise, at every band (this overrides the
AUTO "publish everything" default).

The published frame's `EdgeX` is the sub-sample trigger position; the renderer centres the display
window on it. `EdgeX = −1` means a faithful flat/no-edge capture (rail drawn without a centre).

### 4.5 Published frame contract (the arena `Frame`)

The producer drains into a preallocated `Frame` (buffers reused round-robin; the consumer takes a
deep copy). Full field set the renderer/consumer reads:

| Field | Type | Meaning |
|---|---|---|
| `C1`, `C2` | `[]byte` | drained ADC codes (capacity = arena cols); **only `[:Valid]` is this frame's samples** — consumers MUST slice `C1[:Valid]`/`C2[:Valid]`, the tail is stale |
| `Seq` | `uint64` | monotonic capture sequence (advances only on a real drain) |
| `Triggered` | `bool` | `0x39` bit1/bit2 asserted this frame (comparator anchored it) |
| `TrigPos` | `uint16` | `0x3a/0x3b` HW trigger index latched with the frame |
| `Coherent` | `bool` | done gate `0x39` bit2 asserted AND `0x46` reached `LatchAt` (full frame) |
| `HaltOK` | `bool` | `0x46` froze low after the `0x21=0xC8` halt (engine really stopped filling) |
| `Post46` | `uint16` | `0x46` sampled right after the `0xC8` halt (should be small/frozen) |
| `Ptp` | `int` | `C1[:Valid]` peak-to-peak (flat rail ~2–5, real edge ~150) |
| `Valid` | `int` | samples actually drained this frame (band-dependent) |
| `WinCols` | `int` | display window = samples spanning the 10-division screen (§5.6) |
| `EdgeX` | `float64` | software-centred trigger crossing over `C1[:Valid]`; **`−1` = flat rail (no edge)** |
| `Interp` | `bool` | renderer should LINEAR-interpolate the windowed real samples across the panel columns (set for class-`0x20` native-fast, where the window is fewer samples than the panel is wide) |
| `IsEnv` | `bool` | selects the MIN/MAX envelope renderer (slow/roll bands, §5.5) |
| `EnvCols` | `int` | envelope display columns (800) |
| `EnvMin`, `EnvMax` | `[]byte` | per-column (min,max) pairs, `len ≥ EnvCols`, valid only when `IsEnv` |

A real-time (native-fast/decimated) frame MUST clear stale `IsEnv`/`EnvCols` from a previously
visited slow/roll band before publishing, or the renderer takes the wrong (envelope) draw branch
(§9.7).

---

## 5. Trigger TYPES

Exactly **four** types exist (no runt/interval/dropout/pattern/setup-hold). WINDOW is an EDGE
slope sub-mode, not a type. All four operate on the real drained samples; levels are expressed as
fractions of the frame's own `[min,max]` span (band/gain-independent), and times are nanoseconds
converted to samples via the band sample interval:

| Sample-clock class | Interval per sample |
|---|---|
| `0x20` (native-fast, ≤200 ns/div) | 2 ns (capture); displayed at **1 ns/sample** (see §5.6) |
| `0x01` (native-fast, 500 ns–1 µs) | 4 ns |
| `0x80` (decimated / envelope) | `divisor · 10 ns` |
| roll (`0x80`, ≥100 ms/div) | paced at `divisor · 50 ns` (see §5.5) |

Qualifier conditions (measured value vs the window `[min,max]`): `any` (no time/width test),
`less` (< min), `greater` (> max), `inside` (min ≤ measured ≤ max).

**Numeric codes.** Type: EDGE = `0`, PULSE (GLIT) = `1`, SLOPE (SLEW) = `2`, VIDEO (TV) = `3`.
Condition: `any` = `0`, `less` = `1`, `greater` = `2`, `inside` = `3`. Video standard: PAL = `0`
(≤625 lines), NTSC = `1` (≤525 lines).

**Defaults (used until the matching `Set…Params` setter is called):** EDGE rising; SLOPE
`loFrac = 0.2`, `hiFrac = 0.8`, `tMinNs = tMaxNs = 0`, `cond = any`; PULSE `lvlFrac = 0.5`,
`wMinNs = wMaxNs = 0`, `cond = any`, high-pulse polarity (`rising = true`); VIDEO PAL, `line = 0`
(any line), negative sync (`neg = true`).

### 5.1 EDGE
The HW comparator level (§2) places the threshold; software selects which crossing of it becomes
the trigger. Take the mid-level crossing with the requested slope **nearest the frame centre**
(phase-stable for a periodic wave). Validate the slope by comparing the plateaus **immediately
adjacent** to the crossing — bounded `±winCols/4`, skipping the `±winCols/16` transition band —
NOT the outer eighths (a window spanning ~2 signal periods has its ends on opposite plateaus, so an
outer-eighth test would reject every good frame). Rising requires right-plateau ≥ left-plateau −
margin; falling mirrors it. Margin = span/8 (noise never flips the verdict). `winCols` is defined
in §5.6.

### 5.2 SLOPE (SLEW)
Two thresholds (`loFrac`, `hiFrac`) and a traversal-time window (`tMinNs`, `tMaxNs`, condition).
For rising, find a monotone lo→hi traversal (falling: hi→lo) nearest the centre, time it, qualify
the traversal time vs the window, and trigger at the **second-threshold** crossing (sub-sample
interpolated). Bail a traversal that reverses past the first threshold by more than span/10 (not
monotone). Reject a flat rail (span < 40).

### 5.3 PULSE (GLIT)
One level (`pulseLvlFrac`), a width window (`wMinNs`, `wMaxNs`, condition), polarity
(`rising` = high pulse). A high pulse = the region above the level between a rising and the next
falling crossing (low pulse = below). Measure each pulse width, qualify vs the window, and trigger
at the **completing edge** of the qualifying pulse nearest the centre. Reject a flat rail.

### 5.4 VIDEO (TV)
Standard (PAL ≤ 625 lines / NTSC ≤ 525), line select (0 = any), sync polarity (negative = true).
Set a sync level 30 % up from the sync tip (negative sync at the low rail, positive at the high
rail). Collect the sync-edge crossings into the sync region as line boundaries. Trigger on line N
(1-based, clamped to the standard's max) or, for any-line, the sync edge nearest the centre.
VIDEO supports four sync-select sub-modes — **all-lines**, **line-N**, **odd-field**,
**even-field**; only all-lines and line-N are implemented.
**Open:** field/odd-even (odd-field / even-field) discrimination needs a full video frame in the
record to separate the two fields and is not implemented.

### 5.5 Slow / roll bands (≥5 ms/div) — min/max envelope

The 1 kHz cal square is far faster than these timebases, so the faithful display is a solid
rail-to-rail **MIN/MAX peak-detect envelope** built from real captured samples (metadata contract:
`IsEnv=true`, `EnvCols=800`, per-column `EnvMin[]`/`EnvMax[]` bytes, `Ptp`). There is no per-frame
edge discrimination and no phase-lock/HOLD here — every frame publishes.

- **5–50 ms/div (envelope frame):** ARM → wait a modest deep fill (`fillTarget = winCols`, capped
  `0x600`; budget 250 ms, poll 200 µs) → `0xC8` HALT → drain `winCols` deep samples → RE-ARM →
  accumulate the frame's samples into a MIN/MAX ring (last `envRingN = 24` frames) → reduce to
  `envDisplayCols = 800` display columns (each column min/max over all ring samples mapping to it).
  AUTO / phase-independent; publish every frame. The per-sample interval targets a **moderate
  ~0.23 ms/sample** (`envIntervalS = 2.3e-4`, ≈0.23 period of the kHz cal) so each column's samples
  scatter across a period → a solid band from one frame, reinforced across the ring. `winCols` is
  the per-frame sample window bounded to `[envMinWin=200, envMaxWin=2048]` — the `envMinWin=200`
  floor keeps 5 ms at `winCols ≈ 217` (interval ≈ 0.23 ms) instead of flooring the interval below
  the scatter target. The divisor is **derived** to hit `envIntervalS` over the `10·tdiv` span
  (`divisor = round(span / winCols / 10 ns)`), NOT a fixed constant.
- **≥100 ms/div (roll frame):** the deep capture-halt cannot fill at ≥20 ms/sample, so roll uses
  the free-running roll port. Bring-up ONCE per band: `enableAndDivisor` → `0xC0` reset →
  `0x57` pulse → `0xC3` go → settle → pre-fill the 4096-sample ring from one `0xCB` snapshot. Per
  update: issue `0xCB` (latch WITHOUT halt — arming would freeze the free-run) then read the live
  roll ports `0x41` (C1) / `0x59` (C2) by **IOCTL** (each read pops one FIFO sample). **Pace reads
  to `divisor·50 ns`** (clamped [50 µs, 40 ms]) — un-paced rapid reads re-read a dwell value and
  wedge the port. Scroll into the 4096 raw ring, reduce to the 800-column min/max band, accumulate
  over the ring for a stable solid band. Roll divisor is a phase-scatter value
  (`rollDivisor = 7400`; the class-`0x80` count period is `rollClockNs = 50 ns`, so
  `rollIntervalNs = rollDivisor·rollClockNs ≈ 0.37 ms` — ≈0.37 period of the kHz cal) so successive
  samples land on different phases → a rail-to-rail band. The raw scroll ring is `rollWin = 4096`.
  Frame budget ~220 ms; service commands every ~16 reads and **bail to the frame boundary on a
  staged band change / STOP** (so a TIME/DIV knob turn out of roll takes effect within one read
  interval, not after the whole ~0.22 s frame).

**Discrimination-HOLD does not apply at roll rates:** the trigger is not phase-locked; "hold"
degenerates to the continuously scrolling/accumulated envelope, which is the correct slow-band
look for a signal much faster than the sweep.

### 5.6 Display window `winCols`

`winCols` is the number of real drained samples spanning the 10-division display window:

```
winCols = round( 10 · tdivS / (sampleIntervalNs · 1e-9) )      # clamped to [1, drainCols]
```

Special case: class `0x20` bands are DISPLAYED at 1 ns/sample (`sampleIntervalNs = 1`) even though
they CAPTURE at 2 ns/sample, so 10 divisions shows the same span/screen-fraction as the reference.
`winCols` sizes both the EDGE slope-validation window (§5.1) and the software centring window.
(Cross-reference: spec 04 timebase/band table computes `drainCols`/`winCols` per band.)

---

## 6. Source select

- **Coarse hardware mux:** CS1 `0x22` (+ `0x53` strobe), value `& 3` (0 = C1), engine-safe
  (`CONF_DONE` stays set for all values). It is in the trigger path (deterministically shifts the
  C1 anchor) but its C1/C2 code values are not hardware-pinned and it is not an acquisition-class
  register.
- **Fine software select (used):** the drained record already contains both channels; the trigger
  source simply selects **which channel's samples the discrimination/centring locks onto**
  (0 = C1 default, 1 = C2 when the second channel is enabled). Effective immediately.
- **EXT is unusable:** there are exactly two ADC lanes (C1/C2); EXT has no software-visible
  pseudo-channel or readback, so it cannot be software-refined.

The `0x22` mux value is `0x03` = internal channel, `0x00` = EXT, and is otherwise derived from the
slope+mode state; it distinguishes internal-vs-EXT but does **not** cleanly encode C1-vs-C2 (its 2
low bits are not a pinned C1/C2 selector), so it cannot be used for a reliable runtime C1↔C2 switch.
The mux is channel-independent: C1 and C2 both drive `0x0003` (internal) and EXT drives `0x0000`, so
there is no C1/C2 code pair on `0x22` — it selects internal-vs-EXT only. The trigger source channel is
selected upstream by the front-end relay word (spec 06, `byte2` `trigSrc` field) and refined in software.

---

## 7. Slope
Software: rising or falling. It flips the crossing direction the centring/qualifier discriminates,
so the published/held frame carries the requested slope. There is no per-slope GPMC register (CS3
`0x09` is the front-panel LED latch, not a trigger control). "Rising" = increasing raw code (§3.4).
Effective immediately (next frame).

---

## 8. Coupling, holdoff, position

- **Coupling** is set off the GPMC bus via the spidev1.0 relay word. That word is a 24-bit
  little-endian value emitted over `/dev/spidev1.0` (mode 3, 24 bits/word, 300 kHz, one
  `SPI_IOC_MESSAGE(1)` transfer): byte 0 = CH1 control, byte 1 = CH2 control, byte 2 = the trigger
  companion byte. Byte 2 packs `trigCoupling<<4 | trigSrc<<2` — the trigger coupling is the high
  nibble and the **source is bits [3:2]** (0 = C1, 1 = C2, 2 = EXT), NOT the whole low nibble.
  Trigger-coupling nibbles: DC = `0x7_`, AC = `0x5_`, HFREJ = `0xf_`, LFREJ = `0x4_`. It is analog
  and effective (AC/LFREJ shift the trigger anchor). Because spidev is off the GPMC bus, coupling
  writes are issued directly by the producer, not through the frame-boundary command queue. The
  serializer must emit the selected nibble (`DC = 0x7`, `AC = 0x5`, `HFREJ = 0xf`, `LFREJ = 0x4`);
  HFREJ/LFREJ additionally apply a software high/low-frequency-reject digital filter to the drained
  frame before qualification.
- **Holdoff** has **no FPGA register**. It is implemented as **software re-arm timing** (delay
  before re-arming after a published trigger). CS1 `0x17/0x18` are trigger DELAY/POSITION, not
  holdoff, and are not written.
- **Position / delay** is **software only**: the display trigger position is set by centring the
  window on `EdgeX`. No pre-trigger split (`0x3c/0x3d`) or window-count (`0x17/0x18`) register is
  written; native-fast capture is the deep 20480 `0xC8`-halt drain + software centre (§3.2).

---

## 9. Load-bearing constraints (why the design is shaped this way)

1. **Single GPMC-bus owner.** The bus is hardware-serialized with no software lock. Exactly one
   goroutine (the acquisition engine) may issue GPMC reads/writes. Every trigger register write —
   the level DAC quad, the re-arm, the source mux — and every panel matrix read / LED latch /
   offset DAC write is drained from the owner's command queue at the frame boundary. A stray access
   from a panel/render/publish worker during the engine's burst (especially the `0xC8` halt window)
   black-screens the display.
2. **Bus-idle Arm boundary.** The level DAC quad + re-arm is issued only at the top of an FSM
   iteration, when the engine is armed+filling — never inside a `0xC8` halt window.
3. **Inherited fd.** All GPMC access uses the boot-inherited `/dev/Gpmc` fd. Freshly opening it
   post-takeover faults.
4. **Never write the config port `0x2010000e` at runtime** — it clears `CONF_DONE` and collapses
   the engine (reboot to recover). Runtime source select uses the coarse `0x22` mux only.
5. **Inherit the boot comparator.** Native-fast and decimated bands write NO CS3 comparator
   registers; keeping the inherited comparator is what keeps `0x39` bit2 firing. The **only** code
   that touches CS3 comparator regs is the explicit level-DAC path (`SetTrigLevel` → the §2.3
   sequence).
6. **Level once-on-change, always followed by a re-arm.** No per-frame re-push.
7. **Clear reused frame metadata.** The frame buffers are reused round-robin; on a real-time frame
   clear any stale `IsEnv`/`EnvCols`/min-max metadata from a previously visited slow/roll band
   before publishing, or the renderer takes the wrong (envelope) draw branch.
8. **Pace the roll port.** The free-running roll port (`0x41/0x59`) is IOCTL-read and must be paced
   to its sample-clock interval; rapid un-paced reads re-read a dwell value and wedge it. The roll
   path uses `0xCB` (latch-no-halt), never `0xC8` (a halt freezes the free-run).
9. **CS3 LED latch (`0x09/0x0a/0x0b`) is NOT a trigger event-mask.** It is the front-panel LED
   latch; writing it per-frame on the deep path clobbers the inherited comparator's done gate.

---

## 10. Control-plane surface

Engine commands (staged, applied at the frame boundary or immediately as noted):

| Command | Effect |
|---|---|
| `SetTrigLevel(code uint16)` | stage the raw 16-bit level-DAC code; owner writes the §2.3 sequence (once-on-change). `code 0` = clear/none (inherited comparator kept). |
| `TrigLevelCode(volts) uint16` | volts→code (§2.1 linear fit); exact at 1 V/2 V-div only. |
| `SetTrigSlope(rising bool)` | software slope (immediate). |
| `SetTrigSource(ch int)` | software source channel 0=C1 / 1=C2 (immediate). |
| `SetTrigType(typ)` | edge / pulse / slope / video (next frame). |
| `SetSlopeParams(loFrac, hiFrac, tMinNs, tMaxNs, cond)` | SLOPE config. |
| `SetPulseParams(lvlFrac, wMinNs, wMaxNs, cond)` | PULSE config. |
| `SetVideoParams(std, line, neg)` | VIDEO config. |
| `SetNorm(bool)` / `SetRunning(bool)` | NORM vs AUTO / RUN vs STOP. |

There is no single-shot command; SINGLE maps onto `SetNorm(true)`+`SetRunning(true)` (see §3 note).

Front panel:
- **TRIG LEVEL** knob steps the raw code (CW = higher level = LOWER code) through the engine
  command queue — center `31434` (`0x7aca`), step `40` codes/accel-step (~0.043 V @1 V/div), clamp
  `[27000, 35000]` (~+4.7 V … −3.8 V @1 V/div).
- **SINGLE** button → `SetNorm(true)` + `SetRunning(true)` (enters NORM/arm and holds the display
  until an edge; does not auto-STOP).
- **AUTO** button → `SetNorm(false)` + `SetRunning(true)` (returns to AUTO free-run).
- **RUN/STOP** button → toggles `SetRunning`.

Trigger-mode parameters are guarded by the command mutex and read as a value snapshot at each frame
boundary, so a mid-frame control change never tears the current frame.

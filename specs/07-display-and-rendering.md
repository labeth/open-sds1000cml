# 07 — Display and Rendering

The renderer turns one frozen acquisition frame into an 800×480 RGB565 image on the
panel. It is a pure consumer: it reads a frame **copy** the acquisition owner already
drained and handed off, and it **never touches the GPMC bus, the FPGA registers, or any
capture state**. Everything in this document runs on the render/consumer worker; the
acquisition owner is the only writer of the frame data described here.

The `Frame` and `Arena` types are **shared with the acquisition-engine spec**
([03-acquisition-engine.md](03-acquisition-engine.md)), which owns their production side;
their normative Go definitions are reproduced here (§2.1, §3) so the renderer is buildable
from this document alone. This document defines only what the renderer reads from a frame
and how it paints; the buffer-sharing / read-only boundary is specified in 03.

---

## 1. Panel geometry and pixel format

| Property | Value |
|---|---|
| Visible resolution | **800 × 480** (`W=800`, `H=480`), origin top-left |
| Pixel format | **RGB565, little-endian** (2 bytes/pixel) |
| Virtual resolution | **800 × 960** — two stacked 800×480 pages (double-buffered) |
| Device | `/dev/fb0` |
| Page bytes | `800 × 480 × 2 = 768000` per page |

RGB565 packing: `pixel = (R>>3)<<11 | (G>>2)<<5 | (B>>3)`. Red and blue keep their top
5 bits, green its top 6. Reference values: `RGB(0xFF,0,0)=0xF800`, `RGB(0,0xFF,0)=0x07E0`,
`RGB(0,0,0xFF)=0x001F`.

### 1.1 Framebuffer open and mapping

Open `/dev/fb0` with a **fresh** `O_RDWR` open — `syscall.Open("/dev/fb0", O_RDWR, 0)`.

> **Trap — the framebuffer fd is NOT inherited.** Unlike the GPMC bus fd and
> `/dev/fpga_key` (both of which MUST reuse the inherited boot fd because a fresh
> open EFAULTs post-takeover), `/dev/fb0` is a standard character
> device: a normal fresh open works. Do not attempt to inherit it.

Read `yres_virtual` from `/sys/class/graphics/fb0/virtual_size` (format `"xres,yres"`);
default to `2×480 = 960` if unreadable. Then:

- mmap length = `800 × yres_virtual × 2` bytes, `PROT_READ|PROT_WRITE`, `MAP_SHARED`.
- `pages = yres_virtual / 480` (minimum 1).

### 1.2 Virtual height and page flipping

The renderer draws into an in-memory **back buffer** of exactly one page (`800×480×2`
bytes). `Present` (a method of the framebuffer surface implementation — see §1.3, **not**
of the renderer) makes the back buffer visible:

1. **Page-flip path (preferred, tear-free).** Requires `pages ≥ 2` and a working pan
   ioctl. Copy the back buffer into the **hidden** page (`page = 1 - cur`), never the
   page currently scanned out, then pan the panel to it. This guarantees the scanned-out
   page is never written mid-frame, so there is **no tearing**.
2. **Copy-all fallback.** If panning is unavailable, copy the back buffer into **every**
   page. Frames are complete but this writes the live page, so it may tear.

Pan is attempted two ways, in order (some drivers only honour one):

| ioctl | value | notes |
|---|---|---|
| `FBIOGET_VSCREENINFO` | `0x4600` | read `fb_var_screeninfo` (160 bytes on 32-bit ARM) into a cached buffer |
| `FBIOPAN_DISPLAY` | `0x4606` | primary pan; set `yoffset` (byte offset 20) = `next × 480` first |
| `FBIOPUT_VSCREENINFO` | `0x4601` | fallback; set `activate` (byte offset 84) = `0x80` (`FB_ACTIVATE_FORCE`), issue, then clear `activate` back to 0 |

`fb_var_screeninfo` field offsets used: `yres_virtual` at byte 12, `yoffset` at byte 20,
`activate` at byte 84.

**Trap:** there is one live logical page from the renderer's point of view. The double
page exists purely so `Present` can flip rather than overwrite the live scanout. The
renderer must always draw a **complete** frame into the back buffer before `Present`;
there is no partial/incremental update.

### 1.3 Surface interface

The renderer draws through a narrow, backend-agnostic surface so the **same** renderer
runs byte-identically against the real `/dev/fb0` mmap and an in-memory test surface.
The renderer calls exactly these three methods and nothing else:

```
SetPixel(x, y int, rgb565 uint16)   // write one RGB565 pixel; out-of-range (x,y) ignored
Fill(rgb565 uint16)                 // paint the entire surface one colour
At(x, y int) uint16                 // read back a pixel; out-of-range returns 0
```

Pixel type is `uint16` RGB565 (see §1). The surface implementation is responsible for
clipping out-of-range coordinates and for the little-endian byte layout in the mmap
(store low byte first). `Present()` and page-flipping (§1.2) are methods of the concrete
framebuffer surface — **the renderer never calls `Present`**; the render loop (§2) does.

Keeping the renderer behind this interface is load-bearing: it is what lets an in-memory
surface back the renderer for verification without hardware.

### 1.4 Cold panel bring-up

Bring-up runs once at start before any render, and is a prerequisite for `/dev/fb0` to
exist. It has three ordered stages:

1. **Load the LCD-controller driver stack**, in this exact order (the `da8xx-fb` driver
   depends on the three `cfb*` helpers and **must load last** — loading it first fails):

   ```
   insmod cfbcopyarea.ko
   insmod cfbfillrect.ko
   insmod cfbimgblt.ko
   insmod da8xx-fb.ko      # creates /dev/fb0
   ```

2. **Enable the panel via GPIO7** (AM335x GPIO bank 0, bit 7 — the LCD display-enable /
   panel-power line; driving it low blanks the panel, high lights it), through the sysfs
   GPIO interface:

   ```
   echo 7   > /sys/class/gpio/export
   echo out > /sys/class/gpio/gpio7/direction
   echo 1   > /sys/class/gpio/gpio7/value
   ```

3. **Open and map `/dev/fb0`** (§1.1): fresh `O_RDWR` open, then mmap
   `800 × yres_virtual × 2` bytes `PROT_READ|PROT_WRITE`, `MAP_SHARED`.

The panel geometry/timing is compiled into `da8xx-fb.ko`'s platform data (no app-level
mode string): panel type `HANSTAR_HSD070IDW1_A`, **800×480**, RGB565 (16 bpp), stride
**1600**, pixel clock **30.000 MHz**, mode `800x480-52`, refresh **≈52.16 Hz**, virtual
**800×960** (double-buffered by y-pan). There is no firmware brightness/dimming control —
GPIO7 is a binary on/off with no brightness register; the panel runs at full backlight.

The consumer runs a fixed-cadence loop with a loop period of **50 ms** (~20 fps):

1. `f, fresh := arena.Consume()` — take the newest published frame if one arrived since
   the last call; otherwise re-present the last frame (`fresh == false`).
2. `Draw(surface, f, twoChan, fresh)` — paint the full 800×480 back buffer.
3. `surface.Present()` — flip/copy to the panel.

`twoChan` is true when two channels are enabled. `fresh` is passed through as the
`live` flag (see §7, liveness strip).

**The 50 ms period is a HARD MINIMUM, not a target.** The renderer shares the single ARM
SoC with the acquisition owner, the panel worker, and the publish worker. Running the
loop faster starves those goroutines: dropping to ~33 ms (~30 fps) **regresses both
capture uniformity** (200 ns / 500 ns / 2 µs cross-frame std jumps from ~0.5 to ~13) **and
the served frame rate**. 50 ms leaves ~17 ms/frame of headroom for the ~23 ms roll drain
plus arm/poll on the acquisition owner. Do not lower it.

`Consume` returning `false` is **not** an error — a genuinely quiet NORM display holds
its last frozen frame. The renderer re-draws the held frame; it must not blank or flash.

**Frame ownership.** `Consume` swaps the newest ready frame into the consumer's private
slot under a microsecond mutex (a RAM pointer swap, never a bus operation). The renderer
then owns that slot until the next `Consume`; the acquisition owner cannot mutate it.
The renderer must therefore never retain a `*Frame` pointer across `Consume` calls.

### 2.1 Arena (triple buffer)

The arena is a lock-based **triple buffer** of three preallocated `Frame`s with
drop-newest backpressure. The producer fills its private `write` slot with **no lock
held** (the ~1 ms drain), then swaps it into `ready` under a microsecond critical
section; the consumer swaps `ready` into its private `read` slot the same way. Producer
and consumer never touch each other's private slot, so there is no tearing and the
producer never blocks on the ~50 ms render. The mutex guards only the RAM pointer swap —
it is never on the GPMC bus.

```go
type Arena struct {
    mu    sync.Mutex
    write *Frame // producer's private fill slot
    ready *Frame // most-recent completed frame (nil until first publish)
    read  *Frame // consumer's private slot
    dirty bool   // ready holds a frame not yet consumed
    gen   uint64 // publish generation (fps / liveness token)
}

func NewArena(cols int) *Arena {
    return &Arena{write: newFrame(cols), ready: newFrame(cols), read: newFrame(cols)}
}

// Producer returns the producer's private fill slot to drain into.
func (a *Arena) Producer() *Frame { return a.write }

// Publish makes the just-filled producer slot the newest ready frame (drop-newest).
func (a *Arena) Publish() {
    a.mu.Lock()
    a.write, a.ready = a.ready, a.write
    a.dirty = true
    a.gen++
    a.mu.Unlock()
}

// Consume returns (newest ready frame, true) if one arrived since the last Consume,
// else (the consumer's last frame, false) so the renderer re-presents the held frame.
func (a *Arena) Consume() (*Frame, bool) {
    a.mu.Lock()
    if !a.dirty {
        f := a.read
        a.mu.Unlock()
        return f, false
    }
    a.read, a.ready = a.ready, a.read
    a.dirty = false
    f := a.read
    a.mu.Unlock()
    return f, true
}

// Gen returns the publish generation — advances only on a real producer hand-off.
func (a *Arena) Gen() uint64 { a.mu.Lock(); g := a.gen; a.mu.Unlock(); return g }
```

---

## 3. Frame contract (what the renderer reads)

The renderer reads only these fields of the frozen frame (full type owned by
[03-acquisition-engine.md](03-acquisition-engine.md)). All are produced and frozen by the
acquisition owner; the renderer treats them as read-only truth.

| Field | Go type | Meaning for rendering |
|---|---|---|
| `C1`, `C2` | `[]byte` | ADC sample bytes (8-bit codes). Only `[:Valid]` is this frame's data; the tail is stale. |
| `Valid` | `int` | Number of valid samples in `C1`/`C2`. **Always slice `C1[:Valid]`.** |
| `WinCols` | `int` | Display window width in **samples** — real samples spanning the full 10-division screen at this band's sample interval. |
| `EdgeX` | `float64` | Software-centred trigger crossing position (sub-sample) within `C1[:Valid]`, or **`< 0`** when the frame is a flat rail (no edge). |
| `Interp` | `bool` | When true, linear-interpolate the windowed samples across panel columns. When false, use nearest sampling. |
| `Ptp` | `int` | Peak-to-peak of `C1[:Valid]`. Used only for the liveness strip colour. |
| `IsEnv` | `bool` | When true, render the min/max **envelope** instead of a line trace (slow/roll bands only). |
| `EnvCols` | `int` | Number of `(min,max)` column pairs in the envelope (equals 800). |
| `EnvMin`, `EnvMax` | `[]byte` | Per-display-column min/max ADC codes, stored **planar channel-major**: `channels × EnvCols` entries, channel `ch` occupying `[ch·EnvCols : (ch+1)·EnvCols]`. Valid only when `IsEnv` and `len(EnvMax) ≥ channels·EnvCols`. |

**Load-bearing trap — stale envelope flag.** The arena frame buffers are reused
round-robin. `IsEnv`/`EnvCols`/`EnvMin`/`EnvMax` are set **only** on slow/roll envelope
frames. The real-time (native-fast + decimated) and probe producers **must explicitly
clear `IsEnv=false` and `EnvCols=0`** on the buffer they fill, every frame. If they do
not, a leftover envelope from a previously-visited slow band rides along in the reused
buffer and the renderer takes the envelope FILL branch (§6) on fast-band frames — the
symptom is a **solid rail-to-rail yellow block over the left of the screen** instead of a
clean line trace. This is a producer-side requirement, but the renderer's branch order
(§4) makes clearing mandatory: `IsEnv` is checked first and short-circuits the trace path.

### 3.1 Normative Go definition

```go
type Frame struct {
    C1, C2 []byte // capacity = arena cols; only [:Valid] carries this frame's samples

    Seq       uint64  // monotonic capture sequence (advances only on a real drain)
    Triggered bool    // comparator anchored the frame this cycle
    TrigPos   uint16  // HW trigger index latched with the frame
    Coherent  bool    // done gate asserted AND the record filled to depth
    HaltOK    bool    // the engine really stopped filling after the capture-halt
    Post46    uint16  // fill counter sampled right after the halt (small/frozen)
    Ptp       int     // C1[:Valid] peak-to-peak (flat rail ~2-5; a real edge ~150)
    Valid     int     // number of samples this frame drained; slice C1[:Valid]
    WinCols   int     // display window in samples (10 divisions at this band)
    EdgeX     float64 // software-centred crossing over C1[:Valid], or -1 (flat rail)
    Interp    bool    // renderer linear-interpolates the windowed samples

    IsEnv          bool   // envelope (min/max) render selected (slow/roll bands)
    EnvCols        int    // number of (min,max) column pairs per channel (= 800)
    EnvMin, EnvMax []byte // planar channel-major, len >= channels*EnvCols when IsEnv
}

// newFrame allocates one frame; EdgeX defaults to -1 and Valid to cols (a full record).
func newFrame(cols int) *Frame {
    return &Frame{
        C1: make([]byte, cols), C2: make([]byte, cols), Valid: cols, EdgeX: -1,
        EnvMin: make([]byte, envDisplayCols), EnvMax: make([]byte, envDisplayCols),
    }
}
```

`WinCols` and `Valid` default to `cols`; `EdgeX` defaults to `-1`. C1/C2 are the hi/lo
bytes of the drained round-robin word (one byte per channel per sample).

---

## 4. Draw sequence

`Draw(surface, f, twoChan, live)`:

1. **Fill** the whole surface with the background colour `colBG`.
2. **Draw the graticule** (§5): 8×10 grid plus centre cross-hairs.
3. If `f == nil` or `len(f.C1) == 0`, return (blank graticule only).
4. Compute `valid = f.Valid`, clamped to `1..len(f.C1)`.
5. **If `f.IsEnv` AND `f.EnvCols > 0` AND `len(f.EnvMax) ≥ channels·f.EnvCols`:** draw the
   envelope (§6.3) once per enabled channel (C1 always; C2 when `twoChan`), draw the
   liveness strip (§7), and **return**.
6. Otherwise draw the **line trace** (§4.1) for C1, and for C2 when `twoChan` and
   `len(f.C2) ≥ valid`.
7. Draw the liveness strip (§7).

### 4.1 Trace window centring

The display shows a `WinCols`-sample window of the record, centred on the trigger
crossing:

1. `win = f.WinCols`, clamped: if `win ≤ 0` or `win > valid`, set `win = valid`.
2. `xc = f.EdgeX`.
3. If `xc < 0`, recompute a fallback crossing: `xc = CenterCross(c1, MidLevel(c1), +1)`
   — the rising mid-level crossing nearest the record centre.
4. If still `xc < 0` (a genuinely flat rail — the faithful native-fast result at
   100–200 ns/div), set `xc = valid/2`: centre on the record middle and show the rail.
   **Do not fabricate an edge.**
5. Draw the trace of `c1 = f.C1[:valid]` with window `win` centred at `xc`, colour
   `colC1`, interpolation flag `f.Interp`. Draw C2 identically with the same `xc`/`win`
   so the two channels stay horizontally aligned.

**`MidLevel(sig)`** returns `(min+max)/2` over the slice (integer), or **128** on an empty
slice.

**`CenterCross(sig, lvl, edge)`** returns the sub-sample position of the qualifying crossing
of `lvl` nearest the slice centre, or **−1** if none. `center = len(sig)/2`. Scan
`c = 1 .. len(sig)-1`; a rising crossing (`edge ≥ 0`) is `sig[c-1] < lvl && sig[c] ≥ lvl`
(falling mirrors it: `sig[c-1] > lvl && sig[c] ≤ lvl`). Among all crossings pick the one
whose index `c` minimises `|c − center|`. For the chosen `c`, the fractional part is
`bf = (lvl − sig[c-1]) / (sig[c] − sig[c-1])` when the denominator is non-zero **and**
`bf ∈ [0,1)`, else `bf = 0`. Return `float64(c-1) + bf`.

---

## 5. Graticule

An 8-row × 10-column grid across the full 800×480 panel, drawn before the trace so the
trace overlays it.

- **Vertical lines:** for `c = 0..10`, `x = c × (800-1) / 10`. Colour `colGrid`, except
  the centre column `c == 5` uses `colAxis` (the vertical cross-hair). Draw full height
  `y = 0..479`.
- **Horizontal lines:** for `r = 0..8`, `y = r × (480-1) / 8`. Colour `colGrid`, except
  the centre row `r == 4` uses `colAxis` (the horizontal cross-hair). Draw full width
  `x = 0..799`.

So the graticule is 10 horizontal divisions (matching the 10-division `WinCols`) × 8
vertical divisions, with a brighter centre cross. With the workspace UI the graticule spans the plot area the
layout leaves (beside the 30-px rail, below a workspace's pane; §10.3) with the same 10 × 8
divisions.

---

## 6. Line trace vs. envelope

### 6.1 Vertical mapping (ADC code → row)

Both trace and envelope map an 8-bit ADC code to a panel row with the display inverted
(code `0x00` at the bottom, `0xFF` at the top), leaving a small top/bottom margin:

```
top = 8, bot = 480 - 4 = 476
y   = bot - v × (bot - top) / 255        // integer code v
```

clamped to `0..479`. Higher code → smaller `y` (higher on screen).

For interpolated (fractional) code values `v`, use the rounded float form:
`y = bot - round(v × (bot-top)/255)`. It equals the integer form exactly at integer `v`.

### 6.2 Line trace (`drawTrace`)

Maps the `win`-sample window, centred at `xc`, across all 800 columns and draws a
connected 1-px waveform.

- `left = xc - win/2` — the fractional sample position of the leftmost column.
- For each panel column `x = 0..799`:
  `pos = left + x × win / 800` — the fractional sample position for this column.

**Nearest sampling (`interp == false`):**
`idx = floor(pos)`; if `idx` is out of `[0, n)` skip this column (break the line);
else `y = sampleToY(sig[idx])`.

**Linear interpolation (`interp == true`):**
if `pos < 0` or `pos > n-1` skip this column; else `i = floor(pos)`,
`frac = pos - i`, `v = sig[i]·(1-frac) + sig[i+1]·frac`, `y = sampleToYf(v)`.
If `i+1 ≥ n` (last sample), use `sig[i]` directly.

Connect consecutive drawn columns with a **Bresenham line** (`(prevX,prevY)→(x,y)`). The
first drawn column, and any column following a skipped one, is drawn as a single pixel
(no connecting segment across a gap).

**Which bands set `Interp`.** The producer sets `Interp` from the `nativeFast(class, lo,
hi)` predicate, which has a **single divisor boundary at 4**:

```
Interp = (class == 0x20) || (class == 0x01) ||
         (class == 0x80 && (uint32(lo)|uint32(hi)<<16) <= 4)
```

| Sample-clock class | Divisor `lo \| hi<<16` | `Interp` |
|---|---|---|
| `0x20` (500 MSa/s, ≤200 ns/div) | any | **true** |
| `0x01` (250 MSa/s, 500 ns–1 µs/div) | any | **true** |
| `0x80` (100 MSa/s base) | `≤ 4` (≈2–20 µs/div) | **true** |
| `0x80` | `> 4` (i.e. `≥ 5`, decimated) | false |

There is **no gap band**: divisor 5 and up (all decimated `0x80` bands) is non-interpolated.

On the native-fast bands the display window is far fewer samples than 800 columns wide
(e.g. 125 samples at 25 ns/div), so `Interp=true` and each column reads the window at its
fractional position and blends the two bracketing real samples. On the decimated bands
`WinCols ≥ 800`, every column already maps to its own real sample, and nearest sampling
is used (no upsampling gap to smooth).

**Why linear, not sinc.** With `Interp=true`, nearest sampling would hold each sample flat
across ~3 columns then jump the full inter-sample code delta — visible **stair-steps** on
an edge. Connecting adjacent real samples with straight vectors (linear interpolation)
gives the smooth rise the band-limited edge actually has. `sin(x)/x` reconstruction is
**wrong** here — it rings (≈10-code overshoot) on the bandwidth-limited edge. Linear
interpolation touches only **real captured samples**; no sample is fabricated.

A roll view's samples no capture covered (`Frame.Held`: the last value held between back-to-back
captures, and the view's past before the first) are drawn in the channel colour dimmed to about
40 % (`heldColor`; the web: the same samples at alpha 0.35), so they do not read as signal;
measurements read the view's longest captured stretch (`Frame.MeasureRange`).

**Zoom into a live min/max frame.** The frame's pairs span the whole screen; zoomed until fewer of
them than plot columns are shown, a magnified bucket's pair would draw as a slope (a 1 MHz square at
200 µs/div zoomed 50× read as a slow wave). Each zoomed surface leases its stretch instead
(`Engine.LeaseViewWindow`: the scope's zoom, the web page's, in screen widths from the trigger, and
the columns that show it), and the engine reads that stretch of the same frozen capture again
(`Frame.Views`), a min/max pair per column (raw when short), placed among the frame's values
(`ViewWindow.From`/`To`). The LCD draws it in place of the frame's pairs while it covers the zoomed
window (`zoomView`), the web while it covers the shown one (`viewCovers`; it travels after the
binary frame's payload); AC coupling moves it by the whole trace's DC (`analog.CoupleDisplayBy`).
A zoom the screen's pairs still resolve (one pair every second column or more) reads nothing.

### 6.3 Envelope (`drawEnvelope`) — slow/roll bands only

For a signal far faster than the timebase, one display column spans many signal periods,
so the correct picture is a solid vertical **min..max band** per column (the peak-detect
look), not a single line. This branch runs **only** when the frame's `IsEnv` flag is set
(slow ≥ 5 ms/div and roll ≥ 100 ms/div bands). `EnvMin`/`EnvMax` hold `channels × EnvCols`
per-column `(min,max)` pairs (`EnvCols = 800`), planar channel-major, reduced by the
producer from the real drained samples of each channel.

For each enabled channel `ch` (C1 in `colC1`, C2 in `colC2` when `twoChan`), pick the
channel colour `col`, then for each panel column `x = 0..799`:
1. `c = x × EnvCols / 800` (clamped to `EnvCols-1`).
2. `yTop = sampleToY(EnvMax[ch·EnvCols+c])`, `yBot = sampleToY(EnvMin[ch·EnvCols+c])`
   (max code is higher on screen).
3. Swap if `yTop > yBot`.
4. Fill every row `y = yTop..yBot` with `col`.

Every filled pixel lies between a real captured min and max — nothing is synthesized.
Since `EnvCols` is already 800, the `x → c` map is 1:1 in practice.

---

## 7. Liveness strip

A 2-pixel-tall strip along the very top edge (`y = 0` and `y = 1`, `x = 0..799`) shows at
a glance, from a photo of the panel, whether the displayed frame is a fresh coherent
capture or a held/stale one:

- Colour `colOK` (green) when `live` (i.e. `fresh` from `Consume`) is true **and**
  `f.Ptp ≥ 8`.
- Colour `colStale` (red) otherwise (held frame, wedged, or a flat/dead trace).

The same strip is drawn on both the trace and envelope paths.

---

## 8. Colour palette (RGB565)

| Name | RGB | Use |
|---|---|---|
| `colBG` | `(0, 0, 16)` | background (near-black blue) |
| `colGrid` | `(40, 40, 60)` | graticule grid lines |
| `colAxis` | `(80, 80, 110)` | centre cross-hairs (column 5, row 4) |
| `colC1` | `(255, 236, 0)` | channel 1 trace / envelope (yellow) |
| `colC2` | `(240, 108, 196)` | channel 2 trace (pink, the panel's C2 marking) |
| `colOK` | `(0, 200, 0)` | liveness strip: fresh coherent frame |
| `colStale` | `(220, 40, 40)` | liveness strip: held / wedged / dead |
| `colSR` | `(53, 200, 232)` | super-res overlay and gate markers (cyan) |
| `colMenuBg` | token `lcd-menu` `#080c1c` | sheet tab strip, pane header band, F-key column |
| `colBoxBg` | token `lcd-box` `#060a16` | the sheet's side panel, the byte picker box, measurement boxes |
| `colWarn` | token `warn` | warnings in the panes (a sample-limited eye) |
| `colCursor` | token `cursor` | the eye diagram's sample-point and threshold cursors |

The palette is generated into `lcd/palette_gen.go` from `web/tokens.json` (the source shared with
the web UI); the values above that predate it are the defaults it replaced.

---

## 9. Load-bearing constraints (why the design is shaped this way)

1. **The renderer never touches GPMC.** All capture, halt, drain, and re-arm happen on
   the single acquisition owner. The renderer works from a frozen copy only. A second bus
   consumer during the owner's capture-halt window would wedge the engine — the renderer
   must never introduce one.
2. **The render loop period MUST NOT drop below ~50 ms.** The renderer shares the ARM SoC
   with the acquisition/panel/publish goroutines; running faster starves them and
   regresses both capture uniformity and the served frame rate (§2). 50 ms is a hard
   floor, not a cosmetic default.
3. **Draw only `[:Valid]`.** The `C1`/`C2` buffers are sized to the arena's native-fast
   capacity and reused; the tail past `Valid` is stale data from a previous band.
4. **The producer must clear `IsEnv`/`EnvCols` on every real-time frame** (§3). The
   renderer's `IsEnv`-first branch order makes a stale flag render as a solid fill on fast
   bands. This clearing is mandatory on the reused round-robin buffers.
5. **The framebuffer fd is a fresh open, not inherited** (§1.1) — the opposite of the
   GPMC and `/dev/fpga_key` fds.
6. **Never fabricate an edge.** When there is no real crossing (`EdgeX < 0` and no
   fallback crossing), centre on the record middle and show the rail as captured.
7. **Interpolate only real samples.** Linear interpolation is display reconstruction of
   captured samples; it must never invent samples, and sinc/sin(x)/x must not be used
   (it overshoots the band-limited edge).
8. **Draw a full frame before Present.** There is no incremental update; the back buffer
   is fully repainted (fill → graticule → trace/envelope → strip) each iteration, then
   flipped whole.
9. **Hold, don't blank, on a quiet display.** `Consume` returning `fresh == false` means
   re-present the last frame; the strip goes red but the trace stays.
10. **Draw through the surface interface (§1.3), not `/dev/fb0` directly.** The renderer
    must be surface-agnostic so an in-memory surface can back it for verification.

---

## 10. Overlay layer — HUD, cursors, task workspaces (reference layout)

The minimal renderer of §§2–7 draws **graticule + trace/envelope + the 2-px liveness
strip only**. The on-screen HUD read-outs, cursors, and the workspace UI (§10.3) are a separate
**overlay** painted on top of the same back buffer, after the trace/envelope and before
`Present`. The overlay is text-driven and uses a compact **5×7 bitmap font** (§10.0): at
scale 1 a glyph cell is **5 px wide + 1 px inter-glyph space (advance 6 px)** and **7 px
tall**; row height = `7 × scale`, string width = `len(runes) × 6 × scale`. Out-of-range
pixels are clipped by the surface. The overlay never reads capture state — it is fed the
same frozen frame plus the UI-state snapshot.

### 10.0 Bitmap font

Each glyph is **5 column bytes**; each column byte is a 7-bit vertical bitmap with
**bit 0 = top row, bit 6 = bottom row**. Advance per glyph = `glyphW(5) + 1 = 6` px at
scale 1. `Draw(surface, x, y, str, colour, scale)` renders each set bit as a `scale×scale`
block at `(x + col·scale, y + row·scale)`, then advances `x` by `6·scale`; unknown runes
render **blank** (a space). `DrawRight(surface, xr, y, …)` right-aligns by drawing at
`xr − Width(str, scale)`.

```go
var glyphs = map[rune][5]byte{
    ' ':  {0x00, 0x00, 0x00, 0x00, 0x00}, '!':  {0x00, 0x00, 0x5F, 0x00, 0x00},
    '"':  {0x00, 0x07, 0x00, 0x07, 0x00}, '#':  {0x14, 0x7F, 0x14, 0x7F, 0x14},
    '%':  {0x23, 0x13, 0x08, 0x64, 0x62}, '\'': {0x00, 0x05, 0x03, 0x00, 0x00},
    '(':  {0x00, 0x1C, 0x22, 0x41, 0x00}, ')':  {0x00, 0x41, 0x22, 0x1C, 0x00},
    '*':  {0x14, 0x08, 0x3E, 0x08, 0x14}, '+':  {0x08, 0x08, 0x3E, 0x08, 0x08},
    ',':  {0x00, 0x50, 0x30, 0x00, 0x00}, '-':  {0x08, 0x08, 0x08, 0x08, 0x08},
    '.':  {0x00, 0x60, 0x60, 0x00, 0x00}, '/':  {0x20, 0x10, 0x08, 0x04, 0x02},
    '0':  {0x3E, 0x51, 0x49, 0x45, 0x3E}, '1':  {0x00, 0x42, 0x7F, 0x40, 0x00},
    '2':  {0x42, 0x61, 0x51, 0x49, 0x46}, '3':  {0x21, 0x41, 0x45, 0x4B, 0x31},
    '4':  {0x18, 0x14, 0x12, 0x7F, 0x10}, '5':  {0x27, 0x45, 0x45, 0x45, 0x39},
    '6':  {0x3C, 0x4A, 0x49, 0x49, 0x30}, '7':  {0x01, 0x71, 0x09, 0x05, 0x03},
    '8':  {0x36, 0x49, 0x49, 0x49, 0x36}, '9':  {0x06, 0x49, 0x49, 0x29, 0x1E},
    ':':  {0x00, 0x36, 0x36, 0x00, 0x00}, '<':  {0x08, 0x14, 0x22, 0x41, 0x00},
    '=':  {0x14, 0x14, 0x14, 0x14, 0x14}, '>':  {0x00, 0x41, 0x22, 0x14, 0x08},
    '?':  {0x02, 0x01, 0x51, 0x09, 0x06}, '@':  {0x32, 0x49, 0x79, 0x41, 0x3E},
    'A':  {0x7E, 0x11, 0x11, 0x11, 0x7E}, 'B':  {0x7F, 0x49, 0x49, 0x49, 0x36},
    'C':  {0x3E, 0x41, 0x41, 0x41, 0x22}, 'D':  {0x7F, 0x41, 0x41, 0x22, 0x1C},
    'E':  {0x7F, 0x49, 0x49, 0x49, 0x41}, 'F':  {0x7F, 0x09, 0x09, 0x09, 0x01},
    'G':  {0x3E, 0x41, 0x49, 0x49, 0x7A}, 'H':  {0x7F, 0x08, 0x08, 0x08, 0x7F},
    'I':  {0x00, 0x41, 0x7F, 0x41, 0x00}, 'J':  {0x20, 0x40, 0x41, 0x3F, 0x01},
    'K':  {0x7F, 0x08, 0x14, 0x22, 0x41}, 'L':  {0x7F, 0x40, 0x40, 0x40, 0x40},
    'M':  {0x7F, 0x02, 0x0C, 0x02, 0x7F}, 'N':  {0x7F, 0x04, 0x08, 0x10, 0x7F},
    'O':  {0x3E, 0x41, 0x41, 0x41, 0x3E}, 'P':  {0x7F, 0x09, 0x09, 0x09, 0x06},
    'Q':  {0x3E, 0x41, 0x51, 0x21, 0x5E}, 'R':  {0x7F, 0x09, 0x19, 0x29, 0x46},
    'S':  {0x46, 0x49, 0x49, 0x49, 0x31}, 'T':  {0x01, 0x01, 0x7F, 0x01, 0x01},
    'U':  {0x3F, 0x40, 0x40, 0x40, 0x3F}, 'V':  {0x1F, 0x20, 0x40, 0x20, 0x1F},
    'W':  {0x3F, 0x40, 0x38, 0x40, 0x3F}, 'X':  {0x63, 0x14, 0x08, 0x14, 0x63},
    'Y':  {0x07, 0x08, 0x70, 0x08, 0x07}, 'Z':  {0x61, 0x51, 0x49, 0x45, 0x43},
    '[':  {0x00, 0x7F, 0x41, 0x41, 0x00}, ']':  {0x00, 0x41, 0x41, 0x7F, 0x00},
    '^':  {0x04, 0x02, 0x01, 0x02, 0x04}, '_':  {0x40, 0x40, 0x40, 0x40, 0x40},
    'a':  {0x20, 0x54, 0x54, 0x54, 0x78}, 'b':  {0x7F, 0x48, 0x44, 0x44, 0x38},
    'c':  {0x38, 0x44, 0x44, 0x44, 0x20}, 'd':  {0x38, 0x44, 0x44, 0x48, 0x7F},
    'e':  {0x38, 0x54, 0x54, 0x54, 0x18}, 'f':  {0x08, 0x7E, 0x09, 0x01, 0x02},
    'g':  {0x0C, 0x52, 0x52, 0x52, 0x3E}, 'h':  {0x7F, 0x08, 0x04, 0x04, 0x78},
    'i':  {0x00, 0x44, 0x7D, 0x40, 0x00}, 'j':  {0x20, 0x40, 0x44, 0x3D, 0x00},
    'k':  {0x7F, 0x10, 0x28, 0x44, 0x00}, 'l':  {0x00, 0x41, 0x7F, 0x40, 0x00},
    'm':  {0x7C, 0x04, 0x18, 0x04, 0x78}, 'n':  {0x7C, 0x08, 0x04, 0x04, 0x78},
    'o':  {0x38, 0x44, 0x44, 0x44, 0x38}, 'p':  {0x7C, 0x14, 0x14, 0x14, 0x08},
    'q':  {0x08, 0x14, 0x14, 0x18, 0x7C}, 'r':  {0x7C, 0x08, 0x04, 0x04, 0x08},
    's':  {0x48, 0x54, 0x54, 0x54, 0x20}, 't':  {0x04, 0x3F, 0x44, 0x40, 0x20},
    'u':  {0x3C, 0x40, 0x40, 0x20, 0x7C}, 'v':  {0x1C, 0x20, 0x40, 0x20, 0x1C},
    'w':  {0x3C, 0x40, 0x30, 0x40, 0x3C}, 'x':  {0x44, 0x28, 0x10, 0x28, 0x44},
    'y':  {0x0C, 0x50, 0x50, 0x50, 0x3C}, 'z':  {0x44, 0x64, 0x54, 0x4C, 0x44},
}
```

### 10.1 Status read-outs

Drawn at scale 1. Coordinates are the glyph top-left; `W=800`, `H=480`.

| Position `(x,y)` | Text | Colour |
|---|---|---|
| `(4, 2)` | `C1 <v/div>` (e.g. `C1 500mV`) | `colC1` yellow |
| `(96, 2)` | `C2 <v/div>` | `colC2` pink |
| `(200, 2)` | `M <time/div>` (main timebase) | `colInfo` grey `(200,200,200)` |
| right-justified at `(796, 2)` | `T C<n> <^\|v> <±level>div <state>` — trigger source channel `n`, edge (`^` rising / `v` falling), level in divisions, sweep state | `colTrig` green `(64,255,64)` |
| `(4, 471)` | `C1 Vpp <volts>` + (when the measurement is valid) `  f <freq>` | `colC1` |
| `(410, 471)` | `C2 Vpp <volts>` + `  f <freq>` | `colC2` |

The bottom-row `y = H-9 = 471` keeps the 7-px glyphs inside the panel.

**Value formatting.** All numeric fields use Go `strconv.FormatFloat(x, 'g', 3, 64)`
(3 significant figures) with a unit suffix chosen by magnitude. Note the suffixes are
ASCII (`us`, not `µs`).

| Formatter | Rule |
|---|---|
| Voltage `fmtVolt(v)` | `x = abs(v)`; if `x ≥ 1` → `g,3(x)+"V"`, else `g,3(x·1e3)+"mV"` |
| Time/div `fmtTdiv(s)` | `s ≥ 1` → `+"s"`; `s ≥ 1e-3` → `g,3(s·1e3)+"ms"`; `s ≥ 1e-6` → `g,3(s·1e6)+"us"`; else `g,3(s·1e9)+"ns"` |
| Frequency `fmtFreq(f)` | `f ≥ 1e6` → `g,3(f/1e6)+"MHz"`; `f ≥ 1e3` → `g,3(f/1e3)+"kHz"`; else `g,3(f)+"Hz"` |

**Trigger read-out.** Assembled as `fmt.Sprintf("T C%d %s %+.2fdiv %s", trigSrc+1, edge,
trigLvl, state)` and drawn right-aligned ending at `x = W-4 = 796`, `y = 2`. `edge` is
`"^"` when `trigEdge ≥ 0` (rising) else `"v"`; `trigLvl` is the level in divisions with a
forced sign and 2 decimals (`%+.2f`). `state` precedence (evaluated in this order): start
from the sweep-mode name `"AUTO"` (or `"NORM"` when NORM mode), then **if not running**
override with `"STOP"`, **else if triggered** `"T'D"`, **else if NORM mode** `"WAIT"`.
(STOP overrides the triggered/WAIT states; a running triggered display shows `T'D`.)

**Bottom-row Vpp / frequency.** Per channel over the record `sig` (length = record cols),
compute `cmin`/`cmax` (min/max ADC code) and `vpp = (cmax − cmin) / 127.0` volts — the
render scale is a fixed **1/127 V per code**. Frequency is measured from the mid-level
`lvl = (cmin + cmax) / 2`: scan `c = 1..cols-1` for rising crossings
(`sig[c-1] < lvl && sig[c] ≥ lvl`), recording the first crossing index, the last, and the
count `n`. The measurement is **valid** only when `n ≥ 2 && last > first && sampleS > 0`,
where `sampleS` is the per-sample interval in seconds; then
`period = (last − first) / (n − 1) · sampleS` and `freq = 1/period`. The `  f <freq>`
suffix (two spaces, `f`, space, `fmtFreq(freq)`) is appended to the Vpp read-out **only
when the measurement is valid**; otherwise the Vpp read-out stands alone.

### 10.2 Cursors

Two vertical time cursors, off by default (the CURS tab, or the Adjust push in MEASURE). When on, each
cursor is a **dashed vertical line** in orange `(255,160,32)`, one pixel every 3rd row
from `y = 12` to `y = H-12 = 468`, at column `curX[i]`. Default positions are
`curX = {W/3, 2W/3} = {266, 533}`. Each cursor is labelled `1`/`2` at `(curX[i]-2, H-20 =
460)`. The delta read-out `dT <time>  1/dT <freq>` is drawn at `(330, 2)` in the same
orange, where `dT = |curX[0]-curX[1]| / W × (10 × time/div)` and the `1/dT` term is
appended only when `dT > 0`.

### 10.3 Task workspaces, settings sheets and the F-key column

The panel's UI (spec 08 §7, ADR-TASK-WORKSPACES) is drawn by the overlay from a **view snapshot**
(`panel/wsview.View`) the panel controller builds: plain data (texts, semantic colours, layout
numbers), no logic. The renderer maps the semantic colours onto the generated palette (§8) and draws
with flat 1-px lines and filled rectangles and the 5×7 font only; no colour is hard-coded (the
channel colours are always `colC1`/`colC2`).

**Layout.** With no menu open a **pictogram rail** 30 px wide occupies `x = 770..799` and the plot
uses the freed width (`w = 770`); a workspace with a pane puts it under the top status row and the
plot below it:

| Workspace | Pane rows | Plot rows | Plot |
|---|---|---|---|
| SCOPE | none | full height (the trace mapping of §6.1) | the trace, X-Y |
| BUS | `12..135` | `136..479` | the trace with the decode strip, or the eye diagram |
| SPECTRUM | `12..149` | `150..479` | FFT, spectrogram or Bode |
| MEASURE | `12..141` | `142..479` | the trace with cursors |
| COMPARE | `12..103` | `104..479` | the trace with references, mask and math |

Under a pane the eight vertical divisions span the plot exactly (graticule and trace mapping move
together); the graticule is drawn across `w`, not 800. The bottom Vpp/f readouts and the measurement
boxes give way where a pane shows the same figures.

**Rail.** One 28×34 tab per F-key, vertically centred on the physical F1..F5 (the `softkeyY` row
centres 80, 160, 240, 320, 410): a 22×14 pictogram (a wave; packets on a strip; spectral lines; two
cursors and the distance between them; a wave in a dotted mask band) over a 4-letter tag (`SCOP BUS
SPEC MEAS COMP`). The active tab is filled amber (`colTrig`) with dark content, the others outlined
in `colGrid`. After a workspace key a one-line label (`F3 SPECTRUM   again: FFT / Spectrogram /
Bode`) sits left of the rail beside that key for 1.6 s.

**Pane.** A 12-px header band in `colMenuBg` at `y = 12` with the workspace's name in amber at
`(4, 14)`, a caption, and a dim hint right-aligned; the pane body; a **tool line** at `plotTop − 12`
naming the knobs' jobs (`Adjust: packet  push: pick bytes  Position: next match ...`) with the
active key's next view in amber at its right end (`F2 again: Eye diagram`); an axis rule at
`plotTop − 1`. SCOPE has no pane: its name sits in the top bar and the Adjust tag
(`Adjust: Level 200mV`, amber while just turned) in the bottom row.

- **BUS packets:** a fixed-column hex dump — the list number, a box of 16 hex cells (18-px pitch, a
  6-px gap after the 8th), a separate left-aligned string box with the same bytes as text (`.` for
  non-printables), the byte count and the match bytes in amber (cut packets say so, dim). Eight lines,
  at most three per packet, scrolled to keep the selected packet (a `>` and a `colGrid` band) in view;
  framing errors are `ER` cells in `colStale`. Under the trace the decode strip boxes the selected
  packet in amber with its number and ticks the packets holding the match.
- **Byte picker:** the pane framed in amber; the bytes at scale 2 in a window around the first byte
  (filled amber), the next three underlined, a bar before each message start, `ERR` in `colStale`.
- **Eye diagram:** the plot shows two UI around the bit centre (192 grid columns per UI, 2 px each,
  768 px; labels `-1 UI -0.5 0 +0.5 +1 UI`), the 8 divisions in 2-px rows; the fold's density in four
  shades mixed from the data line's channel colour over the screen colour; the eye opening as a dashed
  amber box; the sample-point (`S`) and threshold (`T`) cursors dotted in `colCursor`. The pane has
  a small live time trace and the figures (bit rate to five figures, UI with its samples per UI,
  crossing, UI folded, eye height and width, rise, fall, TIE rms and pk-pk, RJ / DJ, period and
  cycle-to-cycle jitter) with `<`/`>` on bounds when the edges fall between samples, and a warning
  when sample-limited. The eye folds sampled records (it leases the decode view's samples, not the
  envelope's), every sample of a fast bus; it starts over when its settings change, or when three
  captures in a row are off its clock period. The web's eye draws the same picture with the same figures in the same order (one engine:
  the JS eye and its Go port agree step for step).
- **SPECTRUM:** a small time trace, the peak table (frequency, level dBV, level against the top
  peak, harmonic) in frequency order, a readout (top peak, THD), the marker line; the plot shows the
  marker as a dotted amber line with a triangle on its level and a label, and a dim delta reference.
- **MEASURE:** the statistics table (now, mean, min, max, sdev, N), or all values of both channels in
  two columns of nine (the 18 parameters, preshoot included, in the web's names and order), and the
  cursor block. The statistics count only while MEASURE is shown, as on the web. The readout boxes drawn
  over the trace in the other workspaces use the same names and figures. On a live min/max frame,
  rise, fall and over/preshoot come from the frame's full-rate edge windows (`Frame.Edge`, `EdgeOpp`:
  1024 record words around the trigger edge and around the next edge the other way), and so do the
  timing and the mean of a signal the envelope does not resolve (`WindowTiming`, `WindowMean`); the
  other figures come from the envelope; an inverted channel is inverted after both. The windows are
  read twice a second and carried to the frames between (`Frame.EdgeRead` numbers the reads): the
  statistics count a window figure on the frame its read is new to, each other figure once a frame.
- **COMPARE:** five rows (MASTER, REF A, REF B, MASK, MATH); the selected row's action is written at
  its right end; a stacking master shows a progress bar.

**Settings sheet.** Full screen (the trace is not drawn). A tab strip at `y = 12` (17 px, `colMenuBg`):
the workspace's name, its own tabs, a double bar, the other tabs dim; the open tab filled amber;
`< S/DIV >` right-aligned. Rows from `y = 34`, 17 px each (section headers 13 px), to `x = 470`: the
label at `x = 12`; an enum row writes every option inline from `x = 110`, the current one filled
amber (grey when it is Off or the row is not in use), a list too long for the row windowed around the
current option with `<`/`>`; a number row shows its value and a range gauge; an action row its
labelled buttons; a slot row its summary; the highlighted row has a `>` and a `colGrid` band (an
amber frame and `EDIT` while editing). Rows not in use are dim. Under the rows the tab's live status
(and the SYS tab's guide). Right of the rows (`x = 478`, 202 px) a **live mini view** (`y = 34`,
158 px: the trace with the cursors, or the X-Y, FFT, Bode or spectrogram being shown, tagged `LIVE`,
`WAIT` or `STOP`) above a panel explaining the highlighted row (its value large when it fits, help,
range, the knob that also changes it, and the keys). A hint line at `y = 469` names the keys that
move and close the sheet. The byte picker of the Match bytes row is a box under (or above) that row;
the match editor is a full-screen keyboard left of the F-key column.

**F-key column.** While a sheet, the byte picker or the match editor is open, a 116-px column at
`x = 684` (`colMenuBg`, an amber rule on its left) from `y = 30` (12 for the picker) carries the
menu's title and, on each F-key's row (`softkeyY − 7`), the choice's label over its value; the current
choice, or the key just pressed (for 350 ms), is filled amber.

**Notices.** A short notice (`AUTO: UART 115200 8N1 on C1, decode on`, `Trigger to centre`) is a
boxed line centred on the plot for its duration; a remote change's `REMOTE  TRIG level 1.25V` line
for 1.5 s; the autoset banner as before. All durations run on the panel controller's clock.

**Cost.** The heavy analysis (decode, the eye fold, the spectrum, the measurements, the repeat test)
runs once per published frame in the panel's `OnFrame`, called by the LCD loop before it builds the
view; the renderer only draws. The repaint signature includes a fingerprint of the view, so a held
display is redrawn exactly when something on it changed.

### 10.4 C2 (second-channel) envelope

**Resolved:** the faithful slow/roll picture is a **per-channel** min/max envelope — C1 in
`colC1` (yellow) and, when two channels are enabled, C2 in `colC2` (pink), each built from
its own samples and drawn with the identical per-column min..max fill of §6.3. The min/max
reduction is stored **planar, channel-major**: `channels × EnvCols` entries, channel `ch`
occupying `EnvMin[ch·EnvCols : (ch+1)·EnvCols]` (same for `EnvMax`). The draw loop iterates
`ch = 0..channels-1`, selects the channel colour, and fills every display column from
`sampleToY(EnvMax[ch·EnvCols+c])` to `sampleToY(EnvMin[ch·EnvCols+c])`.

The renderer runs the §6.3 fill once per enabled channel, indexing the planar buffer at
`EnvMin[ch·EnvCols+c]` / `EnvMax[ch·EnvCols+c]`. The producer reduces C1 into the first
planar half and, when two channels are enabled, C2's drained window (the roll producer
already captures C2 into its own ring) into the second half. No new capture or bus access
is introduced — C2 envelope samples are already drained alongside C1.

### 10.5 Screen saver

After the SYS tab's Screen saver time without input (spec 08 §7.5) the loop stops drawing the
workspace and draws only the saver: the screen black (`0x0000`), a 240×144 mini view of the live frame
(the sheet thumbnail's graticule, traces and trigger pointer), and under it the run state at scale 2
(`RUN` in `colOK`, `STOP` in `colStale`, `SINGLE` in `colTrig`), the time/div in `colInfo`, and at
scale 1 a running mask test's `MASK pass N  FAIL M` (`colWarn` once one fails) or `Any key wakes the
screen` in `colDim`. The 240×180 box moves one pixel right or left and one up or down every 200 ms,
bouncing off the edges (`SaverPos`), so no pixel holds one image and the path repeats only after
hours; the loop repaints at that step, not every tick. After the Screen off time it presents one black
frame and drives gpio7 low (spec 08 §10.1); on the next input it repaints the workspace in full and
then drives gpio7 high. While the screen sleeps the loop still runs the panes' analysis, the
spectrogram's rows and the lamps; only the render stops (on the bench: 41 to 44 live frames per
second while asleep against 23 to 24 with the workspace drawn). `/api/screen.png` and the hardcopy
always render the workspace.

### 10.6 Open

- **Per-region pixel rectangles** (exact bounds of the waveform area vs. the status
  strips) are not pinned as hard constants; the whole `800×480` renders correctly as one
  surface and the overlay uses the fixed coordinates above. Confirm exact rectangles
  against a target-hardware screen capture if a future revision needs them.
- **Backlight brightness/dimming** has no firmware-controlled path; the panel runs at a
  fixed/full backlight, and the GPIO enable line is a binary on/off with no brightness
  register. The kernel's `tlc59108-bl` backlight device does not dim this panel (spec 08
  §10.1). The framebuffer bring-up (driver load order, GPIO7 enable, `mmap`) is fully
  specified in §1.4.

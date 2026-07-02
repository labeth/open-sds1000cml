# 07 — Display and Rendering

The renderer turns one frozen acquisition frame into an 800×480 RGB565 image on the
panel. It is a pure consumer: it reads a frame **copy** the acquisition owner already
drained and handed off, and it **never touches the GPMC bus, the FPGA registers, or any
capture state**. Everything in this document runs on the render/consumer worker; the
acquisition owner is the only writer of the frame data described here.

The `Frame` and `Arena` types are **owned by the acquisition-engine spec**
([03-acquisition-engine.md](03-acquisition-engine.md)). This document defines only what
the renderer reads from a frame and how it paints; the buffer-sharing / read-only
boundary is specified there.

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

---

## 2. Render loop

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
| `EnvMin`, `EnvMax` | `[]byte` | Per-display-column min/max ADC codes; valid only when `IsEnv` and `len(EnvMax) ≥ EnvCols`. |

**Load-bearing trap — stale envelope flag.** The arena frame buffers are reused
round-robin. `IsEnv`/`EnvCols`/`EnvMin`/`EnvMax` are set **only** on slow/roll envelope
frames. The real-time (native-fast + decimated) and probe producers **must explicitly
clear `IsEnv=false` and `EnvCols=0`** on the buffer they fill, every frame. If they do
not, a leftover envelope from a previously-visited slow band rides along in the reused
buffer and the renderer takes the envelope FILL branch (§6) on fast-band frames — the
symptom is a **solid rail-to-rail yellow block over the left of the screen** instead of a
clean line trace. This is a producer-side requirement, but the renderer's branch order
(§4) makes clearing mandatory: `IsEnv` is checked first and short-circuits the trace path.

---

## 4. Draw sequence

`Draw(surface, f, twoChan, live)`:

1. **Fill** the whole surface with the background colour `colBG`.
2. **Draw the graticule** (§5): 8×10 grid plus centre cross-hairs.
3. If `f == nil` or `len(f.C1) == 0`, return (blank graticule only).
4. Compute `valid = f.Valid`, clamped to `1..len(f.C1)`.
5. **If `f.IsEnv` AND `f.EnvCols > 0` AND `len(f.EnvMax) ≥ f.EnvCols`:** draw the
   envelope (§6.3) for C1, draw the liveness strip (§7), and **return**. (The envelope
   branch is C1-only; C2 envelope is not drawn.)
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

`MidLevel(sig) = (min+max)/2` over the slice. `CenterCross` returns the sub-sample
position of the rising crossing of the mid-level nearest the slice centre (or −1 if none).

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
vertical divisions, with a brighter centre cross.

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

**Which bands set `Interp`.** The producer sets `Interp` for the **native-fast** band
classes — this is the `nativeFast(class, lo, hi)` predicate:

| Sample-clock class | Divisor `lo | hi<<16` | `Interp` |
|---|---|---|
| `0x20` (500 MSa/s, ≤200 ns/div) | any | **true** |
| `0x01` (250 MSa/s, 500 ns–1 µs/div) | any | **true** |
| `0x80` (100 MSa/s base) | `≤ 4` (≈2–20 µs/div) | **true** |
| `0x80` | `≥ 8` (≥50 µs/div, decimated) | false |

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

### 6.3 Envelope (`drawEnvelope`) — slow/roll bands only

For a signal far faster than the timebase, one display column spans many signal periods,
so the correct picture is a solid vertical **min..max band** per column (the peak-detect
look), not a single line. This branch runs **only** when the frame's `IsEnv` flag is set
(slow ≥ 5 ms/div and roll ≥ 100 ms/div bands). `EnvMin`/`EnvMax` hold `EnvCols = 800`
per-column `(min,max)` pairs, reduced by the producer from the real drained samples.

For each panel column `x = 0..799`:
1. `c = x × cols / 800` (clamped to `cols-1`), where `cols = len(mn)`.
2. `yTop = sampleToY(mx[c])`, `yBot = sampleToY(mn[c])` (max code is higher on screen).
3. Swap if `yTop > yBot`.
4. Fill every row `y = yTop..yBot` with `colC1`.

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
| `colC2` | `(0, 220, 255)` | channel 2 trace (cyan) |
| `colOK` | `(0, 200, 0)` | liveness strip: fresh coherent frame |
| `colStale` | `(220, 40, 40)` | liveness strip: held / wedged / dead |

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

## 10. Open

- **Cursors, measurement read-outs, and on-screen status text** (V/div, time/div,
  trigger level/source, channel labels) are not implemented in the current renderer — it
  draws graticule + trace/envelope + the 2-px liveness strip only. Their layout, fonts,
  and colours are left for a future revision.
- **C2 envelope:** the envelope branch draws C1 only; whether two-channel slow/roll bands
  should show a second (cyan) envelope is not established.

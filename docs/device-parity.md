# Web ↔ on-device (standalone) feature parity

The scope is a self-contained instrument: everything should be reachable from the
**front panel + LCD**, not only the web UI. This tracks that parity.

**How it's validated (remotely):** inject panel events with `POST /api/panel`
(`{"button":"…"}` / `{"knob":"…","dir":±1,"steps":n}`) and capture the exact LCD
with `GET /api/screen.png`. Every item below marked ✓ was driven and screenshotted
this way on the real unit at `192.168.1.209`.

## Matrix

| Capability | Web | Device (LCD + panel) | Notes |
|---|---|---|---|
| Y-T display | ✓ | ✓ | — |
| **X-Y (Lissajous)** | ✓ | ✓ **added** | DISPLAY ▸ View ▸ X-Y |
| **FFT spectrum** | ✓ | ✓ **added** | DISPLAY ▸ View ▸ FFT; Go radix-2 FFT, dB, peak/Nyquist labels |
| **Math C1±C2, C1×C2** | ✓ | ✓ **added** | DISPLAY ▸ Math |
| **Autoset** | ✓ | ✓ **added** | AUTO button (was a stub); cancelable sweep |
| **References REF A/B** | ✓ | ✓ **added** | ACQUIRE ▸ (again) ▸ REF A/B: Save/Show/Clear |
| Cursors (time / volts, Δ) | ✓ | ✓ | **CURSORS key** or HORIZ ▸ (again) |
| **Main menu** (navigate all sub-menus) | ✓ | ✓ **added** | **MENU key** → Trigger/Acquire/Display/Horiz/Cursors |
| Measurements panel (full set, **both channels**) | ✓ | ✓ **added** | **MEASURE key**; Vpp/Vmax/Vmin/Vamp/Vtop/Vbase/Vrms/Vavg + Freq/Per/Duty/Rise/Fall/±Wid/OS |
| **Trigger qualifier params** (pulse/slope/video) | ✓ | ✓ **added** | TRIGGER ▸ (again) → params for the current type |
| **Memory depth** (2k/6k/14k/20k) | ✓ | ✓ **added** | ACQUIRE ▸ Mem |
| Trigger: edge/pulse/slope/video | ✓ | ✓ | TRIGGER menu |
| Trigger holdoff | ✓ | ✓ | TRIGGER ▸ Holdoff |
| Acquire: Normal/Average/ERes/Peak | ✓ | ✓ | ACQUIRE menu |
| ETS | ✓ | ✓ | ACQUIRE ▸ ETS |
| V/div, offset, coupling, probe | ✓ | ✓ | knobs + CHANNEL menu |
| Time/div, trig position | ✓ | ✓ | knobs + HORIZ menu |
| Run/Stop, Single | ✓ | ✓ | front-panel keys |
| Horizontal zoom / window navigation | ✓ | ✓ **added** | HORIZ ▸ Zoom (1–50×); horizpos knob pans the window across the record |
| FFT frequency zoom | ✓ | ✓ **added** | same Zoom control magnifies a spectrum band; horizpos pans it |
| FFT peak markers | ✓ | ✓ **added** | significant peaks (> −40 dBc) ticked in each spectrum |
| Persistence (afterglow) | ✓ | ✓ **added** | CHANNEL ▸ Persist; decaying trace layer composited over the graticule |
| **Protocol decode (ten protocols)** | ✓ | ✓ **added** | MENU ▸ Decode: UART, I²C, SPI, Manchester, SENT, CAN/CAN-FD, MIL-STD-1553B, ARINC 429, USB LS, FlexRay (+ **Auto** detect for UART/I²C/SPI); channel roles, baud/SPI-mode, **Show** Hex/ASCII/Both; on-trace byte strip. Go decoder == web decoder byte-for-byte (243-vector parity test), HW-validated on FPGA signals |
| Trigger source / slope from knobs | ✓ | ✓ **added** | push CH1/CH2 V/DIV knob → trigger source; push TRIG LEVEL knob → flip slope |
| Super-res stack & crunch | ✓ | ✓ **added** | ACQUIRE ▸ (again) ▸ Super-res page: accumulate + on-LCD stack review (falloff compensation is being ported; web applies it already) |
| **Bode / FRA** | ✓ | ✓ **added** | DISPLAY ▸ View cycle: magnitude+phase vs log-f, single-bin DFT, HW-validated |
| **Spectrogram (FFT waterfall)** | ✓ | ✓ **added** | DISPLAY ▸ View cycle: scrolling FFT heatmap |
| **Mask (golden-template) testing** | ✓ | ✓ **added** | on-device mask build/test with pass/fail counters (panel mask page) |
| **Serial / protocol trigger** | ✓ | ✓ | engine-side publish gate — same engine serves both surfaces |
| Eye diagram + TIE jitter | ✓ | ✗ web-only | browser-side analysis (eyejitter.js); no LCD port planned |
| Decode review aids (transcript list / watch / stream) | ✓ | ✗ deferred | web-only convenience: scrollable transcript, save-matching-windows "watch", stitched deep-capture "stream". On-trace strip covers the core need |
| PNG/CSV waveform export to file | ✓ | ✗ deferred | needs a file destination decision (USB stick); SCPI `SCDP` already dumps the screen |
| PNG / CSV export | ✓ | n/a | no user file destination standalone; `SCDP` (SCPI) already returns a screen dump |

## UX + responsiveness (goal requirements)

- **Every LCD mode renders well under the 50 ms refresh tick** (measured: Y-T
  ~0.2 ms, X-Y ~0.18 ms, FFT ~0.77 ms on host; ≤~12 ms on the ARM part). So a
  panel action is reflected on the physical display in <50 ms — comfortably under
  the 100 ms (pref 50 ms) target.
- **The one slow operation (autoset, ~1 s) shows a cancelable wait**: a centred
  "AUTOSET…" banner with an "AUTO again to cancel" hint; other input is inert
  while it sweeps. This is the pattern any future long op (decode capture,
  super-res) must reuse.
- **Menu ergonomics:** related pages share a physical button by re-press
  (DISPLAY→CHANNEL, HORIZ→CURSOR, ACQUIRE→REF), so the whole feature set fits the
  five softkeys + six menu buttons without new hardware.

## Deferred, with rationale

Only three items remain web-only; the rest of the parity work above is done
(persistence, protocol decode and super-res, once deferred, are now on the device):

- **Eye diagram + TIE jitter** — browser-side analysis (eyejitter.js); the LCD
  has no port planned (dense scatter rendering suits the web canvas better).
- **Decode review aids** — the scrollable transcript, the "watch" match buffer,
  and the stitched deep-capture "stream". Web-only conveniences; the on-trace
  byte strip already covers reading the live decode standalone.
- **Waveform file export (CSV/PNG to USB)** — needs a user file-destination
  decision (which mount, naming). SCPI `SCDP` already returns a screen dump.

Note: `/api/screen.png` (the web "device screen" mirror) PNG-encodes on the ARM
CPU and takes ~0.65 s — fine as a mirror, but it is **not** the standalone path
(the physical LCD writes `/dev/fb0` directly every 50 ms).

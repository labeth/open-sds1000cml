# app — the clean-room scope application

The clean-room firmware for the SDS1102CML+, built from [`specs/`](../specs/)
against the app ↔ OTA contract ([`ota/README.md`](../ota/README.md)). It never touches
the OTA agent: it is launched by the agent, inherits the boot fds, and reports health.

## What it does

A working oscilloscope, end to end — every subsystem validated on the real unit:

- **Single-owner acquisition engine** (spec 03): one goroutine owns the inherited
  `/dev/Gpmc` fd and is the only code on the GPMC bus. Per frame: arm → bounded
  wait → capture-halt → mmap drain → immediate re-arm → software discrimination →
  publish through a triple-buffered arena. Wedge-recovery ladder with CONF_DONE probe.
- **Full 33-detent timebase** 1 ns – 50 s/div (spec 04): native-fast, decimated,
  slow-envelope (phase-scatter divisor + 24-frame min/max ring), roll (free-running
  FIFO, arm-once/never-halt, paced pops), and opt-in ETS phase interleave.
- **Triggering** (spec 05): EDGE with software centring + slope validation, plus
  PULSE (GLIT), SLOPE (SLEW) and VIDEO (TV) qualifiers — the qualifier IS the
  trigger; trigger level via the safe CS3 DAC recommit; slope/source software-selected;
  **trigger holdoff** (post-trigger re-arm floor, applied as frame pacing).
- **Acquisition modes** (spec 03 §7.4): AVERAGE (edge-aligned sliding ring),
  ERES (boxcar before discrimination), PEAK (envelope path by construction), and
  cross-frame uniformity telemetry.
- **Vertical front end** (spec 06): 12-detent V/div ladder over SPI (relay word +
  gain DAC, seed-don't-emit), vertical offset DAC via the owner's CS3 staging,
  probe attenuation (×1/×10/×100, tip-referred), and software AC/DC/GND coupling
  (the relay coupling path is unsafe/ineffective on this clone — see spec 06 §6).
- **Measurements** (`internal/measure`): one shared core — Vpp/max/min/mean/rms,
  histogram Vtop/Vbase/Vamplitude, over/preshoot, and interpolated timing
  (freq/period/duty, 10–90 % rise/fall, ± width) — used by both web and LCD.
- **Protocol decode** (`internal/decode`): ten decoders — UART, I²C, SPI,
  Manchester, SENT, CAN / CAN-FD, MIL-STD-1553B, ARINC 429, USB low-speed,
  FlexRay — with auto-detect (protocol / channel roles / baud) for UART/I²C/SPI.
  Each Go decoder has a JS twin served to the browser
  (`internal/web/decode*.js`), held byte-for-byte identical by a 243-vector
  Go↔JS parity test; both web and LCD render the on-trace byte strip.
- **Serial / zone / mask triggering** (`internal/engine`): a serial trigger that
  publishes only frames whose decoded stream matches (re-centred on the match),
  a zone trigger (qualification rectangles), and golden-mask pass/fail testing
  (envelope built from N frames + violation counting; mask flow also on-device).
- **Super-resolution** (`internal/superres`): equivalent-time stack & crunch
  (sub-sample alignment → lucky-frame selection → drizzle/interp onto a fine
  grid), with an ETS mode for non-triggerable clocks, a decode-driven gate, and
  analog-falloff compensation — on the web UI and standalone on the LCD.
- **Analysis**: eye diagram + TIE jitter with software clock recovery and RJ/DJ
  split (web, `internal/web/eyejitter*.js`); Bode / FRA
  (`internal/engine/bode.go`) and a spectrogram FFT-over-time waterfall — both
  on the web UI and the device LCD.
- **LCD** (spec 07): `/dev/fb0` RGB565 renderer — graticule, traces, envelope
  bands, 5×7 font HUD, y-pan double buffering, a softkey menu system, a calibrated
  on-screen MEASURE panel, and on-screen time/volts cursors with Δ readouts —
  plus standalone X-Y / FFT / math views, autoset, REF traces, persistence, zoom,
  the decode byte strip, mask testing, super-res, Bode and the spectrogram
  (parity matrix: [`docs/device-parity.md`](../docs/device-parity.md)).
- **Front panel** (spec 08): SIGIO-driven matrix scan through the owner's
  request/reply channel, hardware-quadrature knobs, LED latch. RUN/STOP, SINGLE,
  AUTO buttons and TIME/DIV, V/DIV, POSITION, TRIG LEVEL knobs are live.
- **Calibration** (spec 10): loads and descrambles the per-unit
  `firmdata0/calibration.dat` (checksum → 3 involutions → Block A) into the gain
  and offset-zero paths, with the compiled-default fallback chain.
- **Host interface** (spec 11): SCPI over VXI-11 (ONC-RPC DEVICE_CORE registered
  with the system portmapper) — `*IDN?`, timebase/vertical/trigger control,
  byte-exact `Cn:WF? DESC/DAT2` WAVEDESC transfers, `SCDP` BMP hardcopy.
- **Web UI on `:8080`**: full-window responsive display (canvas scales to the
  browser, HiDPI-aware, column count tracks the pixel width via `?cols=`), with the
  full auto-measurement set (expandable timing/pulse group), draggable time+voltage
  cursors with Δt/1/Δt/ΔV, Y-T / X-Y / per-channel FFT view modes, waveform
  persistence, math (C1±C2, C1×C2, FFT-carrier subtraction), reference waveforms
  (REF A/B, absolute-voltage overlay), autoset, protocol decode (the ten
  protocols above, with auto-detect), a deep-record navigator strip, a clipping
  indicator, per-channel probe/coupling, and PNG / calibrated-CSV export — plus
  every acquisition control. All graphics surfaces render through a WebGL
  pipeline fed by a binary long-poll frame transport (`/api/frame.bin`).
- **OTA contract**: health token gated on ≥3 coherent frames; clean SIGTERM exit.

Deliberately deferred (spec-documented cuts): BWL relay changes, EXT trigger,
USB-TMC, video field modes, cal Block B.

## Build, deploy, test

```sh
make app                                        # dist/app-arm (ARMv7 static)
make test                                       # engine vs scripted fake bus, no hardware
otactl -tcp <dev>:5900 -stage /usr/bin/siglent/usr/media/U-disk0/agent-slots/staging \
    update-app dist/app-arm
otactl -tcp <dev>:5900 takeover                 # one-time cutover from the factory app
# browse http://<dev>:8080/  — or talk SCPI via any VXI-11 client
```

Recovery: `otactl untakeover`; last resort is the mains power-cycle
(`otactl power -shelly <ip> cycle`).

## Layout

- `cmd/app` — wiring: fds, cal load, engine, fan-out, LCD, panel, web, SCPI, health.
- `internal/bus` — GPMC ioctl + `/dev/mem` drain fast path, never-write guard.
- `internal/engine` — bands, FSM, qualifiers, acq modes, arena, command staging,
  serial/zone/mask trigger gates, Bode measurement.
- `internal/analog` — SPI V/div front end + offset-code mapping.
- `internal/cal` — calibration blob loader (checksum, descramble, Block A).
- `internal/decode` — the ten protocol decoders (JS twins live in `internal/web`).
- `internal/measure` — the shared measurement core (web + LCD).
- `internal/superres` — device-side super-res stacking core.
- `internal/lcd` — framebuffer surface, renderer, font, BMP encoder, FFT /
  Bode / spectrogram / super-res views.
- `internal/panel` — SIGIO matrix/knob controller, LED state, menu system,
  autoset, mask/super-res/REF flows.
- `internal/frames` — single-consumer fan-out for web + LCD readers.
- `internal/scpi` + `internal/vxi11srv` — the host interface.
- `internal/web` — JSON + binary-frame API, the embedded control page, and all
  browser-side JS modules (`app_*.js`, `decode*.js`, `superres*.js`, …).
- `internal/buildinfo` — version stamping.

## Feature → code map

Where each feature lives, and the design doc that explains it (specs are in
[`../specs/`](../specs/), design docs in [`docs/`](docs/)):

| Feature | Go package(s) | Web module(s) (`internal/web/`) | Design doc / spec |
|---|---|---|---|
| Acquisition engine (bands, FSM, acq modes, ETS, roll) | `internal/engine`, `internal/bus` | — | specs [03](../specs/03-acquisition-engine.md) / [04](../specs/04-timebase-and-bands.md) |
| Triggering (edge + qualifiers, holdoff) | `internal/engine` (`qualify.go`) | — | spec [05](../specs/05-triggering.md) |
| Vertical front end + calibration | `internal/analog`, `internal/cal` | — | specs [06](../specs/06-vertical-and-analog.md) / [10](../specs/10-calibration.md) |
| Protocol decode (10 protocols) | `internal/decode` | `decode.js`, `decode_*.js`, `app_decode.js` | [`docs/streaming-decode.md`](docs/streaming-decode.md) |
| Serial / protocol trigger | `internal/engine` (`serialtrig.go`) | `app_serialtrig.js` | — |
| Zone trigger + mask testing | `internal/engine` (`zonemask.go`), `internal/panel` (`mask.go`) | `app_zonemask.js` | [`docs/zonemask-plan.md`](docs/zonemask-plan.md) |
| Super-resolution (stack & crunch, ETS, falloff comp) | `internal/superres`, `internal/panel` (`superres.go`), `internal/lcd` (`render_superres.go`) | `superres*.js`, `app_superres.js` | [`docs/superres-lab.md`](docs/superres-lab.md), [`docs/superres-device-plan.md`](docs/superres-device-plan.md), [`docs/ets-clock-plan.md`](docs/ets-clock-plan.md), [`docs/falloff-comp-plan.md`](docs/falloff-comp-plan.md) |
| Eye diagram + TIE jitter | — (browser-side compute) | `eyejitter.js`, `eyejitter_analysis.js`, `app_eye.js` | [`docs/eyejitter-plan.md`](docs/eyejitter-plan.md) |
| Bode / FRA | `internal/engine` (`bode.go`), `internal/lcd` (`render_bode.go`) | `bode.js` | [`docs/bode-plan.md`](docs/bode-plan.md) |
| Spectrogram | `internal/lcd` (`spectrogram.go`) | `spectrogram.js` | [`docs/spectrogram-plan.md`](docs/spectrogram-plan.md) |
| Measurements + FFT peaks | `internal/measure` | `app.js` (panel), `peaks.js`, `app_fft.js` | — |
| LCD render (traces, HUD, views) | `internal/lcd` | — | spec [07](../specs/07-display-and-rendering.md) |
| Panel / menu system | `internal/panel` | — | spec [08](../specs/08-front-panel.md), [`../docs/device-parity.md`](../docs/device-parity.md) |
| Web transport + rendering (binary frames, WebGL) | `internal/web` (`web_binframe.go`, `web_frame.go`) | `binframe.js`, `app_gl.js`, `app_draw.js` | [`docs/ui-architecture.md`](docs/ui-architecture.md) |
| SCPI / VXI-11 | `internal/scpi`, `internal/vxi11srv` | — | spec [11](../specs/11-host-interface.md) |

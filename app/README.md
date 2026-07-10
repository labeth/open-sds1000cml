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
- **LCD** (spec 07): `/dev/fb0` RGB565 renderer — graticule, traces, envelope
  bands, 5×7 font HUD, y-pan double buffering, a softkey menu system, a calibrated
  on-screen MEASURE panel, and on-screen time/volts cursors with Δ readouts.
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
  (REF A/B, absolute-voltage overlay), autoset, protocol decode across ten
  protocols (UART, I²C, SPI, CAN/CAN-FD, Manchester, SENT, MIL-STD-1553B,
  ARINC 429, USB LS/FS, FlexRay) with auto-detect that scores all ten against
  the live signal, a clipping indicator, per-channel probe/coupling, and PNG /
  calibrated-CSV export — plus every acquisition control.
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
- `internal/engine` — bands, FSM, qualifiers, acq modes, arena, command staging.
- `internal/analog` — SPI V/div front end + offset-code mapping.
- `internal/cal` — calibration blob loader (checksum, descramble, Block A).
- `internal/lcd` — framebuffer surface, renderer, font, BMP encoder.
- `internal/panel` — SIGIO matrix/knob controller + LED state.
- `internal/frames` — single-consumer fan-out for web + LCD readers.
- `internal/scpi` + `internal/vxi11srv` — the host interface.
- `internal/web` — JSON API + embedded control page.

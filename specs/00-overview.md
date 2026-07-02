# 00 — Overview

This is the on-ramp to the open-sds1000cml firmware specification. It describes the instrument, its
hardware, the input signals, what the firmware is responsible for, and the vocabulary the rest of the
specs use. Read it first, then `01-system-architecture.md` for the constraints every other document
assumes, and `02-register-map.md` for the register/value formats every FPGA write depends on.

## 1. The instrument

The target is a two-channel digital storage oscilloscope of the **SDS1102CML+** class (Siglent
SDS1000CML+ family): 100 MHz analog bandwidth, 2 analog input channels, up to 1 GSa/s nominal sample
rate, a 7-inch 800×480 colour LCD, front-panel knobs/buttons, a USB-TMC device port, and a LAN port
speaking VXI-11. The firmware in this specification **replaces the factory application entirely at
runtime** and drives the same hardware directly.

Fixed identity/interface facts:

| Property | Value |
|---|---|
| Model class | SDS1102CML+ (2 ch, 100 MHz) |
| Display | 800×480, RGB565 (R[15:11] G[10:5] B[4:0]), double-buffered (`da8xx_fb`, yvirtual 960 = 2×480) |
| Graticule | 8 vertical divisions × 10 horizontal divisions |
| ADC | 8-bit sample codes (0–255) |
| Deep record | 20480 samples per channel |
| Max sample rate | 1 GSa/s nominal; 500 MSa/s realised in the fast native bands (2 ns/sample) |
| USB-TMC | VID 0xF4EC / PID 0xEE3A |
| LAN | VXI-11 over TCP, portmap (111) → DEVICE_CORE; maxRecvSize 0x800000; LeCroy short-form SCPI |
| Waveform export | `WF? DAT2` = raw 8-bit codes; `WF? DESC` = 346-byte little-endian WAVEDESC (`V = code·VERTICAL_GAIN − VERTICAL_OFFSET`) |

## 2. Compute + acquisition hardware

The instrument is an ARM SoC (DaVinci-class, ARMv5/v7 userspace) paired with an **FPGA** that contains
the acquisition engine (sample clock/divisor, trigger comparator, capture FSM, deep sample memory, and
a roll FIFO). The FPGA is loaded with a fixed bitstream at boot and is **not reconfigured at runtime**.

The CPU reaches the FPGA over the SoC **GPMC** (General-Purpose Memory Controller) bus, exposed to
userspace as the `/dev/Gpmc` character device. The FPGA presents three chip-select regions ("planes"):

| Plane | Role | Access |
|---|---|---|
| **CS1** | Acquisition plane: arm FSM (`0x21`), status bits (`0x39`, `0x46` fill), timebase class/divisor (`0x19`/`0x1a`/`0x1b`), HW trigger position (`0x3a`/`0x3b`), deep-record drain (`0x30–0x34`), roll FIFO (`0x41`=C1 / `0x59`=C2), key-matrix read (`0x64–0x69`) | Memory-mapped at `0x20200000 + sel·2`; also reachable by ioctl. Physical base `0x01000000`. |
| **CS3** | Config/DAC/LED plane: config port (`0x07`), trigger level DAC (`0x14/0x34`, mirror `0x15/0x35`), vertical offset DAC (`0x10/0x30`), front-panel LED latch (`0x09/0x0a/0x0b`) | ioctl only (reg-index `(port&0xffff)>>1`); also mmap at `0x03000000`. |
| **CS2** | Allocated by the kernel bring-up; not used by the acquisition firmware. | — |

The register **value formats** — the `0x1a/0x1b` sample-clock divisor encoding, the volts→code transfer
for the trigger-level and offset DACs, the meaning of every bit in the status registers, and the LED
latch bitmap — are defined in `02-register-map.md`, which is mandatory reading alongside this overview:
no correct FPGA byte can be written from §2 alone.

The analog front end (per-channel V/div gain and coarse range relay) is on the SoC **SPI** bus, off
the GPMC bus entirely: `/dev/spidev1.0` (relay word, coarse range) and `/dev/spidev1.1` (fine gain
DAC). See `06-vertical-and-analog.md`.

Front-panel input arrives as a **SIGIO interrupt** on the inherited `/dev/fpga_key` device; the actual
key-matrix and encoder state are then read from CS1 over GPMC. See `08-front-panel.md`.

## 3. Input channels and the calibration signal

- **Two analog channels**, C1 and C2, each with an independent V/div gain ladder (10 mV/div … 10 V/div),
  a vertical offset (position) DAC, and coupling selection. Vertical is 8 divisions tall; sample codes
  map to volts through the per-unit calibration table. The `calibration.dat` layout and the
  code↔volts derivation (`VERTICAL_GAIN`/`VERTICAL_OFFSET`) are defined in `10-calibration.md`; the
  V/div ladder and analog path are in `06-vertical-and-analog.md`.
- The front panel exposes a **probe-compensation / calibration output**: a ~**1 kHz square wave at
  ~3 V**. This is the reference signal the firmware and its validation assume on C1 (a ~3 V signal fills
  4–6 divisions at an unrailed V/div). A faster repetitive source may be present on C2 for exercising
  the equivalent-time band; the firmware makes no assumption that it is.

## 4. Firmware scope and responsibilities

The firmware is a **single process with concurrent workers**. It owns the whole instrument after boot
hand-off and is responsible for:

1. **Bus ownership + hand-off.** Inherit the already-open, boot-configured `/dev/Gpmc` fd from the
   launching agent, confirm sole ownership, and drive the pre-configured/pre-calibrated FPGA
   register-only. It never reconfigures the FPGA and never writes the config port at runtime. The
   inherited-fd discovery procedure (scanning open fds, matching the boot-configured Gpmc device) is in
   `01-system-architecture.md`.
2. **Acquisition.** Run the per-frame capture FSM (arm → done-gate → capture-halt → drain → re-arm),
   selecting the correct per-band engine (fast native, decimated deep, slow envelope, roll) for the
   current timebase. Equivalent-time (ETS) is an **opt-in** density mode, not an automatic band engine
   (see item 2 caveats in §6). See `03-acquisition-engine.md` for the exact arm→done-gate→halt→drain→
   re-arm sequence, poll intervals, timeouts, and the frame-arena `(generation, index)` publish/consume
   protocol, and `04-timebase-and-bands.md` for the full ns/div → (class `0x19`, divisor `0x1a/0x1b`,
   sample rate, record depth, drain path) band table and cutoffs.
3. **Triggering.** Program the hardware comparator trigger level (DAC), apply the safe recommit
   sequence at a bus-idle boundary, and perform software slope/type/source discrimination and
   edge-hold. A bare level-DAC write without the recommit sequence wedges the engine (black LCD), so
   this is safety-critical. See `05-triggering.md` for the level-DAC volts→code format, the exact
   recommit register sequence and its bus-idle/Arm placement, and the per-type (edge/slope/pulse/video)
   discrimination-hold algorithms.
4. **Vertical / analog front end.** Drive the V/div gain ladder, offset DAC, and coupling; map codes
   to volts via calibration. See `06-vertical-and-analog.md` (SPI transfer parameters, relay-word bit
   map, per-detent gain/coarse codes) and `10-calibration.md` (cal-table layout, volts↔code).
5. **Display.** Render frozen frame copies to `/dev/fb0` — trace (with interpolation and window
   sizing), envelope fill, graticule, HUD — without ever touching GPMC from the renderer. See
   `07-display-and-rendering.md` for interpolation/window sizing, the envelope (min,max) fill rule, the
   graticule/HUD layout, and the software anchor/centering rule.
6. **Front panel + control plane.** Decode knobs/buttons, drive panel LEDs, and stage all control
   changes (timebase, vertical, trigger, run/stop, acquisition mode) as commands applied at the frame
   boundary. See `08-front-panel.md` (matrix bit map, encoder direction/step decode, LED latch strobe,
   inherited `fpga_key` SIGIO wiring) and `09-control-plane.md` (staged-command struct, per-control
   coalescing, boundary apply sequence).
7. **Host interface.** Serve VXI-11 / USB-TMC SCPI and waveform/screenshot export. See
   `09-control-plane.md` §7 for the SCPI command/query interface and the VXI-11 (portmap → DEVICE_CORE)
   / USB-TMC transport framing; the 346-byte WAVEDESC byte layout used by `WF? DESC` is in
   `06-vertical-and-analog.md`.
8. **Health + recovery.** Report healthy only after genuine coherent frames, detect a wedged engine,
   and drive the OTA supervisor's rollback path. See `01-system-architecture.md` for the software
   liveness-watchdog interval, the coherent-frame health criterion, and the OTA rollback signalling
   contract.

Out of scope: the factory application, FPGA bitstream generation, and the U-Boot/kernel bring-up. The
firmware assumes a booted, bitstream-loaded, per-unit-calibrated fabric handed off by the boot chain.

## 5. Load-bearing constraints (stated in full in `01`)

These shape the entire design; they are requirements, not advice:

- **Single GPMC bus owner.** Exactly one worker (the *engine owner*) touches the GPMC bus. Every other
  consumer (panel matrix read, LED latch, offset DAC, control writes) submits a command that the owner
  applies at a frame boundary. A second consumer touching the bus during a capture-halt window
  black-screens the instrument.
- **fd inheritance, never fresh-open.** The `/dev/Gpmc` fd is opened once by the boot chain / agent and
  inherited. A fresh `open()` skips the one-time chip-select timing init, stalls the bus in
  uninterruptible `D` state, and can only be cleared by a physical power-cycle. The firmware discovers
  and reuses the inherited fd (procedure in `01`); a missing inherited fd is a hard fault (refuse to
  drive, report unhealthy).
- **Never write the config port at runtime.** Writing the FPGA config port (`0x2010000e` / CS3 `0x07`
  region) collapses the running engine. Runtime control uses only the acquisition and DAC/LED registers.
- **Watchdog.** The SoC hardware watchdog must be serviced (or the platform resets); it is owned by the
  agent and does not fire on a hung engine owner, so a **software liveness watchdog is mandatory**.
- **Reused frame buffers must have their metadata cleared.** Frame slots are reused round-robin; every
  producer must reset per-frame metadata (e.g. the envelope flag/column count) or a stale flag renders
  the wrong branch on a later band.
- **Pace roll-port reads.** The roll FIFO advances only on the ioctl read and must not be hammered; a
  runaway roll read blocks the single-owner loop and starves control/panel servicing.

## 6. Glossary

- **GPMC** — General-Purpose Memory Controller; the SoC parallel bus connecting the CPU to the FPGA,
  exposed as `/dev/Gpmc`.
- **CS1 / CS3 plane** — FPGA chip-select regions on the GPMC bus. CS1 = acquisition (FSM, status,
  divisor, sample drain, key matrix). CS3 = config/DAC/LED (level DAC, offset DAC, LED latch, config
  port). See §2 and `02-register-map.md`.
- **selector / register** — a CS-plane register index. CS1 registers are at memory address
  `0x20200000 + sel·2`; CS3 registers are addressed by ioctl reg-index `(port&0xffff)>>1`. Value
  formats (divisor, DAC codes, status bits, LED bitmap) are in `02-register-map.md`.
- **arm FSM** — the FPGA capture state machine driven by writes to CS1 `0x21`: `0xC0` = clear/disarm +
  reset head, `0xC3` = arm/fire, `0xC8` = latch + **halt** + reset read pointer, `0xCB` = latch without
  halt.
- **done-gate** — the status condition that signals a capture is complete before draining. **All bands
  poll CS1 `0x39`** (bit1 `0x02` = comparator/trig edge, bit2 `0x04` = capture DONE) plus the `0x46`
  fill counter reaching the band's latch target. The done-gate is NOT band-differentiated; `0x38` is
  not read on the acquisition path. Reading samples before the done-gate/fill condition asserts hangs
  the bus. **Native-fast bands additionally do not rely on the status gate to select a trigger:** at
  those bands `0x39` bit2 asserts on the untriggered free-run fill too, so a captured frame is accepted
  or held by **post-halt content discrimination** (edge peak-to-peak + slope on the drained samples),
  not by the status register.
- **capture-halt** — writing `0x21=0xC8` to latch the captured record and **halt** the engine so the
  frozen frame can be drained coherently, then immediately re-arming. The alternative `0xCB` latches
  without halting (engine keeps clocking) and yields a random-phase snapshot.
- **drain** — reading the captured samples out of the FPGA. The **deep** drain (CS1 `0x30–0x34`, mmap,
  auto-incrementing) reads the frozen `0xC8`-halt record and is used at **both** the native-fast bands
  (100 ns–20 µs) and the decimated bands (50 µs–2 ms). The **roll FIFO** (`0x41` = C1, `0x59` = C2,
  ioctl-only, advances one sample per read) is drained **only at the roll band (≥100 ms/div)**, where
  the engine free-runs and is never armed.
- **band class** — the timebase-dependent acquisition regime, set by CS1 `0x19` (class) + `0x1a/0x1b`
  (sample-clock divisor). Class `0x20` = 500 MSa/s (≤200 ns/div), class `0x01` = 250 MSa/s
  (500 ns–1 µs/div), class `0x80` = divisor-selected (native-fast at divisor ≤4 = 2–20 µs, decimated at
  divisor ≥8 = ≥50 µs). See `04-timebase-and-bands.md` for the full ns/div → (class, divisor, rate,
  depth, drain path) table.
- **fast native band** — 100 ns–20 µs/div: real-time capture via the deep capture-halt (`0x21=0xC8`),
  drained from the deep record memory `0x30–0x34` (the full 20480-sample record — the triggered edge
  lands deep in the record, so a shallow read grabs only flat pre-trigger rail), followed by software
  edge-catch / content-discriminate / hold / centre-zoom. Uses the same deep-drain port as the
  decimated bands; the roll FIFO is not used here.
- **decimated / deep band** — 50 µs–2 ms/div (class `0x80`, divisor ≥8): real-time capture drained from
  deep memory `0x30–0x34`.
- **envelope** — the slow-band (≥5 ms/div) rendering: per display column, a (min, max) pair filled
  vertically to show the signal's excursion (min/max peak-detect) as a solid band. The 5 ms–<100 ms
  envelope bands drain a modest deep record per frame; ≥100 ms rolls (see below).
- **roll** — ≥100 ms/div: the engine free-runs (never armed) and is read continuously/progressively
  from the roll FIFO (`0x41`/`0x59`, paced ioctl reads), with samples scrolling across the display (no
  per-frame re-arm).
- **ETS (equivalent-time sampling)** — an **opt-in** density-refinement mode that reconstructs a
  repetitive signal from many triggered acquisitions binned by sub-sample phase. It is **not**
  auto-selected by timebase: the ≤50 ns/div regime defaults, in AUTO and NORM/SINGLE alike, to the
  native-fast real-time catch-and-hold engine. ETS is enabled only on explicit request.
- **discrimination-hold** — the software policy of publishing only frames that contain a real triggered
  edge (validated by amplitude + slope/crossing) and *holding* (re-displaying) the last good frame when
  a capture contains only flat rail — the "show and hold" behaviour that keeps a fast-band edge stable.
- **anchor** — the horizontal reference position of the trigger edge within the display window. The
  firmware computes it in **software** (deterministic phase-lock / fixed-crossing rule) and centres the
  edge; the raw hardware position register is not a reliable index at fast bands.
- **frame arena** — the fixed set of round-robin frame buffers the engine owner drains into and the
  renderer consumes; the engine publishes a frozen `(generation, index)` atomically and the renderer
  never blocks the producer.
</content>
</invoke>

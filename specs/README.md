# Specifications

Authoritative, implementable specs for the open-sds1000cml firmware. Read in order; each is
self-contained but `01-system-architecture.md` establishes the constraints the rest assume.

**Rules for these documents:** concise, complete, implementable. State what *is* and what the
implementation *must do*. No history, no "how we found this", no "verified on unit N", no
changelog. Register names/addresses, value formats, ordering, and timing are given explicitly so
code can be written directly from the text. Where a value is a fixed constant, give the constant.

## Reading order

| # | Spec | Covers |
|---|------|--------|
| 00 | [`00-overview.md`](00-overview.md) | The device, the channels/signals, the scope of the firmware, glossary. |
| 01 | [`01-system-architecture.md`](01-system-architecture.md) | Process/boot model, single-owner GPMC bus discipline, fd inheritance, the GPMC chip-select planes, the load-bearing constraints and traps. |
| 02 | [`02-register-map.md`](02-register-map.md) | The GPMC register reference (CS1 acquisition plane, CS3 config/control plane): every register the firmware uses, address, meaning, value format. |
| 03 | [`03-acquisition-engine.md`](03-acquisition-engine.md) | The single-owner acquisition FSM: arm → done-gate → capture-halt → drain → re-arm; frame buffering/handoff; status bits. |
| 04 | [`04-timebase-and-bands.md`](04-timebase-and-bands.md) | The timebase step table, the band classes and their sample rates, per-band acquisition (fast real-time, decimated deep, slow envelope, roll, equivalent-time), the display record. |
| 05 | [`05-triggering.md`](05-triggering.md) | The hardware comparator trigger level (DAC + safe recommit sequence), software discrimination/hold, trigger types (edge/slope/pulse/video), source, coupling, holdoff. |
| 06 | [`06-vertical-and-analog.md`](06-vertical-and-analog.md) | Vertical/analog front end: V/div gain ladder (relay + gain DAC), vertical offset DAC, coupling, the calibrated code↔volts mapping. |
| 07 | [`07-display-and-rendering.md`](07-display-and-rendering.md) | Framebuffer format, trace rendering (interpolation, window sizing), envelope rendering, the frame-buffer metadata discipline. |
| 08 | [`08-front-panel.md`](08-front-panel.md) | Front-panel input (inherited key device, interrupt-driven matrix scan, knob/encoder decode), panel LEDs, and how panel bus access obeys the single-owner discipline. |
| 09 | [`09-control-plane.md`](09-control-plane.md) | How commands (timebase, vertical, trigger, run/stop) are staged and applied at the frame boundary; the network/status interface. |
| 10 | [`10-calibration.md`](10-calibration.md) | The calibration data layout and how it maps to the runtime gain/offset tables. |
| 11 | [`11-host-interface.md`](11-host-interface.md) | The external host/remote interface: VXI-11 (LAN) transport (USB-TMC specified, not implemented), the LeCroy short-form SCPI set, the byte-exact `WF?`/WAVEDESC waveform transfer, and the `SCDP` hardcopy image. |

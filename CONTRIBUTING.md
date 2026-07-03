# Contributing

Thanks for your interest in open-sds1000cml. This is clean-room firmware for a
real oscilloscope, so a few rules are load-bearing — please read these before
opening a PR.

## Clean-room provenance

The whole point of this project is that it is written **from behavioural
specifications** ([`specs/`](specs/)), not from the vendor firmware. Do **not**
paste, transcribe, or paraphrase disassembled vendor code, and do not add
behaviour "because that's what the vendor binary does" without a spec basis. If
you discover something new about the hardware, describe it in the relevant spec
and implement from that.

## Build & test

Two Go modules: [`app/`](app/) (the scope application) and [`ota/`](ota/) (the
supervisor + host controller). Each has a `Makefile`.

```sh
cd app
make test          # full offline suite — runs against a scripted fake bus, no hardware
make vet
make app           # cross-compiles the ARMv7 binary (dist/app-arm), version-stamped
```

- **Every change must keep `make test` green.** The engine, front end, LCD,
  panel, measurement, SCPI and web layers all have offline tests that run without
  a device.
- **Web changes** are additionally covered by a browser end-to-end harness
  (`app/internal/web/*_browser.mjs`, driven from `*_browser_test.go`). It needs
  Node + a Playwright Chromium install and **self-skips** when the browser is
  absent, so `make test` still passes locally without it; run it before touching
  `app.js`/`ui.html` if you can.
- Prefer adding a test with each change. The measurement core
  (`internal/measure`) and the web `/api/*` handlers are especially easy to cover.

## The architecture rules you must not break

These are why the instrument doesn't black-screen. They're documented in
[`specs/01-system-architecture.md`](specs/01-system-architecture.md) and the app
`internal/engine`/`internal/analog` doc comments:

1. **Single owner of the GPMC fd.** Exactly one goroutine touches the GPMC bus;
   everything else hands it work. Never touch `/dev/Gpmc` from the render, panel,
   or web layers.
2. **Inherit the fd; never fresh-open.** The GPMC and front-panel key fds are
   inherited from the boot process. A fresh `open()` faults.
3. **Absolute front-end writes only.** Rebuild the whole relay word and re-send
   both gain bytes on every change; never read-modify-write (it collapses the
   untouched channel). Coupling is software-only on this clone for the same reason
   (see [`specs/06-vertical-and-analog.md`](specs/06-vertical-and-analog.md) §6–§7).
4. **Don't reconfigure the FPGA config port at runtime.** The bring-up state is
   inherited; rewriting it collapses the engine.

## Style & commits

- Match the surrounding code — naming, comment density, and idioms. The codebase
  favours short, purposeful doc comments that explain *why*, not *what*.
- Keep the design-system guardrails green (generated palette parity, CSP, inline-
  style budget — all enforced by `go test`).
- Small, focused commits with a clear message body explaining the reasoning.

## Hardware safety

If you test on a real unit: you can always revert with `otactl untakeover`, and
the last-resort recovery is a mains power-cycle. Don't run this firmware on
anything other than an SDS1000CML+-series scope. There is no warranty.

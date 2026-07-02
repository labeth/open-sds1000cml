# open-sds1000cml

Clean-room replacement firmware **specification** for the Siglent SDS1000CML+ series
digital storage oscilloscope (2-channel, e.g. SDS1102CML+).

This repository is **specifications only** — the authoritative, implementable description of
how the replacement firmware must behave and how the hardware works. An implementation is built
*from* these specs; the specs do not describe any particular implementation, how a fact was
discovered, or where it was verified. **Everything written here is taken as correct.**

## What this firmware does

It replaces the vendor scope application with a clean-room application (**"the app"**) that drives
the instrument's real hardware — acquisition, timebase, trigger, vertical/analog front end, LCD,
and front panel — to oscilloscope-grade behaviour, matching the vendor where the vendor is the bar.

## The non-negotiable inputs (read `specs/01-system-architecture.md` first)

These are the constraints that, if ignored, cost the most time. They are load-bearing:

- **Single owner of the GPMC file descriptor.** Exactly one goroutine/thread owns the inherited
  `/dev/Gpmc` fd and is the *only* code that touches the GPMC bus. Every other subsystem (render,
  panel, control plane) hands work to that owner; none touch the bus directly. This is what makes
  the capture-halt safe. Violating it wedges the engine (black screen).
- **Inherit the fd; never fresh-open.** The GPMC (and front-panel key) fd must be *inherited* from
  the process that opened it at boot. A fresh `open()` of these devices faults. The app is launched
  so that it inherits the fd, and it reuses that fd for the life of the process.
- **The bring-up state is inherited, not reconfigured.** The comparator/engine configuration is
  established at boot; the app inherits it. Rewriting the FPGA config port at runtime collapses the
  engine — do not.

The full set of traps and the reasons the architecture is shaped this way are in the architecture spec.

## Layout

- [`specs/`](specs/) — the specifications. Start with `specs/README.md` for the reading order.

Each directory has a `README.md` describing what it contains.

## Status

Specifications under active authorship and adversarial review. Private while in progress.

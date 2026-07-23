# DESIGN — the app acquisition integration (drive the owned FPGA)

See also: [`../../codegen/docs/DESIGN.md`](../../codegen/docs/DESIGN.md) (the interface schema +
generated bindings the app imports) · [`../../fpga/standard/docs/DESIGN.md`](../../fpga/standard/docs/DESIGN.md)
(the standard bitstream this app drives).

Status: **design only** (no code). This document is the design of record for the **app-side**
acquisition rewrite: how the acquisition engine drives **only** the owned FPGA through the
generated `iface` bindings, what vendor code is deleted, what is re-homed, and how the offline
test suite stays green. The register map, the schema, and the generated bindings are fixed by the
codegen doc; the fabric behavior the FSM relies on is fixed by the fpga doc. It is opinionated;
every decision left to the maintainer is tagged **[DECIDE]**. Branch: `owned-fpga`.

---

## 1. Framing — a clean replacement, not a compatibility layer

We **own the acquisition FPGA**. The app's acquisition engine drives **only** the owned FPGA
through the generated bus. This is a **clean replacement, not a compatibility layer.** There is:

- **no vendor factory register map** (the divisor, the arm opcodes, the run word, the bimodal
  status, the jittery position, the five-port round-robin drain, the re-trigger strobe, the force
  pulse, the per-frame tail);
- **no dual mode and no fallback** to a vendor bitstream — the app refuses to drive anything that
  is not our fabric (build-ID handshake, see the codegen doc's `iface.Verify`);
- **no "repurpose the factory quirk" code.** The native-fast half-record re-capture loop, the
  maturation/force machinery, the content-discrimination-as-primary path, and the frame-tail
  re-trigger strobe existed **because the vendor HW trigger and timestamp were untrustworthy.**
  Our fabric provides a trustworthy HW trigger + an interpolating timestamp + result channels
  (fpga doc §4), so that machinery is **deleted, not ported.**

What is **kept** is the genuinely FPGA-agnostic higher-level processing, re-homed to the new frame
source: the triple-buffer arena + publish discipline, and the CPU-side edge/pulse/slope/video
qualifiers, serial-protocol trigger, zone/mask test, FRA/Bode, AVERAGE/ERES, and the protocol
decoders. These operate on drained frames today; the standard FPGA **subsumes them into fabric
over successive versions** (trigger discrim → v1 taps, measure → v1, serial decode → v2), at which
point each becomes a result-channel formatter + a CPU fallback for a fabric that lacks the tap.
§2–§4 below say exactly which app files are deleted, which are re-homed, and which are untouched.

Keep the single-owner discipline and the arena; **delete** the vendor acquisition protocol;
re-home the FPGA-agnostic processing onto the new frame source.

---

## 2. `app/internal/bus` — rewrite the driver around the generated interface

`bus.go` keeps the mmap/ioctl mechanics (inherited fd, `/dev/mem` `O_SYNC` map, the
`//go:noinline` single 16-bit `load16`/`store16`, the ioctl 6-byte encode) and the `Bus`
interface *shape*, but its register knowledge now comes from `iface` (the generated bindings — see
the codegen doc):

- **Delete** the hand-coded selectors/magic (`SelVersion` stays but as `iface.SelVersion`;
  `forbiddenWrite`'s cal-bank / re-trigger / per-frame-tail / result-range magic ranges are
  **gone**) and the 5-port round-robin `DrainInto`/`DrainRead`.
- **Write guard** becomes `iface.Writable(plane, sel)` (schema-derived: R-only ⇒ never written;
  `CONF_DONE` ⇒ never written). No hand-maintained magic ranges.
- **New drain surface** (single fixed auto-inc port, DMA-shaped):

```go
type Bus interface {
    Read(plane iface.Plane, sel uint16) (uint16, error)
    Write(plane iface.Plane, sel, val uint16) error
    // BurstInto drains n frozen record words from iface.SelBurst in one tight
    // pass (hi byte = C1, lo byte = C2). Post-halt only; auto-inc pops one/read.
    BurstInto(c1, c2 []uint8, n int)
    // ChannelInto reads n words from a result-channel auto-inc port (e.g. ENV_DATA).
    ChannelInto(sel uint16, dst []uint16, n int)
    MmapDrain() bool
}
```

`bus.New` calls `iface.Verify` and **refuses to drive on a build-ID mismatch** (replacing the bare
version-magic check). No `DrainWrite` — it existed only for the vendor roll-FIFO latch.

---

## 3. `app/internal/engine` — a much smaller acquisition FSM

**Delete** the vendor-quirk machinery:

- from `engine.go`: the whole factory `sel*`/`op*`/`stat*`/`run*` const block and the tuning knobs
  that modeled vendor quirks — `tuneFrameTail`, `tuneForceMode`/`ForceAfterUs`, `tuneMatureUs`,
  the per-frame-tail knobs, `tuneFillExtraUs`, `tuneHaltSettleUs`, `tuneBusyFillUs`,
  `tuneMaxRetry`, and the `AcqSample`/half-record telemetry that only meant something for the
  vendor's half-record state;
- `engine_capture.go`: the entire vendor capture path — `waitCapture`'s bimodal-done / native-fast
  maturation / reset-on-trigger / force-trigger logic, the half-record `realDepth` re-capture loop
  in `oneFrame`, `frameTail`, `haltSettle`, the round-robin `drain`;
- `engine_bus.go`: the vendor bring-up register sequence (`bringUp`'s divisor/run/status pokes)
  and `doReinit`'s "untried lever" pulses.

**New FSM** (in a rewritten `engine_capture.go`/`engine_bus.go`), preserving the owner loop
(`engine_loop.go` `Run` + `serviceCommands` boundary) and the arena. It is a small, single-owner
FSM driving the generated bus:

```
program(band):  Write RUN{mode}, DECIM_LO/HI, PRETRIG_LO/HI, POSTTRIG_LO/HI   (on change)
arm:            Write OPCODE = OP_GO
wait:           poll STATUS_A until DONE (NORM: real TRIG required; AUTO: DONE or budget)
halt:           Write OPCODE = OP_HALT ; read FILL twice (froze) — coherence telemetry
drain:          slow/envelope band → ChannelInto(ENV_DATA)     (results, O(columns))
                zoom / raw band     → BurstInto(c1,c2,n)        (raw, O(n), DMA-shaped)
timestamp:      read TRIGPOS_HI.IDX → trigger-sample index (telemetry only; software centres)
re-arm:         Write OPCODE = OP_GO   (fill resumes before the frame renders)
publish:        arena.Publish()  (unchanged discipline)
```

- **Software centring stays the primary path; HW-TRIGPOS is telemetry.** The CPU `centerCross` /
  `windowSlopeMatches` path centres every band and needs no bench trust in the HW interpolator.
  `TRIGPOS_HI.IDX` is read only as telemetry (`Frame.TrigPos`); `TRIGPOS_LO.FRAC` is not read.
  Promoting HW-TRIGPOS (fpga doc §4) to the centring source is a future, bench-gated change — until
  then the shipping code centres in software. That same CPU code additionally serves (a) EDGE
  refinement when the fabric lacks a discrimination tap and (b) the PULSE/SLOPE/VIDEO qualifiers,
  serial trigger, zone/mask, which remain CPU-side now and move to fabric taps in v1/v2.
- **Retire `quiet.Lock()` across the drain** (fabric static-freeze guarantee, fpga doc §7): the
  owner is still the sole bus toucher, but render/web/panel no longer pause for the drain because
  the frozen M9K is immune to memory-bus contention. Keep the quiet gate only where it still guards
  a real hazard, if any; the drain no longer needs it. **P1-gated** on the fpga doc's
  static-freeze byte-identity test — until then, keep the lock and flip it in the phase that
  validates the freeze.
- Bands map to `(DECIM, PRETRIG, POSTTRIG, display window)`; envelope/roll bands drive `ENV_COLS`
  and consume `ENV_DATA` instead of software min/max in `envroll.go` (re-homed to format the
  fabric's envelope records, with the software reducer kept as the CPU fallback).

---

## 4. What is re-homed vs subsumed vs untouched

**Re-homed onto the new frame source** (kept, retargeted): the triple-buffer arena + publish
discipline; the CPU decode/measure/trigger-discrimination that runs on drained frames — the
edge/pulse/slope/video qualifiers, serial-protocol trigger, zone/mask test, FRA/Bode, AVERAGE/ERES,
the protocol decoders, and the software min/max reducer in `envroll.go`.

**Subsumed into fabric over successive versions:** trigger discrimination → v1 taps, measure →
v1, serial decode → v2. As each fabric tap lands, the corresponding CPU path becomes a
result-channel formatter (consuming the generated channel record codec) plus a CPU fallback for a
fabric that lacks the tap.

**Untouched (reused as-is):** `analog/`, `cal/`, `frames/` (arena fan-out), `lcd/`, `web/`,
`panel/`, `scpi/`, `vxi11srv/`, `settings/`, `superres/`, `bode/` (CPU DFT), `buildinfo/`.
`cmd/app/main.go` changes only where it wires bus/engine (the `bus.New` handshake); no other
structural change — the engine, fan-out, LCD, panel, SCPI, web wiring is identical.

---

## 5. `testenv` / the scripted fake bus — model the STANDARD FPGA (keep `make test` green)

The offline suite drives the fake bus, so this is load-bearing. Two things, do not conflate:

- **`app/internal/testenv/testenv.go`** is the node/Playwright CI-skip gate — **not** a fake bus.
  It is **unaffected**; leave it as is.
- **`engine_test.go`'s `fakeBus`** is the scripted fake acquisition fabric. **Rewrite it to model
  the owned interface**, keyed on `iface` selectors so it can never drift from the schema:
  - `BUILDID_LO/HI` return the compiled `iface.BuildID` halves; `VERSION` returns `0x0052` — so
    `iface.Verify` passes.
  - `OPCODE` strobe drives an armed/halted/idle model (`OP_GO`→armed+filling, `OP_HALT`→halted+
    frozen, `OP_RESET`→idle); `RUN` sets mode; `PRETRIG/POSTTRIG/DECIM` are stored and read back.
  - `STATUS_A` asserts `VALID/TRIG/DONE` per the armed/mode model (scriptable per test, like
    today's `doneOnGo`/`trigOnGo`/`validOnGo`); `FILL` advances while filling and freezes on halt;
    `TRIGPOS` returns a scripted `{idx,frac}`.
  - `BURST` is a single auto-inc port that walks a deterministic wave from sample 0 after halt
    (replacing the round-robin `DrainRead`); an early read (pre-halt) still trips the `earlyDrain`
    trap so the test still catches a live-buffer read.
  - `ENV_DATA`/`ENV_COUNT` return a scripted set of packed envelope records + a count, with an
    overflow path, so the envelope band's fabric-consuming path is tested.
  - keep `snapWrites`/`clearWrites` and the write-order assertions, retargeted to the new
    arm/halt/program sequence.

There is a **second** `bus.Bus` double: `nullBus` in `internal/settings/apply_test.go` (a
do-nothing bus for constructing an engine in settings tests). It must track the new `bus.Bus`
method set (`BurstInto`/`ChannelInto`, `iface.Plane` args, no `DrainWrite`/`DrainRead`) or the
settings package stops compiling. **Grep for every implementer of `bus.Bus` before changing the
interface:** today they are `bus.Dev`, `engine_test.go`'s `fakeBus`, and `settings/apply_test.go`'s
`nullBus`.

`New(Config{Bus: fb,…})` and the arm/wait/halt/drain/publish assertions carry over with the new
sequence. `make test` stays green with no hardware.

---

## 6. Clean-room `specs/` — the owned FPGA **replaces** the vendor spec content

Per the maintainer: the owned-FPGA acquisition spec replaces the vendor-oriented content of
`specs/02` and `specs/03` (clean-room, describing OUR FPGA).

- **`specs/02-register-map.md`** — rewritten. Keep the still-true human material: the GPMC ioctl
  ABI (planes 1/3, 6-byte struct, `<<1` selector shift, read/write request codes), the `/dev/Gpmc`
  inherited-fd rule, the mmap `O_SYNC` single-16-bit-load fast path, and the single-owner
  requirement. **Replace the register table** with a pointer to the generated
  `fpga/standard/docs/REGISTER-MAP.md` and the schema as the single source of truth (the register
  map is generated, not hand-maintained). It documents OUR blocks (codegen doc §3.2), not the
  vendor selectors.
- **`specs/03-acquisition-engine.md`** — rewritten as the **owned-FPGA acquisition engine**
  behavioral spec (the human clean-room source the schema + RTL derive from): the canonical
  streaming spine + C1 stream contract; the C2 capture contract (circular writer, programmable
  pre/post, `trig_mark`, the exact-window invariant, fpga doc §5); the live-stream envelope reducer
  + the C3 channel/overflow contract (fpga doc §6); the single `BURST` DMA drain + `BURST_REMAIN`;
  the clean `STATUS_A` + interpolating `TRIGPOS`; the build-ID handshake; the static-freeze
  guarantee; and the app FSM (§3). It keeps the single-owner + arena discipline (still correct) and
  the health/recovery contract, re-expressed for the owned registers.
- **`specs/README.md`** — update the rows for 02/03 to describe the owned FPGA. The other specs (04
  timebase, 05 trigger, 06 analog, 07 display, 08 panel, 09 control, 10 cal, 11 host) stay; note
  where they reference the removed vendor registers and repoint them to the owned map (mostly 04's
  band table → `DECIM` values, and 05's level-DAC recommit → the CS3 `frontend` block).
  **[DECIDE]** whether to fold the timebase→`DECIM` table into 04 or keep it in 03.

Provenance stays clean-room: 03 is written from behavior (our own design intent), the schema is
written from 03, and everything generated flows from the schema.

---

## 7. Build order (this module's slice — full plan in the codegen doc)

This module ships **Phase C** and **Phase D** of the whole-effort plan (see the
[codegen doc](../../codegen/docs/DESIGN.md) §5 for the full A–E sequence). Phase A (the schema +
generated bindings) and the standard bitstream (Phase B, fpga doc) provide the interface this work
drives.

**Phase C — app integration:**

1. Rewrite `app/internal/bus` around `iface` (§2); delete the vendor forbidden-write ranges +
   round-robin drain.
2. Rewrite the engine FSM (§3); delete the vendor capture/bring-up/quirk code.
3. Re-home `envroll.go` (consume `ENV_DATA`), and the qualifiers/serial/zone/mask/bode/average as
   CPU-side-on-drained-frames with the fabric-subsumes note (§4).
   **Gate:** `make test` green (needs the Phase D fake bus), `go vet` clean, `make app`
   cross-builds ARMv7.

**Phase D — tests + CI (interleave with C; land together):**

1. Rewrite `engine_test.go`'s `fakeBus` for the owned interface (§5); update every `bus.Bus` test
   double and the write-order assertions.
2. Rewrite `specs/02` + `specs/03` (§6); update `specs/README.md`.
3. CI: module matrix `[app,ota,codegen,fpga]`; the drift step runs in the codegen lane;
   `.gitattributes` for `*.rbf`/`*.vh`.
   **Gate:** full `make test` green offline (all modules); `make drift` clean; cross-build lane
   green; browser lane still self-skips cleanly.

**Phase E (hardware, gated; not CI):** once the fabric's static-freeze byte-identity test passes
on the bench (fpga doc §7/§10), **retire the quiet-lock across the drain** (§3) — and only then.

---

## 8. App-side summary of the key improvements

- Delete the vendor path: the vendor register map, opcodes, drain protocol, and all the
  quirk-workaround code are **removed**, not ported.
- A small clean FSM: program → arm → wait-on-real-DONE → halt → DMA/channel drain → timestamp →
  re-arm → publish.
- Software centring is the **primary** path (it works for every band and needs no bench trust in
  the HW interpolator). `TRIGPOS_HI.IDX` is read as telemetry (`Frame.TrigPos`); `TRIGPOS_LO.FRAC`
  is reserved. Promoting HW-TRIGPOS to the centring source is a future, bench-gated change — until
  then the shipping code centres in software.
- **Retire the quiet-lock across the drain** — the single biggest app simplification, gated on the
  fabric's static-freeze byte-identity test. Until that bench gate passes, the drain still holds
  `quiet.Lock()`.
- The arena + CPU-side processing are re-homed to the new frame source; the fake bus + specs 02/03
  are replaced to describe OUR FPGA; `make test` stays green offline with no hardware.

## 9. FPGA bring-up & deploy — the app carries and loads its own bitstream

The app owns the acquisition fabric, so it also *loads* it. Cold boot leaves the factory NAND image
in the FPGA; at startup — after `bus.New`, before the engine drives the bus and before the analog
front end opens the shared `spidev1.1` — `internal/fpgaload.Bringup` reconfigures the volatile CRAM
to the owned build (method B) and verifies it by the interface build-ID:

1. Verify the fabric via `iface.Verify(bus.Read)`. A cold-boot (factory) fabric fails this, which is
   the expected trigger to reconfigure.
2. Reconfigure (method B): open + set the passive-serial loader mode on `spidev1.1`, pulse nCONFIG
   over the GPMC config port (a **raw** write to the read-only `CONF_DONE` selector — it cannot go
   through `bus.Write`, whose schema guard rejects it), stream the bitstream LSB-first in `bufsiz`
   chunks + init clocks, then poll `CONF_DONE`.
3. Re-verify the build-ID. On any failure the app refuses to drive (like a missing GPMC fd), staying
   alive for diagnosis rather than driving an unknown fabric.

Reconfiguring CRAM is non-destructive: a bad load only black-screens acquisition and a power-cycle
restores the factory image from NAND. Nothing in `fpgaload` writes any configuration flash (NAND or
EPCS) — there is no flash path in the package by construction.

**Two binaries.** `make app` is the default: it carries no bitstream (an ordinary `go build`), so it
verifies-and-drives a fabric already on the standard build but cannot configure a cold-boot one — for
that case set `SCOPE_SKIP_FPGA_LOAD=1`. `make app-release` builds the shipping binary with
`-tags withbitstream`, embedding `fpga/standard/acq.rbf` (built by `cd ../fpga && make bitstream`) so
one self-contained binary reconfigures a cold-boot fabric. The `.rbf` is a hardware build artifact
(candidate pin assignment) and is **not committed**; the release target copies it in at build time.

The nCONFIG bit mapping, low-hold/settle timings, LSB-first bit order, `bufsiz`, and 24 MHz DCLK
ceiling are carried from the proving-ground reloader as documented **bench assumptions** — see the
`fpgaload` package doc and `device.go` — to be confirmed against real silicon.

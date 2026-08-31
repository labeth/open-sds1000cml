# DESIGN — the codegen and the FPGA↔app interface contract

See also: [`fpga/standard/docs/DESIGN.md`](../../fpga/standard/docs/DESIGN.md) (the standard bitstream RTL) · [`app/docs/DESIGN.md`](../../app/docs/DESIGN.md) (the app acquisition integration).

Status: **design only** (no code, no Quartus). This is the lead document of the
owned-acquisition-FPGA set: it defines the module layout, the interface schema, and the
generated output that binds the FPGA and the app together. The two sibling docs above
implement the fabric and the app against the contract fixed here. It is opinionated; every
decision left to the maintainer is tagged **[DECIDE]**. Branch: `owned-fpga`.

The **phased build plan** and the **collected open decisions** for the whole effort live at
the end of this document (§7, §8); the sibling docs cross-reference them.

---

## 0. Framing

We **own the acquisition FPGA**. The project replaces the vendor acquisition path outright:
our own bitstream (`fpga/standard/`), our own FPGA↔app register/bus interface (generated from
one schema), and an app whose acquisition engine drives **only** the owned FPGA through the
generated bus. This is a **clean replacement, not a compatibility layer** (the app-side
consequences — deleted vendor protocol, no dual mode — are detailed in the app doc).

The single load-bearing idea of *this* document: **one schema is the source of truth**, and a
`text/template` codegen emits every artifact that both sides depend on — the FPGA's Verilog
register header and RTL decode, the app's Go bindings, and the human register-map reference —
so the fabric and the app **can never drift**. Editing the interface is a schema edit followed
by regeneration; both sides move in lockstep, and a CI drift gate proves it.

---

## 1. Layout and module boundaries

The repo already runs a **two-Go-module** shape (`app/` module `open-sds/app`, `ota/` module
`open-sds/ota`), each with its own `Makefile`, with CI matrixing over the module list. We
mirror that precedent by adding **two more modules**: `codegen/` and `fpga/`. Four modules
total — `app`, `ota`, `codegen`, `fpga` — mirroring the repo's existing per-module CI matrix.

### 1.1 Why `codegen` is a root module

The codegen generates **BOTH** sides of the interface (the app's Go bindings AND the FPGA's
Verilog), so it is the shared interface-contract tool. It is design-time tooling that never
ships in the scope binary. Therefore:

- It is **not** under `app/`. `app/go.mod` is `module open-sds/app`, go 1.26, with **zero
  external dependencies**; putting the generator there would drag the Verilog emitter,
  `crypto/sha256`, `go/format`, and the embedded templates into the lean ARMv7 scope binary's
  module.
- It is **not** under `fpga/`. It is not FPGA-specific; it generates the app's Go bindings too.

So it is its own **root `codegen/` module**. **[RESOLVED — maintainer]**

The FPGA-specific pieces — the Quartus build driver and `buildacq` — live in the `fpga/`
module, not in `codegen/`.

```
codegen/                               Go module  (module open-sds/codegen, go 1.26, stdlib only)
├── README.md
├── Makefile                           generate / drift / test / vet
├── go.mod
├── docs/DESIGN.md                     this file (design of record for the interface contract)
├── schema/schema.go                   the interface type system + Validate() + BuildID()
├── ifacedef/standard.go               OUR interface, as data  (Standard() schema.Interface)
├── emit/emit.go                       template driver: load data → render → write
├── emit/view.go                       the flattened template view structs (precomputed)
├── emit/templates/                    text/template sources (embedded):
│   ├── regs.vh.tmpl                   Verilog `define header
│   ├── regmux.vh.tmpl                 Verilog write-strobe + read-mux decode include
│   ├── bindings.go.tmpl              Go bindings (constants + tables + record codecs)
│   └── register-map.md.tmpl          generated register-map reference
└── cmd/ifacegen/main.go              validate → emit (writes app/ + fpga/) or -check drift

fpga/                                  Go module  (module open-sds/fpga, go 1.26)  — see the fpga doc
└── standard/{regs.vh, regmux.vh, docs/REGISTER-MAP.md}   GENERATED here, checked in, drift-gated

app/internal/iface/                    NEW package  (module open-sds/app)
└── iface.go                           GENERATED Go bindings (checked in, drift-gated)
```

`ifacegen` **writes into two trees** via `-go`/`-vh`/`-doc` output flags — the Go binding into
`app/internal/iface`, and `regs.vh`/`regmux.vh`/`REGISTER-MAP.md` into `fpga/standard/` (and
its `docs/`). The `-check` drift gate regenerates all four in memory and diffs them against the
checked-in copies.

Future specialized bitstreams are `fpga/<impl>/` siblings of `standard/`, each with its own
`README.md` + `docs/` and its own generated `regs.vh` from this shared codegen.

**Confirmed: nothing collides.** The target repo top level is `app/ codegen/ docs/ fpga/ ota/
specs/` plus root files. There is no vendor `app/internal/iface` or `app/internal/fpga`
package. This design creates the `codegen/` and `fpga/` modules and the `app/internal/iface`
package.

### 1.2 Why the generated **Go** bindings live under `app/internal/`

The app cannot `import "open-sds/fpga/..."` or `"open-sds/codegen/..."` without taking a
cross-module dependency and dragging the Verilog emitter, the Quartus driver, `crypto/sha256`,
`go/format`, and the `embed` templates into the ARMv7 scope binary. So the bindings the app
imports are a **checked-in generated Go file inside the app module**: `app/internal/iface`
(package `iface`). The app just imports its own internal package; the offline test suite
compiles with no knowledge of `codegen/` or `fpga/`.

### 1.3 Naming (no proving-ground names)

Clean, single-word names; the proving-ground `v0`/`test`/`rung`/`v0_spine` names do not carry
over.

| thing | name | rationale |
|---|---|---|
| interface-contract module | `open-sds/codegen` | generates both sides; matches the per-module CI shape |
| FPGA module | `open-sds/fpga` | matches `open-sds/app`, `open-sds/ota` |
| generated Go bindings pkg | `app/internal/iface` (pkg `iface`) | single clean word, like every other `internal/*`; "the app's view of the generated interface" |
| interface definition fn | `ifacedef.Standard()` | not `V0()` — there is one owned interface; versions grow **inside** its reserved blocks |
| clean-room spec | replaces `specs/02` + `specs/03` (app doc §"specs") | — |

The interface `Version` field stays a schema string (`"1"`), but the app never carries two
maps; a fabric/app mismatch is a **build-ID** rejection (§4), not a mode. Feature milestones
(v1/v2/v3 taps) grow inside the reserved blocks (§3.2) without renumbering.

### 1.4 Make / CI wiring, and the drift gate

`codegen/Makefile` owns generation and the drift gate (it owns `cmd/ifacegen`):

```
generate:  go run ./cmd/ifacegen        -go ../app/internal/iface -vh ../fpga/standard -doc ../fpga/standard
drift:     go run ./cmd/ifacegen -check  -go ../app/internal/iface -vh ../fpga/standard -doc ../fpga/standard
test:      go test ./...                 # schema validation, emitter determinism, codec round-trips
vet:       go vet ./...
```

`fpga/Makefile` owns the bitstream target (`go run ./cmd/buildacq`, NOT run in CI — it needs
Quartus and the memory gate) plus its own `test`/`vet`. The root `Makefile` adds
`cd codegen && go test ./...` and `cd fpga && go test ./...` to the `test` target (it already
runs `app` then `ota`), plus convenience `generate:` / `drift:` that delegate to `codegen`.

`.github/workflows/ci.yml`:

- extend the build-test matrix to `module: [app, ota, codegen, fpga]` — the codegen and fpga
  lanes run `go vet ./...` + `go test ./...` exactly like the others;
- add a **drift step** in the codegen lane: `make drift` fails the job if any checked-in
  generated file (the Go binding, `regs.vh`, `regmux.vh`, `REGISTER-MAP.md`) is stale — this
  is the **"fabric and app can never drift" gate**;
- `cross-build` lane unchanged (it still `make app` cross-compiles ARMv7, now against the
  checked-in `app/internal/iface`);
- **no Quartus in CI** (no toolchain; the 3.8 GiB box runs one flow at a time). The `.rbf` is a
  bench artifact; a cheap CI check asserts that, when `acq.rbf` is present, it is exactly
  `RBFBytes` (368011) — a wrong size fails.

---

## 2. Standards checklist (build agents MUST follow)

These are the shared repo standards for generated output and the build; the sibling docs
reference this list rather than restating it.

1. **NO per-file SPDX headers.** The proving-ground sources begin with
   `// SPDX-License-Identifier: MIT` on *every* file — **strip it from everything ported.** The
   repo licenses via the root `LICENSE` (MIT) and documents packages with a **package doc
   comment** (see `bus.go`, `engine.go`, `testenv.go`). Every new Go package opens with a short
   doc comment saying what it is and why. The generated Verilog/Markdown carry a one-line
   `GENERATED … DO NOT EDIT. Source: codegen/ifacedef` banner — **no SPDX line**.
2. **Clean-room provenance.** The schema (`ifacedef.Standard()`) is written **from the
   behavioral spec** (`specs/03`, rewritten — see the app doc), never from vendor code or a
   disassembly. The RTL implements the behavior the spec defines. Generated files derive from
   the schema. The provenance chain is: `specs/03` (human, behavioral) → `ifacedef` (schema) →
   {`regs.vh`, `regmux.vh`, `iface.go`, `REGISTER-MAP.md`}. No step reads vendor firmware.
   (`CONTRIBUTING.md` §"Clean-room provenance".)
3. **`make test` stays green, offline, no hardware.** The engine tests drive a scripted fake
   bus (see the app doc), not `/dev/Gpmc`. Every change keeps `cd app && go test ./...`,
   `cd ota && …`, `cd codegen && …`, and `cd fpga && …` green; `go vet ./...` clean per module.
   The node/Playwright browser lane self-skips without the toolchain (`internal/testenv`) —
   leave that intact.
4. **Match the surrounding style.** Short, purposeful doc comments that explain *why*, not
   *what*; single clean words for `internal/*` package names; gofmt; small, focused commits.
5. **Commit convention** (observed in `git log`): `area: imperative summary` (lowercase, e.g.
   `codegen: template-based emitters`), then a body paragraph explaining the reasoning and what
   was verified without hardware. Trailer: `Co-Authored-By: …`. **[DECIDE]** the repo's existing
   trailer is `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`; the session harness asks
   for a different `Co-Authored-By`/`Claude-Session` pair — the maintainer picks which trailer
   the owned-FPGA commits use. Commit or push only when the maintainer asks.
6. **`.gitattributes`.** Add `*.rbf binary` (never munge the bitstream; it must be exactly
   368011 bytes) and `*.vh text eol=lf` (generated Verilog headers stay LF). Do **not** add
   `.rbf` to `.gitignore` — the built bitstream is a tracked artifact like the proving-ground
   `.rbf`s.
7. **Generated files are checked in and never hand-edited.** They are byte-stable (deterministic
   emitters, gofmt on Go output) and drift-gated (§1.4). Editing the interface is a **schema**
   edit → `make generate` → both sides move in lockstep.
8. **Determinism.** Emitters iterate slices only (no map ranging in output order); `BuildID()`
   is a stable truncated SHA-256 of a canonical schema dump; the Go binding is run through
   `go/format` so regeneration is byte-identical.

---

## 3. The interface schema

### 3.1 Schema shape

Keep the proving-ground type system — it is well-shaped — and carry these forward:
`Plane{CS1,CS3}`, `Access{R,W,RW}`, `Sem` behavioral access-semantics bitset
(`SemNormal|SemStrobe|SemAutoIncPort|SemReadAfterHalt|SemLevelStatus|SemWaitGuarded`),
`Field{Name,Hi,Lo,Desc}`, `Register{Name,Sel,Plane,Access,Sem,Fields,Expect,Desc}`,
`Block{Name,Plane,Base,Span,Regs,Desc}`, `RecField{Name,Bits,Overflow,Desc}`, `Channel`,
`Descriptor`, `Stream`, `Capture`, and `Interface` with `Validate() []error` and
`BuildID() uint32`.

**The four frozen contracts** (`Validate` enforces all, returning every problem):

- **C1 stream:** sample lane ≥ 18 bits AND ≥ 1 reserved bypassable transform stage.
- **C2 capture:** programmable pre-trigger AND a `trig_mark`; `RecordDepth > 0`.
- **C3 channels:** every result/event channel carries an overflow/lost-count field; fields sum
  to `RecordBits`.
- **C4 access-semantics:** every register declares explicit `Sem` (never defaulted).

Plus structural safety: selector collisions per plane, block containment, no block overlap,
well-formed non-overlapping fields, readable build-ID registers.

**Improvements to the schema for the owned interface:**

- **Capture geometry is the single source of truth for the RTL, too.** Add `Capture.AddrBits`
  and a `Margin` (the registered-write pipeline tail — see the fpga doc's capture section) so
  the emitter can put `REC_DEPTH`, `ADDR_W`, `PRETRIG_MAX = REC_DEPTH−Margin` into `regs.vh`.
  In the proving-ground design `REC_DEPTH` existed **both** in the schema and as a Verilog
  module `parameter` — two truths that can drift. Here the RTL uses `` `REC_DEPTH `` etc. from
  the generated header. **One source.**
- **A uniform result-channel port contract.** Add `Channel.Ports` describing the fixed accessor
  triad every channel exposes: a `DATA` auto-inc read port, a `COUNT` level-status port (count
  + overflow), and an optional `RESET` strobe. The emitter generates the port decode + a
  reusable `result_fifo` instance per channel, so future v1/v2 taps (measure, trig_event,
  decode_result) are **instances of one pattern**, not new hand-written FIFOs. The
  proving-ground hard-coded the envelope FIFO; here the envelope is the first instance of the
  general contract.
- **Model the whole CS3 front-end**, not just the level DAC. The proving-ground `frontend`
  block held only the 4 level-DAC lanes; the owned schema also declares the **offset DACs**
  (C1/C2 lo/hi) and the **LED latch** (lo/hi/strobe), so the app's front-end writes and its
  write-guard are entirely schema-derived.
- **Interpolating-timestamp field is real, not a zero hook**: the `TRIGPOS` register pair is
  defined as `{frac:Q16 sub-sample, idx:15-bit physical}`.

### 3.2 Block layout (the owned register map)

Clean reserved blocks with room for v1/v2/v3 to grow without renumbering. Selectors are OUR
choice (this is our fabric); they deliberately do not echo the vendor map.

**CS1 (acquisition / read plane):**

> ⚠ **Selector VALUES are not repeated here.** They were, and the copy went stale the day the
> map was respaced to multiples of 4 (A3–A7-only decode): this table still said `BURST 0x30`
> long after `0x30` had become `PRETRIG_LO`. A hand-maintained second copy of a generated map
> is the exact drift this tool exists to abolish, so the values live in **one** place — the
> generated [`REGISTER-MAP.md`](../../fpga/standard/docs/REGISTER-MAP.md), from
> `ifacedef.Standard()`. What follows is the *design*: which block holds what, and why.

| block | range | registers |
|---|---|---|
| `meta` | `0x10–0x1f` | `BUILDID_LO` R · `BUILDID_HI` R · `VERSION` R (Expect `0x0052`, a cheap addressing self-check) |
| `capture` | `0x20–0x3f` | `OPCODE` W strobe (GO/HALT/RESET) · `RUN` RW {MODE[1:0]=auto/norm/single, RUN[2]} · `DECIM_LO`/`DECIM_HI` RW (stream decimation) · `PRETRIG_LO/HI` RW · `POSTTRIG_LO/HI` RW |
| `drain` | `0x40–0x4f` | `BURST` R auto-inc+read-after-halt (the **single** raw-record port) · `BURST_REMAIN` R level+read-after-halt {READY[15], REMAIN[14:0]} |
| `status` | `0x50–0x5f` | `STATUS_A` R level {VALID[0],TRIG[1],DONE[2]} · `TRIGPOS_LO/HI` R level+read-after-halt · `FILL` R level {COUNT[10:0]} |
| `spine` | `0x60–0x6f` | `XFORM_CTRL` RW {BYPASS0,BYPASS1} · `ENV_COLS` RW (envelope column count) |
| `channels` | `0x70–0x7f` | `ENV_DATA` R auto-inc+read-after-halt · `ENV_COUNT` R level {COUNT[14:0],OVERFLOW[15]} · `ENV_RESET` W strobe |
| reserved | `0x80–0xdf` | `trigger` (v1), `measure` (v1), `decode` (v2) — ranges reserved, **no registers yet, and NOT reachable as drawn** (see below) |

**Only 32 CS1 selectors are addressable at all, and all 32 are taken.** Hardware readback of a
flashed fabric showed that of the GPMC address lines reaching the Cyclone, `A1` (ball `M2`)
carries a clock and `A2` (ball `D1`) floats high, while `A8` is not wired; `acq.v` therefore
decodes `{1'b0, sel[6:2], 2'b00}`. Decodable CS1 selectors are exactly the multiples of 4 below
`0x80`. `schema.Validate` now rejects any CS1 selector outside that set — including anything in
the reserved `0x80–0xdf` ranges above, which must be re-based before they can hold a register.
Of the 32: **22** are schema registers, **9** are hand-decoded in `acq.v` and appear in no
schema (the serial-decode config/result ports and the panel read window), and **1** is the
`0x00` read alias. `ifacedef`'s `TestCS1SelectorCensus` prints this census on every test run.

**Read aliases.** A register may declare `ReadAliases`: extra selectors that decode to its read
data. They exist for bus masters that cannot choose their selector — the GPMC prefetch/sDMA
engine reads the chip-select BASE, so `CS1 0x00` is aliased onto `BURST`. An alias is read-side
only (never a write strobe), must not shadow a register or sit in a reserved block, and is
deliberately **outside** the build-ID: it cannot change how any declared register behaves, so it
cannot mispair a fabric with an app. That alias used to be a hand edit inside the generated
`regmux.vh`, where `make generate` stood ready to delete it and silently change bus behaviour.

**The single `BURST` port replaces the five-port round-robin entirely.** One fixed auto-inc
address is what the GPMC prefetch engine / self-paced EDMA want (1-D, not 3-D) and what an mmap
loop reads fastest; it removes the round-robin modulo from both sides.

**CS3 (config / control plane):**

| block | range | registers |
|---|---|---|
| `config` | `0x00–0x08` | `CONF_DONE 0x07` R only {DONE[7]} — nCONFIG port; app never writes it |
| `frontend` | `0x09–0x3f` | offset DAC C1 `OFF_C1_LO 0x10`/`OFF_C1_HI 0x30`, C2 `0x11`/`0x31`; LED latch `LED_LO 0x09`/`LED_HI 0x0a`/`LED_STROBE 0x0b`; level DAC `LVL_A_LO 0x14`/`LVL_B_LO 0x15`/`LVL_A_HI 0x34` strobe/`LVL_B_HI 0x35` strobe |

### 3.3 What is generated, and from which template

Four `text/template` sources under `emit/templates/`, embedded via `//go:embed`. The Go code
precomputes a **flat view** (`emit/view.go`) — masks, LSBs, build-ID hi/lo, per-channel field
offsets, hex strings — so the templates stay logic-light (ranges + field lookups, no
arithmetic). A shared `FuncMap` provides `hex8/hex16/hex32`, `upper`, `mask`. This
`text/template` approach is the maintainer's explicit preference over the proving-ground's
line-by-line `fmt.Fprintf` emitters.

| template | output | consumes | replaces proving-ground |
|---|---|---|---|
| `regs.vh.tmpl` | `fpga/standard/regs.vh` | selector params, plane params, field `_MASK`/`_LSB`, `IFACE_BUILD_ID(_LO/_HI)`, geometry (`REC_DEPTH`,`ADDR_W`,`PRETRIG_MAX`) | `emit/verilog.go` |
| `regmux.vh.tmpl` | `fpga/standard/regmux.vh` | **NEW**: per-register write-strobe wires (`we_<REG>`) + a read-data mux skeleton, from `Access`/`Sem` | (none — closes a drift gap, §3.5) |
| `bindings.go.tmpl` | `app/internal/iface/iface.go` | `BuildID`, `Plane`, `Sem`, `Sel*` consts, `Registers`/`AutoIncPorts` tables, field masks/shifts, **channel record codecs** (§3.4) | `emit/gobind.go` |
| `register-map.md.tmpl` | `fpga/standard/docs/REGISTER-MAP.md` | the human register reference + the four contracts + build-ID | `emit/doc.go` |

The Go output is post-processed with `go/format` (byte-stable → drift gate). Verilog and
Markdown render deterministically from slice order. The four outputs land in two trees
(`app/internal/iface` and `fpga/standard/` + its `docs/`); both are checked in and drift-gated.

### 3.4 Generated bindings expose **behavioral** access-semantics to the app

The proving-ground bindings emitted addresses + a flat `Registers` table + field offsets, but
the app still hand-packed everything. The owned bindings encode the hazards that dominated the
RE **as typed API**, so the engine cannot misuse them:

- **`Registers` / `AutoIncPorts` tables** (kept): `{Name,Sel,Plane,Access,Sem}`. `AutoIncPorts`
  is **plane-qualified** — match on `{Plane,Sel}`, never selector alone (a selector repeats
  across planes). The bus layer must treat an auto-inc read as a mutation: never dedup,
  speculate, or CSE it.
- **Selector constants** `iface.Sel<REG>` (unshifted; the driver applies `<<1`), and
  `iface.CS1`/`iface.CS3`.
- **Typed field accessors** per bitfield: `iface.StatusADone(w) bool`, `iface.RunWithMode(auto)`,
  etc. — generated from `Fields`, so the engine reads `STATUS_A` via named accessors, not hand
  masks.
- **Channel record codecs.** For each `Channel`, a generated struct + `Unpack([]uint16)` (and
  `Pack`) honoring the LSB-first bit layout — e.g. `iface.EnvelopeRecord{Col,Min,Max,Ch,Overflow}`
  decoded from the 3-word `ENV_DATA` read. The engine consumes typed records, never raw bit
  shifts. This is the concrete "results not raw" plumbing.
- **A generated fabric handshake:** `iface.Verify(r func(plane, sel) (uint16,error))` checks
  `VERSION==0x0052` **and** `BUILDID_LO/HI == iface.BuildID`. The app calls it at bring-up and
  **refuses to drive** a fabric whose build-ID differs — the app generated the bitstream, so a
  mismatch is a mispaired build, not a negotiation.
- **A schema-derived write guard:** `iface.Writable(plane, sel) bool` is `false` for every
  `Access==R` register (e.g. `CONF_DONE`, all status/drain ports). The bus layer's
  forbidden-write check becomes this call — no hand-maintained magic ranges.

The **read-after-halt** and **level-status** semantics are surfaced so the engine's drain path
only reads `BURST`/`ENV_DATA`/`TRIGPOS` after a HALT, and re-reads level registers rather than
treating them as sticky.

### 3.5 Concrete improvements over the proving-ground codegen

1. **Templates, not `fmt.Fprintf`.** (Mandated.) Readable `.tmpl` files; the Go code only
   prepares data and post-formats.
2. **Generated RTL decode (`regmux.vh`).** The proving-ground design hand-wrote the read mux
   and the write `case` in RTL — those could drift from the schema, the exact bug class the
   codegen exists to kill. The owned codegen emits per-register write-strobe wires and the
   read-mux skeleton **from `Access`/`Sem`**, so hand RTL only assigns behavior behind named
   wires (`rdata_STATUS_A = {…}`), never a selector. **[DECIDE]** generate a decode *include*
   (`regmux.vh`, recommended, small) vs a full standalone decode *module*; the include keeps the
   top readable without a port explosion.
3. **Channel record codecs generated** (§3.4) — the proving-ground stopped at offsets.
4. **Generated handshake + write-guard** (§3.4) — behavioral, not layout-only.
5. **One geometry source** (§3.1) — `REC_DEPTH`/`ADDR_W`/`PRETRIG_MAX` come from the schema into
   `regs.vh`, so RTL and app agree by construction.
6. **`ifacedef.Standard()`**, not `V0()` — one owned interface; versions grow inside reserved
   blocks. `Version:"1"`.

The **drift gate** ties all of this together: `make drift` regenerates all four artifacts in
memory and fails CI on any mismatch with the checked-in copies. Because the emitters are
deterministic and the Go output is gofmt-stable, a clean tree round-trips byte-for-byte; a stale
`regs.vh`, `regmux.vh`, `iface.go`, or `REGISTER-MAP.md` cannot survive CI.

---

## 4. Build order and the fabric/app handshake (this module's slice)

This module ships **Phase A** of the whole-effort plan below: the `codegen/` module, the
schema, `ifacedef.Standard()`, the four templates, and `cmd/ifacegen`. Its gate is that
`codegen` tests pass (schema valid, emitters deterministic, channel-codec round-trips, `BuildID`
moves on change), `make drift` is clean, and `app` still builds green (nothing imports `iface`
yet).

The **handshake** the generated bindings define is what makes the "clean replacement, no
fallback" stance enforceable at runtime: `iface.Verify` requires `VERSION==0x0052` and a
matching `BUILDID_LO/HI`, and the app refuses to drive any fabric whose build-ID differs. The
app generated the bitstream, so a mismatch is a mispaired build, not a negotiation — there is no
mode to fall back to.

---

## 5. Phased build plan (whole effort — the sibling docs reference this)

Each phase is one or a few focused commits; every phase keeps `make test` green and `go vet`
clean across all modules.

### Phase A — codegen + schema (this module; no RTL, no app change yet)

1. Create the `codegen/` module (`go.mod`, `Makefile`), `schema/` (ported from the
   proving-ground type system, **SPDX stripped**, package doc comment added), and the schema
   improvements (§3.1).
2. `ifacedef/standard.go` = the owned interface (§3.2). `Validate()` must pass; `BuildID()`
   deterministic.
3. `emit/` with the four `text/template` sources (§3.3), the flat view, and `go/format` on the
   Go output. `cmd/ifacegen` with `-go/-vh/-doc` and `-check`.
4. `make generate` → commits `app/internal/iface/iface.go`, `fpga/standard/{regs.vh,regmux.vh}`,
   `fpga/standard/docs/REGISTER-MAP.md`.
   **Gate:** codegen tests pass; `make drift` clean; `app` still builds and `make test` green.

### Phase B — the standard bitstream (`fpga/standard/`)

RTL modules + Quartus driver + `buildacq`; RTL-review and simulation gates; on the bench box,
`acq.rbf` compiles to exactly **368011** bytes with M9K preserved. **No Quartus in CI.** Detail
lives in the [fpga doc](../../fpga/standard/docs/DESIGN.md).

### Phase C — app integration

Rewrite `app/internal/bus` around `iface`; rewrite the engine FSM; re-home CPU-side processing;
delete the vendor path. Detail lives in the [app doc](../../app/docs/DESIGN.md).

### Phase D — tests + CI (interleave with C; land together)

Rewrite the scripted fake bus for the owned interface; update every `bus.Bus` test double;
rewrite `specs/02` + `specs/03`; extend the CI matrix to `[app,ota,codegen,fpga]`, run
`make drift` in the codegen lane, add `.gitattributes` for `*.rbf`/`*.vh`. Detail (fake bus,
specs) lives in the [app doc](../../app/docs/DESIGN.md).
   **Gate:** full `make test` green offline (all modules); `make drift` clean; cross-build lane
   green; browser lane still self-skips cleanly.

### Phase E — hardware validation (bench, gated; not CI)

Flash `acq.rbf`; `iface.Verify` handshake passes; the two north-stars end-to-end — (1) drain is
not the limit (envelope/measure drain O(columns); raw only on zoom; CPU free during DMA drain),
and (2) the static-freeze byte-identity test — **then retire the quiet-lock across the drain**
in the app. Detail in the [fpga doc](../../fpga/standard/docs/DESIGN.md) and
[app doc](../../app/docs/DESIGN.md).

### Bench-gated inputs (flag to the maintainer)

- **[DECIDE]** `acq.qsf` **pin assignments** come from the pin-discovery work; this design does
  not invent them. Until the bench supplies the map, `cmd/buildacq` can compile against a
  placeholder pinout for timing/fit only — the shipped `.rbf` waits on real pins.
- **[DECIDE]** DMA-request ball for bus-paced sync burst (GPMC-DRAIN lever D) — reserve now,
  build later behind a burst-clock pin.
- **[DECIDE]** the commit trailer (§2.5); the multi-file Quartus driver change (fpga doc);
  `regmux.vh` include-vs-module (§3.5); the timebase→`DECIM` table's home (app doc §"specs").

---

## 6. Open decisions requiring the maintainer's call (collected)

1. Module home for the generator — dedicated root `codegen/` module (recommended, **RESOLVED**)
   vs `app/cmd/ifacegen`. (§1.1)
2. Generated-bindings package name — `app/internal/iface` (recommended) vs `fpga`/`regs`. (§1.3)
3. `regmux.vh` — generate a decode **include** (recommended) vs a full decode **module**. (§3.5)
4. Commit trailer — repo's `Claude Fable 5` vs the session harness's trailers. (§2.5)
5. Quartus driver multi-file support — required for the modular RTL design. (fpga doc)
6. DMA-request ball / sync-burst — reserve now, build post-bench. (fpga doc)
7. Timebase→`DECIM` table home — `specs/03` vs `specs/04`. (app doc §"specs")
8. Interpolating-timestamp accuracy target — first-order floor now, bench-tuned later. (fpga doc)
9. Pin map — bench-supplied; blocks the shipped `.rbf` only. (fpga doc)

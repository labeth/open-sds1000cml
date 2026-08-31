# codegen — the FPGA↔app interface generator

Module `open-sds/codegen` (Go 1.26, standard library only). This is the **shared
interface-contract tool**: one schema is the single source of truth, and a
`text/template` codegen emits BOTH the app's Go bindings AND the FPGA's Verilog,
so the fabric and the firmware can never drift. It ships in neither binary — it
is design-time / build-time tooling.

## The one source of truth

```
specs/03 (human, behavioral)
      │
      ▼
codegen/ifacedef.Standard()   ← the owned register map, as data
      │  Validate() enforces C1–C4 + structural safety
      │  BuildID() = truncated SHA-256 of a canonical dump
      ▼
codegen/emit (text/template)
      ├── ../app/internal/iface/iface.go        Go bindings   (checked in)
      ├── ../fpga/standard/regs.vh              Verilog `define header (checked in)
      ├── ../fpga/standard/regmux.vh            Verilog decode include (checked in)
      └── ../fpga/standard/docs/REGISTER-MAP.md register-map doc (checked in)
```

All four generated files are **checked in and drift-gated** — never hand-edited.
Editing the interface means editing the schema and running `make generate`; both
sides move together.

## Layout

| path | what |
|---|---|
| `schema/schema.go` | the interface type system + `Validate()` (the four frozen contracts + structural safety) + `BuildID()` |
| `ifacedef/standard.go` | `Standard() schema.Interface` — the owned register map (DESIGN §3.2) |
| `emit/view.go` | the flattened, precomputed template view (masks, LSBs, hex, camel-case, record offsets, Go types, read-mux expressions) |
| `emit/emit.go` | the template driver + shared `FuncMap`; runs the Go output through `go/format` |
| `emit/templates/*.tmpl` | the four `text/template` sources, embedded via `//go:embed` |
| `cmd/ifacegen/main.go` | validate → emit, with `-go/-vh/-doc` output flags and the `-check` drift gate |

The templates are logic-light on purpose: `view.go` does all the arithmetic, the
templates only range over slices and look up fields. This replaces the
proving-ground's line-by-line `fmt.Fprintf` emitters.

## The four frozen contracts (`Validate`)

- **C1 stream** — sample lane ≥ 18 bits AND ≥ 1 reserved bypassable transform stage.
- **C2 capture** — programmable pre-trigger AND a `trig_mark`; `RecordDepth > 0`;
  and the geometry (`AddrBits`, `Margin`) is wide enough for the record.
- **C3 channels** — every result/event channel carries an overflow/lost-count
  field, and its fields sum to `RecordBits`.
- **C4 access-semantics** — every register declares explicit `Sem` (never defaulted).

Plus: no selector collisions within a plane, block containment + no overlap,
well-formed non-overlapping fields, readable build-ID registers, and well-formed
result-channel ports.

## Selector aliases — why a generated file may name a bare selector

A register may declare `ReadAliases`: **extra selectors that decode to its read data**. They
exist for bus masters that cannot choose their selector — the GPMC prefetch/sDMA engine reads
the chip-select BASE address, so `CS1 0x00` is aliased onto the auto-inc `BURST` port. An alias
is read-side only (never a write strobe), may not shadow a register or sit inside a reserved
block, and is deliberately kept **out of the build-ID** (it cannot change how any declared
register behaves, so it cannot mispair a fabric with an app).

That alias was previously a **hand edit inside the generated `regmux.vh`**, which meant
`make generate` was one command away from deleting a line that changes bus behaviour. It is
declared in the schema now, so regeneration preserves it.

⚠ Reading an alias of an auto-inc port **pops** it, exactly as reading the register does.

## CS1 selector space is fully allocated

Only 32 CS1 selectors are decodable at all — the multiples of 4 below `0x80` — because `A1`
carries a clock, `A2` floats high and `A8` is unwired, so the fabric decodes
`{1'b0, sel[6:2], 2'b00}`. `Validate` rejects anything else on CS1. All 32 are claimed today:
22 schema registers, 9 hand-decoded in `acq.v`, 1 read alias. `go test ./ifacedef` prints the
census; adding a register needs a wider decode or a reclaimed selector.

## Usage

```
make generate   # (re)generate all four artifacts from the schema
make drift      # fail if any checked-in generated file is stale (CI gate)
make test       # schema validation, emitter determinism, codec round-trips,
                # the drift gate (TestGenNotStale) and the acq.v cross-checks
make vet
```

`make drift` MUST be clean immediately after `make generate`. The same comparison also runs as
`TestGenNotStale` inside `go test ./...`, so a plain test run cannot be blind to a stale
artifact — that blindness is what let a hand edit live inside a generated file.

## What the generated bindings expose (behavioral, not layout-only)

`app/internal/iface` gives the engine a **typed** surface so it cannot misuse the
hazards that dominated the RE:

- selector constants + a plane-qualified `Registers` / `AutoIncPorts` table;
- typed field accessors (`iface.StatusADone(w)`, `iface.RunWithMode(v)`, …);
- channel record codecs (`iface.EnvelopeRecord{…}` with `Unpack`/`Pack`);
- a fabric handshake `iface.Verify(read)` (VERSION magic + build-ID);
- a schema-derived write guard `iface.Writable(plane, sel)`;
- semantics predicates `iface.IsAutoInc/IsReadAfterHalt/IsLevelStatus`.

The Verilog decode include (`regmux.vh`) generates the write-strobe wires and the
read-data mux from `Access`/`Sem`, so the RTL selector decode cannot drift; the
identity/build-ID reads are driven straight from the schema.

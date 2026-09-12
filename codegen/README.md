# codegen — the acq2 FPGA<->app register contract

`codegen/ifacedef/default.go` holds the register map of the default image
(schema `sds1000cml-default`, version 3 — the 32 registers of
`docs/acq2/05-WORKPLAN.md` §2 / §2.1 at their 4-aligned selectors plus the
`docs/acq2/06-TIERS.md` §2 additions in the slots GPMC A1/A2 opened) as Go
data. `make generate` renders it into:

| artifact | consumer |
|---|---|
| `fpga/default/regs.vh` | RTL: `` `SEL_<REG> ``, field `_MASK`/`_LSB`, enum `` `<REG>_<FIELD>_<NAME> ``, `` `OP_* ``, `` `DIAG_* ``, geometry (`` `REC_DEPTH ``, `` `ROW_COLS ``, `` `ROWS ``, `` `DLINE_ROWS ``, `` `ACC_BITS ``, `` `STACK_N_MAX ``), identity, build-ID |
| `fpga/default/regmux.vh` | RTL top module (`` `include `` inside the module): `we_<REG>`, `op_<NAME>`, `pop_<REG>`, `rmux_rdata` |
| `app/internal/iface/iface.go` | app: `Sel*`, field masks/shifts/enum values, `Op*`, `Diag*`, `BuildID`, `Registers()`, `Undecoded()`, `CheckIdentity()`, and the word-format models `Model*` |
| `fpga/default/docs/REGISTER-MAP.md` | people |

`make drift` regenerates into a temp directory and exits 1 if any checked-in
artifact differs (no writes); `go test ./...` runs the same gate plus the
schema, emitter, word-model and generated-decode simulation tests (the last
needs `iverilog`; skipped without it).

## Build-ID

`schema.Interface.BuildID()` = FNV-1a 64-bit over `Canonical()` (every
structural attribute in declaration order: identity values, selector mask,
legacy mask, undecoded selectors, geometry, each register's
selector/since/access/pop/strobe/source/const/alias/stub, fields and enum
values, opcodes, DIAG entries), folded to 32 bits as `hi32 XOR lo32`, with
the RTL source digest (`ifacegen -rtl`) folded in. It is emitted into all
four artifacts and read back through `BUILDID_LO`/`BUILDID_HI`; the app
reloads the embedded fabric when it differs. `Desc` strings (and `SelDesc`)
are not hashed, so documentation edits do not force a fabric rebuild.

## Schema v3 rules (enforced by `schema.Validate`)

- `SelMask` 0x7f: 128 selectors, selector N at CS1 byte address 2N; bit 7 is
  never decoded. `regmux.vh` masks with `` `SEL_MASK `` itself
  (`rmux_wr_sel = wr_sel & SEL_MASK`), so the top module passes the raw
  selector with GPMC A1/A2 (balls B1/A2) in bits 1:0.
- `Legacy{Version: 2, SelMask: 0x7c}` + `Register.Since`: a v2 register must
  keep a 4-aligned selector, a v3 register must take a slot outside the v2
  mask. All 32 v2 registers are present, so every v3 slot is "odd".
- `Undecoded` 0x21, 0x57 (the vendor arm/halt words): no register may take
  them; they read 0 and ignore writes, which the generated testbench proves.
- `Register.Stub` / `DiagEntry.Stub`: declared for the full v3 map but not
  implemented by the current increment (v2.2). The decode reads 0 and emits
  no `we_` strobe for a stub register; the Desc says "v3.0+: reads 0 in
  v2.2". Flip `Stub` in the increment that implements the register — the
  build-ID moves. Fields inside a partly implemented register (IL_CTRL:
  only TSRC is live in v2.2) carry the same mark in their Desc; the fabric
  masks them.
- `Field.Enum`: named values (TSRC, REDUCE_MODE, CHMODE, ENC_RATE, ...)
  emitted for both sides, so a mode number exists in one place.
- Geometry: `RowCols * Rows == RecDepth`, `255 * StackNMax < 2^AccBits`,
  `DlineRows <= Rows`.

## Word-format models

`codegen/emit/wordfmt/wordfmt.go` is the Go model of every drained word
format (`ModelRecord`, `ModelTsrc`/`TsrcCheck`, `ModelDecim`, `ModelPeak`,
`ModelBoxcar8`/`ModelBoxcar16`/`BoxcarParams`, `ModelStack`, `Tier*`). It
is unit-tested there and spliced verbatim into `iface.go` by the Go-bindings
emitter (after the package clause and its single `import "fmt"`), so rung
R2b compares drained words against exactly the tested code and the fabric
RTL is written to match it. `wordfmt/geom.go` repeats the schema constants
the models need for their own tests; `emit.TestWordfmtConstantsMatchSchema`
fails when they drift from `ifacedef`.

## Rules

- Never hand-edit a generated file: edit the schema, run `make generate`, commit both.
- `BURST_ALIAS` (0x00) is declared in the schema as an alias of `BURST`, so the
  CS1-base drain doorway the GPMC prefetch engine needs (owned-fpga
  `sel_is_burst`, a hand edit there) is generated, and `pop_BURST` fires on both.
- An RTL edit under `fpga/common/*.v`, `fpga/default/*.v`, `*.sdc`, `*.qsf`, `lanemap_seed.vh`
  moves the build-ID: run `make generate` after it (the drift gate insists).

The idea (one schema, four artifacts, folded build-ID, drift gate) is from
owned-fpga `codegen/` (commits 570af15, 8938920, 2ee23a0); this is a smaller
re-implementation for the one-plane default image.

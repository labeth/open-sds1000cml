# Owned-FPGA review follow-ups

Tracked follow-ups from the comprehensive adversarial review of the `owned-fpga`
branch. Every **confirmed finding** from that review is already fixed on the branch
(OPCODE app↔RTL mismatch, cold-boot bring-up ordering, envelope-overflow display
corruption, the two schema validation gaps, and five doc/label/comment
reconciliations). The items below are the review's **completeness-critic gaps** —
hardening and enhancements that were deliberately deferred because they need their
own scoped work (or tooling not present here), not because they are unimportant.
They are ranked by risk.

## 1. Generate the envelope record packing (unguarded drift surface) — HIGH

`fpga/standard/envelope.v` hand-packs the envelope FIFO record (`env_wdata`, the
`3'd0..default` case), while the app side (`iface.EnvelopeRecord.Pack/Unpack`) is
generated from the schema. The two **currently agree** (Col[15:0], Min[23:16],
Max[31:24], Ch[35:32]), so there is no live bug — but a future edit to the schema's
envelope channel would regenerate `iface` and *not* `env_wdata`, and the build-ID
handshake does **not** cover the RTL producer side, so the mismatch would ship
silently. This is exactly the drift class codegen exists to kill, still open on the
RTL producer side of the one wired channel.

**Recommended fix:** emit a `env_record.vh` (field LSB/width macros) from
`schema.Channel.Fields`, the same way `regs.vh` is generated, and rewrite
`env_wdata` to assemble the record from those macros. Then the drift gate covers
the RTL packing too.

## 2. Executable RTL coverage (no simulation today) — infrastructure

There is no RTL testbench: no `iverilog`/`verilator` run, no `*_tb.v`. Every "SIM
GATE"/"INVARIANT" block in `capture.v`/`envelope.v` is prose, and the engine's fake
bus shares the app's own constants — so the offline suite is structurally unable to
catch an app↔RTL behavioral divergence. (The OPCODE mismatch that this review's
critic caught was exactly this blind spot; folding the opcodes into codegen closed
that *specific* divergence, but the general gap remains.) `iverilog`/`verilator` are
not installed in this environment.

**Recommended fix:** stand up an `iverilog` testbench for `acq.v` that drives the
real GPMC opcode/selector wire values and asserts the capture/envelope sim-gate
invariants (column-0-folds-sample-0, exact column count, exact pre/post window,
NORM wrap bound, clamp overflow). Wire it into CI so the RTL claims are executed,
not asserted.

## 3. Widen FILL.COUNT to a true sample count — MEDIUM (enhancement)

`FILL.COUNT` is 11-bit (`capture.v` exports `wrote_count[10:0]`) but `wrote_count`
runs to `REC_DEPTH`=20480, so FILL wraps ~10× across a deep record. This is
**benign today** by deliberate design — the engine caps every fill-gated window to
`envFillCap` (0x600 < 2048) and only uses `fillFull` as an AUTO wedged-fabric
fallback where early completion is harmless (AUTO self-triggers in the fabric, so
the normal path completes on `STATUS_A.DONE`). The misleading comment is fixed.

**Recommended enhancement:** widen `FILL.COUNT` to 15 bits (`Hi:14`, fits 20480 <
32768) in the schema, export `wrote_count[14:0]` in `capture.v`, and rework the
`latchAt`/`fillFull`/`envFillCap` gates to true sample counts — this removes the
`envFillCap` capture cap (finer deep envelope/roll). Schema change → build-ID
change → bitstream recompile.

## 4. Mark CONF_DONE as config-port-only — LOW (cosmetic decoy)

`acq.v` drives `gpmc_d` only on CS1 reads; CS3 reads (including `SEL_CONF_DONE`)
stay Hi-Z and `rdata_CONF_DONE` is hardwired 0, so the generated `regmux.vh` CS3
`CONF_DONE` read-mux case is unreachable — CONF_DONE is only meaningful via the
off-fabric config-port ioctl (`fpgaload/device.go`, `engine_bus.go`). The generated
CS3 read entry is a harmless decoy, but `iface.ConfDoneDone` invites reading it
through the normal register interface, where the fabric can never answer.

**Recommended fix:** add a schema marker (e.g. a `SemConfigPort` sem or a
per-register flag) so codegen does not emit a fabric read-mux case for
config-port-only registers, and document that CONF_DONE is read via the ioctl
config port only.

## 5. Release pipeline needs a real bitstream to build a bootable stick — release

`ota/mkstick.sh` now builds the app with `make app-release` (embeds
`fpga/standard/acq.rbf` so the stick can reconfigure a cold-boot factory fabric);
it fails loudly if the `.rbf` is absent. But the `.rbf` is an uncommitted hardware
build artifact and the CI runner has no Quartus, so **the CI release job
(`release.yml` → `mkstick.sh`) cannot currently produce a bootable stick and will
fail** until one of: (a) a Quartus-capable release runner builds the bitstream
first, or (b) a final-pin `acq.rbf` is committed/published as a release input.
Failing loud is intentional — it is strictly better than the previous behavior
(silently shipping a `make app` binary that FATAL-refuses on every factory unit).
This is gated on the physical bench producing a final pin assignment.

## 6. Bench-confirm native-fast NORM completion timing — bench-gate

`waitCapture` returns a native-fast frame on `filled && (anchored || sawTrig || !norm)`,
i.e. it treats the comparator edge (`sawTrig`) as sufficient completion evidence
alongside `STATUS_A.DONE`. The RTL sets `DONE` only at the exact post-trigger count
(`post_count == posttrig_work`), which lags `sawTrig` by the post-fill time. This is
safe at the default poll (native-fast fill 41–82 µs < `pollEvery` 150 µs, so `DONE`
always accompanies `sawTrig` at the first meaningful poll), but it is a timing
assumption: with a much shorter `pollEvery` a poll could land in the `sawTrig`→`DONE`
window and halt a partially-filled deep record. Confirm on the bench that `DONE`
accompanies `sawTrig` for native-fast NORM; if not, require `anchored` (DONE) rather
than `sawTrig` for the NORM native-fast return.

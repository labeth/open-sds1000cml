# Combined stream wrapper functional test

Run `python3 validation/2026-09-14-stream-path/test_stream.py` from repository
root. The runner snapshots/hashes all RTL before compilation and execution.
The bench instantiates only sram_stream_path and observes public interfaces:
source ready/enable, SRAM DQ/control pins, descriptors, RAM reads and status.
It does not rely on hierarchical controller signals.

Passed 10003 words through an AW=13 SRAM model, including address wrap,
2560-word host-bank reuse, odd final length, every 16-bit read response and
final committed/recalled/unread accounting. Source cadence is one word per
128 core cycles; ARM uses a separate 100 MHz clock and approximately 10 MB/s
read cadence. The SRAM pipeline is an ideal behavioral model, not hardware
qualification. This run has no injected ARM stall. Full-image timing, physical
AW=19 scan behavior, trigger geometry and GPMC ABI remain separate work.

Combined diagnostic placement (`build_probe.py --seed 2`) fits in EP4CE10:
5263/10320 LEs, 4198 registers, 38/46 M9Ks. Setup -0.611 ns, hold +0.132 ns.
The top includes ingress, scheduler, SRAM transport and host path; ADC front
end, precision processing, trigger engines and GPMC ABI are not included.
No IO delays or integrated CDC constraints are applied in this diagnostic.
It does not qualify the 250 MHz production image. Fitter/STA and source hashes
are preserved in `initial-probe/`. Synthesis warnings include unsupported
async_reg attributes (CDC must rely on actual register/route audits), deliberate
width truncations requiring review, and undefined dual-clock same-address RAM
read/write behavior (ownership must prevent collisions).

`stream_path_cdc.sdc` now scopes host faults under host and includes both
ingress mailboxes and their request/ack/fault synchronizers. It loads against
the combined placed netlist with all checked endpoint counts matching.
Integrated synthesis eliminates ingress marker bits 35:32 because the controller
consumes sample data 31:0 only. Explicit register-name inspection verifies that
both incoming/outgoing payload and destination registers contain exactly bits
0..31; constraints require 32 endpoints in each, not the standalone 36.
Logs and inspection script are in `cdc-endpoints/`. This is endpoint/syntax
evidence only; placement with constraints and all-corner route auditing remain.

Combined constrained build (`--cdc --seed 2`) completes with 38 M9Ks and
120 CDC audit rows passing (40 groups, 3 corners). This includes all host
crossings, both retained 32-bit ingress mailboxes, request/ack synchronizers,
and all three ingress fault-sync stages. Setup remains -0.418 ns; hold +0.107,
recovery +0.290, removal +0.389, min pulse +1.513 ns. Leading failures include
transport stop_requested -> ingress pending[12], transport remaining decrement,
and controller next_count -> state. These core-to-core failures are real
integration paths, not CDC exceptions. Board IO remains unconstrained. Exact
sources/settings and fit/STA/audit evidence: `bounded-cdc-probe/`.

Rejected parallel pending arithmetic experiment: both ingress phase suites
pass and all 120 combined CDC checks pass, but overall setup worsens from
-0.418 to -0.486 ns. Worst path moves to host packer count_legal_q -> first
metadata; ingress pending still has a -0.310 ns path. Restored exact prior
ingress source. Evidence: `parallel-pending-probe/`. Existing full-geometry
simulation evidence continues to match the restored ingress implementation.

Test naming correction: the new wrapper bench is now
`sim/tb_sram_stream_path.v`. The original tracked `sim/tb_stream_path.v`
packetizer/banks regression was restored byte-for-byte from HEAD and passes
its odd/even/empty-stop, live stop, sequence and overload-reset cases. The
new bench bytes are unchanged (SHA256
08f13338ecdf14365e7284ee432c502de22a0104f83b2e001ad9698bf17dfd73);
its earlier result names the former filename, and the runner now uses the
new one. Both coverage sets are retained.

Seed 3 with unchanged RTL/constraints passes the combined CDC audit but has
setup -0.433 ns, slightly worse than seed 2 (-0.418). Worst paths are host
packer fault -> held first-ordinal metadata. Resource use remains 38 M9Ks.
Evidence: `seed3-probe/`. Seed 2 remains the better combined placement;
neither is timing-qualified.

Rejected registered metadata-enable experiment: eight host tests and all
120 CDC checks pass, but combined setup worsens to -0.765 ns; leading paths
are controller unread_full -> ingress pending and sampled_free -> controller
state. Exact baseline packer restored. Reports, experimental source and tests
retained in `metadata-enable-probe/`. This does not alter prior qualified
component or full-geometry functional source versions.

Seed 4 baseline also misses setup (-0.685 ns), with leading paths through
controller read-valid/index and ingress mailbox reset-to-payload enables.
All 120 CDC checks pass. Evidence: `seed4-probe/`.

Mailbox payload experiment removes reset and reset-release qualification from
payload storage while retaining the original request/acknowledgement control.
No request is published during reset; outstanding payload remains held until
acknowledgement. Valid held high during reset is now exercised by the bridge
bench. Eight bridge phase/ratio cases, both complete ingress stress suites and
the direct 10003-word wrapper run pass. Seed 2 has setup -0.429 ns and core
TNS -8.434 ns (baseline -0.418 / -10.926), 38 M9Ks and 120 CDC checks passing.
The leading failure moves to controller fault -> unread. This is a structural
simplification, not a timing-qualified image; `mailbox-payload-probe/` preserves
its source, reports and functional evidence. The old standalone ingress
qualification is historical and does not qualify this changed bridge source.

Rejected unread-counter toggle-mask experiment: nine controller functional
cases and all 120 CDC checks pass, but setup worsens to -0.643 ns and core
TNS to -37.769 ns. Controller restored byte-for-byte to its checkpoint source;
mailbox simplification remains. Experimental RTL, source hashes and results
are in `count-mask-probe/`. Active RTL therefore matches the mailbox experiment
above, whose direct wrapper and ingress tests passed.

Registered continuous-mode transport write readiness was behaviorally identical
to the frozen transport over 100000 randomized cycles in each of finite and
continuous modes; nine controller cases and all 120 CDC groups passed. Combined
setup was -0.619 ns (`write-open-probe/`). Moving wide accounting clear to the
ORIGIN_CALC setup state passed nine cases and CDC, but setup remained -0.529 ns
(`setup-clear-probe/`). Both RTL changes were reverted to the mailbox checkpoint.
The reusable `test_transport_equivalence.py` compares against a hash-checked
frozen reference. These randomized checks are not exhaustive formal proof.

The acquisition engine is now separate from the physical SRAM transport:
`stream_engine.v` contains ingress, scheduling and host buffering, with explicit
transport commands, write offers, read responses and position. `stream_path.v`
connects it to the existing transport and retains the old public interface.
This lets board integration arbitrate one bus owner with finite capture/recall;
that arbitration and sharing the host write path are not implemented yet.

The refactored wrapper passes 10003-word readback with four banks, odd tail,
SRAM wrap and actual halfword host reads. Seed 2 remains exactly -0.429 ns setup,
+0.143 hold, +0.135 recovery, +0.492 removal, +1.513 minimum pulse, 38 M9Ks;
120 CDC audit groups pass across the changed hierarchy. Timing and IO remain
unqualified. Source snapshots, hashes and evidence: `external-engine-probe/`.

The host buffer is now external to stream_engine.v as well. The engine exposes
32-bit word/index/bank outputs, bank completion metadata, and accepts validated
core-domain release pulses and host faults. stream_path.v connects one host_path
and one transport, retaining the same public interface. Finite recall can now
be connected to those same shared endpoints without instantiating duplicate
host RAM. The finite producer and mode arbitration are still to be integrated.
Never switch producers with active transfers or banks reserved, awaiting
publication, or host-owned. Abort requires coordinated ingress/host epoch reset.

Current external-host-probe: 10003 words, four host banks, exact readback, odd
tail and SRAM wrap pass; 120 CDC checks pass. Memory remains 38 M9Ks and setup
-0.429 ns, hold +0.143, recovery +0.135, removal +0.492, minimum pulse +1.513.
This remains IO-unconstrained diagnostic evidence, not a deployable image.
An initial missing positional clock connection was caught by Icarus and fixed;
the pre-fix Quartus run was explicitly terminated. build_probe.py now performs
Icarus interface elaboration before Quartus, using its frozen source copies.

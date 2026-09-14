# Host pair path functional checkpoint

`test_path.py` snapshots and hashes the source before testing the actual
250 MHz packer, register-backed asynchronous FIFO, 125 MHz sink and host RAM,
with a 100 MHz read port and the actual ownership handshake. `passed.txt` records that exact source set.

Tests run four RAM-clock phases and three captures at each phase:
two 2560-word banks, a full bank plus one word, and a full bank plus 2559 words.
Words arrive on every 250 MHz edge. The bank-0 descriptor overlaps bank-1
input; every valid 16-bit RAM halfword is read back and compared, as is the
zero padding of odd tails. Both first ordinals and valid word counts are checked.

The packer buffers a half pair and pending descriptor separately for each bank.
Pair data takes priority over descriptors; odd tails emit zero-padded DATA
before META. Metadata is transported in the FIFO itself, preserving order.
Malformed word indices and descriptor counts latch a source fault. Faults
invalidate the epoch; consumers must not treat partially completed output as
an intact capture.

This is a functional prototype, not a deployable FPGA image. The testbench
instantiates `sram_host_path`, including ARM descriptor and release CDC logic.
It reads only published banks, releases them after readback, and runs three
captures without intervening reset to exercise bank reuse. Assertions reject
reads of unowned banks and writes to owned banks.
The wrapper synchronizes source faults to RAM, RAM faults back to the core,
and the combined fault to ARM. Nominal tests check internal fault outputs;
the additional fault test checks propagation within 16 host clocks and
sticky quiescence, rather than an exact cycle-latency contract.
Production controller/ownership integration, packer fault-injection coverage, reset under traffic,
and combined timing/resource/CDC qualification remain to be done. Existing
isolated FIFO and RAM timing results do not qualify the new combined path.

Controller integration test: run
`python3 validation/2026-09-14-timeslice-controller/test_controller.py --real-host`.
This adds a 20,001-word capture using actual packer/FIFO/sink/RAM writes,
2560-word banks, a reduced 16K-word SRAM model, and an ARM drain stall.
The controller scoreboard mirrors accepted 64-bit RAM writes and waits for
synchronized RAM-side publication, instead of accepting controller bank_done
as proof of RAM completion. It does not exercise physical ARM read-port timing
or production ownership CDC, nor does reduced SRAM geometry qualify physical
512K-word scan latency.

The current `passed.txt` supersedes the earlier host-path result: all four
phases pass RAM readback, ARM-visible metadata, release accounting and bank
reuse. `controller-passed.txt` remains evidence for the separate older
controller testbench with its modeled publication synchronization; it does
not prove controller integration with the new ownership module.

`host_path.v` is the controller-facing wrapper. It rejects packets for a
published bank, including the single-cycle publication-to-ownership gap,
and uses locally synchronized reset release. Callers must reserve banks
before input, wait for core release before reuse, read only ready banks, and
wait for the last read response before ARM release. The wrapper does not
enforce read ownership. It has not been placed/routed or CDC-qualified.

`tb_host_faults.v` now runs at all four phases in the snapshot runner. It
rejects an odd-index first word, verifies core/host fault propagation, attempts
to overwrite a published bank and reads its original contents afterward, then
resets a pending half-pair and verifies fresh capture. These cases do not cover
every malformed descriptor or physical metastability; combined timing/CDC
qualification and controller integration remain pending.

`build_probe.py` snapshots the six production modules and runs combined map,
fit and timing analysis using 4/8/10 ns clocks. This first diagnostic leaves
IO unconstrained and does not yet apply the FIFO/ownership CDC exceptions.
Its result is explicitly not a timing qualification: examine intra-clock
paths separately, then add bounded payload/token constraints and audit every
crossing. The build produces no programming image and does not access hardware.

Initial combined placement completed: 20 M9Ks, setup -2.538 ns, hold +0.166 ns.
The worst setup path is core-to-core, pack count[1][9] to count[0][2/3/8/9],
with 6.457 ns data delay against a 4 ns clock. This is a real packer logic
bottleneck, not an asynchronous exception artifact. Packer counter validation
and update arbitration need restructuring before qualification. Diagnostic
result and STA report are preserved in `initial-probe/` with source hashes.

Index-update restructuring: expected indices now update independently per
bank from offered word_index+1, clearing on completion. Validation still
latches epoch faults. All eight normal/fault phase runs pass. Combined setup
improves from -2.538 to -1.485 ns with 20 M9Ks unchanged. The remaining worst
path is pack count[0][4] to packet[60/59/27/26], 5.421 ns data delay. This
identifies validation-to-packet arbitration as the next path to pipeline;
the design still fails 250 MHz. Evidence: `index-update-probe/`.

Validation pipeline: offered words and completion metadata now advance through
one registered stage alongside ordinal/count checks. Eight normal/fault tests
pass. Combined setup improves to -0.994 ns, still failing, with 20 M9Ks.
The worst path now runs from a pack packet output term to FIFO slots (4.882 ns);
a pending-to-fault path is also negative (-0.922 ns). Evidence is preserved in
`validation-pipeline-probe/`. No timing qualification is claimed.

Rejected experiment: preserving the packer packet register passes functional
simulation but worsens combined setup to -1.055 ns (20 M9Ks). The worst path
moves to pending[1] -> half[1][29/13], 4.963 ns. The attribute was reverted;
current RTL remains the validation-pipeline version. Preserved diagnostic
reports in `preserve-packet-probe/` are evidence for the rejected source hash,
not the current RTL. Next restructuring must shorten pending/validation
controls of half-pair storage rather than relying on register preservation.

Independent half-pair payload capture passes all eight test runs. Placement
retains 20 M9Ks; worst core setup improves to -0.688 ns at slow 85C. Overall
-0.929 ns is now a core-to-host fault synchronizer first-stage path (2 ns
nominal phase relation), which still needs the intended bounded CDC constraint.
Core paths remain negative and cannot be excused as CDC. Evidence is in
`payload-capture-probe/`. Current design is still not timing-qualified.

FIFO input stage: an aligned packet/push register in the wrapper separates
packet arbitration from FIFO write enables. Eight test runs pass, 20 M9Ks
remain, and core setup improves from -0.688 to -0.585 ns. Overall worst setup
is now -0.585 ns, with hold +0.108 ns. Worst core paths originate at pack valid_q
and terminate in packet output terms (4.511 ns maximum shown). Further packer
arbitration restructuring remains necessary. Evidence: `fifo-input-stage-probe/`.

Rejected free-running packet experiment: all eight tests pass but core setup
worsens to -0.766 ns, worst path FIFO wbin[1] -> slots (4.653 ns). Restored the
exact previously tested packer hash; active design remains FIFO-input-stage
version (-0.585 ns). Diagnostic evidence retained in `free-packet-probe/`.

Packet candidate pipeline: registers paired data, padded tail and descriptor
candidates alongside selection, then selects the output on the following
cycle with an aligned offer. Eight tests pass. Combined placement uses 3019
LEs, 2386 registers and 20 M9Ks; setup -0.544 ns, hold +0.152 ns. Worst core
path is FIFO wbin[0] -> slot write logic (4.463 ns), identifying write-address
decode/fanout as the next target. Still not timing-qualified. Evidence:
`candidate-pipeline-probe/`.

One-hot FIFO writes: registered rotating slot selection replaces binary
write-address decode; binary/Gray occupancy and CDC remain unchanged. All
8 standalone FIFO and 8 combined-path tests pass. Combined setup improves
from -0.544 to -0.301 ns, hold +0.147 ns, still 20 M9Ks. Worst core path is
fifo_push -> slot write enable (4.212 ns). Current FIFO differs from its
previous isolated timing-qualified source: that historical manifest does not
qualify this revision. Combined CDC endpoint constraints/audit must be updated
for explicit slot registers. Evidence: `onehot-fifo-probe/`.

Capacity-qualified writable-slot registers: 16 tests pass and core setup
improves to -0.219 ns (slow 85C), with 20 M9Ks. Overall -0.326 ns is a fault
CDC first-stage path under the diagnostic clock relationship. Remaining core
failures include reset-release fanout to half storage and FIFO slots, plus
pending-to-metadata capture. No reset-release paths have been falsely cut.
Evidence: `writable-slot-probe/`. Still not timing qualified.

Payload reset removal: half-pair payload/address no longer reset; half_valid,
offer and all ownership controls still do. Eight path tests pass including
unfinished-pair reset. Core setup improves to -0.177 ns, but RAM placement
worsens to -0.410 ns on FIFO dest_data -> M9K write/address paths. Overall
this is not timing closure and is not an overall slack improvement. Retained
as a structural reduction of unnecessary reset fanout; RAM-side validation
and write-enable logic now also need pipelining. Evidence: `payload-reset-probe/`.

Registered sink write: valid/address/data are registered after packet checks,
then consumed by host RAM on the next edge. A following descriptor publishes
after that final physical write edge; ownership captures publication later.
Sink standalone and eight host-path tests pass. Slow 85C setup is RAM +0.351 ns,
core -0.115 ns; overall -0.771 ns belongs to a fault synchronizer first-stage
crossing with the diagnostic 2 ns phase relationship. That crossing still
needs bounded CDC constraints/audit. No broad false paths or timing closure
claim. Evidence: `sink-write-stage-probe/`; M9K count remains 20.

Rejected independent metadata-capture experiment: eight tests pass but core
setup worsens to -0.276 ns; restored exact sink-write-stage packer hash.
`metadata-capture-probe/` retains evidence. Draft `host_path_cdc.sdc` matches
all expected endpoint counts on that placed netlist and loads without warnings.
This verifies syntax and coverage counts only, not all route delays/corners;
full CDC audit and rebuild with constraints remain required.

Bounded CDC build (`build_probe.py --cdc`): constraints are included during
placement, and `audit_cdc.tcl` checks 25 endpoint groups at each of three
corners (75 rows). All payload, metadata, token and sticky-fault crossing
bounds/stage paths pass. Slow 85C setup: core -0.219 ns, RAM +0.582 ns,
host +0.791 ns. Overall hold +0.150 ns; 20 M9Ks. This establishes the checked
component CDC routes, not full design closure: IO is unconstrained and core
setup still fails. Evidence: `bounded-cdc-probe/`. Prior unbounded placement
slacks are diagnostic comparisons, not equivalent qualification runs.

Rejected parallel next-state experiment: independently computed write/hold
writable-slot masks pass 16 functional tests and all CDC audit groups, but
setup worsens to -0.307 ns (wbin[3] -> writable_slot[6]). Restored the exact
bounded-CDC FIFO hash with -0.219 ns setup. Evidence: `parallel-next-probe/`.

Seed 2 component timing checkpoint: `build_probe.py --cdc --seed 2` passes
setup +0.009 ns, hold +0.150 ns, recovery +0.678 ns, removal +0.404 ns,
minimum pulse +1.513 ns, all 75 CDC groups; 20 M9Ks. RTL is unchanged from
the bounded seed-1 build. Exact sources, constraints, project settings,
audit and fit/STA reports are preserved in `seed2-passed/`, verified against
source hashes. Margin is narrow. IO remains unconstrained; this qualifies
only checked internal component paths and crossings, not a complete FPGA
image, physical ADC/SRAM timing, or controller integration.

Real ownership/controller integration: `test_controller.py --owned-host-only`
now instantiates sram_host_path, drives an independent 100 MHz ARM clock,
reads both 16-bit halves through the actual host RAM port, checks every word,
and releases matching descriptor tokens. Controller bank_release comes only
from the real ownership module. Passed 10003 words, four banks, max ingress
pending 65, max unread 5152 on AW=13 (8192-word SRAM), proving SRAM wrap and
host-bank reuse. `owned-controller-passed.txt` records exact source hashes.
The stall setting is not exercised because the default stall starts at bank
six; a separately targeted stall test is still required. Physical AW=19
geometry and whole-design timing remain unqualified. Earlier controller logs
using mirrored RAM writes do not establish this stronger read-port coverage.

Targeted ARM stall: `test_controller.py --owned-host-stall` uses REAL_HOST,
AW=13, 10003 words and a 250000-core-cycle (1 ms) pause before reading the
first published bank. REQUIRE_STALL asserts exactly one pause was exercised.
Every actual RAM read is checked and matching ownership releases are used.
This covers reduced-geometry stall recovery; it is not a maximum stall or
physical full-depth throughput qualification. Evidence and source hashes:
`owned-controller-stall-passed.txt`.

Physical SRAM geometry with real host module: `--owned-host-full` passes
AW=19, 65539 words / 26 banks, exactly one 10 ms ARM pause, four busy-bank
read skips and 16 scan/drain overlaps. Peak ingress occupancy 4101/4608,
peak unread SRAM 24437/524288. Every actual host RAM read and final accounting
passed. Evidence/source hashes: `owned-controller-physical-passed.txt`.
This verifies modeled physical-depth scheduling, not electrical hardware or
full-image STA, and is not a test filling the entire SRAM with retained data.

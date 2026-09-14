# Shared streaming / frozen-recall backend

`capture_engine.v` selects `stream_engine.v` or `finite_recall.v` over one
`host_path` and an external SRAM transport. `capture_path.v` connects that
engine to one physical transport for integration tests and placed diagnostics.
Finite capture writing, ADC/precision processing, trigger geometry and the
GPMC/kernel ABI still belong to the unfinished board integration.

All producer controls are in the core clock domain. `start && start_ready`
accepts an operation and latches finite_mode. A one-clock launch stage retains
settings from that edge and reserves the operation immediately. A held or
new start while busy produces start_rejected and does not replace the mode.
Finite range validation snapshots its operands, then compares the range on
a separate clock. Changing settings after acceptance cannot alter the request.

Host reads/releases use the existing host clock interface. Validated release
pulses return only to the selected producer. Mode selection cannot change while
active or while any bank is reserved, pending publication or host-owned. Done
is suppressed during launch, on fault and until every bank is released.
Faults invalidate the epoch and require coordinated reset; the board must also
quiesce outstanding transport commands before granting another bus client.

committed and unread expose the streaming producer's counters. read_ordinal is
selected by mode and is relative to the current transfer. Frozen record capture
metadata remains separate from recall progress. Source offers belong to the
streaming operation; source_finished follows the final offered word, including
pipeline drain after source_enable falls.

Run from repository root:

```
python3 validation/2026-09-14-stream-path/test_capture_engine.py
python3 validation/2026-09-14-stream-path/test_capture_start.py
python3 validation/2026-09-14-stream-path/build_probe.py --capture --cdc --seed 2
```

The mode test uses both the engine with an external transport and the complete
wrapper. Each executes five operations without reset: a 5121-word finite read,
10003 streaming words with SRAM wrap, recall of that stream's last 8192 words,
a second 257-word stream, and recall of its last word. Every host halfword is
checked, including singleton/odd tails and ownership reuse. Each run rejects
ten busy starts and wrong-token releases, including a held final bank after
stream acquisition stops. The start test changes every setting immediately
after acceptance, holds start for a second clock, and checks the old address
and length in the emitted read commands; it also checks reset and stream launch.

These are ideal-memory functional and IO-unconstrained placement diagnostics.
They do not establish on-device throughput, ADC ordering, trigger performance,
or a deployable default image. The shared backend still needs timing closure
and integration with the real ADC/precision/GPMC top.

Current evidence: tests.txt passes both forms of the five-operation sequence;
start-tests.txt passes accepted-settings retention and pending-start rejection.
The current finite reader also passes the full 524288-word / 205-bank test in
../finite-recall/full-passed.txt, including its fault suite.

Placed diagnostic: 6109/10320 logic elements, 4813 registers, 38/46 M9Ks.
All 120 CDC rows pass. Setup -1.096 ns, hold +0.145, recovery +0.559, removal
+0.299, minimum pulse +1.513. The worst setup path is selected_finite into the
stream ingress payload enable. This is a real setup failure, not an IO or CDC
exception. The target still excludes the ADC/precision frontend and GPMC ABI.

Historical shared-capture-initial/ reports -1.557 ns before launch pipelining;
shared-start-pipeline/ reports -1.576 ns before staged finite range validation.
Those reports explain the changes; only this directory describes current RTL.

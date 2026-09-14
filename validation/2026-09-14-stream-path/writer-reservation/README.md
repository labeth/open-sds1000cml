# Registered finite-writer reservation

The previous arbitration path decoded finite-writer state.IDLE and combined
it with launch_pending to form active/writer_request. Candidate uses one
preserved reserved register: accepted start sets it immediately; completion
of DRAIN clears it. Reset clears it. Invalid state clears it unless the existing
source-fault priority sends an active writer to DRAIN, which retains ownership.
The sequencer and capture geometry are unchanged.

A simulation-only assertion compares the reservation every cycle with the
original launch_pending || state!=IDLE definition. Focused fault tests pass
pending launch, repeated starts, held geometry, reset, coincident halt,
startup/trigger/drain/frozen source faults and lost transport readiness.
Full finite capture, composed acquisition and exact fit are pending. This is
an optimization candidate, not a qualified image.

Composed acquisition completed successfully with the reservation equivalence
assertion enabled: raw and precision capture, 6001-word streaming, recall
ownership, stale-record rejection and frontend faults. Fit remains pending.

## Completed candidate

Full default capture suite and fault suite pass, with the every-cycle reservation
equivalence assertion. Exact-source fit: 9182 LEs, 7724 registers, 44 M9Ks,
644/645 LABs. Setup -2.816 ns, hold +0.103 ns, recovery -5.065 ns, removal
+0.372 ns, minimum pulse +1.513 ns. Existing 135-row CDC audit passes, exit 0.

Compared with 728ddab: saves 36 LEs but uses two more LABs; setup worsens
0.043 ns while recovery improves 0.287 ns. Retained as a local ownership
simplification and logic reduction, not as overall timing/placement improvement.
Worst setup now starts at host-packer fault and ends at finite-writer frozen.
Setup/recovery, wider frontend CDC/reset coverage, physical top/GPMC integration
and on-device continuous transfer qualification remain incomplete.

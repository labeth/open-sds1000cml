# Writer validity separated from public backend faults

Original command interface and settings paths remain. writer_record_valid is
finite_record && cfr && !cf; public record_frozen additionally masks bf, exactly
as before. Backend recall sees writer_record_valid and already processes
host_core_fault and its own fault states locally. This removes combined
backend fault status feeding back into recall's frozen input. No new latency,
command registers or sample-memory changes.

Directed active-recall host-fault injection passes immediate public status
invalidation, local fault and transport drain. Full composed integration and
exact-source timing build are pending. This isolates the useful structural
change found during the rejected command-dispatch experiment.

Composed acquisition passes all operations and final ownership, stale-record
and frontend-fault assertions. Build remains pending.

Rejected fit: 9201 LEs, 7724 registers, 44 M9Ks, 643/645 LABs. Setup
-3.395 ns, recovery -5.822 ns; CDC audit fails (exit 3). Adds 19 LEs and
worsens setup 0.579 ns versus retained RTL, despite one fewer LAB. Reverted
acquisition_path to HEAD; host-fault regression retained.

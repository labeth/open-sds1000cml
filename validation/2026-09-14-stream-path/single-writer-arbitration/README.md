# One arbitration check for integrated finite starts

Only acquisition_path's writer connection changes: capture_allowed is tied
high because capture_start already requires acquisition start_ready, which
checks cached backend readiness and immediate faults/lock/ownership. The
writer keeps its own local active, fault, source-fault and geometry checks.
Standalone writer behavior and backend start validation are unchanged.

A simulation assertion at capture_start requires the previous full backend
permission expression: bready, no backend activity, no bank owner, no writer
grant and no backend fault. This prevents removing the same-epoch contract
when future control paths change. The immediate rejection test passes lock
loss, source/backend faults, reset and owned-bank starts. Full composed test
and exact timing build are pending; no timing benefit is claimed yet.

This differs from the earlier rejected validated-start experiment: it changes
only the redundant external permission connection, leaving local writer
validation and the entire backend acceptance logic intact.

Composed integration passes all three operations and final ownership, stale
record and frontend-fault assertions with the new permission assertion active.
The build is still pending.

## Rejected result

9201 LEs, 7724 registers, 44 M9Ks, 644/645 LABs. Setup -3.743 ns,
hold +0.113 ns, recovery -5.319 ns, removal +0.466 ns, minimum pulse
+1.513 ns. Existing 135-row CDC audit passes (exit 0). Functional tests
pass, but this adds 19 LEs and worsens setup 0.927 ns versus f6095da.
No LAB saving. Candidate was reverted to f6095da after preserving exact
sources and reports here. Worst path is finite bank_busy through acceptance
into finite-recall state.DRAIN/PREP. No deployment occurred.

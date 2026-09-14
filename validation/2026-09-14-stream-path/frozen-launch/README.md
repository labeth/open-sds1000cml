# Frozen flag cleared by registered launch

Candidate masks public frozen with !launch_pending, preserving immediate
invalidation after accepted start. The private frozen_l flag clears on
launch_pending instead of accepted_start, removing the acceptance fan-in
from its clear path. Fault, drain completion, reset and invalid-state handling
retain their prior priority. Reservation behavior is unchanged.

Focused pending-launch/reset/halt and source-fault tests pass. Full capture,
composed integration and exact fit are pending. This is unqualified RTL.

Directed frozen-rearm regression passes: rejected permission preserves the
frozen record, accepted start invalidates on its first edge, and status remains
invalid through a delayed writer grant. Composed integration also passes all
operations and final ownership/fault assertions. Full capture/build pending.

## Rejected fit

9217 LEs, 7724 registers, 44 M9Ks, 642/645 LABs. Setup -3.257 ns,
hold +0.110 ns, recovery -7.000 ns, removal +0.375 ns, minimum pulse
+1.513 ns. CDC audit fails, build exits 3. Compared with f6095da, adds
35 LEs, frees two LABs but worsens setup 0.441 ns and recovery 1.935 ns.
Worst setup is stream-controller fault into finite-writer halt_pending.
RTL reverted to f6095da; the directed frozen-rearm regression is retained.

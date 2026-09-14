# Finite writer launch pipeline — not timing qualified

The finite writer reserves ownership immediately on an accepted start, then
advances its state machine one clock later. This separates start acceptance
from the state transition. Private geometry remains held during the pending
clock. A halt coincident with acceptance is retained; reset cancels the launch.

`finite.txt` passes the eight AW13 capture/recall cases using this RTL.
`integration.txt` passes raw, precision, streaming, recall and ownership cases.
`faults.txt` additionally checks immediate reservation, repeated-start rejection,
held geometry, reset during pending launch and coincident halt. The earlier
finite run predates these added fault-bench cases; source hashes distinguish it.

The balanced seed-2 build uses 9333 LEs, 7698 registers, 44 M9Ks and 642/645
LABs. Setup is -3.284 ns and recovery -6.091 ns. Two of 135 CDC audit rows
fail (encode source and destination views of the same crossing at slow 85 C).
The design fits but is NOT qualified for 250 MHz or deployment.

`encode-path-diagnostic.rpt` analyzes the same placed design with unchanged
constraints. All ten encode paths are present; four fail setup. The worst has
1.493 ns data delay and -2.765 ns clock skew, yielding -0.307 ns slack under
the 4 ns max-delay exception. A logic cell exists before the meta register.
The exploratory direct register-to-register net-delay report found no nets;
it is not valid replacement coverage. No CDC constraints were relaxed.

Build snapshots were checked against all 31 SHA-256 values in result.json
before archiving. These are virtual-IO core results, not the default board top.
No scope operation, FPGA load or permanent device write was performed.

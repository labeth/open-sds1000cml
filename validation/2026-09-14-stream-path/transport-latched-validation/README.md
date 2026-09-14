# Transport validation from accepted command registers

Candidate moves legal-command decode from live producer inputs to the existing
remaining and reading registers, which are latched at command acceptance.
VALIDATE is already the following cycle, so no protocol latency is added.
This removes live backend selection from the comparator input cone. No range
check is bypassed: zero-length reads remain invalid; zero writes remain
unlimited; the full 2^AW count remains legal and larger counts remain invalid
except for CONTINUOUS_ONLY writes as in the original contract.

The frozen transport oracle is f24d6fd8d9878b0e4eb722b293cab5a06c380ef42c58e6becccc0156eec1bfae.
Original random test passes 200000 cycles in both modes at AW8. Expanded test
passes 400000 cycles across AW8/AW19 and both modes, with zero, one, full
capacity, capacity-1 and capacity+1 mixed into random input counts. Outputs,
SRAM pins, data, positions and completion are compared cycle-for-cycle,
including stop, reset and lock transitions. See source-hashed test outputs.

Integrated acquisition passed raw 3017-word capture, 48-word precision capture,
6001-word streaming, recall ownership, stale-record rejection and frontend
fault handling. The exact candidate build is pending. No timing
improvement or device qualification is claimed until those results exist.

## Completed build

9212 LEs, 7723 registers, 44 M9Ks, 643/645 LABs. Setup -3.452 ns,
hold +0.146 ns, recovery -5.689 ns, removal +0.499 ns and minimum pulse
+1.513 ns. Existing 135-row CDC audit passes (build exits 0), but it does not
cover all frontend reset/control crossings and does not establish physical
IO qualification. Setup and recovery still fail.

Compared with 3ea3cb7, this saves 10 LEs and improves worst setup by 1.268 ns.
Worst setup now runs from backend launch_finite to finite-recall state.DRAIN.
The latched validation change is retained. Default top/GPMC integration and
physical SRAM/ADC/streaming qualification remain incomplete.

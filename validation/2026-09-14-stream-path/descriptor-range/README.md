# Split descriptor range validation

The input stage rejects upper descriptor-count bits. The packing stage checks
the retained 12 bits for nonzero, <=2560 and equality with the bank count.
This removes the full range-comparison path from the live mode-selected input
without adding packet latency or changing descriptor acceptance cycles.

79463 cycle comparisons against the original packer pass, including malformed
offers, full/odd counts and reset. Integrated capture/recall/streaming passes.
Exact source hashes are in the logs. Fit/timing build is pending in
/tmp/acq-descriptor-range-build.txt (exec session 6619). No image deployed.

The first split build failed at 646 LABs; source and reports are in split-build.
Current RTL removes the retained-count upper comparison using the invariant
that count <=2560 while fault is clear. Counts reset to zero or become index+1;
invalid indices set fault on the same edge that commits their count. Descriptor
equality, nonzero and discarded-upper-bit rejection are retained. The test now
asserts the count invariant every cycle as well as packet/fault equivalence.
Refreshed equivalence and integration tests pass. Current build is pending in
/tmp/acq-descriptor-invariant-build.txt (exec session 86302). No deployment.

Final descriptor build fits: 9493 LEs, 7706 registers, 44 M9Ks, 645 LABs.
135 CDC rows pass. Setup -3.766 ns (worse than -3.094 ns), recovery -4.916 ns.
Worst path is readiness from finite recall state into top encode_l settings.
Descriptor simplification saves 12 LEs; this is not an overall timing win.

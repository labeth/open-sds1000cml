# Reuse sampled recall length — simulation passes, build pending

Expose the backend's existing record_words_q through record_words_sampled and
use it for top-level recall geometry validation. This separates record output
selection from the acceptance comparator without adding another register bank.
An assertion checks sampled length equals live frozen length whenever ready.
The readiness-cache candidate remains included.

Integration passes raw 3017, precision 48, stream 6001 words, recall ownership,
stale-record rejection and frontend faults with the new assertion active.
Immediate readiness invalidation and finite-writer fault tests pass.
Four wildcard-port testbenches were updated with explicit unused observation
outputs; all four compile (board capture, capture start, finite capture, engine).

Balanced seed-2 build completed: 9359 LEs, 7721 registers, 44 M9Ks,
643/645 LABs. Setup -4.682 ns; recovery -4.612 ns; CDC audit fails.
All 31 source hashes verified before archive. Worst path is host RAM/packer
fault through acceptance into backend selected_finite. This is an unqualified
intermediate result, not an overall timing improvement. No device deployment.

# Retained internal mode — rejected timing experiment

Internal transport/host arbitration used selected_finite_l while public mode
and source faults retained the immediate bypass. Public mode comparison passes
10000 cycles. Integration and both backend transport configurations pass,
including five operations without reset and busy/wrong-token rejection.

Balanced seed-2 build: 9359 LEs, 7726 registers, 44 M9Ks, 644/645 LABs.
Setup -4.412 ns; recovery -6.994 ns; CDC audit fails. All 31 source hashes
verified. Worst path moved to writer frozen/start control; overall timing
regressed from the committed mailbox-fixed baseline (-3.531 ns, 643 LABs).

Both retained internal mode and the preceding recall count-preload experiment
were reverted to the committed baseline. Their evidence remains preserved.
The short-rearm mailbox correctness fix remains committed. No device writes.

# Backend mode via launch stage — integration passes, build running

Use registered launch_finite/launch_stream to select the mode during pending
launch, then retain it in selected_finite_l. This preserves the immediate mode
visible after acceptance without a second mode register driven by acceptance.
A 10000-cycle unit comparison against original accepted-command history passes,
including rejected requests, mode changes and reset. That test drives readiness
explicitly and does not replace integrated datapath/ownership verification.

Integration exited 0: raw 3017, precision 48, stream 6001 words, recall
ownership, stale-record rejection and frontend faults pass (integration.txt).
Balanced seed-2 build completed: 9338 LEs, 7721 registers, 44 M9Ks,
640/645 LABs. Setup -3.281 ns; hold +0.037 ns; recovery -6.769 ns;
removal +0.491 ns. CDC audit fails. All 31 build source hashes verified.
Worst reported setup is acquisition enable/control into frontend overflow_s[0]
in the pack-clock domain. Backend mode selection is no longer the worst path.
Readiness-cache and sampled recall-length changes remain included.

This checkpoint improves placement versus the committed launch baseline
(642 LABs) and preceding recall-length build (643 LABs), but it is NOT timing
qualified or a default board image. No device operation was performed.

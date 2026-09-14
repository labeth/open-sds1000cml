# Explicit operation flags — rejected timing experiment

Retained capture/stream flags preserve the previous mode semantics in all 24
Boolean mode/start/reset combinations. Integrated raw 3017, precision 48 and
streaming 6001 word tests, recall ownership, stale-record rejection and frontend
fault tests pass; source-versioned output is in integration.txt.

Balanced seed-2 build: 9372 LEs, 44 M9Ks, 644/645 LABs. Setup -4.916 ns,
recovery -5.571 ns. CDC audit fails. The worst path is backend launch_finite
through acceptance to selected_capture; request_error and writer frozen also
have comparable paths. This regresses the launch baseline (-3.284 ns, 642 LABs).
All 31 build-source SHA-256 values were checked before archiving.

This experiment and the preceding idle-halt RTL experiment were reverted to
the committed launch baseline after preserving their evidence. The acceptance
pipeline needs structural redesign; mode encoding alone did not improve it.
No image was deployed and no scope storage was written.

# Event broadcast — rejected timing experiment

Broadcast transport done/read-valid and validated bank releases to both clients,
retaining output arbitration. Full acquisition integration passes. Standalone
backend tests pass both transport configurations: five consecutive operations,
busy-start rejection and wrong-token rejection. Source-versioned logs retained.

Balanced seed-2 build: 9349 LEs, 7726 registers, 44 M9Ks, 645/645 LABs.
Setup -3.903 ns; recovery -5.947 ns; CDC audit fails. All 31 source hashes
verified before archiving. The worst path is host packer fault through start
acceptance into launch_finite/launch_stream. This regresses the mailbox-fixed
build (-3.531 ns, 643 LABs), so only the event-broadcast RTL was reverted.
The short-rearm mailbox fix and local pack controls remain in the worktree.
No FPGA image or device qualification is claimed; no device writes performed.

# Local pack controls and short-epoch mailbox validity

Integrated SYNC_ENCODE frontend uses a three-clock local enable release and
two-stage consume synchronizer. Disable clears local control state immediately;
legacy defaults retain their previous behavior. Startup and sustained modeled
ADC chronology at 1 GB/s pass.

A new --short-epoch option in test_encode_startup.py disables for four ns
between pack-clock edges. The first local-control version failed chronology at
word 2: stale mailbox validity allowed old words before fresh data. Immediate
clear of wide_valid on disable fixes the modeled regression without resetting
the 64-bit payload. Both initial and restarted 40000 ns epochs pass. This uses
ideal FIFO/phase models, not physical timing or metastability qualification.

The earlier local-control-only build is archived in before-mailbox-fix:
9347 LEs, 7726 registers, 44 M9Ks, 642/645 LABs; setup -3.341 ns,
recovery -5.437 ns; CDC audit fails. All 31 source hashes verified, including
interleave SHA 96da12f8ddaaa4468f83776cbd4d6b68780bd9436bd84c2d609d0e61c7203698.
Worst reported setup is backend launch_finite through finite recall control
into chunk/PREP. This source still fails the short-epoch regression.

The current mailbox-fixed RTL needs its own build; prior timing is not its
qualification. No image deployed; full default top/GPMC/hardware work remains.

The expanded --short-epoch regression passes all eight 1 ns phase offsets
within the 8 ns pack period, each with a fresh 40000 ns capture. phase-sweep.txt
records current RTL and generated-bench hashes. The old 30 s timeout expired on
the expanded test; the runner now allows 180 s for this longer option and flushes
phase progress. The completed rerun passes; no assertion failure was hidden.

Mailbox-fixed build session 79503 is running with log
/tmp/acq-pack-mailbox-build.txt. It includes interleave SHA
476b55abaf2d18c524eecaa34bb32a5962689caf1804e1ddaa1e55fa0d5ebe94.
Re-poll this handle before restarting; its timing result is pending.

Mailbox-fixed build completed and is archived in mailbox-fixed-build:
9340 LEs, 7726 registers, 44 M9Ks, 643/645 LABs; setup -3.531 ns,
recovery -5.398 ns; CDC audit fails. All 31 build-source hashes verified.
The detailed recall-launch-path.rpt shows launch_finite through selected_finite
and a transport-event gate into finite command_count enable. This is not a
qualified image; pack-control/mailbox fix remains functionally verified only.

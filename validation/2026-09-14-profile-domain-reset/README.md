# Physical profile: domain reset release

Exact diagnostic fit before the follow-up ownership output reset correction.
Sources and constraints are bound by `manifest.json`; all 40 manifest entries
were checked against this archive. No assembler, conversion or device load.

The board reset now enters two-stage asynchronous-assert/synchronous-release
chains in the command bridge, command port and profile core. Functional state
uses its local reset. The SDC checks all 16 added reset stages and exempts only
the asynchronous board-reset entry; local reset release remains timed.

Seed 2: 8393 LE, 7000 registers, 635/645 LABs, 26/46 M9Ks. Setup -4.639 ns,
hold +0.111 ns, recovery -2.197 ns, removal +0.476 ns. All eight derived clocks
passed the build's period/phase checks. This still fails timing and is not a
qualified image. External interface timing and the full CDC audit remain open.

The mailbox passed four clock phases with both normal and 1 ns reset pulses,
including reset and command delivery while the core clock was stopped. Command
port tests and the combined GPMC capture/physical-width SRAM recall simulation
passed. The latter mocks the ADC and is not a hardware or full-depth proof.

Remaining paths include command payload geometry into writer halt_pending
(-4.639 ns), core reset through ownership host_ready into the host read cursor
(-2.649 ns), release-token synchronization, ADC encode crossings and precision
state RAM feedback. The ownership path still directly masks host_ready with
the source reset despite its local host reset chain. The next trial removes
that bypass and retains asynchronous assertion through the local chain.

The prior physical fit is retained separately in `../2026-09-14-profile-board`.

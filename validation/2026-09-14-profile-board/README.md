# First physical profile fit

`python3 fpga/acq_sram/build_profile.py` fits the deep profile with the board
wrapper, PLLs, GPMC interface and all 160 real pin assignments. No virtual pins,
assembler, bitstream conversion or device deployment. `--prepare-only` checks
pin completeness/uniqueness and elaboration (vendor primitives unresolved).
The source/constraint/pin manifest and exact fitted source snapshot are retained.

Clock plan follows the existing SRAM board build: M2 reference constrained to
100 MHz, core 250 MHz, SRAM sampling +1 ns at 250 MHz, RAM writes 125 MHz.
Host C2 uses a conservative 100 MHz constraint; previous board scripts used
80 MHz. These constraints do not establish the measured physical input clocks.

Balanced seed 2: 8409 LE, 6988 registers, 627/645 LABs, 26/46 M9Ks.
Setup -6.775 ns, hold +0.134 ns, recovery -6.222 ns, removal +0.487 ns.
Worst setup starts at reset_hold: reset release needs explicit domain handling.
The worst reported pad-to-pad GPMC read path is 20.630 ns against the inherited
30 ns budget. This is not complete bus timing qualification.

Internal host/ADC bundled-data constraints are included. Command/status payloads
and first synchronizer stages receive explicit route bounds. Internal board
reset remains timed. No blanket asynchronous clock groups or ADC/SRAM pad cuts
are introduced. External pad delays, complete CDC endpoint auditing and reset
qualification are incomplete; warnings/unconstrained paths must be resolved.
Do not interpret successful fit/STA process exit as timing success.

Next: correct per-domain reset release, inspect resulting timing paths, add
complete physical interface/CDC qualification, then integrate the ARM driver
and perform only volatile device testing. Hardware qualification flag stays 0.

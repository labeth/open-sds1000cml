# Acquisition idle settings capture

ADC/trigger data settings capture while no producer or transport owner is
active. The accepted edge captures the presented settings and active holds
them afterward. Frozen record bookkeeping remains separate. This removes
start validation and fault fan-in from wide data-setting enables without
changing start acceptance timing. Mode and finite-record control retain
accepted-start qualification.

Integrated raw/precision capture, immediate setting changes, invalid starts,
recall ownership, streaming and frontend fault tests pass. Build is pending
in /tmp/acq-idle-settings-build.txt (exec session 62523). No deployment.

The aggressive-performance build failed at 646 LABs. Its changed sources,
settings and reports are in aggressive-build; unchanged sources match the
preceding descriptor-range/integrated-build snapshot. A same-RTL experiment
uses build_probe.py --balanced, which generates OPTIMIZATION_MODE BALANCED.
The normal build default is unchanged. Current build is running in
/tmp/acq-idle-balanced-build.txt (exec session 38793).

Balanced result FITS: 9328 LEs, 7697 registers, 44 M9Ks, 643/645 LABs.
All 135 CDC checks pass. Setup -3.969 ns, recovery -4.297 ns, hold +0.136 ns,
removal +0.367 ns, minimum pulse +1.513 ns. Worst path is backend fault/control
through launch logic into finite writer state.PRIME_DRAIN. This is an area
tradeoff, not timing closure. Default build optimization is unchanged; reproduce
this fitted core with --acquisition --cdc --balanced --seed 2. No deployment.

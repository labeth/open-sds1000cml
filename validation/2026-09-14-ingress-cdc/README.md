# Acknowledged ingress clock bridges

The new `word_bridge.v` transfers one held payload between clock domains. It
acknowledges only destination consumption, preventing source overwrite during
sink stalls. Both domains share an epoch reset with independently synchronized
release; resetting only one side is unsupported.

`ingress_path.v` connects core (250 MHz) -> ingress RAM (125 MHz) -> core.
Its 36-bit words carry the Q8.8 sample pair plus four marker bits. The original
RAM still uses 18 M9Ks with this width. The core-domain pending count includes
both mailboxes and RAM prefetch slots. Offering a source word without ready
latches fault because ADC samples cannot be retried. RAM overflow/underflow also
latches core fault. The entire epoch is invalid after fault; a lost word may
remain represented in pending, so recovery must reset rather than wait for it
to drain to zero. The future SRAM controller must halt on fault.

Bridge tests cover four clock phase relationships in both directions, payload
and marker ordering, held outputs, and common reset while requests cross. The
125 -> 250 MHz direction delivers approximately 25–31.25 Mword/s depending on
phase, exceeding the conservative scheduling service-rate budget. Simulation
does not model metastability; physical constraints and synchronizers address
that separate requirement.

The path tests hold the SRAM writer unavailable until 4100 words accumulate,
then continue accepting /256 source samples while draining. They also reset
buffered data and inject source overload and RAM capacity overflow. The full
SRAM transport test now instantiates this two-clock path and checks marker
alignment as well as SRAM payload order.

`ingress_cdc.sdc` keeps both payload buses timed within 6 ns. Token synchronizer
first stages have a 4 ns physical bound; no broad clock-group cut is used.
`audit.tcl` independently checks every surviving payload endpoint in both
directions at every available timing corner, and fails on missing/untimed bits.
The build script also rejects negative setup, hold, recovery, removal or minimum
pulse-width slack. Common external reset assertion alone is excepted.

The initial fit's crossing audit passed (maximum payload delay 2.367 ns), but
core setup failed by 0.137 ns in pending-word arithmetic. That evidence and its
path source are archived in `initial/`. The pending counter was then changed to
one direction-selected adder, retaining exact same-cycle accounting.

This is still an isolated probe and scheduling testbench. The normal FPGA image,
non-power-of-two host RAM buffer, SRAM command controller, source-index trigger
bookkeeping, full-image timing, and hardware transitions remain to be integrated
and qualified. No scope deployment is part of this checkpoint.

## Passing combined probe

The revised isolated 250/125 MHz path passes setup +0.111 ns, hold +0.140 ns,
recovery +1.291 ns, removal +0.489 ns, and minimum pulse +1.513 ns. It uses
18 M9Ks. `passed/` contains exact source copies, wrapper, constraints, complete
reports and the per-endpoint CDC audit. The audit also checks that both stages
of every request/acknowledgement synchronizer survive and remain timed.

The full-depth simulation with the real 250/125 MHz bridges passes eight
5120-word read excursions and the 10 ms host delay: 40,960 returned words,
53,944 new writes, maximum total pending ingress 4101/4608. Each read excursion
still takes 524369 core clocks. Every consumed marker matches its source word.
The ideal SRAM model still lacks board-specific warm-up/origin behavior; this
result does not qualify on-device transitions or replace full-image timing.

Reproduce with `test_bridges.py`, `test_paths.py`, the SRAM budget directory's
`test_transport.py --full`, and `build_probe.py`. `verify.py` checks archive
consistency only. The normal scope image remains unchanged.

The final standalone bridge/path patterns mix all 32 payload bits and the four
marker bits; `bridges.log` and `path-tests.log` contain those rerun results.
`verify.py` verifies hashes, fitted source identity, clock periods, every timing
category, RAM usage, CDC endpoint counts and the simulation result records.

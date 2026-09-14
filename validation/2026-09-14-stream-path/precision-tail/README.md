# Scheduled precision tail

The original 100 MHz precision chain uses four configurable dual-channel CIC3
stages after the fixed /16 filter. The first configurable stage stays parallel.
For /256 and faster modes, the remaining stages bypass. For slower modes, the
new `cic_precision_tail` schedules the remaining three stages through one
28-bit add/subtract datapath and one 64x32 synchronous state RAM.

Six channel/stage contexts use six 28-bit words each (three integrators and
three comb delays), in addresses 0..45 with padding between contexts. Four
queued input pairs occupy addresses 48..51. Queue writes take priority over
filter writes; a conflicting filter update pauses for that clock. Explicit
synchronous prefetch protects the first queued word and full simultaneous
push/pop. Reset clears filter addresses 0..47 sequentially and resets queue
ownership; it never clears queued data that arrived during initialization.

The arithmetic reproduces the parallel `cic_stage`: integrators use the old
preceding integrator state; combs use the old third integrator on a decimation
event, modulo 28-bit arithmetic, signed normalization and Q8.8 representation.
Each stage discards its first eight comb outputs. Both channels share counters
and warmup state, as their valid schedules are identical. Output latency changes;
values and order are preserved. The subsequent scope time/trigger calibration
must account for the processing latency when the image is qualified.

The intended non-bypass input rate is at most 1.953125 million dual-channel
words/s at 100 MHz, after /256. A burst of four is supported; sustained overload
sets a sticky fault. The precision wrapper propagates it through its existing
overflow synchronization into the acquisition fault. The bypass accepts one
word per clock. Configuration must remain fixed while enable is asserted.

`reference_precision.v` is a frozen parallel implementation, SHA-256
`dc03fe4fd3d2daf0d0f757f1f0f9c942793f650f8ab709aba881b95860598b33`.
Tests verify that hash, so changes to the new implementation cannot silently
change its oracle. `test_precision_tail.py --full` compares remaining logs 0..12,
random full-range data, rails, signed center, fractional words, input gaps,
four-word bursts, initialization with pending data, overflow and reset.
`test_precision_shared.py` checks the complete first-CIC/FIFO/configurable-stage
composition at /16 through /8192 with ideal FIFO interfaces. Faster CIC module
text is asserted unchanged. It checks values/order, not physical CDC.

The integrated source selects `adc_precision.SHARED_TAIL=1`. The old board top
keeps the default parallel implementation until its migration/qualification.
The first scheduled build used a register queue: 647 LABs required, versus 750
for the fully parallel build and 645 available. `first-build` records it. The
current queue shares unused state RAM addresses to save further logic without
consuming another M9K. Placement/STA/CDC results must be inspected independently
of simulation; no device or default-image qualification is implied.

Current result: all 16 full-tail equivalence epochs, queue corner tests, and
complete precision comparisons through /8192 pass. The packed-queue integrated
build fits with 9500 LEs, 7655 registers and 44 M9Ks. All 645 LABs are occupied
at least partially. Backend CDC audit passes 120 rows, but setup (-7.496 ns)
and recovery (-5.028 ns) fail; added ADC CDC and IO remain unqualified. The worst
setup path is the held core-domain decimation setting into the 100 MHz filter.
See `integrated-build/result.json` and the original reports. No bitstream was
assembled, no scope action was performed, and the default board top is unchanged.

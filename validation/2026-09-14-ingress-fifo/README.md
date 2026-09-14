# SRAM ingress hardware development

New synthesizable components:
- `fpga/acq_sram/ingress_fifo.v`: 4608 x 32 bits in nine explicit 512-word
  banks, non-power-of-two pointer wrap, synchronous registered read responses,
  and sticky overflow/underflow faults. The response latency is four clock
  edges (a pop at edge n is returned just after edge n+3). Full+pop rejects a simultaneous push
  explicitly, avoiding a same-address RAM collision dependency.
- `fpga/acq_sram/ingress_stream.v`: ready/valid adapter with eight response slots that reserves space for
  read responses before requesting them. Output is stable while stalled and
  supports one word per clock after prefetch.

`test_fifo.py` tests the physical-size configuration, non-power-of-two rows,
and a single bank. It checks independent payload order and occupancy, full and
empty collisions, repeated wraps, sustained output, random traffic and abrupt
sink stalls. These are RTL simulations, not physical RAM timing tests.

`build_probe.py` builds an isolated registered-input/output wrapper for the
EP4CE10F17C8, now defaulting to 125 MHz. `--mhz=250` retains the rejected
clock experiment. It runs map, fit and timing analysis only; it neither
assembles nor deploys a scope image. Virtual I/O pins make this an internal
resource/timing probe. Integration with the actual acquisition pipeline still
requires a complete fitting and timing audit, including interfaces.

Historical timing attempts and their source are retained in subdirectories:
- `initial`: 18 M9Ks, setup slack -2.564 ns; occupancy-to-read-enable bottleneck.
- `unregistered-output`: read-enable path removed, but RAM-to-bank-selector
  path gives -3.418 ns.
- `registered-output`: explicit output stage improves setup to -0.248 ns;
  still insufficient for 250 MHz. This probe covers the FIFO without the
  ready/valid adapter.

The command scheduler in `tb_sram_timeslice.v` now uses the actual ingress
modules instead of only modeling queue occupancy. The scheduler itself remains
a testbench; no new default FPGA image or scope deployment is claimed.

## Remaining integration work

The new queue is not yet connected to the normal acquisition top level. That
integration must decouple ADC acceptance from transport.write_ready: the current
source/trigger pipeline gates write offers with SRAM readiness. During a read
excursion, ADC words and their trigger association must continue entering the
queue. Record bookkeeping currently advances on accepted SRAM writes; trigger
position must instead preserve the original sample ordinal while commits are
delayed. A queued trigger marker or equivalent ordinal bookkeeping is required.

The ideal SRAM model does not include the board's qualified sparse-write origin
correction (+3 versus +2 for continuous writes), initial write priming, or the
host's 16-word fresh-read warm-up discard. Switching between sparse writes,
catch-up bursts and reads needs independent device verification. Do not apply
the old constant origin blindly to the new scheduling mode.

The 5120-word host read buffer, non-power-of-two CPU pointer wrap, read request
handshake, unread-overwrite guard, finite trigger stop/drain behavior and ARM
stream integration remain to be implemented. Full acquisition fitting must
verify the estimated combined RAM budget; isolated 18-M9K ingress fitting alone
does not prove the whole image fits.

## RAM clock restriction and revised direction

`pulse-width.rpt` identifies a 4.201 ns minimum-period requirement on the M9K
address/data/write-enable registers. At 250 MHz the actual period is 4.000 ns,
violating it by 0.201 ns. The final 250 MHz probe reports setup-limited Fmax
240.85 MHz but **restricted Fmax 238.04 MHz** due to this RAM requirement.
No amount of placement tuning establishes a valid 250 MHz configuration here.
The build script now fails on negative setup, hold OR minimum-pulse slack,
instead of accepting the timing tool's successful process exit alone.

Use the existing 125 MHz half-clock for ingress RAM and retain 250 MHz external
SRAM addressing. Related-clock input/output bridges remain to be implemented
and timed. The existing recall path already packs two 32-bit read words into
64-bit writes at 125 MHz; the new 5120-word host buffer should retain that
principle. A 250 MHz 32-bit host-buffer RAM would have the same restriction.

The updated budget calculation allows slower catch-up writes: at a conservative
25 Mword/s service rate, 5120-word batches still have an ideal 2.224 Mword/s
ceiling versus the required 1.953125 Mword/s. Minimum ideal batch becomes 4444
words. This is a feasibility bound; it does not qualify a clock-crossing bridge.
`--full-slow` in the scheduling test throttles accepted SRAM writes to one per
ten core clocks. It tests the service-rate budget with the actual FIFO logic,
but still uses a single simulation clock and does not verify the proposed CDC.

The isolated 125 MHz FIFO+adapter probe passes all reported timing categories:
setup +3.016 ns, hold +0.144 ns, minimum pulse width +3.663 ns. The complete
reports, wrapper, constraints and machine-readable result are in
`qualified-isolated-125mhz/`. This is internal single-clock qualification only;
it does not cover the eventual related-clock bridges or full acquisition image.

The slower-write full-depth scheduling test passed: eight 5120-word reads,
54,389 new writes, maximum ingress occupancy 4101/4608, and a 10 ms host delay.
The normal-rate model passed 40,960 returned words and 52,500 new writes with
maximum occupancy 4100/4608. `transport-25mword.log` and `transport-final.log`
contain the results. The slower-write test validates the reduced service-rate
budget, not a physical dual-clock FIFO/bridge implementation.

Run `python3 verify.py` from any directory to check the archived hashes, timing
categories, RAM count, and presence of successful simulation records. This
checks evidence consistency; use `test_fifo.py`, the transport tests and
`build_probe.py` to rerun the underlying validation.

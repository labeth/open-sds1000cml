# Host-buffer integration status — full image not yet qualified

The streaming scheduler reserves two 2560 x 32-bit banks. Total storage is
5120 32-bit words / 2560 64-bit pairs. The existing 8192-word revision-11 ABI
and kernel capacity whitelist do not describe this geometry.

## Storage and clocks

Keep the transport at 250 MHz; write the host RAM at 125 MHz. Cyclone IV M9K
minimum-period qualification already ruled out clocking these RAMs at 250 MHz,
even with a slower write enable. Pack successive 32-bit words into 64-bit pairs.
The implemented five explicit 512 x 64-bit RAM sections use 20 M9Ks in the
host component fitter result (seed 2).
Pair address 0..2559 selects section address[11:9] (0..4), row address[8:0].
Logical host bank 1 begins at pair address 1280, halfway through section 2.
Do not derive logical bank ownership directly from the physical RAM section.

## Delivery and publication

The controller cannot backpressure a read burst. A small register-based FIFO
between the 250 MHz pair packer and 125 MHz RAM writer is implemented in
`host_path.v`. It has eight logic-backed FIFO slots plus a prefetched output. Its
storage must be flip-flops, not 250 MHz M9K. Budget/simulate occupancy from
pointer synchronization, startup, burst boundaries and metadata insertion;
select depth from that evidence and verify total logic/register resources.
A single word-bridge handshake cannot sustain 125 million 64-bit pairs/s.

An odd-length final bank requires one padded 64-bit pair; padding must never
increase the published 32-bit word count. End-of-bank notifications must be
ordered after the RAM write containing the final real word. Controller
bank_done means words were emitted, not that RAM writes have completed.
A metadata notification may coincide with a data pair from the next bank;
the queue/arbiter must preserve both rather than assuming one event per cycle.
Do not publish a bank until its RAM-side completion is observed. Keep the bank
reserved until ARM release tokens are validated and returned to the controller.

Both controller reporting ordinals now trail acceptance by one core clock.
Bank-first metadata compensates that latency, including a one-word tail.
Metadata held while a bank is reserved can cross as a constrained bundled bus
with ordered completion notification; exact timing and ownership need tests.

## ARM ABI and required evidence

A 16-bit auto-increment host read pointer covers 10240 halfwords, not a power
of two. Test pointer wrap, quarter selection within a 64-bit pair, explicit
index priming, both banks and one-word tails. Revise capability/version checks
and kernel capacity acceptance together with a qualified new image.

Before deployment: prove sustained pair delivery, non-overlap of ownership,
RAM-side publication, reset recovery and bounded FIFO occupancy across clock
phases. Run timing with the actual controller, transport, CDC and RAM, check
all M9K clock periods/resources, then board-qualify the new read-origin and
warm-up behavior. Component evidence below does not replace whole-design or board qualification.

## Current evidence and remaining integration

The actual host_path module passes normal/odd-tail readback, ownership reuse,
malformed-input/owned-bank protection and reset recovery. Standalone FIFO
checks include delayed Gray-pointer observations and overflow. Its seed-2
placement has setup +0.009 ns, 20 M9Ks and all 75 CDC audit groups passing;
IO remains unconstrained and margin is narrow. Evidence is in
`validation/2026-09-14-host-path/seed2-passed/`.

Controller simulation now uses the actual host_path, independent 100 MHz ARM
clock, actual RAM port reads, descriptor tokens and returned core release.
Reduced AW=13 SRAM wrap passes 10003 words, including a separately asserted
1 ms ARM pause. Physical AW=19 / 65539 words / 10 ms pause test has been added;
its result must be checked before claiming physical-geometry coverage.

Still required: integrate controller/ingress/transport/host_path into a buildable
full design; close controller and combined timing with actual IO constraints;
implement host register ABI and kernel capacity/version changes for 5120 words;
qualify read-origin/warmup and burst streaming on hardware; connect ARM/app
streaming, timebase policy, precision processing and trigger geometry. Existing
finite raw/high-rate capture must remain available while slower-rate continuous
streaming uses the new scheduler. No new complete image has been deployed.

`fpga/acq_sram/stream_path.v` now provides the combined ingress/controller/
transport/host module with fixed 2560-word host banks and nine ingress RAM
sections. Icarus elaboration of the full module passes without warnings using
the DDR simulation model. This is wiring/elaboration evidence only: the
existing controller bench still instantiates components separately. Direct
wrapper simulation and integrated timing/IO qualification remain required.

Physical-geometry result is now PASS: 65539 words, 26 banks, 10 ms ARM pause,
4 skipped busy-bank reads, 16 scan/drain overlaps, max ingress pending 4101,
max unread 24437. See host-path `owned-controller-physical-passed.txt` for
frozen-source evidence. Full stream module map/fit timing is still separate.

# SRAM streaming checkpoint — not a deployable image

The combined path joins nine-bank 125 MHz ingress RAM, a 250 MHz forward-address
SRAM scheduler/transport, paired host buffering, and a 100 MHz host read port.
The 32-bit word format preserves both channels' Q8.8 samples. It is intended
for /256 and slower continuous transfer; faster finite capture remains separate.

Evidence:
- Host component seed 2: setup +0.009 ns, 20 M9Ks, 75 CDC audit groups pass.
- Combined module: 38 M9Ks, about 51% of available logic; 120 CDC groups pass.
- Original combined seed 2 setup -0.418 ns; active mailbox simplification -0.429 ns
  with lower core TNS (-8.434 vs -10.926 ns). Timing closure remains required,
  IO unconstrained. Current source hashes and tests: mailbox-payload-probe/.
- Direct wrapper: 10003 words, four banks, SRAM wrap and actual RAM reads pass.
- Physical AW=19 model: 65539 words, 26 banks, 10 ms ARM pause, four busy-bank
  skips and 16 scan/drain overlaps pass. Peak ingress 4101/4608; unread 24437.
- Odd tails, ownership reuse, fault propagation, overwrite rejection and reset
  have component coverage; reports state the precise scope of each run.

No new image has been deployed. Production work remaining includes integrated
IO/CDC/timing closure, GPMC ABI and kernel support for 5120 host words, ARM/app
continuous streaming, ADC/precision/trigger integration, trigger record geometry,
and on-device electrical and sustained-throughput validation. The physical
model run does not prove an entire SRAM-depth retained capture or hardware
behavior. Rejected experiments are retained as evidence, not active RTL.

The old packetizer test remains at sim/tb_stream_path.v. The new combined
wrapper test is sim/tb_sram_stream_path.v; its recorded earlier hash is unchanged.

The mailbox payload reset was removed independently of token reset. Eight
clock ratio/phase cases, reset with valid high, both ingress stress suites and
the direct wrapper pass. The unread counter toggle-mask experiment worsened
timing and was reverted exactly; its reports remain in count-mask-probe/.

Current structure: stream_engine.v exposes an external transport interface;
stream_path.v connects the existing transport without changing its public ports.
External-engine-probe verifies the current source hashes, 10003-word wrapper
readback, 38 M9Ks and all 120 CDC groups. Setup remains -0.429 ns. Default-image
arbitration with finite capture and sharing the host write path remain to do.

Latest structure: stream_engine.v also exposes its host write/metadata/release
interface. One external host_path in stream_path.v provides the RAM. Current
source/evidence is external-host-probe/: full wrapper readback and 120 CDC groups
pass, unchanged 38 M9Ks and -0.429 ns setup. Finite recall/mode arbitration and
all subsequent default-image/hardware requirements above remain unfinished.

Finite recall producer added in finite_recall.v. It reads a frozen requested
range through the same host interface, supports continuation or fresh seeks,
retains faulted epochs until reset, and completes only after final host release.
Finite-recall/full-passed.txt verifies all 524288 words / 2 MiB through 205 host
banks using the actual host-buffer RTL and an ideal SRAM model. Both read modes,
offsets, odd tails, empty/invalid requests and fault recovery have tests. This
new producer has no placed timing qualification and is not yet selected by the
default image's producer/transport arbitration. The full objective remains open.

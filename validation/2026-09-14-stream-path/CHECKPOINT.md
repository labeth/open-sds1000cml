# SRAM streaming checkpoint — not a deployable image

The combined path joins nine-bank 125 MHz ingress RAM, a 250 MHz forward-address
SRAM scheduler/transport, paired host buffering, and a 100 MHz host read port.
The 32-bit word format preserves both channels' Q8.8 samples. It is intended
for /256 and slower continuous transfer; faster finite capture remains separate.

Evidence:
- Host component seed 2: setup +0.009 ns, 20 M9Ks, 75 CDC audit groups pass.
- Combined module: 38 M9Ks, about 51% of available logic; 120 CDC groups pass.
- Combined seed 2 setup -0.418 ns: timing closure remains required, IO unconstrained.
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

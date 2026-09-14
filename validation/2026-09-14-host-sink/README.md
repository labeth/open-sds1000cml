# Ordered host RAM sink — functional prototype

The 80-bit FIFO packet carries type (bit 79), bank (78), pair address or
valid word count (77:64), and sample pair or first-word ordinal (63:0).
DATA acceptance registers a validated write request; RAM writes it on the
following 125 MHz edge. A following descriptor publishes after that edge,
once the final physical write has occurred.
Odd tails occupy a padded pair but advertise their actual word count.

The sink checks contiguous addresses independently per bank, bank bounds,
nonempty length, and ceil(word count/2) against accepted pairs. Fault is sticky
until epoch reset. Upstream faults must already be synchronized to this clock.

Caller must enforce bank ownership: this component does not stop a producer
from overwriting a published bank. Publication is in the RAM clock domain;
ARM publication/release CDC is still required. Pair packing and FIFO integration are functionally exercised by
`../2026-09-14-host-path/test_path.py`; the controller and ARM ownership
handshake are not yet connected to that path. No integrated timing qualification or device deployment yet.

Verification from repository root:

```sh
iverilog -g2012 -s tb_host_sink -o /tmp/acq-host-sink-test fpga/acq_sram/host_sink.v fpga/acq_sram/host_ram.v fpga/acq_sram/sim/tb_host_sink.v
vvp /tmp/acq-host-sink-test
```

Passed: actual RAM full bank readback (5120 halfwords), bank-1 single-word
padded tail, no early publication, mismatched descriptor length, wrong bank
address, duplicate pair, empty descriptor, upstream fault and epoch reset.
The test does not establish asynchronous FIFO throughput or ARM visibility.

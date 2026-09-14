# Host ownership functional prototype

`host_ownership.v` holds RAM-side metadata until ARM releases its descriptor.
Two stages sample metadata; three stages carry publication tokens. Matching
ARM releases cross to the RAM domain first. Its synchronized acknowledgment
then crosses to the controller, so reuse follows RAM-side acceptance.
A duplicate or wrong-token release cannot reclaim the current bank. Tokens
are one bit: the interface permits one outstanding release per descriptor,
not arbitrary delayed transactions spanning two subsequent generations.

Run from repository root:

```sh
iverilog -g2012 -s tb_host_ownership -o /tmp/acq-owner-test fpga/acq_sram/host_ownership.v fpga/acq_sram/sim/tb_host_ownership.v
vvp /tmp/acq-owner-test
```

Passed simultaneous two-bank publication, reversed release order, wrong and
duplicate releases, 20 bank reuses with metadata verification, rejection of
publication while busy without descriptor mutation, and epoch reset.

Not integrated or timing-qualified. Production integration must constrain
bundled metadata settling and token synchronization, propagate faults, and
prevent writes to an owned bank. `producer_busy` covers published descriptors,
not controller reservations or in-flight FIFO packets. Core release now
follows RAM acknowledgment; controller reservations must still protect new
writes until the corresponding ARM release.
The common epoch reset must reach all participating components. Host software
must discard descriptors from the old epoch and never read after release.

The paused-producer-clock test proves controller release waits for RAM
acknowledgment. A negative control replacing `rel1<=ack2` with the previous
`rel1<=released` fails the release-before-ack assertion, as recorded in
`negative-control.txt`. This simulation is not a physical CDC timing proof.

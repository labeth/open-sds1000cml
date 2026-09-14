# Host RAM component checkpoint

Implemented `sram_host_ram`: 2560 x 64-bit pairs, representing two 2560-word
32-bit host banks. Five explicit 512 x 64-bit sections use 20 M9Ks on
EP4CE10F17C8. Bank 1 starts at pair 1280, in the middle of RAM section 2.
The caller must prevent same-pair read/write collisions and retain bank
ownership until all consumers finish. This module does not manage ownership.

Writes accept one 64-bit pair per 125 MHz clock. The 100 MHz read interface
accepts a 16-bit halfword address every clock; a request at edge N produces
its response after edge N+1. Addresses cover 0..10239. Invalid reads produce
an error response and zero data; invalid writes are suppressed and latch a
fault. Reset clears interface state/faults without erasing memory contents.

Simulation passed 30757 responses: every valid halfword, physical/logical
section boundaries, concurrent accesses to different rows within shared
physical section 2, invalid-address non-aliasing, write suppression during
reset, read-pipeline reset and memory preservation. Source hashes are in
ram-tests.log. `test_ram.py` freezes input sources before compiling them.

The isolated Quartus probe uses 8 ns write and 10 ns read clock constraints,
with no false paths or clock-group cuts. Setup slack +1.403 ns, hold +0.143 ns,
minimum pulse +3.674 ns; synchronous reset gives no recovery/removal paths.
Five dual-clock simple-dual-port RAM instances each occupy four M9Ks.
`verify.py` checks the archived reports, geometry, clocks, hashes and log.

This is component evidence only. The 250-to-125 MHz packer/FIFO, ordered RAM
publication, bank token validation, non-power-of-two ARM pointer ABI, full
integration timing/resources and hardware qualification remain unimplemented
or unverified. The separate timeslice controller still fails its 250 MHz
component timing target. No new FPGA image has been deployed.

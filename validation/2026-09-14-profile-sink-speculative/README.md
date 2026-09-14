# Speculative sink metadata physical trial

Sink descriptor payload capture no longer depends on ownership or geometry
validation. Publication and pair accounting retain all checks. Ownership copies
the payload on the following edge and holds it until release. Rejected input
may change private sink metadata but cannot publish it or alter an owned copy.

Host-path tests pass at four clock phases, including full banks, odd tails,
RAM contents, reuse and injected malformed descriptors. A direct FIFO injection
of a descriptor for an owned bank verifies the private payload changes while
the owned descriptor remains intact. Combined GPMC capture/recall also passes
(ADC mocked). See the source hashes in the test outputs.

Physical seed 2, build b3796c80: 8394 LE, 7000 registers, 630 LABs, 26 M9Ks.
Setup -5.412 ns, hold -2.942 ns, recovery -2.599 ns, removal +0.517 ns.
This is a failed diagnostic fit, not a deployable image. Overall slack regressed.
Worst setup is command payload geometry into writer halt_pending; worst hold
is ownership published[0] to pub1[0]. Both require further investigation.

The build snapshot precedes a comment-only cleanup in host_sink.v. Its manifest
binds the exact fitted source, constraints and pin assignments. No assembly or
device load was performed. External pad timing and full CDC qualification are
still incomplete.

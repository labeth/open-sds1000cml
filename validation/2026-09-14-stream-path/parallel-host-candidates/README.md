# Parallel host packet candidates

Build pair and odd-tail payloads independently, then select their registered
values using the delayed validated selector. Publication offer latency and
fault semantics remain unchanged. This follows the separate recall-count
change recorded in ../recall-count.

Passed four-offset host data/metadata/ownership/reuse and malformed-descriptor,
owned-bank, fault/reset checks. Deep integrated raw/precision capture and recall
also pass. Full 524288-word recall was tested for the preceding count change
with the prior host packer; its exact snapshot is in ../recall-count.

Balanced seed 2 probe: 7631 LE (+55 versus recall-count), 630 LAB (+4), 26 M9K.
Setup -2.185 ns (versus -2.090), recovery -3.363 (versus -3.610); CDC audit
passes. Worst setup path is now trigger_falling_l -> record.remaining; the
wide validated packet-candidate path is no longer worst. This change is retained
as pipeline separation, not evidence of an increased operating frequency.

No 250 MHz timing closure or device qualification. Board integration with real
PLL/pin/GPMC constraints is the next substantial step; virtual-pin core fits
cannot prove a working default instrument image. All other RTL source hashes
match 5ccd992; the two changed RTL files are included with result.json hashes.

# Speculative host descriptor payload

Descriptor data captures on done_q independently of count validation; pending
and fault still validate publication. This removes the count-equality path to
64-bit first-sample metadata enables without adding transfer latency.

Passed: deep acquisition integration, host RAM/publication/reuse at four clock
offsets, and malformed descriptors (zero, wrong count, discarded high bits),
owned-bank protection, sticky fault propagation and reset recovery at four
offsets. Source hashes are in test outputs. ADC primitives are mocked there.

Real-ADC deep probe, balanced seed 2: 7579 LE (+43), 627 LAB (+1), 26 M9K.
Setup -2.046 ns versus -2.033 baseline; recovery -3.326 versus -3.349.
The worst setup path moved from host descriptor validation to finite recall
 distance[14] -> command_count. No overall 250 MHz timing closure is claimed.
CDC audit passes all 90 rows, minimum slack +0.096 ns; baseline failed encode
crossing -0.232 ns. The audit is not complete board/ADC/reset qualification.
Sources other than host_packer.v match e7395a7, with hashes in result.json.
Board IO, GPMC integration and device qualification remain unfinished.

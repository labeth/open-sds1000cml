# Preliminary SRAM streaming budget — not hardware qualification

At /256, two 500 MS/s inputs produce 1,953,125 Q8.8 pairs/s,
7.8125 MB/s. A forward-only 524288-address counter circuit at 250 MHz takes
2.097152 ms. Incoming samples must be buffered while reading older SRAM data
and returning to the write pointer.

An ideal batch B requires 524288+B clocks for the circuit and write catch-up.
Thus a 4096-word batch cannot sustain /256, even without turnaround overhead.
The minimum ideal batch is 4129 words; 5120 words permits an ideal ceiling
of 2,417,795 words/s. Run `python3 budget.py` for the arithmetic checks.
This assumes the read excursion requires exactly one circuit, which must be
verified against actual warm-up and physical address corrections.

Candidate memory layout: 5120-word host RAM plus 4608-word single-clock
ingress FIFO. Estimated usage is 20+18+5 other M9Ks = 43 of 46. This needs
actual synthesis and fitting. Current host pointers use power-of-two lengths;
explicit wrap and RAM banking are necessary. The loaded 8192-word host build
already uses 37 M9Ks and cannot simply add this FIFO.

4096 words arrive during an ideal counter circuit. The proposed ingress FIFO
leaves 512 words, or 262 us, for initial occupancy and switching overhead.
Transport setup/hold/drain, pipelined writes and warm-up must be simulated
before claiming that margin is sufficient. SRAM's theoretical /256 retention
is 268.435 ms; usable host-stall allowance is smaller because of backlog,
reserved read space, ingress data and safety margins.

Next requirements: cycle-accurate alternating read/write/discard simulation;
FIFO and pointer-wrap checks under host stalls; explicit unread-overwrite
failure; trigger indices tied to sample arrival, not delayed SRAM commits;
preserve existing direct writes at high rates; Quartus timing and RAM fit;
then hardware read/write transition qualification. Frozen recall tests do not
establish this proposed mode. No scope configuration was changed for this work.

## Transport experiment

`fpga/acq_sram/sim/tb_sram_timeslice.v` exercises the actual
CONTINUOUS_ONLY transport, two-stage SRAM output model and a deterministic
source producing one pair every 128 core clocks. The testbench tracks ingress
occupancy and payload order; the original checkpoint modeled ingress with counters. The current test
uses synthesizable ingress FIFO/adapter modules; its command scheduler is still
a testbench, not a synthesizable controller. Physical writes and returned words are checked
against independent sequence counters.

A reduced-address run completed 144 excursions, 2304 returned words and 2296
new writes, crossing both physical pointers repeatedly. Maximum modeled ingress
occupancy was 9 of 32. Negative cases detect insufficient FIFO space and a
missing read/write separation reserve. Run `python3 test_transport.py`; add
`--full` for the physical-depth run with a 10 ms host delay.

The initial experiment exposed a second-circuit hazard: a fresh read's flush
pulse advances beyond the last returned word. Reading all the way to the write
pointer therefore forces another circuit to return. The revised scheduling
experiment reserves 32 unread words between the batch end and write pointer.
This is a conservative model reserve, not a measured board requirement.

The full 19-bit depth run passed eight excursions with a 10 ms host delay:
40,960 words read, 52,500 new words written, maximum ingress 4100/4608.
Each excursion cost 524369 clocks (one full circuit plus 81), approximately
2.097476 ms at 250 MHz. Read pointer wrap was exercised. The reduced-address
case above additionally exercises repeated write pointer wrap. Logs are
`transport-full.log` and `transport-checks.log`. This proves the scheduling
experiment under its stated model, not SRAM board timing, RAM resource fit,
trigger handling, or sustainable ARM transfer.


Later ingress synthesis found a 4.201 ns M9K minimum-period restriction; the
ingress and paired-word host RAM must use a slower clock. The planned RAM clock
is 125 MHz while SRAM addressing remains 250 MHz. `budget.py` now also checks
125, 31.25 and 25 Mword/s write-service rates. Even at 25 Mword/s, 5120-word
reads have sufficient ideal average throughput. See the ingress-fifo validation
directory for timing reports and the remaining clock-crossing work. The original
full-speed scheduling log is retained as historical model evidence.

# Register-backed host FIFO component

`sram_host_fifo` defaults to eight 80-bit slots plus one registered output.
Source clock is 250 MHz; destination clock is 125 MHz. Slots remain in logic:
the fitted default uses zero M9Ks. Full/empty detection uses registered Gray
pointers and two-stage synchronization. The output holds stable while blocked.
`push` is a non-retry offer: an offer while full is dropped and latches overflow.
The caller must invalidate/recover that epoch. Offers during common reset and
local synchronized release are discarded; only a common reset is supported.

The destination prefetch register extends total capacity to nine entries.
The modeled maximum two-bank burst carries 2560 data pairs plus two completion
messages. Four phase offsets, repeated with one-clock delayed pointer
observations, passed; worst observed occupancy was eight entries. Tests also
cover wraparound, sustained throttled transfer, stable blocked output, exact
full capacity, rejected overflow, sticky overflow and reset with data pending.
These finite digital tests do not constitute an analog metastability proof.
The packer and publication semantics still need integration tests.

The isolated EP4CE10F17C8 probe meets 4 ns source and 8 ns destination clocks:
setup +0.118 ns, hold +0.186 ns, recovery +1.251 ns, removal +0.505 ns,
minimum pulse +1.513 ns. CDC audit checks all 640 stored bits, all 80 output
bits, and each pointer synchronization stage at all three operating corners.
Maximum payload delay is 4.296 ns against a 6 ns bundled-data bound. First
pointer stages have a 4 ns bound; second stages retain normal clock timing.
Only external common-reset assertion is excepted; clocks are not broadly cut.

`host_fifo_cdc.sdc` currently targets the default `dut` probe instance and
checks exact endpoint counts. Adapt its scope and rerun the audit when
integrating it; do not silently omit constraints when hierarchy changes.
`build_probe.py` runs synthesis, fit, STA and the CDC audit. `test_fifo.py`
freezes input sources before eight simulations. `verify.py` checks archived
hashes, timing/resource/CDC reports and the simulation evidence.

Not yet integrated into an FPGA image. Pair packing, odd-tail padding,
ordered RAM publication, bank-token validation and ARM ABI remain pending.
The separate streaming controller still fails its 250 MHz component target.
No hardware deployment is claimed by this component checkpoint.

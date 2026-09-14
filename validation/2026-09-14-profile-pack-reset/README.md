# Packing-domain reset release

With SYNC_ENCODE enabled, consume_s and wide_valid now use pack_enable as
their asynchronous reset source. pack_enable asserts reset on disable and
releases through the existing three packing-clock stages. Raw enable no
longer deasserts reset directly into these working registers. Consume takes
two additional packing clocks to synchronize after local release.

Both startup and --short-epoch runs exited successfully. The simulation checks
ten encode enables/polarities, chronological delivery with modeled converter
delays of 4.5 to 6 ns, and eight phases of a 4 ns disable pulse. Each restart
produces a fresh chronological epoch. The archived interleave.v hash matches
the short-epoch run. This uses modeled ADC timing and an ideal FIFO; it is
not a physical throughput, metastability, or minimum-pulse-width proof.

The physical fit is a separate diagnostic. No image was assembled or deployed
as part of these tests. Full pad timing and device qualification remain open.
The archived runner is evidence; run the original validation script from its
repository location so its relative source paths resolve correctly.

Physical seed 2 result is archived under build: 8404 LE, 7000 registers,
630 LABs, 26 M9Ks; setup -4.812 ns, hold +0.131 ns, recovery -1.934 ns,
removal +0.374 ns. All eight derived clock checks pass and archived manifest
hashes match. Worst setup is command geometry into writer frozen; worst
recovery is core reset into host_reset[1]. This still fails qualification.

# Exact ARM FIR optimization

The scope's Cortex-A8 executes an unchanged Q22 symmetric FIR dot product using signed multiply-accumulate long instructions. All products and accumulation remain 64-bit. The common Go code still performs the same rounding, clipping and guard handling.

`isolated.json` compares the original implementation (617.8–617.9 ms per 524,288-sample channel) with a bounds-check simplification (663.2–665.8 ms). That simplification alone was rejected. `mulal.json` records the final assembly dot product (250.4–250.6 ms), about 2.47 times faster than the original. All device runs pass the DC, fractional-code, Nyquist, phase/passband, and direct-convolution oracle tests before benchmarking. `results.json` preserves the initial benchmark process killed under memory pressure with the app running; obsolete RAM test helpers were removed and successful comparisons ran with the app paused.

`app-start.json` identifies the RAM-only deployment of the optimized app. `app-status.json` records the full 524,288-sample/channel precision record, 1.698 s read-plus-conditioning time, and zero bus errors. No FPGA reload or internal permanent storage write was needed for this improvement. The normal app was restored on the qualified revision-9 image; experimental host-interface FPGA candidates are separate.

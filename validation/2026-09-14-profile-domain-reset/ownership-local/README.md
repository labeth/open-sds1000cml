# Local ownership reset follow-up

Removes the raw reset bypass from host_ready and core_release. Their local
reset chains already assert asynchronously and release synchronously.
The ownership test now covers a 1 ns reset while all three clocks are stopped,
clearing the old epoch and successfully publishing/releasing after restart.
The combined GPMC capture and SRAM recall test also passes (ADC mocked).

Physical seed 2: 8402 LE, 7000 registers, 623/645 LABs, 26/46 M9Ks.
Setup -5.037 ns, hold +0.095 ns, recovery -2.678 ns, removal +0.486 ns.
All eight derived clock checks pass; the 40 manifest inputs were hash-verified.
No assembly or device load. Timing and physical qualification still fail.

The previous direct core-reset to host cursor setup path is removed. The new
worst setup path is ownership published[0] through sink validation into first1
metadata (-5.037 ns). Command payload geometry to writer reserved remains
-4.570 ns. Recovery failures include ADC mode-dependent FIFO reset and entry
into nested domain reset chains. External pad timing and CDC audits remain
incomplete. Keep this structural reset fix, but do not claim overall timing
improvement from this seed: placement changed and worst slack regressed.

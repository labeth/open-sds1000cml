# Finite trigger pipeline

Two core-clock stages separate channel/format selection from threshold
comparison. The finite writer receives data delayed by those same stages.
Force and external-match flags travel with the input word. Streaming remains
on the direct source path. Configuration must remain fixed during an epoch.

The previous sample is represented by its above/below-threshold relations.
Equality retains the original strict-before/inclusive-after crossing rules.
The first crossing wins when both packed samples cross; precision words contain
one dual-channel sample pair. Disable clears validity and previous history.

`unit-tests.txt`: 71520 words checked against a sequential sample oracle across
96 combinations of format, channel, direction, mode and threshold, with gaps,
force/external matches, equality, rails and reset with data in flight.
`integration-tests.txt`: real finite writer, SRAM transport and host ownership
with ADC/CIC mocks; raw second-sample trigger, precision capture and streaming
recall pass. The finite scoreboard independently delays source data two cycles.
These simulations do not establish physical ADC timing or maximum frequency.

`first-build` failed placement: 659 LABs required versus 645 available.
Mapping reported 11347 LEs and 7793 registers. No STA result or FPGA image
exists for this revision. Trigger behavior is simulation-verified, but hardware
fit and timing remain unresolved.

Compared with the preceding mapped build, trigger logic costs 103 registers
and 89 ALUTs, while the parent loses 17 registers and 129 ALUTs. The unchanged
host sink also gains 316 mapped ALUTs without extra registers. Investigate
that synthesis change as well as pipeline area before choosing a remedy.
Full depth and precision have not been reduced.

A follow-on shares four explicit acceptance controls for metadata and pair
advancement in host_sink.v. `sink-equivalence.txt` compares every output and
both pair counters with the frozen first-build host sink over 54124 cycles.
`sink-integration.txt` repeats the integrated acquisition test after this edit.
The shared-control build in `shared-sink-build` reduces the sink from 695 to
375 mapped ALUTs, with 256 registers unchanged. It still needs 646 LABs, one
above capacity. A subsequent edit lets trigger payload and comparison registers
advance through bubbles; only validity and previous-sample history need their
conditional updates. The refreshed unit/integration logs pass for that source.
The final `integrated-build` FITS: 9504/10320 LEs, 7733 registers, 44/46 M9Ks,
645/645 LABs. All 126 backend/configuration CDC audit rows pass. Setup is
-4.416 ns, hold +0.145 ns, recovery -5.553 ns, removal +0.491 ns, minimum
pulse +1.513 ns. The worst setup moved to capture-start control, from the
record halted flag through readiness/acceptance into latched configuration.
This remains an unqualified core, with no GPMC top or board IO qualification.
No FPGA image was generated or deployed.

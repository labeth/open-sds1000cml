# Mode-specific start acceptance

Capture, stream and recall acceptance use separate kept control signals.
Capture configuration enables therefore do not depend on recall record-length
and range validation. Acceptance timing and request-error behavior are unchanged.
No multicycle exception or relaxed start-input stability contract is introduced.

The integration test still checks accepted settings after immediate input
changes, busy starts, trigger position, both sample formats, host-bank ownership,
streaming and source faults. Added cases reject invalid decimation/mode, zero
post count, over-capacity geometry, absent frozen records and out-of-range recall;
an invalid recall must preserve the existing frozen record. All pass.

The build FITS: 9522 LEs, 7738 registers, 44 M9Ks and 645 LABs. All 126
existing backend/configuration CDC rows pass. Setup -3.836 ns, hold +0.110 ns,
recovery -5.725 ns, removal +0.494 ns and minimum pulse +1.513 ns.
The overall worst path is ADC encode configuration into a phase clock.
Core timing still fails at -3.814 ns: host FIFO overflow passes through start
readiness to the finite writer configuration enable. This is progress on the
recall cross-coupling, not completed start-path or ADC timing closure.
No image was generated or deployed.

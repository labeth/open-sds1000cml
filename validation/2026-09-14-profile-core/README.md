# Combined profile core

`acq_profile_core` connects the real GPMC slave, command/configuration/results,
coherent status, finite acquisition core, SRAM transport and owned host readout.
Deep is the default composition. Identity advertises draft profile ABI and a
hardware qualification flag of zero. Raw ADC snapshot and external match are
explicitly unavailable in this base composition.

Run `python3 validation/2026-09-14-profile-core/test_profile.py`.
The test uses GPMC transactions for identity, invalid-service rejection,
17-word raw capture, frozen status polling, physical 19-bit SRAM addressing,
recall, token selection, exact halfword readout and release. A second recall
injects an acquisition fault to verify host-window invalidation and status.
Only ADC/interleave/precision are mocked using the existing integration models;
transport, record, host RAM and ownership are actual RTL. This is not proof of
ADC timing, electrical SRAM operation, physical GPMC timing or sustained ARM
transfer throughput. Earlier full-depth recall simulation is retained under
`validation/2026-09-14-stream-path/recall-count`.

Next required work: real PLL and pin wrapper; timing and CDC constraints for
command and status mailboxes, fault synchronizer and external interfaces;
fit/timing closure; profile-aware ARM/kernel support; volatile device tests.
The existing core-only 250 MHz timing failures remain unresolved. No bitstream
was generated or deployed by this test.

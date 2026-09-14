# ADC encode control crossing

The integrated source selects SYNC_ENCODE=1: two preserved local phase-clock
registers synchronize each independent converter enable. Legacy default remains
unchanged. First-stage paths are bounded to 4 ns; subsequent stages and DDIO
remain normally phase-clock timed. This does not make the ten bits an atomic bus.

Adding synchronization exposed stale startup frames in the ADC model when
enables changed at capture start. startup-before.txt and before-interleave.v
reproduce first-word stale zeros. Current warm-up waits seven phase-clock stages
before FIFO writes and asserts warm-up reset immediately on disable.
startup-after.txt checks all ten independent encode-enable bits and their phase
polarities after settling, then passes modeled chronology at 1 GB/s with converter propagation
4.5..6 ns. FIFO is ideal; this does not prove metastability or board aperture.
Source and integrated control regression tests pass (ADC/CIC mocks for control).

The sync-only build predates the warm-up fix: 9481 LEs, 7702 registers, 44 M9Ks.
Encode source/meta/settled checks pass at all corners. Overall CDC audit fails
on existing bad_host_first (slow corner slack -0.075 ns, data delay 4.6 ns).
Setup -3.536 ns, recovery -4.147 ns. No timing bound was relaxed.
The final warm-up build in integrated-build FITS: 9505 LEs, 7706 registers,
44 M9Ks, 645 LABs. All 135 CDC audit rows pass, including the 9 new encode
rows and the existing host-fault crossing that failed in the prior placement.
Setup -3.094 ns, hold +0.123 ns, recovery -5.569 ns, removal +0.485 ns,
minimum pulse +1.513 ns. Worst setup is selected_finite through host count
selection into packer count_legal_q. No clock bounds were relaxed.
Other ADC CDC/reset and board IO remain unqualified. No image generated or deployed.

# Host packer delayed count validation

Expected counts commit from the existing registered input stage. Equality
checks now run in that same packing stage against the count before the current
word commits; range checks remain in the input stage. Descriptor equality uses
the matching delayed metadata. Live word_valid no longer enables the wide
count registers. Packet timing and throughput are unchanged in simulation.

The frozen oracle is writer-settings/integrated-build/sources/host_packer.v.
Cycle equivalence compares push/fault every cycle and every published packet
bit across 79463 cycles: full/odd banks, alternating banks, bubbles, malformed
offers and reset. The script's historical output label says forwarding, but
current RTL has no forwarding mux. Integrated acquisition/recall/stream tests
also pass. Logs contain exact RTL hashes.

Two earlier variants failed fit at 648 LABs versus 645 available.
`forwarding-build` contains the first full frozen build; `payload-build`
contains the changed source and reports for removal of metadata resets, with
other source files identical to forwarding-build. That reset edit saved no
space and is reverted. Current packing-stage comparison build is pending in
/tmp/acq-packer-check-build.txt. No image generated or deployed.

The delayed-comparison variant (`compare-build`) reduced demand to 646 LABs,
still one over capacity. Current RTL retains only 12 descriptor-count bits
after the full-width range check; count_legal_q rejects any upper bits or
counts above 2560 before the payload can be used. This preserves malformed
input handling while avoiding unnecessarily wide delayed equality checks.
Refreshed equivalence and integration tests pass. Current build is running
in /tmp/acq-packer-narrow-build.txt (exec session 99949).

The narrowed descriptor variant (`narrow-build`) still required 646 LABs.
Current RTL also shares one data packet register stage between complete pairs
and odd tails instead of storing both candidates separately. It retains
metadata separately and selects the corresponding delayed packet kind.
Refreshed 79463-cycle equivalence and integrated tests pass. The current build
is /tmp/acq-packer-shared-build.txt (exec session 92813); fit/timing pending.

Final shared-packet build FITS: 9468 LEs, 7682 registers, 44 M9Ks, 645 LABs.
All 126 existing backend/configuration CDC rows pass. Setup -4.228 ns (ADC
encode-setting crossing), hold +0.128 ns, recovery -5.296 ns, removal +0.486 ns,
minimum pulse +1.513 ns. Core setup improves to -3.601 ns; worst core path
is packer fault through start readiness into selected_operation.
Compared with d509c72 this saves 31 LEs and 57 registers, but overall setup
is worse because ADC crossings remain unqualified. This is not a deployable
250 MHz image. Exact final sources and fit/STA/audit reports are in integrated-build.

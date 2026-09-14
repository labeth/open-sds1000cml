# Finite writer speculative idle configuration

Private pre/post settings capture whenever the writer is IDLE. The accepted
start edge therefore captures exactly the presented settings; leaving IDLE
holds them through setup, ARM and capture. Rejected starts can alter only
these private registers, not the separate frozen record metadata. This removes
the readiness/fault chain from the wide configuration enable without adding
latency, changing acceptance, or relaxing input stability requirements.

Integration tests pass. The finite suite tests an 8192-word model ring, immediate
input changes after acceptance, wrapped capture/recall, sparse samples, invalid
rearm preserving a frozen record, ownership and shared streaming. Fault tests
cover startup, trigger-word and drain errors, frozen-record invalidation, halt,
lost readiness, rejected rearm and reset. All pass. This is not a new physical
full-depth qualification or a new 19-bit full-depth simulation.

Build fits: 9499 LEs, 7739 registers, 44 M9Ks, 645 LABs. All 126 existing
backend/configuration CDC rows pass. Setup -4.159 ns, hold +0.095 ns, recovery
-4.193 ns, removal +0.500 ns, minimum pulse +1.513 ns. Overall setup is worse
than the preceding -3.836 ns build; this is not a timing win. The worst path
now runs from writer_granted into host packer count registers. The private
finite settings no longer use a wide readiness-qualified enable, and fit uses
23 fewer LEs. Readiness-to-top-configuration paths still fail (~-4.005 ns).
ADC configuration/reset crossings and IO remain unqualified.
No image generated or deployed.

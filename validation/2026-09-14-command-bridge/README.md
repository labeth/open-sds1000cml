# Board command handoff

`acq_command_bridge` is the coherent host-to-core mailbox for the forthcoming
profile board shell. It is not yet connected to GPMC or acquisition hardware.
Host acceptance snapshots the whole payload; later shadow-register writes
cannot alter it. A second request while busy is rejected without overwriting
the pending command. Core delivery is one pulse with aligned payload.

Acknowledgment means delivery, not acquisition acceptance. The board shell
must separately report acquisition request errors. Reset must assert to both
domains and aborts outstanding commands; it must not be used as a one-sided
mailbox reset. Delivery acknowledgment crosses two host registers, so held
payload cannot be overwritten before core sampling finishes.

Required board timing checks: bound held_payload to core_payload within 6 ns
(request crosses two core synchronizer stages before sampling); bound request
and acknowledgment routes to their first synchronizer stages, and time the
remaining stages normally. Check endpoint counts against the implemented
payload width after synthesis. No timing exceptions or CDC qualification are
claimed by these functional tests.

`python3 validation/2026-09-14-command-bridge/test_bridge.py` exercises 4000
offered payloads per clock offset, with randomized fields and gaps, continuous
requests, rejection while busy, exact once-only ordered delivery, and reset
at four points in a handoff. Tests cover four relative clock offsets with
100 MHz host and 250 MHz core. Simulation cannot prove metastability behavior.

`test_port.py` now covers `acq_command_port` configuration shadows and command
decoding for both profiles at four offsets, plus a simulation through the real
`gpmc_slave`: bus writes, readback, malformed request rejection and tri-state
release. The command window is documented in `docs/fpga-profile-command-abi.md`.
These tests do not yet instantiate the acquisition core behind the decoder.

Next: connect coherent result/status snapshots and host RAM readout, instantiate
the deep acquisition core and real PLLs, then fit with board pin and interface
constraints. These modules alone do not provide an ARM-compatible or deployable
FPGA image.

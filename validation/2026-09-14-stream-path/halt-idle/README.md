# Idle halt tracking — fits, timing fails

The writer tracks halt while idle and retains it while reserved. This removes
start-acceptance fan-in from halt_pending without delaying halt recognition.
Fault simulations pass, including coincident start/halt, reset cancellation,
previous idle halt dropping at acceptance, and busy rejected start retaining halt.
Integrated raw/precision capture, streaming and recall simulation also passes.

The balanced seed-2 acquisition build completed with 9360 LEs, 7720 registers,
44 M9Ks and 644/645 LABs. Setup -4.029 ns; recovery -4.706 ns. The two encode
CDC rows at slow 85 C fail (-0.315 ns), so this is not qualified for deployment.
The previous launch build used 642 LABs and had -3.284 ns setup slack.

The halt register is no longer among the worst reported setup paths, but the
new worst path is backend ram_bad_core[1] through acceptance to selected_operation.
This experiment does not establish an overall timing improvement. Next work
must address the shared acceptance cone and operation-selection consumers.
All 31 archived build source hashes were verified against result.json.
The full default image and hardware qualification remain incomplete.

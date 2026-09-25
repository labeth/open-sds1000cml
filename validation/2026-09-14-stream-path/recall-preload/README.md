# Recall count preload — simulation and existing CDC audit pass; setup fails

SEEK_WAIT preloads chunk+READ_WARM without gating command_count with transport
done. The prior seek count is sampled at its command edge; the next count is
ready before the following read command. Both backend transport configurations
pass five operations without reset, busy-start and wrong-token rejection.

Balanced seed-2 build: 9380 LEs, 7726 registers, 44 M9Ks, 645/645 LABs.
Setup -3.565 ns; recovery -5.773 ns. Existing CDC audit passes, but it does not
qualify all new frontend pack/reset crossings or physical IO. All 31 archived
source hashes were verified. The worst paths now run from launch_finite via
mode selection into stream/recall state rather than command_count.

This experiment was reverted with retained-mode after neither produced an
overall timing/placement improvement over the mailbox-fixed checkpoint. Default board top,
GPMC integration and sustained hardware streaming remain incomplete.
No device operation was performed.

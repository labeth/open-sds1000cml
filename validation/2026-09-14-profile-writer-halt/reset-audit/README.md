# Asynchronous reset entry audit

Reruns recovery analysis on the parent fitted netlist without replacing its
original reports. The applied constraints match build_profile.py at this
checkpoint. No routing, RTL simulation, assembly or device load is implied.

Checks exactly six precision enable/pack clear pins, seven frontend warm-up
clear pins, three frontend packing enable clear pins, and twelve generated
FIFO reset-release clear pins. Only these asynchronous clear pins are cut.
Data inputs, stage-to-stage paths and downstream working-state reset pins
remain timed. Generated dffpipe_3dc shows two serial destination-clocked
registers; each FIFO's rdaclr/wraclr ties its input high and feeds its output
to the corresponding local pointer reset and request gating.

All endpoint checks passed. Remaining recovery failures include nested host
reset-release entry and ownership pub1. These require further audit; this is
not full recovery qualification. Setup remains failed in the parent build.
The seven analyzer warnings include unmatched optional PLL endpoints and
the intentionally absent deep-profile streaming instances; none are hidden.

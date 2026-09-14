# Writer idle halt preload

The private halt_pending register tracks halt while idle, including the
accepted-start edge, then becomes sticky while reserved. Geometry validation
no longer feeds this register. Reset explicitly clears it.

The integrated AW=13 simulation passes wrapped full-ring capture/recall,
sparse triggering, halt, invalid rearm, streaming and ownership. Writer tests
pass coincident halt/start, delayed grant, repeated-start rejection, reset and
fault injection. This is not a new physical-depth or device qualification.

Seed 2 build 25f04128: 8404 LE, 7000 registers, 628 LABs, 26 M9Ks.
Setup -4.278 ns; hold +0.143 ns; recovery -4.605 ns; removal +0.489 ns.
All eight clock checks and all 40 archived-input hash checks pass.
No assembly or device load; physical timing remains failed.

Worst setup is command geometry into writer reserved. Worst recovery is writer
reserved into precision enable_s[0]. The earlier publication-token hold failure
does not recur in this fit, but its clock-latency-sensitive constraint remains
an audit item. No timing exceptions were changed for this trial.

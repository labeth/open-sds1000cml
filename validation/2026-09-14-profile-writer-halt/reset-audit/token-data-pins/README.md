# Ownership token exception scope

The 4 ns maximum and 0 ns minimum token crossing exceptions previously
targeted whole registers. Quartus applies such exceptions to reset recovery
as well as D-input timing. Limit these exceptions to the six D pins of ack1,
pub1 and rel1, with exactly two pins checked per group. Register endpoint
checks remain. Local reset release is now analyzed with its actual clock.

On the unchanged parent routing, the ownership pub1 recovery violation no
longer appears among the four worst paths. Remaining violations include host
reset synchronizer entries and frontend consume_s. This is a targeted recovery
audit, not an all-corner timing pass. Setup remains failed. No RTL or device
behavior changes in this constraint correction; token routes remain bounded.

The diagnostic Tcl references temporary SDC paths; exact copies are retained
here as profile.sdc and stream.sdc. Run only against the matching parent build
after adjusting those paths. No full-fit result is replaced by this audit.

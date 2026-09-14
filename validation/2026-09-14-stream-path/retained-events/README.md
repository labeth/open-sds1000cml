# Retained mode for transport events

Candidate gates transport done, read-valid, write-ready and bank releases with
selected_finite_l. Public selected_finite, source faults, producer commands
and host payload selection retain the launch-pulse bypass. This is narrower
than the rejected retained-mode experiment, which changed all internal muxes.

Acceptance requires no active producer, no owned bank and idle transport. The
child consumes its start one clock later, while selected_finite_l updates. It
does not consume transport data or release an owned bank on that launch edge.
Subsequent events therefore use the retained owner without the extra launch
decode. Reset still aborts the whole epoch.

10000-cycle accepted/rejected/reset oracle confirms immediate public selection
is unchanged. Composed acquisition, both backend ownership configurations,
and an exact-source timing build are pending. No qualification is claimed.

Composed acquisition passed all three operations and final ownership, stale-record
and frontend-fault assertions. The standalone backend run and fit remain pending.

## Retained result

Both standalone configurations pass all five operations without reset, busy
start rejection and wrong-token rejection. Exact RTL build uses 9218 LEs,
7723 registers, 44 M9Ks and 642/645 LABs. Setup -2.773 ns, hold +0.151 ns,
recovery -5.352 ns, removal +0.488 ns, minimum pulse +1.513 ns. Existing
135-row CDC audit passes and build exits 0, but setup/recovery fail and the
audit does not cover every frontend control/reset crossing. No deployment.

Relative to 7deeefa this improves setup 0.679 ns and frees one LAB, at six
additional LEs. Worst setup is finite-writer state.IDLE through start arbitration
to backend launch_stream/launch_finite. Event gating change is retained.

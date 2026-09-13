# Two-pass recall qualification

Revision 10 remained loaded; app stopped; helper deployed to /dev only.
No FPGA load or persistent scope write occurred.

The ARM stages a complete frozen window, reads bulk spans forwards, then fills
small intervening holes in a second pass. Every burst retains 16 discarded
warm-up words. It never uses continuation opcode 7. Normal Recall and the app
remain unchanged; only SCOPE_RECALL_FORWARD=1 in profile-recall selects this path.

- Baseline full 2 MiB recall: 0.777 s.
- 17-word gaps: approximately 0.520 s, initially exact, then two sequence breaks
  on a repeat full record (results.json). This is rejected.
- 18-word gaps (one explicit counter advance between bulk bursts): approximately
  0.535–0.540 s. Several exact reads, then two sequence breaks (advance-one.json).
  This is also rejected. Explicit advancement alone does not fix the fault.
- Full successful counter SHA256:
  ae42b13d7e0af3e77723caf8357d34c7e061526ed9eefeb67b04a0aaa69f33e2

Both sweeps stopped at their first mismatch, before precision modes. Local
coverage and full-window/wrapped/boundary tests pass; they do not reproduce the
physical failure. Next isolate the error location and compare repeated baseline
and conservative host timings before attributing it to SRAM restart behavior.

## Qualified first-word handoff fix and app integration

`timing-isolation.json` localizes failures to the first word of a DMA burst.
Six baseline reads at each timing passed. Fast forward reads failed intermittently;
six conservative forward reads passed. The wrong word was four counts behind the
expected word, followed by the correct sequence. This implicates the host buffer
selection handoff; it is not proof of the precise physical timing mechanism.

Before burst-index reset, the forward path now sets the indexed RAM address to
word 16 and reads selector 17 to prime the non-burst RAM output to the same first
word. This avoids switching from an unrelated RAM address on the first DMA cycle.
The 18-word inter-block gap remains; no continuation opcode is used.

- `prime-index.json`: 12 repeated full counter reads, all exact, ~0.540 s.
- `prime-modes.json`: three full reads each at raw, /16 and /256, plus boundary,
  tail and offset/multi-block windows. Every SHA256 checked against the exact
  expected counter bytes; all passed.
- App and startup VerifyCounter now select forward recall on revision 10 only.
  Revision 9 keeps the prior path. Startup full counter passed at fast timing.
- `app-frame.json` / `app-frame.bin`: coherent hardware-edge capture, 524288
  samples/channel, decimation 16, eight retained fractional bits.
- `app-status.json`: zero bus errors, about 1.30 s recall plus conditioning,
  versus 1.52–1.53 s in the preceding checkpoint. This remains freeze/read/rearm.
- Host tests cover complete coverage, wrapped full records, boundaries, retries,
  and delivering no data if a staged read fails. Engine/app tests also passed.

The earlier failures above are retained as rejected candidates. The primed
18-word-gap implementation is the candidate enabled in this checkpoint.

# SRAM recall profiling

Same qualified revision-9 FPGA throughout; no FPGA reload. The experimental app was stopped, the inherited GPMC descriptor retained by the agent, and helpers uploaded only to `/dev`. Timing overrides were restored by a deferred restore on both success and panic. The final baseline counter passed after the failed timing tests. The app was then restarted from its existing RAM binary.

## Findings

- Baseline: 2,097,152 bytes in 0.946–0.954 s, exact SHA-256 `ae42b13d7e0af3e77723caf8357d34c7e061526ed9eefeb67b04a0aaa69f33e2` on all three runs. 129 DMA calls take approximately 0.360 s in aggregate (5.83 MB/s during those calls). This includes the EDMA implementation's setup, coherency and copy work, not just wire time. Control reads/writes take approximately 15 ms; remaining time includes waits and the hashing/checking sink.
- 67,630,959 discarded counter advances per repeat recall: the 16-word warm-up prefix forces nearly a complete address-space wrap between chunks. At 250 MHz these advances alone require approximately 0.271 s, excluding command overhead.
- Experimental continuation: retain the first prefix, then set prefix to zero and permit existing command 7 whenever the next address equals position-1 and prefetch is valid. This cut discarded advances to 524,271 and wall time to 0.623–0.626 s, but yielded 256 sequence breaks in every full record and a wrong hash. **Rejected**, and the experimental API was removed. Correct boundary handling needs an FPGA fix or another qualified read strategy.
- A bounded 40 us polling window before the existing timer increased status reads but did not improve wall time (0.952 s). **Removed**, retaining the previous polling behavior.
- GPMC started at read cycle 31, access 13, OE 4..16, CS 0..20, same-CS gap 5. Shorter cycles 16, 12 and 10 with tested gaps 5, 8 and 10 all failed the burst-pointer check. These are rejected timing points, not successful transfers. The logs record exact settings and errors. The earlier owned-fpga throughput is not yet reproduced on this fabric.

## Reusable profiling helper

`acqsram profile-recall OFFSET WORDS` uses the normal recall path with a timed bus wrapper, validates the pointer through the backend, and reports the complete output hash and counter continuity. It never writes output data to scope storage. `SCOPE_EDMA=1` enables the existing coherent EDMA path.

Optional `SCOPE_PROFILE_RD_CYCLE` and `SCOPE_PROFILE_RD_ACCESS` scope a CS1 read-timing override to the helper process. `SCOPE_PROFILE_GAP` optionally overrides the same-CS gap. Controller readback validates application; the exact prior settings are restored when the helper exits normally or panics. External termination cannot execute deferred restoration, so allow the bounded command to finish or restore/cold-cycle explicitly before resuming acquisition.

Next FPGA experiments should measure the host clock on-device and qualify pop-port behavior at faster read timings, then repair or replace discontinuous SRAM refills. A direct reduced-rate FIFO must separately pass sustained producer/consumer, overrun and continuity tests. Do not infer continuous-stream limits from either failed faster timing or the current whole-record conditioning rate.

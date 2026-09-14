# Frozen-record recall producer

`fpga/acq_sram/finite_recall.v` reads a requested range of a frozen external
SRAM record into the same host producer interface as `stream_engine.v`.
It issues only read/seek commands. Payload is opaque 32-bit words, so raw
sample pairs and both Q8.8 channels retain their existing representation.
It allocates no sample RAM; tests connect the existing 5120-word host buffer.

The controller accepts record-relative offset/length with AW+1-bit counts,
including the complete 524288-word physical depth. Empty ranges complete
without touching SRAM. Invalid ranges or non-frozen records are rejected
without publishing a bank. Descriptors use ordinals relative to the requested
range, starting at zero. `done` waits for the final validated host release.

Integration contracts:
- Record start and transport position use the same physical coordinate origin.
  Read bias is applied once to the requested first address.
- The caller preserves the frozen record and exclusive transport ownership
  through completion, including host stalls. Continuation depends on retaining
  the transport/SRAM prefetch state during those stalls.
- Bank releases are validated core-domain pulses from `host_path`. A mode
  change cannot reuse reserved, pending-publication or host-owned banks.
- A fault requires a coordinated producer/host epoch reset. SRAM data is not
  erased. Transport must finish its outstanding read before ownership changes.
- Production uses BANK_WORDS=2560, READ_WARM=16, AW=19. Other parameters require
  1 <= BANK_WORDS <= 2560 and BANK_WORDS + READ_WARM <= 2**AW.
- CONTINUE_READS=0 uses fresh seek/warm-up for every bank as a qualification
  fallback. CONTINUE_READS=1 uses the retained prefetch after the first bank.

Run from repository root:

```
python3 validation/2026-09-14-stream-path/test_finite_recall.py
python3 validation/2026-09-14-stream-path/test_finite_recall.py --full
```

The runner freezes and hashes all test inputs. Small tests exercise both read
modes, empty and invalid requests, offsets, physical wrap, odd tails, repeated
requests without reset, host stalls, actual host RAM reads and immutability of
the SRAM model. A separate protocol test covers host fault, lost frozen state,
short/extra read responses, epoch recovery and waiting for the final release.
The full test checks every halfword of a preinitialized SRAM model; it is not
an ADC acquisition or an electrical test.

Finite/stream arbitration, the default FPGA top, GPMC ABI/kernel integration,
and placed timing/IO/continuation qualification remain unfinished. The existing
stream-only timing result does not qualify this new controller.

Verified current results: `passed.txt` covers both read modes, each with 16366
words across four requests, plus the fault suite. Continuation uses 40888 SRAM
pulses versus 90040 for fresh seeks, with identical returned data. The final
`full-passed.txt` checks 524288 words / 2097152 bytes in 205 host banks and the
fault suite. Both result files hash the current RTL and testbench inputs.

The ARM-side model issues one halfword per 100 MHz clock; these runs verify
buffer/data correctness and are not GPMC throughput measurements. SRAM timing
uses an ideal two-stage memory model. No new FPGA image has been deployed.

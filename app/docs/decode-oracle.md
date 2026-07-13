# Decoder soundness: the sigrok oracle suite

`internal/decode/oracle_*_test.go` cross-validates the protocol decoders
against **libsigrokdecode** (via `sigrok-cli`) on identical synthetic
waveforms — sigrok, with a decade of protocol-analyzer field use, acts as the
reference implementation. Each protocol file generates traffic (clean paths
plus the edge cases that historically break decoders), decodes it with the
repo decoder AND with sigrok, and requires agreement on payload bytes, ids,
error detection, and (where 1:1) sample alignment. The Go decoders are held
byte-identical to their JS twins by the existing parity suite, so the oracle
transitively covers the web UI and LCD decoders too.

Run it: `sigrok-cli` must be installed (`apt-get install sigrok-cli`), then
`go test ./internal/decode/ -run TestOracle`. Without sigrok-cli the suite
self-skips; the CI `oracle-decode` lane sets `CI_REQUIRE_SIGROK=1`, which
turns that skip into a failure (same policy as the browser lane), so the
suite cannot silently vanish.

## Coverage

| protocol | sigrok PD | edge cases exercised |
|---|---|---|
| UART | `uart` | back-to-back frames, fractional samples/bit, even/odd parity clean + violated (error POSITION pinned), frame error (bad stop, position pinned), 7- and 9-bit data, break condition, auto-baud vs explicit oracle — clean and with deterministic ring glitches (1–2-sample bounces on every transition, exercising the cluster-walk inference) |
| I2C | `i2c` | write/read, repeated START, NAK'd address, NAK'd last read byte, address extremes 0x00/0x7F, asymmetric clock duty, fractional samples/clock, clock stretching mid-byte and pre-ACK, SDA glitch during SCL high (divergence pinned — see below) |
| SPI | `spi` | all four CPOL/CPHA modes, LSB/MSB order, fractional bit rate, long-gap re-framing, gaps straddling the 1.5× reset threshold (1.2× and 2.0× on byte boundaries, 1.4× mid-word), back-to-back words, the no-CS framing difference (pinned per side) |
| CAN (+FD base) | `can` | DLC 0..8 sweep, extended ID, RTR, recessive ACK slot, stuff-bit maximizers (0x00/0xFF/0x55), corrupted CRC, classic DLC>8 (divergence pinned), auto-baud vs explicit oracle — clean and on the sparse-single-bit-gap percentile-killer payload, minimum interframe space, fractional samples/bit, FD base frame |
| FlexRay | `flexray` | static frames, sync/startup flag combinations, 0x00/0xFF payloads (BSS stress), corrupted header CRC-11, corrupted frame CRC-24, dynamic-frame DTS tail (no phantom TSS), fractional samples/bit — pinned and auto-inferred (SPB asserted, making resync and the refine loop load-bearing), back-to-back frames |
| USB LS | `usb_signalling` + `usb_packet` | SETUP/DATA0/ACK exchange, IN→NAK and SETUP→DATA0→STALL sequences, zero-length DATA (CRC16 = 0x0000), bit-stuffing payloads, corrupted CRC16, corrupted PID complement (both flag), token ADDR/ENDP extremes, EOP packet separation, keep-alive trains (auto-bitrate survives an idle bus), fractional samples/bit |

Every payload assertion is anchored twice: repo == sigrok AND repo == the
generated truth, so agreement can never be vacuous. Error cases assert the
error's position, not just its count. `eqAligned` checks span starts and ends
(with explicit per-site tolerances where sigrok's rendering convention
differs, e.g. its i2c data annotations extend one SCL period further).

Manchester, SENT, ARINC 429 and MIL-STD-1553 have **no libsigrokdecode
decoder** and cannot be oracled; they are covered by the package's own
round-trip and adversarial `decode_break_*_test.go` suites.

## What the oracle found

- **Three real repo gaps, fixed** (each in the Go decoder AND its JS twin):
  `DecodeFlexRay` verified only the header CRC-11 — a corrupted 24-bit frame
  CRC decoded as a clean frame while sigrok flagged it; it now seals
  header+payload with `flexFrameCRC24` and gates `OK` on both CRCs.
  `inferCANspb`'s blind 10th-percentile halved the rate on a legal frame
  whose 0x33/0xCC-style payload leaves one single-bit gap in a sea of 2-bit
  ones — auto decode then hallucinated a clean-looking garbage frame with
  `ok=true`. And the USB-LS auto-bitrate estimator collapsed on a realistic
  idle-bus keep-alive train (2-bit SE0 every millisecond) until decode
  hard-failed on a capture sigrok reads fine. Both estimators now use
  `inferUARTspb`'s deterministic cluster walk (integer-multiple validation,
  ties to the larger period, >16-bit gaps excluded as non-evidence); each
  fix has a red/green-verified oracle subtest pinning the exact failure
  vector.
- **Four sigrok PD bugs, pinned** (the repo decoder is correct in each):
  the `can` PD decodes phantom data bytes for RTR frames with DLC>0 (remote
  frames carry no data field per ISO 11898-1); the `can` PD's CRC check is a
  TODO stub that accepts corrupted CRCs (the repo flags them); the `can` PD
  applies the CAN-FD length table to classic frames with DLC>8 and then
  stalls waiting for bytes that never arrive (the repo caps at 8 per spec and
  its CRC verifies over the real wire bytes); and the `i2c` PD's FSM misses
  a glitch-recovery path — after a 1-sample SDA dip during SCL high it
  decodes a phantom address and misses the real STOP (the repo flags the
  glitch as START+STOP and still sees the real STOP). Each pin self-announces
  when a libsigrokdecode fix lands so it can be tightened to full equality.
- **Annotation-vocabulary differences, pinned per side:** sigrok has a
  dedicated `rx-break` annotation for UART breaks while the repo reports one
  frame-error and resyncs (payloads agree byte-for-byte); sigrok reports a
  recessive CAN ACK slot as `ACK slot: NACK` while the repo emits a `nak`
  span — agreements, pinned exactly.
- **A documented architectural difference:** on a single line the repo's
  USB-LS decoder cannot see the EOP (it splits packets on idle > 10 bit
  times), so packets closer than that merge; sigrok, decoding the true
  D+/D− pair, separates them. Pinned with a subtest that auto-upgrades to
  full equality if the repo ever gains EOP framing. Similarly, sigrok's
  no-CS SPI stitches words across mid-word idle gaps while the repo
  re-frames on them — each side's contract is pinned by its own subtest.

## Harness notes (oracle_test.go)

sigrok consumes the waveform as logic via its CSV input (`column_formats`,
`samplerate`); the repo decoders consume the same waveform as two-level ADC
codes (`bitsToCodes`). The `timeline` builder accumulates float time so
fractional samples-per-bit land exactly like a real capture. Annotations are
fetched ONE class per `sigrok-cli` invocation (`-A pd=class`) because the
output does not name the class per line; `--protocol-decoder-samplenum`
supplies sample ranges for alignment checks. Annotation texts are prose and
version-sensitive — parsers live next to the tests that need them.

The CI lane installs ubuntu-latest's sigrok-cli (0.7.2 / libsigrokdecode
0.5.x today). The pinned-PD-bug subtests are deliberate tripwires: when the
distro ships a libsigrokdecode that fixes one, the pin fails loudly and gets
upgraded to a strict comparison — treat such a failure as a signal, not
noise.

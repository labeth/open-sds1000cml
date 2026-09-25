# Compatibility and hardware contracts

All source references below are to the pinned commit in [the source manifest](evidence/source-manifest.json). These contracts describe the repository's evidence, including unfinished work.

## Fabric domains

| Domain | Host entry | Contract authority | Status in this branch |
|---|---|---|---|
| Vendor factory image | Agent takeover boundary | `specs/01-system-architecture.md`, `ota/internal/agent/takeover.go` | STOP/idle observation before takeover; not the owned-fabric runtime ABI |
| Owned default image | `SCOPE_CAPTURE=default` | `codegen/ifacedef/default.go`, `fpga/default/default.v`, generated `app/internal/iface/iface.go` | Boot loader with optional embedded image; build identity checked; physical key matrix absent; codegen drift currently fails |
| SRAM probe and precision revisions | `SCOPE_CAPTURE=sram`, `default-sram`, `acqsram` | `fpga/acq_sram/top.v`, `app/internal/sramcapture`, specs 12/13 | Separate magic `0x5a52`; default-sram startup accepts revisions 9, 10 and 11 with lock; capabilities vary by revision |
| New profile composition | `acq_profile_core` / `acq_profile_top` | `profile_core.v`, `command_port.v`, `host_read_port.v`, `docs/fpga-profile-command-abi.md` | Draft ABI 1, magic `0xacd2`, hardware-qualified flag zero; board wrapper exists but profile-aware ARM/kernel integration and device qualification are unfinished |
| SRAM benchmark | `fpga/sram_bench`, `srambench` | Benchmark RTL and diagnostics | Test image; a memory speed result does not qualify the ADC acquisition path |

The new profile defaults to `ENABLE_STREAM=0`. Discovery capability bits advertise finite capture, precision and edge triggering; stream support is added only for a stream-enabled composition. Base-profile snapshot opcode 6 and external-match mode reject because adapters are absent. Existing Go/browser protocol decoders do not imply hardware protocol triggers.

## Data path and clocks

The Cyclone IV E is `EP4CE10F17C8`, with 10,320 logic elements, 46 M9Ks and two PLLs. A 100 MHz board reference feeds image-specific PLLs. The interleaved acquisition path rate-matches 80 bits at 100 MHz, 64 bits at 125 MHz and 32 bits at 250 MHz. Five encode pairs deliver ten logical bytes, targeting 500 MS/s per channel. CH2's nominal sampling offset is 1 ns relative to CH1; physical aperture and gain/offset calibration remain distinct from logical lane mapping.

The logical chronological ten-byte frame is `E4.CH1, E5.CH2, E3.CH1, E1.CH2, E2.CH1, E4.CH2, E5.CH1, E3.CH2, E1.CH1, E2.CH2`. Rotated record starts and ring wrap matter to per-core calibration. The source carries an unresolved conflict between reported physical ADC population and ten active logical cores. The model does not resolve that conflict by inventing parts.

The NETSOL `S7A163630M` SRAM is 512K × 36, with 32 payload bits used. The external ring holds 524,288 words, or 2,097,152 payload bytes. Factory MAX V logic advances a 19-bit sequential address counter; its implementation is unavailable. K2 pulses, including read-pipeline flushes, affect position. G1 enables counting and K1 selects direction. Empirical origin/read-bias conventions must not be generalized to untested gap patterns.

| Format | One 32-bit SRAM word | Full record per channel |
|---|---|---|
| Raw | `CH1(t0), CH2(t0), CH1(t1), CH2(t1)` as four unsigned bytes, oldest first | 1,048,576 samples; 2.097152 ms at 500 MS/s |
| Precision | One unsigned Q8.8 value per channel | 524,288 samples; interval is `2^log / 500e6` seconds |

Legal precision log2 reductions are 4–20; zero selects raw. The new continuous core additionally requires log2 ≥ 8. This admission rule is not a measured sustained host throughput guarantee. The 63-tap ARM compensation FIR preserves the full sample count while exposing 31-sample edge guards. Guard-aware measurement and full fractional transport are required.

## Profile host contract

The new host RAM has **5,120 32-bit words**, split into **two banks of 2,560 words**. It uses five explicit asymmetric memory sections; the logical bank boundary is not a physical section boundary. The host packet FIFO has eight slots. Legacy revision-11 total capacities of 4,096 or 8,192 words are different geometries and cannot be hard-coded into a new-profile client.

Commands snapshot twelve host shadow words into the acquisition domain. Busy commands are discarded with sticky rejection. An accepted command result is not proof that capture has finished. Result status is a coherent 128-bit snapshot; query again after completion and interpret record geometry only while frozen and non-faulted.

| Profile selectors | Purpose |
|---|---|
| `0x00–0x0c` | Discovery: identity, ABI, profile/capabilities, geometry, formats, encode frequency, qualified flag and build ID |
| `0x10` write, `0x11–0x14` read/control | Opcode, pending/rejected/result-valid, result code, accepted opcode, accepted-command sequence |
| `0x20–0x2b` | Pre/post counts, recall offset/length, read bias, reduction/trigger settings, level, encode mask |
| `0x30` | Select bank/token/halfword offset; read current cursor |
| `0x31` read | Prefetched halfword; pop once at completed GPMC read |
| `0x32–0x36` | Window state, explicit release/fault clear, ready/tokens, valid bank counts |
| `0x38–0x3f` | First-word ordinals for each published bank |
| `0x40–0x47` | Coherent acquisition status snapshot, low halfword first |

Opcode 1 starts finite capture, 2 recalls, 3 starts streaming when composed, 4 halts, 5 forces, 6 requests the unavailable base snapshot adapter, and 7 queries status. Reserved bits reject. A zero recall length is empty, not full depth. Invalid configuration must not destroy an existing frozen record. Force/halt do not consume geometry, though mailbox availability still applies.

A bank must be ready with a matching token before selection. Read data-ready before popping. There is no WAIT pin: qualified physical GPMC timing must permit prefetch. End-of-bank does not release or cross banks. Only explicit valid release returns ownership. Pending RAM responses, token loss and upstream faults constrain release/read behavior. Ordinals expose gaps independently of the wrapping physical address.

## CPU and operations boundaries

The AM335x Linux host inherits `/dev/Gpmc` and `/dev/fpga_key` through the USB boot chain. The agent preserves descriptor lifetime; the app owns active acquisition bus transactions. SPI range/gain devices are off the GPMC bus. The default engine, legacy SRAM backend and profile RTL must never be driven as though their register meanings were interchangeable.

The agent owns the watchdog independently of the app, supervises A/B slots, and uses capture progress for health. The browser consumes HTTP frame/settings APIs, SCPI clients use VXI-11, and `otactl` uses operations TCP/NATS. Network exposure is a privileged trust boundary; checksums are not authentication. USB-TMC is a documented deferred implementation, not an enabled host transport.

Pin assignments are indexed in `evidence/pin-assignments.json`. `fpga/default/default.qsf` is the base mapping; `fpga/acq_sram/build.py` and `build_profile.py` contain image-specific SRAM-DQ/control overrides. The pin index must not be mistaken for a complete schematic or a BOM. The MAX V internals, analog component values and precise board revision are outside the available committed implementation evidence.

## Default loader build variants and verification scope

`bitstream_stub.go` is selected without `withbitstream`; `Default()` returns nil and the default-image boot path can only succeed when the fabric already verifies. `bitstream_embed.go` is selected with `withbitstream` and requires the generated, Git-ignored `default.rbf` input. The model traces both source variants, but the recorded offline tests use the stub variant. They do not validate an embedded bitstream or establish that the currently generated default interface agrees with an FPGA build; the recorded codegen drift remains relevant.

The injected-port tests check container size/header rules, bit order, nCONFIG/nSTATUS sequencing, data/init clocks, CONF_DONE timeouts/retries and pre/post identity checks. `device.go` provides real ioctl/mmap adapters; those hardware adapters were source-inspected and compiled, not exercised against device registers. `Force` affects order/header override, while `AllowAnyLen` separately bypasses the length check.


## Reviewed RTL simulation scope

The ordinal counter’s 64-bit progress is independent of physical SRAM wrap. Its test seeds values near every carry boundary and includes stalled steps. The host RAM test checks 30,757 halfword responses, including complete address ranges, disjoint concurrent access, invalid addresses and reset handling. Read-window and read-port tests exercise GPMC completion pulses, token ownership, short and full banks, final addresses and malformed controls using the existing digital GPMC model.

The word bridge holds one payload through its request/acknowledgement round trip. The destination acknowledges only consumption. Its reset contract is a common epoch reset with local synchronized release; independently resetting one side is unsupported. Four source/destination clock configurations pass ordered-delivery, stall and reset-epoch tests. These are ideal-clock simulations: they do not prove metastability immunity or physical bundled-data timing. Host RAM uses the behavioral branch, not the fitted Altera primitive. The packet packer is exercised by the full host-path tests with full banks and odd tails (1 and 2559 words), including zero padding and exact RAM readback.

Run `python3 models/sds1000cml/scripts/run_rtl_tests.py` from the instrument root, then `python3 models/sds1000cml/scripts/rtl_evidence.py --sync`. Generation independently verifies commands, source hashes, successful outcomes, reviewed module links and the bounded native result summaries.


The host FIFO has eight storage slots plus a registered output, allowing nine outstanding packets. Six phase/stall runs verify a 2562-packet packed burst, wrapped pointers, blocked output stability, overflow rejection and common-reset discard. Delaying first-stage observations is a digital stress case, not an analog metastability model.

Host ownership uses one-bit publication tokens. Immediate stale or duplicate releases are rejected, but a token retained across two reuses can match again. The ownership test checks 20 reuse cycles, paused producer acknowledgement, attempted descriptor overwrite, and reset while all clocks are stopped. Core release waits for acknowledgement in the RAM domain.

REQ-SDS-094 captures publication integrity: accepted data pairs must be ordered and match the nonzero descriptor before publication. A DATA packet is written on the edge after acceptance; a following descriptor publishes after that write. Four phase variants of both the complete host-path test and the fault test verify full/odd banks, malformed counts, preservation of owned data and metadata, and reset recovery. Invalid speculative descriptor payload may change private sink registers; it must not replace the held, published ownership copy.

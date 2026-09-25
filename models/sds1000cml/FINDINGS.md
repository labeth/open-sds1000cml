# Findings and evidence limits

Fresh results are retained in [the test-run index](evidence/test-runs/results.json).

## Current source findings

1. **Generated default ABI drift:** `GOPROXY=off go test ./...` in `codegen` fails `TestNoDrift`. Stale files are `fpga/default/regs.vh`, `fpga/default/regmux.vh`, `app/internal/iface/iface.go`, and `fpga/default/docs/REGISTER-MAP.md`. The model preserves this state; it does not regenerate or silently repair the modeled branch.
2. **Application UI style budget:** the full app suite fails `internal/web.TestInlineStyleBudget`: 18 inline styles exceed the budget of 14. Other package outcomes remain visible in the log; this is not reported as a passing full application suite.
3. **Profile integration is incomplete:** the new profile GPMC shell, acquisition composition, board wrapper and PLL exist. The ordinary application still selects `default`, `default-sram` or `sram`; its SRAM backend does not negotiate the new `0xacd2` profile ABI. Discovery explicitly reports hardware-qualified zero.
4. **Historical prose is stale in places:** `fpga/acq_sram/README.md` predates several precision/streaming changes. `docs/fpga-profile-command-abi.md` and the profile-core validation README say the wrapper is unfinished, while `profile_top.v` and `build_profile.py` now exist. The final sections of spec 13 describe an earlier unconnected streaming draft even though later streaming code and tests are present. No blanket claim is taken from these documents.
5. **Physical panel capability differs from README feature lists:** the current default engine logs that the owned fabric has no key matrix. Panel/menu code and software panel actions exist, but this does not establish working physical keys on that image.
6. **ADC population conflict:** spec 12 records conflicting operator package/core counts and measured logical activity. The model states ten logical cores and leaves physical population unresolved.
7. **Qualified checkpoint versus current source:** old timing/device reports apply to their stored source hashes and bitstream digests. The newer profile/control path must not inherit a predecessor's qualification. Recorded integrated-core timing failures remain relevant evidence, not a measured result for every later commit.
8. **Streaming rate limits:** legacy /512 and /256 trials include overruns. A successful finite recall, packet simulation or fast DMA segment is not proof of sustainable continuous processing, browser delivery or lossless storage.
9. **Authentication is not implied:** operations expose privileged commands/update handling. The model does not relabel checksums or private-network assumptions as cryptographic authentication.

10. **Agent listener disable comment disagrees with loading behavior:** `config.Config.TCPListen` says an empty value disables the listener, but `Load` uses `env("OTA_LISTEN", ":5900")`, whose empty-value rule selects the fallback. The default-settings test confirms `:5900`. The model records actual loading behavior; no source behavior is changed.

11. **Settings size check follows allocation:** `Load` reads the file with `os.ReadFile` before `Parse` rejects inputs larger than 64 KiB. The limit bounds accepted input, not the initial allocation. Existing oversize tests prove rejection only; source behavior is preserved.

12. **Analog comments and assertions differ from implementation:** offset conversion uses a fixed 100 DAC codes per input volt, despite older per-division comments. DC coupling returns the original slice, while AC/GND use copies. Probe factors are not restricted to 1/10/100 and nonfinite values are not rejected. Trigger calibration accepts nonzero-CPV overrides without coefficient validation. The `TestRemoveDC` assertion comparing a uint8 with 255 cannot prove saturation. The model records these distinctions in `evidence/analog-review.json`; source behavior is preserved.

13. **Q8 measurement histogram discrepancy:** the histogram mode helper clamps its upper search index to 255 even for Q8.8 histograms. An unchanged-source characterization using equivalent records reports raw overshoot 20.83% but Q8.8 overshoot 0%, because Q8 settled levels fall back to extrema. All 13 existing package tests still pass. See `evidence/measurement-histogram-characterization.json`. The model preserves the implementation and records the missing assertion.

14. **SCPI handler boundaries:** WFSU updates are incremental, reset retains trigger delay/level shadows, and ATTN/TRDL permit some nonfinite values. ATTN 0.5 reports a handler factor of 0.5 while the real analog front end stores 1. Waveform DAT2 uses an 8-bit window, while DESC retains full-frame counts. These behaviors are reproduced by `scripts/check_scpi_boundaries.py` using a test-only overlay; all 17 existing handler tests pass. See `evidence/scpi-review.json`.

15. **VXI-11 transport limitations:** an owned server connection with pending data panics on a `0xffffffff` read size in the local 386 probe; amd64 does not panic. This confirms 32-bit Go behavior, not ARM execution. Server RPC versions/authentication and portmapper reply acceptance are unchecked, and queued commands/replies have no cumulative limit. The takeover client accepts short write replies, can return after 64 unterminated reads without an error, and lacks overflow-safe validation for malformed reply arithmetic. Existing server/client tests pass with race detection but use local fakes. See `evidence/vxi-review.json`, `evidence/vxi-client-review.json` and `evidence/vxi-read-size-characterization.json`; these are documented baseline limitations, not repaired behavior.

16. **OTA envelope types are duplicated:** the canonical package comment claims the agent imports shared definitions, but the agent declares its own request/response types. Canonical args decode through `any` while agent args remain `json.RawMessage`; response data uses the reverse arrangement. Golden and fixed-point tests do not execute agent dispatch or prove arbitrary numeric precision. `evidence/rpcproto-review.json` records these limits. The model now audits type declarations explicitly; previous function coverage did not establish type coverage.

17. **Takeover has explicit exceptions and asynchronous completion:** force still attempts STOP/idle gates but ignores failures, while already-taken-over bus reclamation kills holders despite failed idle checks or a missing descriptor. Normal success can coexist with surviving holders, watchdog retries and pending network restoration. The boot overview and REQ-SDS-027 now reflect this behavior. Nine local fixture tests pass with race detection; successful hardware reads and physical idle remain unqualified. See `evidence/takeover-review.json` and `evidence/ota-gpmc-review.json`.

18. **Agent persistence errors leave live flags changed:** update mutates memory before the temporary-file write. A missing-parent write failure leaves TakenOver true in memory while a fresh load remains false. Load also ignores JSON decode errors, allowing valid fields to survive a later field type error. The isolated temporary-file probe reproduces both cases. A subsequent takeover call can take its already-taken-over branch based on the stale live flag. REQ-SDS-104 and `evidence/agent-state-characterization.json` preserve this limitation; source behavior is unchanged.

19. **Agent health timing has a misleading wire unit:** `since_change_ms` serializes a Go duration as nanoseconds; one millisecond emits 1000000. REQ-SDS-105 records this reproduced limitation. File activity establishes neither coherent frames nor valid token semantics. Three temporary-file tests cover grace, staleness and stale-token removal; I/O failures and physical acquisition remain unqualified. See `evidence/agent-health-characterization.json`.

20. **Upload failure cleanup and digest errors are limited:** failed commits retain closed sessions; eight missing-hash failures exhaust the session limit until a later start evicts sessions older than ten minutes. Directory creation precedes capacity rejection. The hash helper ignores read failures, reproduced by hashing a directory as empty content. REQ-SDS-106 records these limits; sequential tests do not establish concurrent eviction safety or filesystem durability. See `evidence/agent-upload-review.json`.

21. **TCP buffer size does not bound requests or shutdown:** a request larger than one MiB is accepted in the isolated pipe probe. The plain TCP implementation has no authentication/TLS or connection cap. Stop is observed between accepts; it does not directly unblock an idle listener or close established connections. Four tests pass, including stop followed by another accept; REQ-SDS-107 preserves their narrower scope.

22. **NATS delivery and reconnect announcements are limited:** reconnect buffering is disabled and publish errors are ignored. The reconnect callback logs but does not emit the online event promised by its comment. The localhost test proves the initial event and selected command parity only. REQ-SDS-108 retains these boundaries and does not claim broker authentication, TLS or device networking qualification.

23. **Host downloads can truncate before failure or report an incomplete file as complete:** GetFile truncates its local destination before the first RPC. A fake-transport probe confirms both first-request failure leaving an empty file and successful completion on an empty non-EOF reply advertising 100 bytes. Uploads compare digests but have no retry/resume/abort path. REQ-SDS-109 through REQ-SDS-112 separate transport, transfer, broker startup and relay control; the latter two remain inspection-only.

24. **Agent RPC serialization has two different guarantees:** direct Dispatch may return success with data that JSON cannot encode; DispatchJSON substitutes an error envelope. Argument decoding can return partially populated values with an error. REQ-SDS-113 and the isolated overlay preserve these boundaries. Requirement graphs now distinguish requirement-specific source coordinates from the complete verification artifact, preventing unrelated coordinates from overwriting scoped labels.

25. **Agent CLI comments advertise an unsupported status command:** all version/help aliases succeed, but `agent status` and an unknown command exit 2. Eight safe CLI cases confirm this mismatch. The supervisor, probe and signal paths were not exercised by that check; see `evidence/agent-cli-review.json`.

26. **Execution timeout does not bound completion with inherited output pipes:** a race-enabled isolated probe requests 500 ms but returns after about two seconds while its own sleep descendant holds the pipe. Command-start failure returns exit code zero with a non-nil error; consumers must inspect the error envelope. No device or factory command was executed. See `evidence/agent-exec-characterization.json`.

27. **Slot updates accept absent digests and arbitrary contents:** temporary-file probes confirm both application and agent updates accept an omitted digest and a non-image payload. Application activation accepts a nonempty regular file without execute bits. REQ-SDS-029 now describes conditional supplied-digest comparison; upload commits remain a separate required-digest contract. No payload ran, and the agent exit was captured. See `evidence/agent-activation-characterization.json`.

28. **Supervisor confirmation does not require a successful final outcome:** isolated race-enabled shell fixtures confirm slot B after stale-health termination and after exit status 7 when a prior health observation and elapsed stable duration satisfy the confirmation branch. A missing executable reaches the failure threshold without invoking rollback, leaving active B selected. These cases constrain REQ-SDS-028; they do not qualify continuous health or recovery. See `evidence/agent-supervisor-characterization.json`.

29. **Boot-loop success messages do not establish successful recovery:** isolated unchanged-source tests confirm six baseline behaviors. Four additional cases show that an agent exiting 7 after the stable interval is confirmed, arbitrary intent text suppresses rollback, a failed confirmation-pointer write still emits the confirmation log, and commands exiting 7 do not prevent agent launch. Private namespaces isolate the fixed `/tmp` marker and all stub processes. See `evidence/boot-anchor-test-run.json` and `evidence/boot-anchor-characterization.json`; native boot-contract integration is still pending.

30. **Browser epochs do not authenticate clients:** an in-memory fixture confirms unauthenticated GET and DELETE claims increment the epoch. The supersession predicate rejects only older nonzero parsed values; absent, zero, malformed, current and future values are exempt. Numeric prefixes with trailing text are parsed. The inspected main path passes the mux directly to plain HTTP, and diagnostic wiring checks only object availability. This is a selected source/fixture finding, not a full endpoint or deployment assessment. See `evidence/web-http-boundary-review.json` and `evidence/web-epoch-characterization.json`.

31. **Browser binary decoding is not strict header-schema validation:** fractional dimensions are coerced to signed integers while original header fields survive; unknown flag bits are accepted and envelope decoding precedes Q8 validation. Five deterministic cases characterize these boundaries. FFT processing takes only the largest power-of-two prefix and relies on caller-supplied options. REQ-SDS-023 and REQ-SDS-070 distinguish these source behaviors from live browser/server or physical qualification. See `evidence/browser-pure-review.json`.

32. **Test/runtime publication separation repaired:** requirement, functional-group and security path diagrams now exclude recognized test source from implementation/runtime inference while preserving verification coordinates. Regression cases cover Go, JavaScript and Verilog tests, mixed owners, explicit runtime evidence and direct test-file source roots. Recorded PDF samples confirm the distinction for binary-frame and RPC evidence. Numeric test coordinates still wrap mid-number; this remains a separate layout issue. See `evidence/publication-test-runtime-gap.json`.

33. **Spectrogram axis labels strip significant zeros:** the 100 MHz endpoint becomes `1M` because trailing-zero removal also applies to the integer string `100`. A recording graphics context reproduces this in `sgBlit`; existing palette/row Node tests do not cover axis labeling. REQ-SDS-068 preserves this defect. Export boundary checks also show envelope-tagged channel data is accepted, zero calibration selects a fallback, and VCD end time rounds up to a whole decimation stride. See `evidence/export-spectrogram-review.json`; instrument logic is unchanged.

## Validation meaning

The model's strict schema/reference checks and exhaustive source coverage tests prove that the reconstructed model is internally linked to the intended committed tree. They do not prove analog accuracy, external SRAM electrical timing, warm-reload reliability, FPGA timing closure or every user-facing feature on the device.

All 2,491 audited Go functions/methods and 336 top-level type declarations have requirement/model links. Three generated Go files are excluded from declaration annotation and traced through generator contracts. The RTL audit inventories 154 module symbols across 117 files; expression-macro diagnostics remain explicit and module links do not establish elaboration or physical behavior. The JavaScript audit records 958 named declarations/direct bindings across 75 .js/.mjs/.cjs files. These are declaration inventories, not exhaustive behavioral verification. See `evidence/trace-audit.json`, `evidence/verilog-trace-audit.json` and `evidence/javascript-trace-audit.json`.

Further source modeling is limited by the user to Go, TypeScript and Verilog. The pinned inventory contains no TypeScript files. Existing JavaScript work is retained, while Python support scripts, shell, C and assembly are excluded from remaining source-modeling obligations. Model verification utilities remain usable. See `evidence/active-source-scope.json`.

The combined profile simulation uses real command, status, SRAM transport, record and host-bank RTL, but mocked ADC/precision production and a behavioral memory. This proves only the exercised digital contract. Source-module test logs and their exit status are recorded separately from model consistency results.

## Completion work remaining for this model

- Finish the current combined publication refresh and visual review. Keep each view and requirement diagram complete rather than splitting it into repeated panels.
- Refresh final trace and MCP evidence after the last model edit. Older gap reports are checkpoints, not the current completion state.
- Reconcile requirement-level implementation and verification links with the source reviews. Missing implementations, known test failures, proposed profile features and unavailable hardware evidence must remain explicit; they do not require implementing new instrument features to describe the current system accurately.
- Preserve the reviewed register, pin, clock, image and feature evidence, including both distinct 160-pin maps and contradictory historical claims.
- Complete the delivery audit against the active language scope and the current generated documents. Declaration-link counts alone do not prove complete behavior, enforced interfaces, threat/control coverage or visual quality.

These are model-delivery checks. The instrument's missing features and physical unknowns above can remain explicit findings in a complete model; they are not silently treated as completed instrument engineering.


## 34. Browser view controls retain state across operations

REQ-SDS-068 source and recording-global cases show that channel changes retain old spectrogram rows and sequence, clearing retains sequence, and floor changes before allocation are discarded. REQ-SDS-125 records that equal Bode reference/DUT channels still send an arm request despite displaying a warning. The original two Chromium fixtures pass with synthetic frames; neither establishes physical accuracy, and the Bode test checks returned points rather than rendered pixels. Evidence: `evidence/browser-view-review.json` and `evidence/browser-view-test-run.json`.


## 35. Bode renderer labels and malformed inputs

REQ-SDS-125 renderer characterization reproduces 100 Hz/100 kHz/100 MHz labels as 1/1k/1M. Hostile gain, phase and frequency values produce nonfinite drawing coordinates; unsorted input can place points outside the plot; missing arrays throw. An infinite tick upper bound exceeds a bounded child-process deadline. The original breaker tests do not assert finite coordinates for hostile cases. Clean log-axis coordinates pass a recording-context check, which is not pixel or measurement accuracy. Evidence: `evidence/bode-renderer-review.json`.


## 36. Bode measurement state and SRAM integration

REQ-SDS-066 uses raw ADC-code ratios without channel voltage scaling. Channel changes retain old curve/live data; early invalid-length or sample-time returns retain live validity. Clear keeps the accepted-evaluation sequence, which is independent of Frame.Seq. At curve capacity new frequencies update live status but are not inserted. The sole direct production evaluator call is in legacy oneFrame; Run dispatches SRAM configurations into runSRAM, whose complete reviewed body does not accumulate Bode points. Six synthetic tests and nine race-enabled state cases do not establish acquisition integration or physical transfer accuracy. Evidence: `evidence/engine-bode-review.json`.


## 37. Numeric source-coordinate wrapping repaired

Engineering-model REQ-EMG-003 publication formatting now adds display-only spaces after filename colons and between line numbers in requirement implementation/verification diagrams. Canonical evidence and node identities remain unchanged. All 211 coordinate labels pass the structural check; focused PDFs and full publication pages 320, 322 and 378 show whole numeric coordinates. Internal filename/verification-ID wrapping, edge-label crowding, whitespace and tiny-text pages remain separate issues. Evidence: `evidence/coordinate-wrap-review.json`.


## 38. LCD foundation preconditions and physical boundary

REQ-SDS-021 assumes a full 800 by 480 RGB565 pixel buffer. Short buffers panic on in-range pixel access/fill; BMP export appends arbitrary pixel lengths while declaring a complete image. PNG bit expansion yields white RGB 248,252,248. SI-prefix formatting still emits scientific notation for extreme values and preserves NaN/Inf text. Bringup ignores GPIO write failures; OpenFB assumes fixed geometry and 32-bit ARM framebuffer-info offsets. The original prefix-boundary test and nine isolated characterization cases pass, but do not exercise device access, GPIO, framebuffer mapping or panning. Evidence: `evidence/lcd-foundation-review.json`.


## 39. Renderer cache, input and spectral limits

REQ-SDS-021 measurement caching reuses equal-sequence results despite changed samples, precision codes, filter guard and elapsed refresh time. Changed sequences remain throttled for 250 ms; voltage-scale changes invalidate the cache. Negative frame validity and decoder selectors, and a direct zero-window mask call, panic. Persistence compositing requires a concrete MemSurface. Mask geometry rejection now has a separate enabled-mask test; the original test also disabled the mask. REQ-SDS-070 LCD FFT uses power-of-two sizing and integer stride: a 31-sample record uses only its first 16 samples. A flat 64-sample spectrum with Nyquist 32 kHz returns the lower visible-bin frequency of 1 kHz. Fourteen original rendering tests mostly assert color presence/counts. Thirteen overlay cases characterize these limits and compare deterministic 16/32-point FFT bins with a direct DFT; they do not establish general numerical or physical spectral accuracy. Evidence: `evidence/lcd-render-test-run.json`.


## 40. Large LCD coverage diagram partitioned

REQ-EMG-003 publication generation now partitions requirement diagrams with more than four production files into groups of three. Canonical graph labels/node identities and trace matrix are unchanged; owner, verification and delegation context repeat. At the eight-file checkpoint, REQ-SDS-021 had three readable panels on pages 286–288 of that historical PDF, with explicit page breaks keeping continuation headings with their diagrams. Regression and model checks preserve all eight files and 63 source coordinates. Other dense diagrams remain outside this repair. Evidence: `evidence/coverage-panel-review.json`.


## 41. LCD waterfall history and address-label limits

REQ-SDS-068 LCD helper retains old rows when rate or channel changes and keeps scale/floor on Clear. Negative-infinite floor paints white; nonfinite Nyquist survives. Helper calls accept repeated sequences and envelope/flat frames; the reviewed LCD loop separately gates active view, new sequence and non-envelope data, always choosing C1. Breaker comments exceed their assertions: row advancement is not checked, and an empty len-17-sine fixture is skipped. REQ-SDS-021 address selection proves neither route preference nor reachability; typed-nil address pointers panic and enumeration failure caches an empty label until expiry. Thirteen original tests and twelve separate characterizations pass under race detector, with partial native status and no device/network qualification. Evidence: `evidence/lcd-spectrogram-netaddr-test-run.json`, `evidence/lcd-spectrogram-boundary-review.json`, `evidence/lcd-netaddr-boundary-review.json`.


## 42. Super-resolution display and synthetic-evidence limits

REQ-SDS-021/070 display helpers consume supplied fine-grid means. Negative bins are interpolated for FFT; XY retains invalid-bin pen lifts. Leading/trailing NaN means can panic during interpolation, while nonfinite sample intervals can yield an accepted FFT plan with zero Nyquist. Equal-size resampling samples half-bin positions. The 8192 cap has no explicit anti-alias filter: the characterized 16384-bin alternating grid becomes constant after resampling. Original 700/800 MHz tone assertions do not exercise acquisition reconstruction; the claimed median noise-floor statistic is an arithmetic mean of normalized magnitudes including signal bins. Review modes 3/4 fall back to the time trace. Six original tests and eight overlay cases pass under the race detector; physical spectral and reconstruction accuracy remain unqualified. Evidence: `evidence/lcd-superres-source-review.json`, `evidence/lcd-superres-test-run.json`, `evidence/lcd-superres-boundary-review.json`.


## 43. Remaining LCD tests and generated palette

All named LCD functions now have reviewed links, including Bode rendering, inversion, golden helpers and the fixed-seed no-panic test. The latter is 400 predetermined random cases, not a Go fuzz target; it excludes several known invalid-input classes. Thirteen golden tests permit 64 differing RGB pixels and preserve observed HUD/plot-label collisions. Bode lacks parallel-array validation, includes unused gain tail values in its range, and does not sanitize all-NaN/infinite ranges. The shared palette generator reproduces committed CSS and Go artifacts byte-for-byte in isolation. An unknown LCD mapping fails after CSS was already written, retaining the prior Go artifact; malformed hexadecimal text succeeds with a black Go color and invalid CSS literal. Original generated files and golden images remain unchanged. These are host evidence, not optical or physical gain/phase qualification. Evidence: `evidence/lcd-remaining-test-run.json`, `evidence/lcd-bode-boundary-review.json`, `evidence/lcd-palette-test-run.json`.


## 44. Decoder acceptance differs from full frame validity

REQ-SDS-018 implementations use protocol-specific OK semantics. Four race-enabled characterizations reproduce important limits: FlexRay accepts header-valid records of 5, 7 and 11 bytes despite an implied length of 10, while rejecting an exact-length bad frame CRC; SENT accepts a CRC calculated over clamped data despite a coding-error span; classic CAN retains a CRC-bearing body without delimiter or ACK; CAN-FD accepts its base-format data without a validated trailer. USB does not verify payload/token CRCs or differential SE0; UART/Manchester can format wider words as eight-bit text. Thirty-three original ordinary tests cover selected synthetic round trips and bounded robustness cases, often using shared or mirrored CRC/field helpers. They do not establish full protocol compliance, false-positive probability or physical input qualification. Remaining breaker/oracle/parity evidence is not yet credited. Evidence: `evidence/decode-implementation-test-run.json`, `evidence/decode-acceptance-boundary-review.json`.


## 45. JavaScript decoder equivalence is bounded

REQ-SDS-018 now includes all eight JavaScript decoder implementations. Their named declarations and direct function bindings are fully linked, but anonymous callbacks and top-level behavior are not separate trace declarations. The reviewed algorithms retain the Go acceptance limitations for CAN-FD trailers, USB CRC and length-dependent FlexRay CRC. JavaScript additionally has negative gap sentinels, configurable UART options, MSB-default SPI and different formatting behavior. Six Node characterizations preserve decimal/binary fallback to hex, middle-dot both-format, wide-value masking, positive-infinite frame timing, NaN sample-index fallback and missing-C1 dispatch failure. Seven original extended tests pass with 286 selected parity vectors, comparing OK, integer-coerced bytes, text and auto protocol choice rather than spans or all metadata/configurations. Their Go declarations now feed three native partial-verification summaries; no browser, hardware or standards conformance is claimed. Evidence: `evidence/javascript-decode-source-review.json`, `evidence/decode-extended-source-review.json`.


## 46. Extended decoder verification integration

REQ-SDS-018 links eight declarations in three additional Go test files, including the robustness configuration helper. Seven original tests contribute exact race-enabled outcomes to native verification: two ordinary fixed-seed robustness tests, four literal-vector tests and one Node parity test. Helper declarations do not become independent test outcomes. Comments preserve baseline tokens and exact source recovery. Bounded synthetic parity does not prove full result equality or published-standard conformance; browser execution, physical protocol qualification and the remaining breaker/oracle tests stay separate. Evidence: `evidence/decode-extended-source-review.json` and the corresponding `test-results/go/app/internal/decode/` summaries.


## 47. Breaker tests provide bounded evidence and disable two USB assertions

All ten decoder breaker files are reviewed. Twelve original tests pass with partial native status, but several labels overstate their assertions. SENT allows up to half its fixed noise captures to produce an accepted full-length result; MIL1553 logs some automatic-bitrate misses; SPI's nominal corruption affects leading idle and only checks no panic. Manchester truncation fixtures cut within the first preamble word. CAN negative confidence checks apply to classic CRC spans and sometimes require the full original payload to survive before failing. FlexRay positive fixtures commonly lack length-consistent payloads and frame CRCs; some negative fixtures have no clean accepted control. USB sets pinKnownBugs=false: its two named known-defect subtests log without asserting. A separate temporary overlay enables those assertions and both fail; these expected failures are retained outside native passing evidence. No protocol, electrical or browser qualification follows from the synthetic pass count. Evidence: the word, clock and framed breaker review reports and `evidence/test-runs/decode-usb-enabled-assertions.jsonl`.


## 48. UART/SPI oracle review does not imply external execution

The reviewed common harness queries one sigrok annotation class per subprocess and silently ignores unmatched output lines; positive payload/count checks are stronger than negative empty-output checks. UART's named back-to-back fixture adds one idle bit beyond the stop bit. BREAK and SPI mid-word gaps intentionally have distinct repository and oracle annotation/framing contracts. Selected endpoint comparisons allow one bit period. On this host, sigrok-cli is absent: both top-level tests skip when optional and fail before subtests when required. These availability probes are not protocol comparisons, and neither contributes passing native verification. Evidence: `evidence/decode-oracle-clock-source-review.json` and its two hashed execution logs.


## 49. I2C/CAN oracle contracts are narrower than protocol qualification

REQ-SDS-018: I2C merges start classifications and allows unequal endpoint tolerances; its glitch fixture intentionally expects different framing. CAN shares production CRC generation, pins expected external deficiencies for remote frames and DLC12, and does not use the external decoder to validate a corrupt CRC verdict. Its FD case covers base BRS0 payload only with a synthetic trailer. These are source-inspected assertions, not observed external results or independently verified claims about sigrok versions. Neither file contributes passing native evidence while sigrok-cli is absent. Evidence: `evidence/decode-oracle-bus-source-review.json`. USB and FlexRay oracle review remains before batch trace integration.


## 50. USB helper CRC verdicts and stale FlexRay comments

REQ-SDS-018: USB oracle CRC verdicts are recomputed by test helpers over raw application output; they are not application CRC error detection. The four-bit gap case explicitly skips after confirming a single merged packet. FlexRay introductory comments incorrectly claim header-only CRC checking: the implementation and corruption test check frame CRC for exactly sized frames, leaving the known length-mismatch gap outside these fixtures. Both suites compare selected synthetic fields and tolerate coordinate differences. All seven oracle source files are now reviewed; six optional skips and six required-dependency failures establish gating only. No external protocol comparison has run. See `evidence/decode-oracle-framed-source-review.json` and `evidence/decode-oracle-all-availability.json`.


## 51. Oracle trace integration preserves missing-execution status

REQ-SDS-018 now links all seven reviewed oracle files: 66 functions and eight types. Six exact optional dependency skips produce native not-run summaries; the shared harness does not contribute an independent test. Required dependency failures remain outside native functional outcomes. All affected decoder evidence was refreshed after the comment-only annotations, with token identity and exact baseline recovery verified. The model now has 1121 linked Go functions and 68 linked types; 1370 functions and 268 types remain unlinked, so overall model completion is not established. PDF regeneration remains deferred.


## 52. SRAM helper tests do not execute the capture loop

REQ-SDS-003/012/036/037/040: the reviewed engine SRAM path retains full physical depth, reads bounded live previews, and can replay a stopped full record. The twelve original tests cover planners, raw/Q8 packing, fixed precision rate, ERES and block averaging. None executes runSRAM, so retained replay, force/stop conditions, cancellation, bus faults and processing/trigger combinations remain source-inspected in this batch. The average aligns integer-rounded trigger offsets and crops common overlap; it is not fractional-delay alignment. Preview boundary tests do not exercise FractionBits8 sizing. Evidence: `evidence/engine-sram-source-review.json` and its hashed race-enabled log. Native integration remains pending.


## 53. Legacy interleave correction infers phase without record metadata

REQ-SDS-074 requires valid record-phase metadata. The SRAM engine collaborator instead matches a joint five-phase offset fingerprint and bypasses on weak/ambiguous matching, clipping, unsupported scales or incompatible lengths. Its two passing tests cover constructed rotations and selected guards; they do not establish metadata-based phase qualification or physical measurement of verified ranges. The JSON loader is untested by these originals, and lookup-table reuse assumes offsets remain unchanged after initialization. See `evidence/engine-interleave-source-review.json`. Do not credit these tests as satisfying REQ-SDS-074.


## 54. Legacy acquisition-mode tests provide limited arithmetic and telemetry evidence

REQ-SDS-012: legacy averaging is a sliding, integer-byte ring with per-column validity masks, distinct from SRAM Q8 block averaging. Six selected mode tests pass, but the off-record test permits all-midscale output and the uniformity test permits unchanged zero telemetry. Mode processing and admission excerpts are reviewed; qualify_test.go and engine_loop.go are not yet completely reviewed. See `evidence/engine-modes-source-review.json`. No measured noise or general locking qualification follows.


## 55. Software qualifiers operate on record-relative thresholds and sample counts

REQ-SDS-011/012: pulse widths and slope traversal times use whole-sample index differences; only anchors interpolate. Slope scanning tolerates some reversals despite its monotone description. Video line selection counts sync entries from the captured record, not an identified video-frame origin. Thirteen original qualifier-file tests pass under the race detector, including six previously reviewed mode tests; those six must not be double counted. Fake-bus publish policy does not validate runSRAM. See `evidence/engine-qualify-source-review.json`.


## 56. Uniformity telemetry has no explicit requirement in the current model

The reviewed uniRing implementation computes cross-frame statistics, but REQ-SDS-025 concerns health gating/progress and REQ-SDS-009 concerns per-frame interpretation metadata. Neither explicitly specifies uniformity telemetry. Its three methods, type and TestUniformityStats therefore remain unlinked in the prepared engine processing batch. Add an accurate source-derived requirement before claiming complete declaration traceability; do not hide the gap with unrelated links. The batch deduplicates overlapping test reports and retains 26 eligible original tests.


## 57. Engine processing integration retains bounded verification

The SRAM, interleave, mode and qualifier batch adds 54 function links and seven type links, preserving Go tokens and exact baseline recovery. Twenty-seven unique tests pass; 26 contribute native partial summaries for existing requirements. Uniformity telemetry remains unlinked, and REQ-SDS-074 gains no metadata qualification credit. The capture loop remains source-inspected only. Browser view and Bode evidence was refreshed because it snapshots the changed engine files. Current evidence: `evidence/engine-processing-test-run.json`.


## 58. Explicit uniformity contract resolves the trace gap without overstating evidence

REQ-SDS-126 now specifies centred and fixed-position cross-frame sample dispersion, matching uniRing and the inspected publication block. This resolves the missing contract in finding56; it does not convert TestUniformityStats into proof of telemetry updates or physical locking. The completed processing batch has 58 function links, eight type links and 27 partial test outcomes. REQ-SDS-074 remains unproven on the legacy fingerprint path.


## 59. Timebase planner comments exceed directly tested policy

REQ-SDS-010/008: legacy envelope capture sizing adds margins and caps counts without checking a deadline, despite deadline-gated comments. Integer divisor rounding does not guarantee exact displayed-span identity. Six original tests pass, but their wait-budget cases only hit the40ms floor, and bands_test.go does not directly validate envelope/roll geometry or high divisor words. The legacy5ns quantization is separate from SRAM planning. See `evidence/engine-bands-source-review.json`.


## 60. Slow display paths apply reconstruction policies beyond raw sample placement

Envelope fill is target/deadline gated, and a detected edge can switch it to an anchored trace. Unseen envelope columns copy previous observed ranges. Roll can expand all columns to global observed extrema and initially repeats the first sample or midscale fallback. ETS averages and interpolates; its phase coverage is not identical to output-column coverage. Eight fake-bus/planner tests pass but do not qualify physical losslessness, full reconstruction accuracy or all timeout/fault paths. See `evidence/engine-slow-paths-source-review.json` (1018 completely reviewed lines).


## 61. Timebase and slow-path trace integration preserves reconstruction limits

The five-file batch links50 functions and two types. Fourteen original tests contribute partial native evidence, with exact source/log hashes and unchanged Go tokens. Envelope margins, roll global-range expansion and ETS interpolation remain explicit policies; no gapless physical acquisition or timing qualification is inferred. Engine processing, browser and Bode evidence was refreshed for changed source snapshots. See `evidence/engine-timebase-native-integration.json`.


## 62. Serial trigger matching is span-based, not universal packet validation

REQ-SDS-013/018: the registry connects ten decoders, but most share a data-span matcher that ignores explicit packet/error boundaries and applies only a gap-width heuristic. I2C uses address/direction and transaction-bounded data, without ACK qualification. Twelve originals pass; the named fuzz test is4000 deterministic cases restricted to protocol IDs-1 through4. The fake-bus mask-composition test covers five NORM mismatches, not the60-hold AUTO fallback boundary. See `evidence/engine-serial-source-review.json` for852 reviewed lines and exact evidence.


## 63. Zone/mask publication tests coexist with API preconditions

REQ-SDS-014: nine original tests pass, including NORM/AUTO publication, held-frame mask counting, stop-on-fail and SINGLE composition. MaskFails returns shallow frame-buffer references; maskEval trusts envelope lengths; identity indexing and sample selection differ for invalid channel values. ClearMaskFails does not reset the timestamp epoch or capture ordinal. The 432-case geometry test is deterministic and shares the implementation mapping formula. Two breaker files remain for review. See `evidence/engine-zonemask-policy-source-review.json`.


## 64. Serial and zone/mask verification is consolidated with explicit exclusions

REQ-SDS-013/014: the ten-file batch links 57 functions and nine types. All 23 original tests pass under the race detector. The zone differential run asserts 2184 of 2250 verdicts, skipping 66 boundary ties; the mask campaign conditionally omits non-detectable candidate violations without counting them. Synthetic anchors bypass actual trigger estimation, and production mask construction is not an independent morphology reference. The 432-case geometry and 4000-case serial robustness tests are fixed deterministic campaigns, not Go fuzz targets. Snapshot aliasing and malformed-envelope preconditions from finding 63 remain. See `evidence/engine-zonemask-breakers-source-review.json` and the combined `evidence/engine-serial-zonemask-test-run.json`.


## 65. Engine orchestration trace covers implementation without converting heuristics into guarantees

The remaining 16 engine files (4,922 lines) link 162 functions and 12 types. REQ-SDS-127 records diagnostic history, owner callbacks and device-clock tuning; REQ-SDS-128 records dead/degraded capture recovery telemetry. Forty-five original host tests pass. Failed bus writes still advance command shadows, callback timeout does not cancel work, and retry data can retain initial-capture metadata. Fill equality and period-five tails remain heuristics. Stitched time/gap accounting is not a gapless acquisition guarantee. The concurrency campaign omits several subsystems and its final assertion uses a conditional cumulative publication count. Source detail: `evidence/engine-core-source-review.json`; execution: `evidence/engine-core-test-run.json`.


## 66. Bus transport and timing evidence distinguish checked transfers from historical fallbacks

The 15-file Go bus batch covers 4,233 lines, 198 functions, 37 types and 33 original host tests. Six contracts (REQ-SDS-129..134) separate register transport, window verification, timing sweeps, persisted boot gates, surveys and experimental kernel ownership. REQ-SDS-081 retains a cache-coherency gap: invalidation errors are discarded and the fresh-buffer fallback contradicts the adjacent recorded cache observations. Timing restoration can fail; read-only mappings can have device side effects. The three reviewed C modules remain inspection-only, with no build/load or hardware credit. The two arena types now link to frame ownership/metadata, completing the engine declaration links. See `evidence/bus-source-review.json` and `evidence/bus-test-run.json`.


## 67. Complete panel source review separates host control tests from live acquisition behavior

FU-APP-PANEL / REQ-SDS-022: all 25 Go files (4,810 lines) have been reviewed. The 31 original tests and three subtests pass with the race detector, and the subsequent integration now links all 196 functions and 15 types under REQ-SDS-135..140. Constant-sequence fixtures leave mask building and super-resolution accumulation idle; autoset tests cover selected helpers rather than the full sweep. Timer-based settling does not prove fresh captures, cancellation does not restore the complete instrument state, and worker replacement does not join the preceding worker. Settings restoration can allow equal UART channel roles that a later protocol switch does not repair. These are source findings, not new hardware qualification or comprehensive runtime failure demonstrations. See `evidence/panel-source-review.json` and `evidence/panel-review-test-run.json`. PDF regeneration remains deferred to a major milestone.

The integrated execution is recorded in `evidence/panel-test-run.json`; the earlier review-run log remains preserved. Eleven native summaries retain partial assertion evidence. Duplicate `chaos_test.go` basenames are admitted only with disjoint requirement sets and exact publication attachment checks.


## 68. Super-resolution estimates and compensation remain algorithm evidence

FU-APP-SUPERRES / REQ-SDS-141..142: ten Go files (2,179 lines), 46 functions and 15 types now have reviewed links; two Node harnesses were also inspected. All seven original tests pass with the race detector and Node required. Stack parity compares dispositions/counts and mean checksums on one synthetic waveform family, not complete arrays or independent physical truth. The compensation target formula reaches half amplitude at its parameter frequency despite the -3dB wording, and the automatic40MHz floor can override the raw-Nyquist ceiling. Caller preconditions, nontransactional reseeding, correlated occurrence halves and unbounded score history remain explicit limits. The stored response curve is not newly qualified hardware calibration. See `evidence/superres-source-review.json` and `evidence/superres-test-run.json`.


## 69. Diagnostic sweep progress has observed data races; restoration is conditional

FU-APP-DIAG: all six Go files (4,132 lines) have been reviewed. Thirteen of the 14 original tests pass under the race detector; `TestGpmcSweepPersistApply` fails. Four reports concern production sweep state read through Phase/Setting while Step mutates it; another concerns the timing-port fake. The failure is retained in `evidence/diag-review-test-run.json` and `evidence/diag-race-characterization.json`, with the sweep failure preserved in native evidence after integration.

The full review also identifies conditional restoration, ignored bus errors, unmatched hardware hypotheses, and untested probe/crank/listening flows. Census restores DIAG_IDX rather than LANE_IDX; generator cleanup and CS3 restoration are not universal; a crank PASS denotes movement, not verified SRAM data. The diagnostics remain source behavior under review, not authorization or evidence for running physical probes. Requirement-link integration now covers all 95 functions and 44 types under REQ-SDS-143..151. See `evidence/diag-source-review.json`.

The integrated diagnostic execution is `evidence/diag-test-run.json`; REQ-SDS-146 is explicitly failing in both the requirement evidence and native test summary. Thirteen other outcomes remain passing assertions with partial coverage. The summary aggregator now propagates failures instead of marking every tested requirement partial. The baseline race log remains retained.


## 70. FPGA build success is separate from report policy and hardware qualification

FU-FPGA-QUARTUS / REQ-SDS-152..154: the complete three-file driver (1,554 lines) now links 53 functions and seven types. All 22 original race-enabled tests pass, including both optional existing-report tests; input hashes preserve which reports were consumed. Host tests use temporary fake Quartus tools. Unreadable memory bypasses the gate, staging removes the configured output directory before validation, and QSF interpretation is a literal subset. Callers must enforce report defects separately from Run errors. Setup-only parsing accepts recognized tables without proving complete clock/constraint coverage; NaN slack is not negative. Bitstream byte count does not establish device identity or contents. See `evidence/quartus-source-review.json` and `evidence/quartus-test-run.json`.


## 71. Shared interface generation retains drift failure and implementation boundaries

The complete codegen module (16 Go files, 4,138 lines) now links 109 functions and 15 types under REQ-SDS-155..160. Thirty-six original race-enabled tests pass, including generated decode simulation and temporary Go-binding compile/vet; TestNoDrift fails. The failure remains attached to REQ-SDS-160. Source review distinguishes validation from arbitrary-schema compile guarantees, folded fingerprints from collision-free identity, schema declarations from implemented RTL, and reference arithmetic from physical timing. Nested generated table copies alias field slices; constant/build-ID aliases are not resolved generically by the read-mux emitter. Raw RTL comments change the digest and wordfmt annotations propagate to newly rendered bindings; existing generated instrument artifacts were not overwritten. See `evidence/codegen-source-review.json` and `evidence/codegen-test-run.json`.


## 72. Web APIs retain route-specific validation and owner boundaries

FU-APP-WEB / REQ-SDS-161..166: eight production files and 14 host test files (4,306 lines) now link 169 functions and 17 types. All 34 original tests and six subtests pass with the race detector. Client epochs are advisory admission checks, not authentication or cancellation. Diagnostic peek accesses the bus directly outside diagnostic-owner locking; several routes accept GET operations. Control success can mean staged work. Frame shaping and measurement caching assume valid owner geometry and immutable sequence identity. SRAM uses a separate handler, with non-atomic readiness/recall observations and best-effort chunk deadlines. These fake-owner and recorder tests do not qualify hardware or network timing. The existing diagnostic sweep race and code-generation drift failures remain explicit. See `evidence/webapi-source-review.json` and `evidence/webapi-test-run.json`. PDF review remains deferred to a major milestone.


## 73. Startup health and diagnostic commands have mode-specific limits

The application and SRAM command batch reviews 12 files (2,256 lines), linking 58 functions and eight types under REQ-SDS-167..174 and the existing descriptor, capture-mode, fabric, health and revision-10 timing contracts. The two original helper tests and five timing subtests pass; six packages compile without invoking main. Default diagnostic health can precede coherent frames, and frame-labelled refresh follows engine beats after the initial threshold. This is an explicit exception to the literal REQ-SDS-025 coherent-capture gate. SRAM-only storage validation is lexical rather than symlink-aware.

HUD composition can arm/disarm Bode accumulation, and the LCD cache omits several visible configuration fields. Screenshot rendering does not reproduce live freshness or persistence history. Default process exit does not join its services. Diagnostic commands have branch-specific argument, cleanup and error behavior; factory burst probes do not enforce image identity or vendor suspension, and reads can consume data. Bench and profiling output is observational evidence, not physical qualification. The FPGA build CLI explicitly enforces parsed report defects beyond the driver's execution result. See `evidence/entrypoints-source-review.json`; no command main, hardware IO, FPGA load, Quartus flow or real-time scheduling was executed.


## 74. OTA command and boot policy are bounded by host and shell behavior

Eight source/config files (1,204 lines, including 894 Go lines) are reviewed. Five Go files gain 32 function links under REQ-SDS-175..178 and existing host-client/boot contracts. Six original boot tests pass with bounded temporary agents under host /bin/sh; four packages compile. They do not prove BusyBox, descriptor inheritance on the instrument, mount behavior, power-loss recovery or remote command success. Shell artifacts remain source references pending native shell declaration inference.

The anchor confirms on elapsed duration after exit, irrespective of health or exit code, and any nonempty intent suppresses crash counting. Pointer writes are unchecked direct writes; environment/config and commands are trusted executable shell, and rotation does not bound long-running agent logs. The CLI uploads update-app twice and prints remote exec status without propagating it as its local exit code. The reference stub reports timer-based health even without GPMC. These are modeled behaviors and limitations, not authorization to run device, power or broker commands. See `evidence/ota-entrypoints-source-review.json`.


## 75. Browser harness outcomes distinguish fixture races, performance and policy failures

FU-APP-TESTENV and FU-APP-WEB / REQ-SDS-179..182: the complete 40-file harness/support review adds 87 Go function and three type links. The original 22-test race-enabled campaign records 14 passes, six failures and two skips. Acceptance, Chaos, Superres and ZoneMask report races involving shared fake state or synthetic frame-generator closures; browser assertions reporting success do not override those failures. Stack performance fails the greater-than-threefold cache-speed ratio (cold best 2.9 ms versus warm best 2.6 ms), although its 2.8 ms warm mean satisfies the separate 200 ms bound. Inline style count 18 exceeds budget 14. PerfServe is intentionally disabled; external sigrok interoperability is skipped because sigrok-cli is unavailable.

Source review narrows the evidence: serial-trigger ordering measures request dispatch, invert checks mutate browser state directly, and the deep-memory driver does not check FFT or decoding despite its wrapper comment. Fuzz and chaos use bounded synthetic fixtures and can skip or suppress unsuccessful actions. Numerical Node fixtures do not establish calibrated hardware accuracy; locally reimplemented drizzle comparisons and shared response curves are identified explicitly. See `evidence/browser-harness-source-review.json` and the immutable original `evidence/browser-harness-review-test-run.json`. All Go functions now have declaration links; this is not complete model coverage, and type, other-language and hardware gaps remain.


## 76. Complete Go declaration links do not close behavioral or cross-language gaps

The remaining 74 top-level types across 31 previously function-reviewed files now have explicit requirement mappings. All 2,491 audited Go functions/methods and 336 top-level type declarations are linked. The type review distinguishes analog display zoom from hardware gain, calibration records from validated physical accuracy, saved offset intent from numerical zero, SCPI echo shadows from implemented hardware, and SRAM word coordinates from sample timing. OTA pointer/digest/status records remain observations rather than proof of atomic update or healthy execution. Test doubles remain test-support evidence.

Each added comment preserves exact recovery of the pinned baseline and the previous Go token hash. Historical review hashes are retained with an explicit transition record in `evidence/remaining-go-types-source-review.json`; they are not silently replaced with current hashes. The full model still has requirement-evidence and other-language gaps. PDF refresh remains deferred.


## 77. Acquisition RTL results retain digital scope and the missing FIFO model

Thirteen acquisition implementation files and seventeen original benches (2,847 lines) now carry 38 module links, including explicit test-support mocks. REQ-SDS-183..185 add ADC source epoch/fault policy, trigger-word alignment and command acceptance/result snapshots; existing lane, record, recall, ownership, command and reset contracts gain reviewed source and test links. Thirty-nine of forty frozen-input cases pass, including 19-bit full-depth finite capture and recall. The original ADC interleave bench fails compilation because `dcfifo` is unavailable, so its behavioral result remains not-run. No replacement FIFO model was substituted.

The lane permutation test shares its map macro with the implementation; integration benches replace ADC/precision with mocks or use ideal SRAM/clock models. Command acknowledgment establishes delivery, with acquisition acceptance reported separately. The interleave mailbox depends on related-clock phase/rate assumptions; finite capture origin and physical timing remain unqualified. Comments are proved equivalent to the exact pre-annotation compilation inputs rather than silently replacing execution hashes. Native summaries preserve both positive and unavailable evidence.

Engineering-model-go now distinguishes unresolved expression macros in assignment, index and shift positions from macros that could hide declarations. Expression values remain explicit warnings requiring build evidence; declaration ambiguity remains an error. Regression tests cover preserved endmodule boundaries, macro identifiers with digits and declaration-position rejection.


## 78. Precision arithmetic equivalence has explicit oracle and clock-domain limits

REQ-SDS-040 now links four precision modules, the shared tail and seven original testbenches, including their ideal FIFO support modules. All seven frozen-source simulations pass: full-width pair accumulation/stalls/rearm; 500 independent first-CIC FIR outputs; legacy DC/fractions; shared-wrapper comparison; one-core-cycle configuration resets; queue corner cases; and the full remaining-log 0..12 tail sweep with overflow, illegal configuration and reset. The full tail run retains all 16 equivalent epochs. Exact baseline/comment equivalence preserves the execution hashes after annotation.

The shared composition oracle is hash-pinned but shares unchanged fast CIC modules. Full tail-only coverage does not imply complete-wrapper coverage through decimation log20. Ideal FIFO models do not prove vendor timing or physical CDC; the wrapper reports a sticky fault while the acquisition source rejects faulted epochs. The legacy top bench is reviewed but its simulation is deferred to the top-level batch. The separate five-file stream implementation review preserves distinctions between the legacy CPU FIFO, reduced-rate packet streaming and SRAM-backed acquisition; it does not yet add verified trace coverage.


## 79. Stream variants retain distinct buffering and verification contracts

Five implementation files and three original benches now carry eight module links. Six packetizer/bank cases pass at BANK_AW 3, 10 and 11, checking order, odd/even/empty final seals, simultaneous word and stop, matching releases, retained banks on overrun and reset. Two SRAM-wrapper cases pass: AW13 with 10,003 words wraps physical addressing; AW19 with 65,539 words uses production address width and crosses 16-bit accounting, but does not cover the full 19-bit wrap. Those benches exercise ideal memory and fixed clocks, with no injected source fault or producer-arbitration scenario.

REQ-SDS-186 covers the reduced-rate acknowledged mailbox, separately from the profile eight-slot FIFO. REQ-SDS-187 records the retained standalone adc_stream implementation, whose 4096-word vendor FIFO is read low halfword then high halfword and latches overflow/underflow. No instantiation was found in the searched FPGA .v/.qsf sources, and no direct original behavioral bench exists; its verification remains not-run. Parameterized stream_banks are not credited as the profile's 5120-word host geometry. Exact annotations preserve the frozen execution inputs.

The board/profile source review covers nine further files and 835 lines. Its original profile-core GPMC bench passes with real bus commands/readout, ADC/precision mocks and ideal SRAM; other build variants and native trace integration remain pending. It records the profile ABI's explicit hardware-qualification=false flag, unavailable snapshot/match services, reset stretching, fixed PLL intent and the legacy top's compile-time variants and clock-domain assumptions. Source comments claiming historical board qualification are not treated as fresh evidence.


## 80. Board variant evidence retains the legacy origin mismatch

Sixteen complete implementation/test files (1,202 lines) add 32 literal module links. Existing profile discovery and unavailable-service requirements now have direct source/test links; REQ-SDS-188 distinguishes selected GPMC transfers and qualified read completion from the default legacy read-edge policy. Twelve of fourteen exact cases pass, including the original profile-core recipe, both stream-buffer sizes, vendor-backed precision and ADC chronology/trigger/recall variants, burst readout and the qualified-read phase/skew sweep. The continuation bench only prints values; an external oracle requires all 37 expected triples.

The original base and interleave full-capture benches fail their capture-metadata assertion. A separate read-only observer reports record length 524,288, origin 17 and position 17; those assertions expect origin and position 16. No instrument code or original assertion was changed, and later assertions are not credited. These results identify the discrepancy without deciding whether the implementation or historical expectation should be revised.

Three local Quartus simulation-library files are copied and hash-pinned for vendor-dependent variants. Those runs omit the separate behavioral DDR definitions to avoid duplicate primitive modules. This extends digital evidence; it neither replaces the earlier standalone ADC compile failure nor qualifies physical lane timing, board SRAM origin, PLL phase accuracy or analog performance. The profile hardware-qualified flag remains explicitly zero. Common-source annotations are connected to earlier frozen test inputs by reconstructing the exact approved comment overlay from pinned Git bytes; no execution hashes are rewritten.


## Command status and explicit release trace review

REQ-SDS-077 now describes the actual status-validity lifetime: a returned response makes the coherent status readable, and accepting the next command or resetting invalidates it. The previous wording incorrectly kept status readable until the next response. REQ-SDS-079 and REQ-SDS-080 now link their existing bank-release and force/halt implementations and original benches. No executable source or assertion was changed. The host-window and dependent host-port simulations were rerun and passed. Other frozen digital outcomes retain their original hashes; historical requirement comments are accepted only after exact reconstruction from pinned Git bytes. See `evidence/command-window-trace-review.json` for assertion coverage and untested combinations.

REQ-SDS-074 remains an unresolved metadata obligation. The reviewed legacy calibration infers rotation from a measured two-channel fingerprint and cannot establish valid record-phase metadata. Its rotation tests must not be relabeled as proof of that condition; `evidence/engine-interleave-source-review.json` already records this distinction.

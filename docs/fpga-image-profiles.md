# FPGA image profiles — proposed architecture

Status: profile composition implemented in the acquisition core; board images,
ARM profile selection and hardware qualification remain unfinished. This replaces the goal
of putting every acquisition engine and protocol trigger in one FPGA image.
It does not reduce the requested sample rates, precision, capture depth or
protocol support of the instrument as a whole.

## Selection policy

The shared RTL exposes `ENABLE_STREAM` (default 1). Building with 0 removes
the continuous producer and ingress storage while preserving finite capture,
recall, precision processing and the shared host path. Operation 1 then returns
a request error without invalidating an existing frozen record. The diagnostic
build accepts `--deep-only`; it uses a separate output directory and checks that
streaming CDC endpoints are absent. This is not yet a board bitstream.

Timebase, sample reduction, gain, threshold, trigger position, UART baud rate,
SPI mode and trigger bytes are runtime settings. None should cause an FPGA
reload. A user chooses an acquisition mode and a trigger family; the ARM selects
a qualified image advertising those capabilities. Reuse the loaded image when
it already supports the request. Multiple protocol families may share an image
if measured fit/timing permit; there is no requirement for one image per decoder.

## Initial profiles

| Profile | Included acquisition engines | Trigger capability | Use |
| --- | --- | --- | --- |
| Deep capture (default) | finite writer + frozen recall | edge, automatic/forced, external match | Full SRAM pre/post-trigger record across all timebases |
| Deep capture + serial family | same finite writer + recall | edge plus selected UART, I2C or SPI module(s) | Hardware protocol triggering with full SRAM recall |
| Continuous | continuous SRAM buffering + ARM drain | basic event markers initially | Sustained reduced-rate precision data and ARM history |

Continuous mode is an explicit acquisition-mode choice, not an automatic
consequence of changing timebase. Above the measured sustainable ARM rate,
the app must reject gapless recording or offer deep triggered capture; it must
not silently discard samples or advertise gapless operation. Slow timebases
remain usable in deep-capture mode. Protocol decoding of already captured ARM
data does not require a new FPGA image; only an unavailable hardware trigger
capability does.

## Shared RTL, independent compositions

Reuse one implementation each of ADC lane mapping/interleave, precision filters,
trigger-event alignment, SRAM electrical transport, host packing/FIFO/RAM,
ownership, GPMC register shell, reset handling and identification. Keep the
8-slot host packet FIFO: a 4-slot trial overflowed during integrated traffic.
FPGA resources should be removed through compile-time engine composition,
not by cloning and gradually diverging implementations.

The default deep profile omits the continuous engine and its ingress FIFO.
Recent hierarchy reports attribute roughly 1700 LEs and 18 M9Ks to the ingress
queue and stream scheduler combined (estimates from the unified fit, not a
promise of post-fit savings). These are much larger targets than small control
edits. Keep the complete external SRAM record: 1,048,576 raw samples/channel
or 524,288 Q8.8 samples/channel, two channels, 100 MHz encode / five-phase
interleave targeting 500 MS/s/channel. Actual ADC order, timing and effective
precision still require qualification.

The continuous profile omits the finite-trigger writer. Whether its frozen
recall reader is needed depends on the final pause/recall contract; do not
remove it while promising SRAM recall that the profile cannot deliver. ARM
history and storage are separate from FPGA capture depth and must report their
actual retained interval, gaps and epoch boundaries.

Protocol modules consume the shared chronological sample/threshold stream and
emit a standardized event with a sample ordinal and reason code. Their declared
latency must align the event with the ring write position, including matches
recognized after a complete byte/string. Baud, address, masks, byte patterns and
SPI mode are registers. UART/I2C/SPI are first; additional families plug into
this interface. Front-end bandwidth and available channels constrain which
buses can be observed; do not infer logic-analyzer channel capability from
having a decoder module.

## Common ARM interface and image manifest

All profiles share one versioned GPMC shell and command/record conventions.
Expose image/profile identity, ABI version, build digest, feature bitmap,
channel/sample format, SRAM depth, host-bank geometry, supported reduction
range, trigger modules and their latency/limits. Unsupported operations return
an explicit error. Do not reuse a register with different meaning in another
profile. The kernel negotiates advertised geometry; no hard-coded assumption
that the new 5120-word total host buffer (2560 words per bank) equals legacy
rev11 buffer geometry.

A build manifest binds profile, board revision, lane map, RTL digest, bitstream
SHA-256, ABI, clocks, fitted resources, timing/CDC reports and hardware test
results. Only qualified manifests enter the normal image selector. Developer
images remain explicitly marked. Protocol packages reuse the same shell and
acquisition modules; the host loads parameters through a common trigger API.

## Switching and retained data

Stop new acquisition, drain/export data to ARM RAM or USB if it must survive,
quiesce the sole GPMC/DMA owner, and preserve user/analog settings in ARM RAM.
Load only volatile Cyclone configuration through CS3 selector07, keeping the
inherited GPMC descriptor ownership rules. Never use selector08/MAXV flash or
scope internal permanent storage. Verify identity, ABI, PLL lock, reset state
and a bounded bus/SRAM self-check before rearming. Restore analog and trigger
configuration through the profile's advertised interfaces.

An image change is an acquisition gap and starts a new epoch. Do not promise
SRAM records survive reconfiguration: physical retention alone is insufficient
without validated address-counter and metadata restoration. Export retained
records before switching. Keep a known-good image on USB for recovery.

The user clarified that cold mains cycles were experimental recovery for a
potentially stalled MAX, not a requirement for normal reconfiguration. Warm
reload is authorized for testing and is the intended normal path once the
images and switch sequence are qualified. Use a cold cycle for recovery when
the configuration path is stalled; do not require one for every image change.
Warm-switch reliability and duration remain qualification targets.
Measure separately: USB/RAM staging, bitstream transfer, configuration/PLL
startup, register/analog restoration, validation, and time to first valid
capture. Repeat switches between profiles under idle, frozen and previously
streaming states. No warm-switch speed or reliability claim until measured.
Do not require network download at every switch: stage qualified images on USB
and optionally cache them in ARM RAM.

## Implementation order and proof

1. Introduce a deep-only composition using existing finite writer, recall and
   shared host/transport. Fit and close timing with real board IO constraints.
2. Integrate the common GPMC shell, manifest identity/capabilities and kernel/app
   support. Hardware-test ADC order, full-depth write/recall, triggers and all
   timebases using only volatile device changes.
3. Build the continuous composition. Measure sustainable Q8.8 transfer under
   ARM stalls and implement bounded ARM history with explicit gap detection.
4. Add protocol modules to deep capture, starting with UART then I2C/SPI;
   validate event-to-sample alignment and runtime settings.
5. Qualify warm reload and recovery. Normal profile selection uses the tested
   warm path, with cold-cycle recovery for a stalled configuration path. Do not
   claim a switching duration until measured on the device.

A core-only fit is not a deployable image. Every published profile must pass
its functional tests, clock/IO/CDC checks and device workload qualification.

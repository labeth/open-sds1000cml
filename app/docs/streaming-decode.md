# High-bandwidth / stitched protocol decode

How fast a bus this scope can decode, how the **stream (stitch)** mode works, and
the measured per-protocol limits. All numbers measured on the real unit
(SDS1102CML+) against an Arduino Uno signal source (2026-07-03).

## The two ways to decode a fast bus

1. **Burst decode (default).** One deep capture (up to 20480 samples) at a fast
   timebase decodes a whole transaction/burst that fits the window. The window is
   trigger-centred and gap-free *inside itself*. This is the highest **instantaneous**
   bandwidth.
2. **Stream / stitch (the `stream` button in the Decode panel).** The engine
   captures deep records **back-to-back** with a *pure timed wait* (no trigger/
   saturation wait — that was the recoverable overhead) and un-paced publishing,
   and the client **accumulates every window's decoded packets into a scrolling
   history**. This gives a near-continuous picture of *everything on the bus over
   time*, with an honest blackout (the drain gap) marked between windows.

## Why it can't be perfectly gapless

The acquisition pipeline is **arm → wait(fill) → halt → drain → re-arm**. `halt`
stops the FPGA, so the **drain is a real gap** in the bus timeline, and the FPGA
is not watching the bus during it. Measured:

- **Drain rate: ~0.54 µs/sample** (mmap block read, ~1.85 MS/s) — constant.
- The only genuinely *gapless* path is the roll FIFO, but it reads **one sample per
  latch+pop ioctl (~100× slower per sample)** and an unpaced burst **wedges** the
  FIFO (spec 04 §10). So for high-bandwidth decode, **deep-stitch wins** — it just
  isn't 100 % duty.

**Duty cycle** = window / (window + gap), where window = `N·dt` (dt = sample
interval, set by timebase) and gap ≈ drain + re-arm. So slow timebases → near-
continuous but low sample rate; fast timebases → high sample rate but big holes.

## Measured sample rate & stitch duty per timebase (memdepth 20k)

| time/div | dt (sample) | sample rate | window (20k) | stitch gap | **stitch duty** |
|---|---|---|---|---|---|
| 50 µs   | 80 ns  | **12.5 MS/s** | 1.6 ms  | ~15 ms | ~11 % (burst only) |
| 200 µs  | 400 ns | 2.5 MS/s | 8.2 ms  | ~15 ms | ~22 % |
| 500 µs  | 800 ns | 1.25 MS/s | 16.4 ms | ~15 ms | **~52 %** (knee) |
| 1 ms    | 2 µs   | 500 kS/s | 41 ms   | ~15–40 ms | ~40–65 % |
| 2 ms    | 4 µs   | 250 kS/s | 82 ms   | ~34 ms | **~69 %** |

- **Highest instantaneous rate:** 12.5 MS/s at 50 µs/div (a decimated deep band),
  and native-fast bands reach ~41 MS/s but only a 2048-sample (≈50 µs) window.
- **Best "read near-continuously" knee:** ~500 µs–1 ms/div (≈1.25 MS/s–500 kS/s,
  ~50–65 % duty).
- **Near-gapless (≈70 %):** 2 ms/div (250 kS/s).

## Per-protocol limits

Decode needs **≥ ~3 samples/bit** (I²C/SPI cols-per-clock guard; UART SPB guard), so
the theoretical max bus rate ≈ `sampleRate / 3`. At 50 µs/div (12.5 MS/s) that's
~**4 Mbps**; at 500 µs/div (1.25 MS/s) ~**417 kbps**. The scope is rarely the limit
— the validation source (an Arduino bit-banging with ~1 µs/edge jitter) is:

| protocol | robust to timing? | measured decode ceiling (Arduino-limited) | scope ceiling (Fs/3) |
|---|---|---|---|
| **I²C** | yes (START/STOP + clock self-frame) | **≥ 250 kHz–1 MHz** SCL, decoded cleanly | ~4 Mbps @50 µs/div |
| **SPI** | yes (clock self-frames); no CS → needs an idle gap to re-align | clean at ~125 kHz; garbles as the Arduino's edges jitter | ~4 Mbps @50 µs/div |
| **UART** | **no** — async, must match baud | clean to ~**100–330 kbaud** with auto-baud; jitter-limited above | ~4 Mbps @50 µs/div |

Notes:
- **I²C is the most robust** at high rates because SCL is sampled by the decoder
  and START/STOP reframe every transaction — a jittery clock still decodes.
- **UART is the least robust** at high rate: with no clock, auto-baud must lock the
  bit period; the Arduino's non-uniform bit-bang (~43 µs when asked for 40 µs)
  limits it, not the scope. A crystal-clocked UART source would go much higher.
- **SPI** without a CS line re-aligns bytes on the inter-transaction idle gap; a
  truly gapless SPI stream can't be byte-framed.

## How to use

- **Burst:** set a fast time/div (50–500 µs), `mem` to 20k, pick the protocol +
  channels. One deep window decodes the whole transaction.
- **Stream:** click **stream** in the Decode panel — it forces 20k depth, un-paces
  the engine, and accumulates a packet history. The count line shows
  `N windows · duty X%`; each row is `#seq (gap Xms): <transcript>`. `clr` resets.

## Engine/API surface

- `SetStreamMode(on)` / `POST /api/set {"control":"stream","value":1}` — toggle.
- `SetFramePeriod(ms)` — publish pace (0 = back-to-back). `SetMemDepth(samples)`.
- Frame carries `stream_seq`, `window_ns`, `gap_ns`; Stats carries `stream`,
  `gap_ms`, `drain_ms`, `valid_depth`.
- `stitchFrame` (engine.go): arm → timed wait `N·dt` → halt → drain → re-arm →
  publish raw. Decimated bands only; native-fast is burst-only via SINGLE.

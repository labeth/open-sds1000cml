# FPGA images

Seven images for the EP4CE10F17C8. The app embeds them and loads the one a
feature needs; `general` is the power-on image. Every trigger is decided in
the FPGA; the app only places the trigger point on the event.

| Image | Adds |
|---|---|
| `general` | UART, I²C and SPI triggers; precision decimation |
| `packet` | ARINC 429, CAN, FlexRay, MIL-1553 and SENT triggers |
| `line` | Manchester and USB low-speed triggers |
| `qualify` | pulse width, slope time and video line triggers, and the zone trigger (four zones around the edge); precision decimation |
| `stacking` | FPGA super-resolution stacking engine |
| `stream` | continuous capture for gap-free roll: decimated or peak-detect (bucket min/max) words, buffered in the SRAM ("drum") and streamed to the host through two ping-pong banks |
| `fast` | SPI decoded and triggered on the samples, to a 125 MHz clock; raw samples only, with the sequence trigger |

The board around the FPGA (balls, clocks, ADCs, SRAM, GPMC bus, panel,
configuration) is specified in
[`../specs/12-fpga-board-interface.md`](../specs/12-fpga-board-interface.md).

## Stream image

The SRAM has no address bus, only a forward-stepping counter, so
`images/stream/stream_drum.v` serves it like a drum: words gather in an
on-chip FIFO, are written as bursts (16 junk words, data, a gap) where the
counter passes the write head, and each burst is read back whole into a host
bank where it passes the read head; descriptors record each burst. The
control and its block RAMs run at 125 MHz (block RAM cannot run at 250 MHz);
thin register stages on the 250 MHz clock pair words with the transport.

Measured on the instrument (every word of a counter source
checked): loss-free at decimation ×512 (978,000 words/s, 3.9 MB/s) and
×1024; at ×1024 a single 900 ms host stall is absorbed and a 1.2 s stall is
reported as a fault. Reading back what was just written costs one lap of
the counter (2.1 ms), so the FIFO must hold a lap of input: ×256 (4.2k words
a lap, 7.8 MB/s) is beyond both the FIFO and the GPMC bus (about 5.1 MB/s).

Board facts the drum relies on (also modelled in `images/stream/sim/`): read
words arrive one pulse late (READ_DELAY 1), so a fresh read starts 17 words
early and a continued read would lose a word; the front end is drained
continuously while the drum runs.

## Fast image

The general image's SPI, UART and I2C receivers sample the lines as 125 MHz
levels: SPI reaches a clock of about 20 MHz, the UART counts whole 8 ns ticks
a bit and never re-times (8N1, about 5 MBd with a transmitter's 2 %), I2C
reads SDA to a tick. The fast image decodes all three from the samples
themselves (ADR-SAMPLE-DOMAIN-SPI), on the 125 MHz receiver clock with four
samples a channel per clock (two acquisition words, paired from the 250 MHz
clock), in `images/fast/sample_bus.v`:

- `images/fast/sample_edges.v` finds one line's crossings in the directions
  asked: the first sample at or past the level after a sample beyond the
  re-arm band (the trigger's hysteresis), at most one a receiver clock. A
  direction re-arms once the line has been beyond the band's other edge
  since its last crossing (a Schmitt trigger: the converters sit up to 11
  codes apart, and on a slow edge their samples dip back across the band),
  or after a whole clock beyond the band's own edge (a runt pulse that
  crossed the level but not the band). Each crossing is timed by linear
  interpolation between the two samples around the level, in sixteenths of
  a nanosecond (a six-bit restoring division over three clocks); the other
  line's level is taken at the sample nearest the crossing plus a delay,
  and the line's own four levels of the clock come out with it. Two
  extractors run: one on the first line (SPI's clock, the UART line, I2C's
  SCL) with the delay of register 58 (the channels' sampling phase, C2
  1.44 ns after C1, and SPI's data delay, in sixteenths of a nanosecond on
  the first line's grid), one on the other (I2C's SDA) with its negation.
- `images/fast/spi_sample.v` frames SPI bytes by an idle gap, as the level
  receiver did, reading the data line at each sampling edge. Register 64
  reads 0x5303 (the level receiver's 0x5301).
- `images/fast/uart_sample.v` starts a frame at a falling crossing of the
  idle line and reads bit k at its interpolated time plus k and a half bit
  times (register 49/50: the bit time in sixteenths of a nanosecond, at
  least 256: 62.5 MBd); with resync (register 48 bit 12) every crossing in
  the frame moves the next bit to half a bit time after it. 5 to 9 data bits
  LSB first (bits 9:6), no, even or odd parity (bits 11:10), the stop bit
  checked: data (kind 2) or an error (kind 4), and uart_trigger.v's match
  over the low eight bits. What a crossing gives on its own (its half bit,
  whether that falls in its clock) is worked out a clock ahead. Register 48
  reads 0x5503.
- `images/fast/i2c_sample.v` takes the two lines' crossings in the order of
  their interpolated times (one held to the next clock when both come in one;
  the ordering takes a clock of its own): SCL's rising crossing reads SDA at
  its time (the bit); SDA's crossing reads SCL at its time and, SCL high, is a
  START (falling) or STOP (rising). Events, metadata and the match are
  i2c_trigger.v's. Register 53 reads 0x4902.

Every receiver publishes its level receiver's events and match, so the
decoded-event transport and the sequence trigger take them unchanged. An
edge reaches SPI's framing eight receiver clocks after its word, the UART and
I2C receivers nine; nothing is counted until words have passed the pipeline,
so a frame never starts mid-transaction after a reset. The app's Go models
(`app/internal/decode/spi_sample.go`, `sample_bus.go`) are the pipelines
clock for clock: their tests drive `images/fast/sim/tb_spi_sample.v` and
`tb_sample_rx.v` with synthetic buses (SPI 1 to 104 MHz in every mode; UART
115200 Bd to 62.5 MBd in every width and parity, 2 % off rate; I2C 100 kHz to
3.4 MHz with open-drain rises) and records of the bench sources, and require
the same edges, events and matches on the same clocks;
`images/fast/sim/tb_top_fast.v` checks the image's top level (registers over
GPMC, both channels, the events and the trigger) against the models.

The fast image: 8,668 logic elements of 10,320 (84 %), 327,520 memory bits,
seed 2, worst internal slack 0.134 ns.

## Qualify image

`images/qualify/qual_trigger.v` measures a pulse's width between edges at
the trigger level, a slope's time between two levels, or counts the line
syncs after a video vertical sync, in 16-bit counts of ticks of P clocks of
250 MHz (P a power of two up to 65536; an interval whose edges are W clocks
apart counts floor((W-1)/P) ticks), and fires two clocks after the closing
edge. A slope's two levels are crossed on sums of five consecutive samples
(`sum_edge_pair.v`), one from each interleaved converter: the converters sit
up to 11 codes apart in the raw codes, which moved a slow edge's crossings.
`images/qualify/zone_qual.v` keeps, per zone, a running count of buckets
with a sample inside the zone's codes (1024 buckets, a power of two of the
8 ns half clock, in block RAM) and decides from two counts per zone whether
it was entered in its window around the edge; it fires a fixed time after
the edge (the wait plus 48 half clocks), so the app can move the trigger
point back onto the edge. Registers 21, 22, 24 and 26-28 hold the qualifier,
83-92 and 69-73 the zones; register 74 reads 0x5154 and 75 the kinds.

## Source layout

| Path | Used by |
|---|---|
| `common/` | every image: acquisition top (`top.v`, image features selected by defines), SRAM record and transport, ADC unpack and interleave, front-panel scan, clocks, GPMC slave, ADC lane inputs |
| `trigger/` | general, packet, line, qualify and fast: decoded-event queue and transport, sequence trigger, sample timeline, envelope recall, and the shared Quartus project (`project.qsf`, `project.sdc`) |
| `images/<image>/` | that image only: its decoders or engine (stacking also has its own Quartus project; stream and qualify also use the general image's precision decimator) |
| `*/sim/` | testbenches for the sources beside them |
| `bitstreams/` | the shipping images and `images.json` (seed and sha256 of each) |

## Build

```sh
node --experimental-strip-types fpga/build.ts --image=general|packet|line|qualify|stacking|stream|fast [--seed=N] [--prepare-only] [--install]
```

Builds into `fpga/out/<image>/` with Quartus 21.1 Lite (`QUARTUS_BIN`, default
`~/intelFPGA_lite/21.1/quartus/bin`). A build publishes only if timing passes;
the stacking image also checks ADC input registers, pin placement and memory.
The default seed is the shipping one, so a plain build reproduces the
committed bitstream bit for bit. `--install` copies a passing build into
`bitstreams/` and updates `images.json`.

## Simulate

`fpga/sim.sh` runs the self-checking testbenches with iverilog. Testbenches
driven by fixtures from the app's Go reference models run in the app's
`go test` (internal/decode, internal/superres, internal/sramcapture).

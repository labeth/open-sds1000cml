# FPGA images

Five images for the EP4CE10F17C8. The app embeds them and loads the one a
feature needs; `general` is the power-on image.

| Image | Adds |
|---|---|
| `general` | UART, I²C and SPI triggers; precision decimation |
| `packet` | ARINC 429, CAN, FlexRay, MIL-1553 and SENT triggers |
| `line` | Manchester and USB low-speed triggers |
| `stacking` | FPGA super-resolution stacking engine |
| `stream` | continuous capture for gap-free roll: decimated or peak-detect (bucket min/max) words, buffered in the SRAM ("drum") and streamed to the host through two ping-pong banks |

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

Measured on the instrument (2026-10-10, every word of a counter source
checked): loss-free at decimation ×512 (978,000 words/s, 3.9 MB/s) and
×1024; at ×1024 a single 900 ms host stall is absorbed and a 1.2 s stall is
reported as a fault. Reading back what was just written costs one lap of
the counter (2.1 ms), so the FIFO must hold a lap of input: ×256 (4.2k words
a lap, 7.8 MB/s) is beyond both the FIFO and the GPMC bus (about 5.1 MB/s).

Board facts the drum relies on (also modelled in `images/stream/sim/`): read
words arrive one pulse late (READ_DELAY 1), so a fresh read starts 17 words
early and a continued read would lose a word; the front end is drained
continuously while the drum runs.

## Source layout

| Path | Used by |
|---|---|
| `common/` | every image: acquisition top (`top.v`, image features selected by defines), SRAM record and transport, ADC unpack and interleave, front-panel scan, clocks, GPMC slave, ADC lane inputs |
| `trigger/` | general, packet and line: decoded-event queue and transport, sequence trigger, sample timeline, envelope recall, and the shared Quartus project (`project.qsf`, `project.sdc`) |
| `images/<image>/` | that image only: its decoders or engine (stacking also has its own Quartus project; stream also uses the general image's precision decimator) |
| `*/sim/` | testbenches for the sources beside them |
| `bitstreams/` | the shipping images and `images.json` (seed and sha256 of each) |

## Build

```sh
node --experimental-strip-types fpga/build.ts --image=general|packet|line|stacking|stream [--seed=N] [--prepare-only] [--install]
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

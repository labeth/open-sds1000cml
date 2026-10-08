# FPGA images

Four images for the EP4CE10F17C8. The app embeds all four and loads the one a
feature needs; `general` is the power-on image.

| Image | Adds |
|---|---|
| `general` | UART, I²C and SPI triggers; precision decimation |
| `packet` | ARINC 429, CAN, FlexRay, MIL-1553 and SENT triggers |
| `line` | Manchester and USB low-speed triggers |
| `stacking` | FPGA super-resolution stacking engine |

The board around the FPGA (balls, clocks, ADCs, SRAM, GPMC bus, panel,
configuration) is specified in
[`../specs/12-fpga-board-interface.md`](../specs/12-fpga-board-interface.md).

## Source layout

| Path | Used by |
|---|---|
| `common/` | every image: acquisition top (`top.v`, image features selected by defines), SRAM record and transport, ADC unpack and interleave, front-panel scan, clocks, GPMC slave, ADC lane inputs |
| `trigger/` | general, packet and line: decoded-event queue and transport, sequence trigger, sample timeline, envelope recall, and the shared Quartus project (`project.qsf`, `project.sdc`) |
| `images/<image>/` | that image only: its decoders or engine (stacking also has its own Quartus project) |
| `*/sim/` | testbenches for the sources beside them |
| `bitstreams/` | the shipping images and `images.json` (seed and sha256 of each) |

## Build

```sh
node --experimental-strip-types fpga/build.ts --image=general|packet|line|stacking [--seed=N] [--prepare-only] [--install]
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

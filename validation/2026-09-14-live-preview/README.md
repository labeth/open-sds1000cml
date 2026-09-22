# Live preview / stopped SRAM recall

Deployed RAM executable `/dev/acq-preview-app`, SHA256
`a36dfae5ae0d9316d887fd23a65c15f660769c913f3b9a2cca0683f6a7f6aaeb`.
Existing revision 11 FPGA unchanged; inherited GPMC only, ioctl transfer.
No internal permanent-storage writes.

RUN always captures full SRAM; at 500 ns/div it recalls 3012 samples/channel
around the trigger. ADC calibration uses integer phase sums and an exact
lookup table; measurement sample loops use integer arithmetic. Live averaging
retains Q8 values and aligns waveform crossings. STOP reuses the frozen record
without rearming and recalls every sample, marked as a single acquisition.
Single-shot also transitions to full recall after the preview average finishes.
At slower timebases the live transfer grows to cover the visible span; this
benchmark does not claim 20 Hz at all timebases.

30-second hardware test, 1 MHz triangle both channels, 2 V/div, 500 ns/div,
NORM rising CH1, average 4, full 1,048,576-sample/channel capture:
- Web HTTP delivery 24.48 distinct frames/s (5-second intervals 24.45–24.59).
- LCD actually presented 24.11 distinct acquisition frames/s.
- Capture loop 24.58/s; zero bus errors.
- STOP recalled 1,048,576 samples/channel in 3.97 seconds; scope-rendered PNG
  inspected and preserved 1 MHz triangles. RUN resumed after the check.

Web measurement mirrors the webpage long poll, full=1, cols=2048, and 10ms
client delay over a persistent HTTP connection. It measures received frames,
not workstation browser paint events. LCD counter increments after Present
only when acquisition sequence changes. Reproduce with bench.py from repo root.

Tests: engine, frame fanout, measurements, and app pass. Window tests cover
trigger positions at each boundary and raw/precision samples per SRAM word.
60 Hz with both outputs has not been achieved; 20 Hz minimum passed here.

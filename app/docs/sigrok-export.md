# Sigrok export — design notes and usage

The web UI's export row offers **SR**, **VCD**, and **WAV** next to PNG/CSV.
All three run the same contract as the CSV export: they encode **the frame on
screen** — live, frozen, a capture under review, or a superres/ETS view — as
calibrated volts (`(code − 128) · vpc − off`; vpc/off already carry probe
attenuation and the software coupling model, so the file reads at the probe
tip exactly like the on-screen measurements). Envelope/roll frames aggregate
many acquisitions and have no per-sample record, so the buttons no-op there,
same as CSV.

The encoders live in `sigrok_export.js` (pure, zero-dependency, node-tested by
`sigrok_export.test.cjs`). Byte layouts were written against what libsigrok's
*readers* actually parse (`session_file.c`, `session_driver.c`,
`input/vcd.c`, `input/wav.c`) rather than the wiki; the load-bearing reader
facts are documented at the top of `sigrok_export.js`.

## Formats

| button | file | notes |
|---|---|---|
| SR | `.sr` (srzip v2) | The native PulseView / sigrok-cli session: stored ZIP with `version`=`2`, INI `metadata` (integer-Hz samplerate, `total analog` before the `analogK` names), and `analog-1-<k>-<chunk>` chunks of raw little-endian float32 volts (≤4 MiB per chunk, like libsigrok's own writer). Lossless: names, rate, volts. Needs libsigrok ≥ 0.5.0 (2017) — every analog-capable PulseView. Gaps (unfilled superres bins) encode as NaN. |
| VCD | `.vcd` | Analog channels as `$var real 64` with `r<value>` changes, sigrok's own timescale algorithm, and a `$scope module libsigrok` wrapper so re-import keeps plain CH1/CH2 names. Reads in GTKWave anywhere; **analog VCD import needs libsigrok git newer than 0.5.2**. Gaps hold the previous value (value-change semantics). Above 131072 points it stride-decimates like the CSV export (superres fine grids are interpolation; the `$comment` header records the step and the decimated rate keeps the axis true). |
| WAV | `.wav` | 18-byte `fmt` chunk, format 3 (IEEE float32), interleaved, calibrated volts verbatim (no ±1 normalization) — exactly what sigrok's wav output writes. The rate field is a uint32 in Hz, so fine-grid superres exports above 4.29 GHz are refused (button no-ops) — use `.sr` for those. Gaps hold the previous value. |

Import one-liners:

```sh
pulseview scope-1234.sr                    # or File → Open
sigrok-cli -i scope-1234.sr --show
sigrok-cli -i scope-1234.vcd --show        # libsigrok git for analog VCD
sigrok-cli -i scope-1234.wav --show
# the CSV export imports too (its '#' header line needs the comment-leader option):
sigrok-cli -i scope-1234.csv -I csv:column_formats=t,2a:comment_leader='#' --show
```

## The time axis: `dt_s` vs `col_span_s`

The 1–200 ns/div bands capture at 2 ns/sample but size the display window at a
1 ns nominal so 10 divisions match the labelled tdiv (spec 04 §6). That makes
`col_span_s` a *display* quantity: reconstructing time from `col_span_s/cols`
on those bands compresses the axis 2× (the axis-derived frequency reads twice
`m1.freq`, which is computed with the true sample interval). The frame reply
therefore carries `dt_s` — the TRUE capture time per served point
(`SampleS` on deep serves, `min(WinCols, Valid) · SampleS / cols` on windowed
serves, 0 on envelope/roll) — and the CSV and sigrok exporters prefer it,
falling back to `col_span_s/N` for client-synthesized frames (superres/ETS
view, mask replay — their spans are already true) and captures saved before
`dt_s` existed. The on-screen axis keeps the spec'd nominal.

Known follow-up: the protocol decoders (`decode.js`) still derive bit timing
from `col_span_s`, so on 1–200 ns/div their absolute baud/timing readouts (and
auto-detect's baud scoring) inherit the same 2× nominal. Fixing that means
threading `dt_s` through the decode call sites *and* the JS/Go parity vectors —
a separate change.

## What the exported record is

Whatever the browser holds — see the frame paths in `web_frame.go`:
- **Deep serves** (decimated bands with `full=1`, the web client's default;
  stream mode; triggered envelope-band captures): the full drained record,
  one point per hardware sample (up to 20480), true rate. This is raw data.
- **Windowed serves** (native-fast 1 ns–20 µs/div): the 10-division display
  window resampled onto the requested columns (linear interpolation on
  native-fast) — display resolution, not the raw record.
- **Superres view**: the stacked fine grid (fractional codes, up to 1.31 M
  points; the ETS view tiles its reconstructed period twice) — `.sr` and WAV
  export it verbatim (binary, ~10 MB at worst); VCD and CSV stride-decimate
  above 131072 points.
- The mask-failure replay view hard-codes `vpc = 1/25, off = 0`, so exports
  from it are voltage-correct only at 1 V/div ×1 (pre-existing; also true of
  its CSV export).

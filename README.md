# open-sds1000cml

Open replacement firmware for the **Siglent SDS1000CML+** two-channel oscilloscope (developed on the
SDS1102CML+). It turns a budget 100 MHz scope into a deep-memory protocol and analysis instrument:
both channels at 500 MS/s into a 1 M-sample record, hardware triggers on ten serial protocols,
continuous decoding, super-resolution and a full web interface. It replaces the application on the
scope's ARM processor and the FPGA images that application loads. Nothing is flashed: a power cycle
always brings back the factory firmware.

> ⚠️ **Use at your own risk.** This firmware takes over a mains-powered
> instrument and drives its relays, DACs and acquisition bus. It is for the
> SDS1000CML+ series only. There is no warranty (see [LICENSE](LICENSE) and
> [SAFETY.txt](SAFETY.txt)).

## In action

![Native scope LCD in the BUS workspace: the decoded UART packet 48 69 20 55 AA 0F F0 0A in a hex column and a string box, the serial trigger matching AA, and the packet centred on the trace](docs/images/scope-bus.png)

**Trigger on the data, directly on the scope.** The serial trigger matches the byte `AA` in a
115,200-baud UART and centres it. The BUS workspace lists each decoded packet in a fixed hex column
with its text in a separate box, so the table does not jump as the bytes change.

<sub>Every capture here is from a live SDS1102CML+ running this firmware. An Alchitry Au drives the same
115,200-baud UART message (`Hi U`, then `55 AA 0F F0 0A`) into C1 and C2, and the serial trigger matches
`AA`. LCD images are the native 800 × 480 screen.</sub>

### On the scope

Five task workspaces, one per printed F-key on the rail at the right. Pressing the shown workspace's key
again switches to its next view.

| | |
|---|---|
| ![SCOPE workspace: both channels' UART traces with readouts and the decoded bytes under the trace](docs/images/scope-scope.png) | ![SCOPE again: X-Y, a diagonal line because both channels carry the same signal](docs/images/scope-xy.png) |
| **SCOPE:** the full trace, readouts and the decoded strip | **SCOPE again:** X-Y (here a diagonal: C1 and C2 carry the same signal) |
| ![BUS again: an eye diagram of the UART with bit rate, eye height and width, and TIE jitter](docs/images/scope-bus-eye.png) | ![The match editor: a full-screen keypad for the trigger's match bytes, in hex](docs/images/scope-match-editor.png) |
| **BUS again:** the eye diagram, with eye height, width and TIE jitter | **The match editor:** the knobs move over a full-screen keypad, hex or text |
| ![SPECTRUM: the FFT with a peak table, harmonics and THD over a small time trace](docs/images/scope-spectrum.png) | ![SPECTRUM again: the spectrogram, the spectrum over time](docs/images/scope-spectrogram.png) |
| **SPECTRUM:** the FFT, peak table, harmonics and THD | **SPECTRUM again:** the spectrogram (and then Bode, for a swept source) |
| ![MEASURE: statistics of the chosen readouts, now, mean, min, max and sdev](docs/images/scope-measure.png) | ![MEASURE again: every value of both channels](docs/images/scope-measure-all.png) |
| **MEASURE:** statistics of the chosen readouts | **MEASURE again:** every value of both channels |
| ![COMPARE: a master stacked from 64 captures in REF A and its mask testing live captures](docs/images/scope-compare.png) | ![The TRIG settings sheet: every trigger setting written out, with a live preview and help](docs/images/scope-sheet-trig.png) |
| **COMPARE:** a master stacked from 64 captures (REF A), its mask testing live captures | **A settings sheet:** every option written out, with a live preview and help |

### In a browser

The web UI on port 8080 has the same five workspaces (Alt+1 to Alt+5), a strip with run control,
timebase, channels and trigger, and All settings (Ctrl+,). A workspace's key pressed again shows its
next view, as on the scope.

| | |
|---|---|
| ![Web SCOPE workspace](docs/images/web-scope.png) | ![Web SCOPE in X-Y: C1 against C2, a diagonal because both channels carry the same signal](docs/images/web-xy.png) |
| **SCOPE** | **SCOPE again:** X-Y |
| ![Web BUS workspace: the decoder, the packet table and the trigger on bytes](docs/images/web-bus.png) | ![Web BUS eye diagram with jitter analysis](docs/images/web-bus-eye.png) |
| **BUS:** decoder, packet table, trigger on bytes | **BUS again:** eye diagram and jitter (it folds sampled records: here two full records) |
| ![Web SPECTRUM: FFT with peaks and THD](docs/images/web-spectrum.png) | ![Web spectrogram](docs/images/web-spectrogram.png) |
| **SPECTRUM:** FFT, peaks, THD | **SPECTRUM again:** spectrogram |
| ![Web MEASURE: statistics, cursors and the mask test](docs/images/web-measure.png) | ![Web MEASURE, All values: every measurement of both channels in this capture](docs/images/web-measure-all.png) |
| **MEASURE:** statistics, cursors, mask and zones | **MEASURE again:** every value of both channels |
| ![Web COMPARE: REF A against the live trace with the difference readout, and the mask test counting](docs/images/web-compare.png) | ![All settings: every control of the app in one list, with where it lives](docs/images/web-allsettings.png) |
| **COMPARE:** REF A against the live trace, and the mask test counting a master stacked on the scope | **All settings:** every control, and where it lives |

### On a phone

`http://<scope>:8080/r` is a remote for the scope's settings: it shows no waveform and never opens
anything on the scope's screen.

| | | |
|---|---|---|
| ![Phone remote Home: big tiles for time/div, trigger level and V/div, the workspace chips, Run/Stop, Single and Auto](docs/images/remote-home.png) | ![Phone remote in BUS: the match bytes and serial trigger tiles](docs/images/remote-bus.png) | ![Phone remote Menus: every settings tab, with search](docs/images/remote-menus.png) |
| **Home** | **BUS:** match bytes, serial trigger | **Menus:** every tab, with search |
| ![Phone remote typed entry: a keypad with units for time/div](docs/images/remote-sheet.png) | ![Phone remote History: each change with who made it, and undo](docs/images/remote-history.png) | ![Phone remote Keys: the knob pushes and keys, and the phone's keep-awake and fullscreen](docs/images/remote-keys.png) |
| **Typed values** with units | **History** with undo | **Keys** and the phone's settings |

## At a glance

| | |
|---|---|
| **Channels** | 2, sampled together |
| **Sample rate** | **500 MS/s per channel**, real time, on both channels at once (1 GS/s aggregate) |
| **Record length** | **1,048,576 samples per channel** at full rate, captured on every acquisition; the full record is read on STOP or SINGLE |
| **Live display** | **16–24 frames/s** from 1 µs/div to 1 ms/div, protocol decode included (about 12 at 5 ms/div) |
| **Precision mode** | 16-bit filtered samples, 524,288 per channel; decimation ×16 to ×1,048,576 for long records (137 s at 50 s/div) |
| **Super-resolution** | up to a **×64 time grid** (31.25 ps); about **+4 bits** from FPGA stacking |
| **Timebase** | 1 ns/div to 50 s/div |
| **Roll mode** | AUTO at 10 ms/div and slower scrolls continuously; optional gap-free streaming, verified loss-free at **~1 M samples/s per channel (3.9 MB/s)** with **about 1 s of buffering** in the scope's SRAM |
| **Vertical** | 2 mV/div to 10 V/div; DC, AC or GND; 20 MHz bandwidth limit; probe ×1, ×10 or ×100 |
| **Serial protocols** | **10**, each with hardware trigger and decode: UART, I²C, SPI, CAN / CAN FD, FlexRay, ARINC 429, MIL-STD-1553, SENT, Manchester, USB low-speed |
| **Streaming decode** | **continuous, gap-free protocol decoding**, not one capture at a time: 921,600-baud UART ran for 3 minutes without losing a byte, with the full-rate record kept for every frame |
| **Analysis** | 18 automatic measurements, cursors, math, FFT, spectrogram, eye diagram and jitter, mask test |
| **Interfaces** | web UI in any browser, SCPI over VXI-11 (LAN), the scope's own LCD and front panel |

## Acquisition

* **Full-depth capture on both channels.** Every acquisition fills the scope's 2 MiB SRAM: 1 M
  samples per channel at 500 MS/s, 2.1 ms. While running, only a display-sized view of it is read,
  so the screen keeps up; **STOP or SINGLE reads the full record** (about 2 s, with a loading
  indicator), which can then be zoomed, measured, decoded and exported at full resolution.
  Changing V/div, offset or timebase on a stopped record redraws it from memory.
* **Calibrated five-way interleaving.** Each channel runs five 100 MS/s converters in turn. Per-core
  correction removes the interleave pattern (the comb drops from 11 codes to 0.15).
* **Precision mode.** At slow timebases the FPGA decimates by a power of two, ×16 to ×1,048,576,
  through a CIC filter. The ARM applies a 63-tap compensation filter and keeps 16-bit samples, so a
  slow record gains resolution instead of aliasing. The UI shows the resulting bandwidth and bit
  gain.
* **Modes:** normal, average (up to 256 frames), enhanced resolution and precision.
* **Live min/max envelope:** the FPGA reduces the screen to 4,096 min/max points, so narrow events
  stay visible at any zoom. The points are put back in time order, so protocol decode, masks and
  qualified triggers run on live frames too. Average, ERES and precision use a decimated live
  capture instead (their filtering needs true samples); eye diagram and super-resolution read raw
  samples while in use.
* **Roll mode.** In AUTO at 10 ms/div and slower the view scrolls instead of waiting for a
  trigger. By default it is built from back-to-back decimated captures, so a timebase change
  responds at once (short gaps hold the last value). HORIZ F4 (web: SCOPE, Horizontal, Roll: Stream) switches to
  **gap-free streaming** on the FPGA `stream` image: every averaged (or min/max) sample reaches
  the ARM, at up to 978,000 samples/s per channel (decimation ×512, 3.9 MB/s; the bus carries
  about 5.1 MB/s). The scope's 2 MiB SRAM buffers the stream, so the ARM can stall for about
  1 s at ×1024 (0.5 s at ×512) without losing a sample; a longer stall is reported, never
  silent.
* **Responsive controls:** front-panel keys and lamps answer within tens of milliseconds, even
  during a long readout; a LOADING indicator (LCD and web) shows while a record is being read.

## Faster FPGA readout

Compared with CPU readout from the factory FPGA, our DMA path demonstrated
**roughly 3× higher raw transfer throughput** in earlier benchmarks
(11.1 MB/s versus 3.1–3.9 MB/s). DMA replaces the vendor's per-word CPU read loop;
the current driver still polls for completion.

For live viewing, the FPGA sends an **8 KiB min/max envelope instead of a 2 MiB
raw record—256× less sample data to transfer** (about 10–30 ms instead of about 2 s),
while keeping the full record in SRAM for STOP, SINGLE and detailed review.

## Triggering

* **Edge** in hardware, with sub-sample placement: 0.19 ns peak-to-peak edge wander at 5 ns/div.
* **Pulse width, slope and video** (PAL / NTSC, line select), qualified on the captured record.
* **Auto, normal and single** modes; holdoff up to 10 s; force trigger.
* **Protocol triggers in the FPGA** for all ten protocols. Match a byte, word, address, ID or frame
  pattern, with masks and wildcards.
* **Sequence trigger:** chain up to **32 protocol events**, including **trigger on error**.
* **Zone trigger:** draw boxes on the screen that the waveform must cross or avoid.
* **Mask test:** build a golden mask from live frames with time and voltage tolerances. Count
  failures or stop on the first one, and browse the failure gallery.

## Serial protocols

Every protocol has an FPGA trigger and a decoder. The scope loads the FPGA image a protocol needs
automatically.

| Protocol | FPGA image | Highlights |
|---|---|---|
| UART | general | auto baud from 300 Bd to 3 MBd |
| I²C | general | 7-bit address, direction, data bytes |
| SPI | general | all four modes, both bit orders |
| CAN / CAN FD | packet | Classic CAN CRC check; CAN FD ID/data decode (CRC not checked) |
| FlexRay | packet | header and payload; header CRC |
| ARINC 429 | packet | masked 32-bit word |
| MIL-STD-1553 | packet | 16-bit words; parity |
| SENT | packet | tick-calibrated nibbles; CRC |
| Manchester | line | IEEE 802.3 and Thomas conventions |
| USB low-speed | line | PID and payload bytes |

* **Auto-detect** finds the protocol, channel roles, threshold and settings from a capture.
* **Continuous decoding:** a live decoded stream, with the full-rate record kept for every frame.
  UART at **921,600 baud ran lossless for 3 minutes** (16.6 M bytes). At higher rates a lost event
  is marked, never silently dropped.
* Hex, ASCII and combined views, and a watch buffer with regex search.

## Analysis

* **18 automatic measurements:** Vpp, Vmax, Vmin, Vmean, Vrms, AC rms, Vtop, Vbase, amplitude,
  overshoot, preshoot, frequency, period, duty cycle, rise time, fall time, +width and −width.
* **Cursors** in time and voltage, with Δt, 1/Δt and ΔV.
* **Math:** C1 − C2, C2 − C1, C1 + C2, C1 × C2, and carrier removal using FFT peaks.
* **FFT** with a peak table (up to 64 peaks), and a **spectrogram** waterfall.
* **Eye diagram and jitter:** software clock recovery; TIE rms and peak-to-peak, RJ / DJ split,
  period and cycle-to-cycle jitter, eye height and width; TIE histogram and spectrum.
* **Super-resolution:** stacks repeated edges on a time grid up to ×64 finer than the sample period,
  with interpolating, cubic or drizzle kernels, or triggered on a decoded UART byte. The **FPGA
  stacking image** accumulates the hits in hardware, using the same captured records across
  every tile to avoid acquisition-dependent seams. Phase-coherent equivalent-time folding resolves clocks above the trigger
  comparator's range.
* **Reference waveforms** (two), **persistence**, **XY mode**, **freeze** and **autoset**.

## Connect and control

* **Phone remote** at `/r` on port 8080: settings, workspaces and keys from a phone (below).
* **Web UI** on port 8080 in any browser, with WebGL rendering and every feature above. It is laid
  out as the scope's five task workspaces: SCOPE, BUS (decode with a packet table, trigger on bytes,
  eye diagram), SPECTRUM (FFT, harmonics and THD, spectrogram, Bode), MEASURE (statistics, cursors,
  mask test) and COMPARE (references, math, difference readout, stacked master). Alt+1 to Alt+5
  switch, and the shown workspace's key again shows its next view. A strip with run control,
  timebase, channels and trigger stays at the bottom, and All settings (Ctrl+,) lists every setting
  and shows where it lives.
* **Export:** PNG, CSV, sigrok `.sr` (opens in PulseView, with thresholded logic channels), VCD and
  WAV.
* **SCPI over VXI-11** (LAN): Siglent / LeCroy short-form commands, including `WF?` waveform
  transfer with a WAVEDESC header and `SCDP` screenshots, so existing Siglent scripts and VISA tools
  work.
* **On the scope itself:** the LCD and the real front-panel keys and knobs, as five task workspaces
  (SCOPE, BUS, SPECTRUM, MEASURE, COMPARE) on F1 to F5, each workspace's key again showing its next view;
  MENU and the printed keys open full-screen settings sheets with every option written out. Settings are
  kept across restarts.
* **Safe updates:** an on-device agent installs new versions over the network into A/B slots and
  rolls back automatically if a version fails its health check. Nothing is written to the scope's
  flash.

## Phone remote

Open `http://<scope>:8080/r` on a phone (the LCD shows the scope's address; the web UI's top bar links it). The
remote changes the scope's settings without opening anything on the scope's screen, which confirms each change
with a short REMOTE line. It shows settings only, no waveform. Several phones can use it at once.

* **Home:** big tiles (time/div, trigger level, V/div and the workspace's own settings), each with two buttons
  that name the value they go to; the workspace chips (the active one again: its next view); Run/Stop, Single
  and Auto.
* **Menus:** every settings tab and row, with search that knows everyday words ("averaging", "screenshot");
  a long press pins a row to Home.
* **History:** every change, from the phone (as the scope reports what each of its calls changed), the scope or
  another phone, with undo; the tab's badge counts what is new since you last looked.
* **Keys:** the knob pushes and keys, the menu keys (they open the menu on the phone) and Default Setup (hold 1 s).
* Typed numbers with units ("1.25V", "500us", a comma for the point works too), hex values ("0x50") and match
  bytes in hex or text.
* The phone's back gesture closes the open sheet or page before it leaves the remote.
* The phone stays awake: a wake lock where the browser has one; on the scope's plain HTTP a muted video behind
  the controls (Android), or a video with a silent sound track (iPhone, or any phone with Stronger keep-awake in
  Keys, which may pause other apps' music). It goes fullscreen on Android; on an iPhone, Share, Add to Home
  Screen opens it without browser bars.

## What it adds over the factory firmware

The factory firmware advertises 1 GSa/s real-time sampling, a 40 k-point normal record (2 M points
maximum), and edge, pulse, video, slope and alternate triggers. It lists no serial protocol
triggering or decoding. This firmware adds:

* the full 1 M-sample record on **both** channels on every acquisition;
* hardware triggering and decoding for **ten serial protocols**, sequence triggers and trigger on
  error;
* **streaming decode**: protocol traffic decoded continuously, without the gaps between captures;
* zone triggering, eye-diagram and jitter analysis, a spectrogram, precision mode and
  super-resolution;
* a complete browser interface, sigrok export, and open source you can extend.

## Documentation

The engineering model is the documentation: requirements, architecture, behaviour and design
decisions are in [`model/`](model/) (start with `requirements.yml` and `decisions.yml`). The
behavioural specifications the implementation was written from are in [`specs/`](specs/).

## Layout

| Path | What it is |
|---|---|
| `app/` | The scope application (Go, ARMv7): acquisition engine, triggers, decoders, LCD, front panel, web UI, SCPI/VXI-11. Embeds the four FPGA images. |
| `fpga/` | The FPGA images (Verilog, Cyclone IV EP4CE10): what is common, what each image adds, the shipping bitstreams and the build. |
| `ota/` | The on-device agent and `otactl`: install, launch, health, A/B slots, USB stick builder. |
| `tools/deploy.sh` | Build the release app and install it on a scope over the network. |
| `model/` | The engineering model (documentation). |
| `specs/` | Behavioural specifications. |

## Build, test, deploy

```sh
make test                      # app and ota test suites
fpga/sim.sh                    # self-checking RTL testbenches (iverilog)
make -C app app-release        # ARMv7 app with the four FPGA images embedded
tools/deploy.sh                # build and install on the scope (DEV=192.168.1.209)
node --experimental-strip-types fpga/build.ts --image=general   # rebuild an FPGA image (Quartus 21.1 Lite)
```

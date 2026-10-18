# open-sds1000cml

Open replacement firmware for the **Siglent SDS1000CML+** two-channel oscilloscope (developed on the
SDS1102CML+). It turns a budget 100 MHz scope into a deep-memory protocol and analysis instrument:
both channels at 500 MS/s into a 1 M-sample record, hardware triggers on fifty-two serial protocols,
continuous decoding, super-resolution and a full web interface. It replaces the application on the
scope's ARM processor and the FPGA images that application loads. Nothing is flashed: a power cycle
always brings back the factory firmware.

> ⚠️ **Use at your own risk.** This firmware takes over a mains-powered
> instrument and drives its relays, DACs and acquisition bus. It is for the
> SDS1000CML+ series only. There is no warranty (see [LICENSE](LICENSE) and
> [SAFETY.txt](SAFETY.txt)).

## In action

![Native scope LCD in the BUS workspace: a 104 MHz SPI flash read triggered on its Fast Read command 0B 00 10 00, the idle bus before the trigger and the clock starting at it, and 286 decoded reads of SPI FLASH 104MHZ OPEN-SDS in the packet table](docs/images/scope-bus.png)

**Trigger on the data of a 104 MHz SPI flash, directly on the scope.** The FPGA decodes the bus from
the converters' samples and triggers on the flash's Fast Read command `0B 00 10 00` (read at address
0x001000). The BUS workspace lists each decoded read in a fixed hex column with its text beside it:
here the 286 reads in one stopped record, every byte of them.

> **One capture, 1,048,576 samples a channel.** A SINGLE capture holds 2.1 ms at the full 500 MSa/s.
> The bar under the trace below is that whole record, all 286 flash reads in it; the large view is
> 2 µs of it at 200 ns/div, the triggered read with every clock edge resolved and its bytes decoded
> under it; the packet list decodes all 19,769 bytes of the one capture.
>
> ![Web BUS workspace after one SINGLE capture: the bar under the trace shows the whole 2.1 ms record of 286 flash reads, the large view 2 us of it at 200 ns/div with the bytes 0B 00 10 00 FF 53 50 49 decoded under the trace, and the packet list of every read](docs/images/web-single-1m.png)

<sub>Every capture here is from a live SDS1102CML+ running this firmware, with 10x probes. An Alchitry Au
drives a look-alike of an SPI flash read at 104 MHz (clock on C2, data on C1; each read is `0B 00 10 00`,
a dummy byte, `SPI FLASH 104MHZ OPEN-SDS 0123` and 32 counting bytes), and the serial trigger matches
`0B 00 10 00`. LCD images are the native 800 × 480 screen.</sub>

### On the scope

Five task workspaces, one per printed F-key on the rail at the right. Pressing the shown workspace's key
again switches to its next view.

| | |
|---|---|
| ![SCOPE workspace: the SPI data on C1 and its 104 MHz clock on C2 with readouts, and the decoded bytes under the trace](docs/images/scope-scope.png) | ![SCOPE again: X-Y, the SPI data against its clock](docs/images/scope-xy.png) |
| **SCOPE:** the full trace, readouts and the decoded strip (the trace ends above it) | **SCOPE again:** X-Y (here the data against its clock) |
| ![BUS again: the eye diagram of the 104 Mbit/s SPI data with bit rate, eye height and width, and TIE jitter](docs/images/scope-bus-eye.png) | ![The match editor: a full-screen keypad for the trigger's match bytes, in hex](docs/images/scope-match-editor.png) |
| **BUS again:** the eye diagram of the 104 Mbit/s data, with eye height, width and TIE jitter | **The match editor:** the knobs move over a full-screen keypad, hex or text |
| ![SPECTRUM: the FFT of the SPI clock with its 104 MHz peak marked and the peak table over a small time trace](docs/images/scope-spectrum.png) | ![SPECTRUM again: the spectrogram, the spectrum over time](docs/images/scope-spectrogram.png) |
| **SPECTRUM:** the FFT and peak table (the clock at 104 MHz) | **SPECTRUM again:** the spectrogram (and then Bode, for a swept source) |
| ![MEASURE: statistics of the chosen readouts, now, mean, min, max and sdev](docs/images/scope-measure.png) | ![MEASURE again: every value of both channels](docs/images/scope-measure-all.png) |
| **MEASURE:** statistics of the chosen readouts | **MEASURE again:** every value of both channels |
| ![COMPARE: a master stacked from 64 captures of a UART in REF A and its mask testing live captures](docs/images/scope-compare.png) | ![The BUS settings sheet: the SPI decoder, the clock rate and data delay measured on the bus, and the match, with a live preview and help](docs/images/scope-sheet-bus.png) |
| **COMPARE:** a master stacked from 64 captures (REF A), its mask testing live captures (a 115,200-baud UART: a master wants a signal sampled many times a period) | **A settings sheet:** every option written out, with a live preview and help |

### In a browser

The web UI on port 8080 has the same five workspaces (Alt+1 to Alt+5), a strip with run control,
timebase, channels and trigger, and All settings (Ctrl+,). A workspace's key pressed again shows its
next view, as on the scope.

| | |
|---|---|
| ![Web SCOPE workspace](docs/images/web-scope.png) | ![Web SCOPE in X-Y: the SPI data against its clock](docs/images/web-xy.png) |
| **SCOPE** | **SCOPE again:** X-Y |
| ![Web BUS workspace: the decoder, the packet table of a stopped record's 286 flash reads and the trigger on bytes](docs/images/web-bus.png) | ![Web BUS eye diagram of the 104 Mbit/s data with jitter analysis](docs/images/web-bus-eye.png) |
| **BUS:** decoder, packet table, trigger on bytes | **BUS again:** the eye diagram and jitter, drawn as on the scope with the same figures |
| ![Web SPECTRUM: FFT with the peak table of the 104 MHz clock](docs/images/web-spectrum.png) | ![Web spectrogram](docs/images/web-spectrogram.png) |
| **SPECTRUM:** FFT and peaks | **SPECTRUM again:** spectrogram |
| ![Web MEASURE: statistics, cursors and the mask test](docs/images/web-measure.png) | ![Web MEASURE, All values: every measurement of both channels in this capture](docs/images/web-measure-all.png) |
| **MEASURE:** statistics, cursors, mask and zones | **MEASURE again:** every value of both channels |
| ![Web COMPARE: REF A against the live trace with the difference readout, and the mask test counting](docs/images/web-compare.png) | ![All settings: every control of the app in one list, with where it lives](docs/images/web-allsettings.png) |
| **COMPARE:** REF A against the live trace, and the mask test counting a master stacked on the scope (the UART) | **All settings:** every control, and where it lives |

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
| **Vertical** | 2 mV/div to 10 V/div; DC, AC or GND (AC and GND applied to the samples); 20 MHz bandwidth limit; probe ×1, ×10 or ×100 |
| **Serial protocols** | **52**, each with a hardware trigger, decode and AUTO detection: UART, I²C, SPI, CAN / CAN FD, FlexRay, ARINC 429, MIL-STD-1553, SENT, Manchester, USB low and full speed, LIN, DMX512, MIDI, Modbus RTU, 1-Wire, WS281x, DHT / AM230x, HDMI-CEC, SAE J1850 VPW, IR NEC, IR Sony SIRC, IR RC-5, IR RC-6, DALI, USB Power Delivery, S/PDIF, PS/2, SWD, MDIO, Wiegand, quadrature encoder, stepper STEP/DIR, PWM, PT2262 / EV1527 remote codes, Oregon Scientific OOK, ISO 14443 Miller, Morse, DCF77, EM4100, EM4305, T55xx, STM8 SWIM, SDQ, PJON (PJDL), DSI, Qi, SD card (SD mode), AVR PDI, cJTAG, Maple bus, digital caliper, IR multi-protocol (Samsung, JVC, NEC16/42, Kaseikyo, Matsushita, Denon) |
| **Streaming decode** | **continuous, gap-free protocol decoding**, not one capture at a time: 921,600-baud UART ran for 3 minutes without losing a byte, with the full-rate record kept for every frame |
| **Analysis** | 18 automatic measurements, cursors, math, FFT, spectrogram, eye diagram and jitter, mask test |
| **Interfaces** | web UI in any browser, SCPI over VXI-11 (LAN), the scope's own LCD and front panel |

## Acquisition

* **Full-depth capture on both channels.** Every acquisition fills the scope's 2 MiB SRAM: 1 M
  samples per channel at 500 MS/s, 2.1 ms. While running, only a display-sized view of it is read,
  so the screen keeps up; **STOP or SINGLE reads the full record** (about 4 s, with a loading
  indicator), which can then be zoomed, measured, decoded and exported at full resolution. SINGLE
  shows its capture at once, as a live frame, and stops; the full record of that same capture
  follows. In Average, ERES and Precision, SINGLE completes one result as the live view does
  and stops on it, and STOP keeps the result shown.
  Changing V/div, offset or timebase on a stopped record redraws it from memory.
* **Calibrated five-way interleaving.** Each channel runs five 100 MS/s converters in turn. Per-core
  correction removes the interleave pattern (the comb drops from 11 codes to 0.15).
* **Converters checked.** About one FPGA load in a hundred left some converters not converting,
  which put a fake pattern at a fifth of the sample rate under every capture. The scope restarts
  the converters' encode clocks after every load and reads the ten cores; it reads them again
  every minute and whenever a full-rate record shows a core holding one code, and brings stuck
  ones back (an encode restart, then reloading the image). If they stay stuck, a notice on the LCD and the
  web UI's status line names them ("ADC fault: converter E2 not converting - restart the scope") and
  `/api/status` carries `adc_fault`; `adc_recoveries` counts the ones brought back.
* **Precision mode.** At slow timebases the FPGA decimates by a power of two, ×16 to ×1,048,576,
  through a CIC filter. The ARM applies a 63-tap compensation filter and keeps 16-bit samples, so a
  slow record gains resolution instead of aliasing. The UI shows the resulting bandwidth and bit
  gain.
* **Modes:** normal, average (a running average of up to 256 captures, aligned on the trigger),
  enhanced resolution and precision.
* **Live min/max envelope:** the FPGA reduces the screen to 4,096 min/max points, so narrow events
  stay visible at any zoom. Zoomed in until fewer of those points than pixels are shown (the LCD
  from 10×, the web page alike), the zoomed stretch of the same capture is read again at full
  detail, a min/max pair per column: a 1 MHz square at 200 µs/div zoomed 50× shows its 40 cycles,
  not the screen's points stretched (read in the same request as the screen: 1 to 3 frames a
  second of cost, the most where the zoom shows a fast signal's edge every few columns). The points are put back in
  time order, so protocol decode, masks and trigger placement run on live frames too. Average, ERES
  and precision use a decimated live capture of 8,192 samples instead (their filtering needs true
  samples; the FFT takes 8,192 to 16,384 filtered samples, its 8,192 points one for one, or up to
  32,768 at the full rate at 5 µs/div and faster); eye diagram and super-resolution read raw samples
  while in use.
* **Roll mode.** In AUTO at 10 ms/div and slower the view scrolls instead of waiting for a
  trigger. By default it is built from back-to-back decimated captures, so a timebase change
  responds at once; it captures about two thirds of the time, and the gaps between captures hold
  the last value, drawn dimmed, and are left out of measurements. HORIZ F4 (web: SCOPE, Horizontal,
  Roll: Stream) switches to
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
* **Pulse width, slope and video** (PAL / NTSC, line select) in hardware: the `qualify` FPGA image
  measures the width or slope time in 4 ns ticks and counts video lines, and fires on the event.
* **Auto, normal and single** modes; holdoff up to 10 s; force trigger.
* **Protocol triggers in the FPGA** for all fifty-two protocols. Match a byte, word, address, ID or frame
  pattern, with masks and wildcards. The forty-two added after the first ten (LIN onward) share one programmable
  block in the `qualify` image: it times each run of a line, classifies it through a table the app
  writes, takes the bits by pulse width, by a re-timed sampler or by a clock line, and matches a 64-bit
  window. AUTO in BUS finds a bus from an edge-triggered capture and arms its frame-start trigger.
  The decoded-event stream (BUS Stream view) covers the first ten.
* **Sequence trigger:** chain up to **32 protocol events**, including **trigger on error**.
* **Zone trigger:** draw up to four boxes on the screen that the waveform must cross or avoid; the
  FPGA keeps a history of where the signal was and decides each edge.
* Every trigger is decided in the FPGA; the app only places the trigger point exactly on the event.
* **Mask test:** build a golden mask from live frames with time and voltage tolerances. Count
  failures or stop on the first one, and browse the failure gallery.

## Serial protocols

Every protocol has an FPGA trigger and a decoder. The scope loads the FPGA image a protocol needs
automatically.

| Protocol | FPGA image | Highlights |
|---|---|---|
| UART | general; fast past 5 MBd or not 8N1 | 5 to 9 data bits, parity; to 62.5 MBd decoded on the samples |
| I²C | general; fast in high-speed mode | 7-bit address, direction, data bytes; to 3.4 MHz on the samples |
| SPI | general; fast past 20 MHz | all four modes, both bit orders; to 104 MHz on the samples, with a data delay |
| CAN / CAN FD | packet | Classic CAN CRC check; CAN FD ID/data decode (CRC not checked) |
| FlexRay | packet | header and payload; header CRC |
| ARINC 429 | packet | masked 32-bit word |
| MIL-STD-1553 | packet | 16-bit words; parity |
| SENT | packet | tick-calibrated nibbles; CRC |
| Manchester | line | IEEE 802.3 and Thomas conventions |
| USB low-speed | line | PID and payload bytes |

* **Auto** detects the bus once from a sampled capture at any timebase and fills the protocol, channel
  roles, rate, SPI mode and bit order into the decoder settings, where the LCD, the web page and the
  phone show and edit them; then it steps to a timebase whose live view reads the bus, and says so.
  Everything (the decode, the packet list, the stream, the serial trigger) runs on those settings. When
  they stop decoding for about two seconds at the timebase they decoded at (the probes moved to another bus),
  Auto detects again. It looks at a
  decimated capture first, so UART, I2C, SPI, SENT and ARINC 429 are named in under a second and a half;
  buses that need the full sample rate (Manchester, MIL-1553, CAN, USB, FlexRay) take a few seconds.
* **AUTO in BUS** scans for the bus anew and frames it for reading: the vertical fitted with the
  threshold midway, the hardware trigger on the start of each frame (I2C's START, SPI's first word after
  its idle, CAN's SOF, a MIL-1553 command word, the first UART character after an idle gap, ...), the
  trigger near the left and whole messages on screen with every bit resolved. The trigger point sits on
  the frame's own SYNC or SOF. The protocol images for Manchester, USB, SENT, CAN, FlexRay, MIL-1553 and
  ARINC 429 record 2.1 ms at the full rate, so their triggers run at 200 us/div or faster, as do the
  pulse width, slope and video triggers (they fire on full-rate captures only). AUTO on a
  UART framed before stays on the qualifier image it triggers with (about 5 s); a packet or line-code
  bus loads the general image for the fit and detection and its own image again (11 to 15 s). A
  stream that waits for an image load says so.
* **Continuous decoding:** a live decoded stream, with the full-rate record kept for every frame.
  BUS's third view (F2, F2, F2) streams the bus as decoded, with the transcript on the LCD; leaving the
  view ends the stream.
  UART at **921,600 baud ran lossless for 3 minutes** (16.6 M bytes). At higher rates a lost event
  is marked, never silently dropped.
* Hex, ASCII and combined views, and a watch buffer with regex search.

## Full-speed protocol decoding in the FPGA

The `fast` FPGA image decodes SPI, UART and I²C from the converters' own samples instead of 125 MHz
line levels. Each line's crossings are found in the raw 2 ns samples and timed to a sixteenth of a
nanosecond by interpolation; the receivers read the bits at those times. A byte trigger decided in the
FPGA fires on the matched frame, and the decoded stream carries every byte.

| | Bench source | FPGA trigger | Decoded in the FPGA |
|---|---|---|---|
| **SPI** | 104 MHz flash fast-read look-alike, its data settling 6.7 ns after the launching edge; and 10 to 104 MHz with data on the other edge | `OPEN`: every record's trigger 6 to 8 acquisition words after the receiver's match | 3,947 of 3,947 bursts exact at the eye's middle (none at the edge); 4,945 of 4,945 at 104 MHz |
| **UART** | 62.5 MBd in 8N1, 7E1 and 9O1, characters back to back; the same line inverted | `-SDS`, and a counting character on the inverted line | 3,961 bursts of 32 characters (8N1) and 3,982 (9O1), 0 errors; 10, 25 and 50 MBd as well |
| **I²C** | 3.4 MHz high-speed mode: a ten-bit address, a repeated START, NACKs | a read of address 0x50; a write of `00 10` | 3,799 transfers, 0 errors; 100 kHz, 400 kHz and 1 MHz as well |

All three run in one `fast` image (86 % of the FPGA's logic, timing met). The general image's
receivers stay for everything they read: UART to 5 MBd 8N1, I²C to fast-mode plus, SPI to about 20 MHz.
Past that, or with **Receiver = Samples** on the BUS sheet, the scope loads the fast image by itself (about
four seconds). Detection names these buses too: a 62.5 MBd UART and high-speed I²C.

**SPI data delay.** A flash read at 104 MHz puts its data out 6 to 8 ns after the clock edge that
launches it, most of a period late. The BUS sheet's **Data delay = Auto** reads the record's data eye:
every data transition between clock edges gives its phase, and the eye is the widest phase interval
holding none. The receiver reads the data at the middle of that interval, and the row shows the delay
and the timing margin left on each side.

| | |
|---|---|
| ![Native LCD, BUS workspace: the 104 MHz SPI flash look-alike decoded and triggered on OPEN](docs/images/scope-fast-spi.png) | ![The BUS sheet: Data delay Auto with the eye's margin, the clock rate and the receiver](docs/images/scope-fast-spi-sheet.png) |
| **SPI 104 MHz:** the flash look-alike, triggered on `OPEN` | **Data delay = Auto:** the eye's middle and the margin |
| ![Native LCD, BUS workspace: a 62.5 MBd UART decoded and triggered](docs/images/scope-fast-uart.png) | ![Native LCD, BUS workspace: 3.4 MHz high-speed I2C decoded and triggered on a read of 0x50](docs/images/scope-fast-i2c.png) |
| **UART 62.5 MBd** | **I²C 3.4 MHz** |
| ![Web BUS workspace: the 104 MHz SPI bus decoded at the data delay, the hardware trigger's matches counted](docs/images/web-fast-spi.png) | ![Web BUS workspace: the 62.5 MBd UART, triggered on -SDS](docs/images/web-fast-uart.png) |
| **Web, SPI 104 MHz** | **Web, UART 62.5 MBd** |
| ![Web BUS workspace: 3.4 MHz I2C transfers listed with their addresses](docs/images/web-fast-i2c.png) | |
| **Web, I²C 3.4 MHz** | |

## Analysis

* **18 automatic measurements:** Vpp, Vmax, Vmin, Vmean, Vrms, AC rms, Vtop, Vbase, amplitude,
  overshoot, preshoot, frequency, period, duty cycle, rise time, fall time, +width and −width.
  On the live min/max view, rise, fall and over/preshoot come from two short full-rate reads
  around the trigger edge and the next edge the other way (twice a second, a few milliseconds
  each); so do the frequency, duty, widths and mean of a signal faster than the min/max points
  resolve (a 1 MHz square at 200 µs/div). The statistics count each reading once.
* **Cursors** in time and voltage, with Δt, 1/Δt and ΔV. They stay on the signal: a timebase, V/div,
  offset or zoom change moves them with the waveform (an arrow marks one off the screen), so Δt and ΔV
  keep reading the same edges and levels.
* **Math:** C1 − C2, C2 − C1, C1 + C2, C1 × C2, and carrier removal using FFT peaks.
* **FFT** with a peak table (up to 64 peaks; a peak you pick on the web follows its tone within a few bins; when
  the tone is gone it reads greyed as lost for 3 s, then is cleared), and a **spectrogram** waterfall. On
  the LCD and the web each capture paints a row for every 2048-point stretch of its record, so the
  waterfall fills in about 20 s at 5 us/div instead of 85 s, at the same frame rate.
* **Eye diagram and jitter:** software clock recovery; TIE rms and peak-to-peak, RJ / DJ split,
  period and cycle-to-cycle jitter, eye height and width; TIE histogram and spectrum.
* **Super-resolution:** stacks repeated edges on a time grid up to ×64 finer than the sample period,
  with interpolating, cubic or drizzle kernels, or on a UART byte the FPGA triggers on. The **FPGA
  stacking image** accumulates the hits in hardware, using the same captured records across
  every tile to avoid acquisition-dependent seams. Phase-coherent equivalent-time folding resolves clocks above the trigger
  comparator's range.
* **Reference waveforms** (two), **persistence**, **XY mode** and **freeze**. REF A and B live on the
  scope: one stored on the LCD, the web page or the phone shows on all three, and the web's difference
  readout compares the live trace with the scope's references.
* **AUTO frames the signal for the workspace:** SCOPE two cycles with the swing on about six
  divisions; MEASURE at least ten cycles, with the cursors on one period and on the top and base, and
  SPECTRUM at least 50 (the marker on the fundamental),
  both with the largest swing that stays on the screen; COMPARE takes the setup of the mask or
  reference so the live trace sits on it; BUS frames the bus (below). A trace left past the screen's
  edge steps to a coarser V/div, and the notice says what AUTO did ("AUTO: 2 cycles, C1 500mV/div").
  AUTO on the web page frames for the workspace the page shows, whichever the LCD shows.

## Connect and control

* **Phone remote** at `/r` on port 8080: settings, workspaces and keys from a phone (below).
* **Web UI** on port 8080 in any browser, with WebGL rendering and every feature above. It is laid
  out as the scope's five task workspaces: SCOPE, BUS (decode with a packet table, trigger on bytes,
  eye diagram, the scope's own stream), SPECTRUM (FFT, harmonics and THD, spectrogram, Bode), MEASURE (statistics, cursors,
  mask test) and COMPARE (references, math, difference readout, stacked master). Alt+1 to Alt+5
  switch, and the shown workspace's key again shows its next view. A strip with run control,
  timebase, channels and trigger stays at the bottom, and All settings (Ctrl+,) lists every setting
  and shows where it lives.
* **Export:** PNG, CSV, sigrok `.sr` (opens in PulseView, with thresholded logic channels), VCD and
  WAV. A channel displayed inverted exports as shown (its volts negated, named in the CSV and VCD
  headers); SCPI `WF?` keeps the acquired volts, and `Cn:INVS?` tells a script the display is
  inverted.
* **SCPI over VXI-11** (LAN): Siglent / LeCroy short-form commands, including `WF?` waveform
  transfer with a WAVEDESC header and `SCDP` screenshots, so existing Siglent scripts and VISA tools
  work.
* **On the scope itself:** the LCD and the real front-panel keys and knobs, as five task workspaces
  (SCOPE, BUS, SPECTRUM, MEASURE, COMPARE) on F1 to F5, each workspace's key again showing its next view;
  MENU and the printed keys open full-screen settings sheets with every option written out. Horizontal
  Position moves the trigger in every workspace; its push switches it to the workspace's second job
  (BUS: next match or the eye's sample point, SPECTRUM: fine marker, MEASURE: cursor B) and back. In BUS,
  push Adjust to pick a byte from the packets and push it again to trigger on it. Settings are kept
  across restarts.
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
  another phone, with undo; the tab's badge counts what is new since you last looked. What the scope sets
  itself as a result (the memory depth a timebase brings, "(auto)") is part of the change that caused it: one
  entry, and one undo reverts both.
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
* hardware triggering and decoding for **fifty-two serial protocols**, sequence triggers and trigger on
  error;
* **streaming decode**: protocol traffic decoded continuously, without the gaps between captures;
* zone triggering, eye-diagram and jitter analysis, a spectrogram, precision mode and
  super-resolution;
* a complete browser interface, sigrok export, and open source you can extend.

## Install, update and remove

**Requirements:** a Siglent SDS1000CML+ (developed and validated on the SDS1102CML+), a USB stick,
the scope on a LAN for the web UI, the phone remote and SCPI, and a browser with WebGL.
Nothing is written to the scope's own flash: the firmware runs from the stick, and its settings are
kept on the stick.

**Install** from a [release](https://github.com/labeth/open-sds1000cml/releases), no build needed
(what is new and the known limitations: [release notes](docs/release-notes-1.0.md)):

1. Download `open-sds1000cml-<version>-usb.zip` (and `SHA256SUMS` to check it:
   `sha256sum -c SHA256SUMS --ignore-missing`).
2. Format the stick as **MBR + FAT32**, one primary partition (Windows: Rufus, partition scheme MBR,
   FAT32, as Windows' own tools stop at 32 GB; macOS: Disk Utility, MS-DOS (FAT) with Master Boot
   Record; Linux: GParted, an msdos table with one fat32 partition). A GPT stick is not seen.
3. Extract the zip onto the **root** of the stick: `startup.sh`, `ota/` and `agent-slots/` must be at
   the top, not inside a folder named after the zip.
4. Eject the stick, plug it into the scope and **power-cycle** the scope. The factory firmware runs
   `startup.sh` from the stick at boot; a few seconds later this firmware takes over the LCD and the
   front panel.
5. Open `http://<scope-ip>:8080/` (the phone remote is `/r`). The LCD shows the address; your router's
   DHCP client list has it too.

With the stick in, the scope runs this firmware at every boot; without it, the factory firmware.

**Update:** extract the new release's zip onto the stick the same way, replacing the files, and
power-cycle. Settings stay (they live in `scope-settings.json` on the stick, which the zip does not
contain); `ota/agent.env` is the zip's again, so make any edits to it again. Or update over the network with `otactl` (below): it installs a new app into the other
of two app slots and goes back to the previous one if the new one fails its health check.

**Roll back:** `otactl -tcp <scope-ip>:5900 activate A` (or `B`) runs the app in the other slot; after
a zip install both slots hold the same app, so the earlier release's zip is the way back.

**Remove:** take the stick out and power-cycle. The scope boots the factory firmware, unchanged.

**Network:** the web UI (8080), SCPI (VXI-11) and the agent's control port (5900) have no
authentication: anyone who can reach the scope can control it, and port 5900 runs commands on it as
root. Use the scope on a trusted network. `OTA_LISTEN=127.0.0.1:5900` in `ota/agent.env` closes port
5900 to the network (updates then go by the stick).

**Version:** the scope's SYS settings tab (the Help key) names it, the web UI's status line ends with
it, and `/api/status` has it (`version`). `otactl version` and `otactl -tcp <scope-ip>:5900 status`
name the controller's and the agent's.

**otactl** is the host controller for the agent on the scope (status, logs, updates, rollback). The
release has it for Linux, macOS and Windows; see [`ota/README.md`](ota/README.md):

```sh
otactl -tcp <scope-ip>:5900 status
otactl -tcp <scope-ip>:5900 update-app agent-slots/A/app   # the app from a release zip
otactl -tcp <scope-ip>:5900 logs agent
```

## Documentation

The engineering model is the documentation: requirements, architecture, behaviour and design
decisions are in [`model/`](model/) (start with `requirements.yml` and `decisions.yml`). The
behavioural specifications the implementation was written from are in [`specs/`](specs/).

## Layout

| Path | What it is |
|---|---|
| `app/` | The scope application (Go, ARMv7): acquisition engine, triggers, decoders, LCD, front panel, web UI, SCPI/VXI-11. Embeds the seven FPGA images. |
| `fpga/` | The FPGA images (Verilog, Cyclone IV EP4CE10): what is common, what each image adds, the shipping bitstreams and the build. |
| `ota/` | The on-device agent and `otactl`: install, launch, health, A/B slots, USB stick builder. |
| `tools/deploy.sh` | Build the release app and install it on a scope over the network. |
| `model/` | The engineering model (documentation). |
| `specs/` | Behavioural specifications. |

## Build, test, deploy

```sh
make test                      # app and ota test suites
fpga/sim.sh                    # self-checking RTL testbenches (iverilog)
make -C app app-release        # ARMv7 app with the six FPGA images embedded
tools/deploy.sh                # build and install on the scope (DEV=192.168.1.209)
node --experimental-strip-types fpga/build.ts --image=general   # rebuild an FPGA image (Quartus 21.1 Lite)
```

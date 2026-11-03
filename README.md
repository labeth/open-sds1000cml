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

To try it, see [Install, update and remove](#install-update-and-remove); what each release adds and
its known limitations are in the release notes ([1.2.1](docs/release-notes-1.2.1.md), [1.2](docs/release-notes-1.2.md)).

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

<sub>Unless a caption says otherwise, every capture here is from a live SDS1102CML+ running this
firmware, with 10x probes. An Alchitry Au drives a look-alike of an SPI flash read at 104 MHz (clock on
C2, data on C1; each read is `0B 00 10 00`, a dummy byte, `SPI FLASH 104MHZ OPEN-SDS 0123` and 32
counting bytes), and the serial trigger matches `0B 00 10 00`. LCD images are the native 800 × 480
screen.</sub>

### On the scope

Five task workspaces, one per printed F-key on the rail at the right. Pressing the shown workspace's key
again switches to its next view: SCOPE Y-T and X-Y; BUS Packets, Eye diagram, Stream and Visual;
SPECTRUM FFT, Spectrogram, Bode and Sound; MEASURE statistics and all values; COMPARE stacks a master.

| | |
|---|---|
| ![SCOPE workspace: the SPI data on C1 and its 104 MHz clock on C2 with readouts, and the decoded bytes under the trace](docs/images/scope-scope.png) | ![SCOPE again: X-Y, the SPI data against its clock](docs/images/scope-xy.png) |
| **SCOPE:** the full trace, readouts and the decoded strip (the trace ends above it) | **SCOPE again:** X-Y (here the data against its clock) |
| ![BUS again: the eye diagram of the 104 Mbit/s SPI data with bit rate, eye height and width, and TIE jitter](docs/images/scope-bus-eye.png) | ![The match editor: a full-screen keypad for the trigger's match bytes, in hex](docs/images/scope-match-editor.png) |
| **BUS again:** the eye diagram of the 104 Mbit/s data, with eye height, width and TIE jitter | **The match editor:** the knobs move over a full-screen keypad, hex or text |
| ![BUS Stream: the scrolling hexdump transcript of a gap-free UART stream, with its rate, total and loss counters](docs/images/scope-bus-stream.png) | ![BUS Visual: a MIDI bus drawn as a piano, the notes pushed and released marked above their keys](docs/images/scope-bus-visual-midi.png) |
| **BUS, Stream:** the bus decoded without gaps, as a transcript | **BUS, Visual:** the bus drawn as what it carries (here MIDI on a piano) |
| ![SPECTRUM: the FFT of the SPI clock with its 104 MHz peak marked and the peak table over a small time trace](docs/images/scope-spectrum.png) | ![SPECTRUM again: the spectrogram, the spectrum over time](docs/images/scope-spectrogram.png) |
| **SPECTRUM:** the FFT and peak table (the clock at 104 MHz) | **SPECTRUM again:** the spectrogram |
| ![SPECTRUM, Bode: gain and phase against a log frequency axis from 500 kHz to 5 MHz, measured from a stepped source on C1 and the same signal through a delay on C2: flat gain, phase falling with frequency](docs/images/scope-bode.png) | ![SPECTRUM, Sound: level meters, the tuner and the pitch of an audio tone streamed gap-free](docs/images/scope-sound.png) |
| **SPECTRUM, Bode:** gain and phase against frequency (here a delay: flat gain, a phase ramp) | **SPECTRUM, Sound:** meters and tuner on the gap-free audio stream |
| ![MEASURE: statistics of the chosen readouts, now, mean, min, max and sdev](docs/images/scope-measure.png) | ![MEASURE again: every value of both channels](docs/images/scope-measure-all.png) |
| **MEASURE:** statistics of the chosen readouts | **MEASURE again:** every value of both channels |
| ![COMPARE: a master stacked from 64 captures of a UART in REF A and its mask testing live captures](docs/images/scope-compare.png) | ![COMPARE again: Stacking, captures of a UART being averaged into a master, with the count so far](docs/images/scope-compare-stacking.png) |
| **COMPARE:** a master stacked from 64 captures (REF A), its mask testing live captures (a 115,200-baud UART: a master wants a signal sampled many times a period) | **COMPARE again:** stacking the master |
| ![The BUS settings sheet: the SPI decoder, the clock rate and data delay measured on the bus, and the match, with a live preview and help](docs/images/scope-sheet-bus.png) | |
| **A settings sheet:** every option written out, with a live preview and help | |

### In a browser

The web UI on port 8080 has the scope's five workspaces and SOUND as a sixth (Alt+1 to Alt+6), a strip
with run control, timebase, channels and trigger, and All settings (Ctrl+,). A workspace's key pressed
again shows its next view, as on the scope.

| | |
|---|---|
| ![Web SCOPE workspace](docs/images/web-scope.png) | ![Web SCOPE in X-Y: the SPI data against its clock](docs/images/web-xy.png) |
| **SCOPE** | **SCOPE again:** X-Y |
| ![Web BUS workspace: the decoder, the packet table of a stopped record's 286 flash reads and the trigger on bytes](docs/images/web-bus.png) | ![Web BUS eye diagram of the 104 Mbit/s data with jitter analysis](docs/images/web-bus-eye.png) |
| **BUS:** decoder, packet table, trigger on bytes | **BUS again:** the eye diagram and jitter, drawn as on the scope with the same figures |
| ![Web BUS Stream view: the gap-free transcript of a decoded bus with its rate and loss counters, and the option to decode in this browser](docs/images/web-bus-stream.png) | ![Web BUS Visual view: a MIDI bus as a piano with the held notes, the message count and the recording to a .mid file](docs/images/web-bus-visual-midi.png) |
| **BUS, Stream:** the transcript, decoded on the scope or in the browser | **BUS, Visual:** MIDI as a piano, sounding in the browser and recorded to `.mid` |
| ![Web SPECTRUM: FFT with the peak table of the 104 MHz clock](docs/images/web-spectrum.png) | ![Web spectrogram](docs/images/web-spectrogram.png) |
| **SPECTRUM:** FFT and peaks | **SPECTRUM again:** spectrogram |
| ![Web SPECTRUM Bode: the gain and phase points of the delay measured against a stepped source, with the point table](docs/images/web-bode.png) | ![Web SOUND workspace: level meters, the tuner with cents and pitch over time, and Listen](docs/images/web-sound.png) |
| **SPECTRUM, Bode** | **SOUND:** meters, tuner, envelope, modulation, spectrum, Listen |
| ![Web MEASURE: statistics, cursors and the mask test](docs/images/web-measure.png) | ![Web MEASURE, All values: every measurement of both channels in this capture](docs/images/web-measure-all.png) |
| **MEASURE:** statistics, cursors, mask and zones | **MEASURE again:** every value of both channels |
| ![Web COMPARE: REF A against the live trace with the difference readout, and the mask test counting](docs/images/web-compare.png) | ![All settings: every control of the app in one list, with where it lives](docs/images/web-allsettings.png) |
| **COMPARE:** REF A against the live trace, and the mask test counting a master stacked on the scope (the UART) | **All settings:** every control, and where it lives |

### On a phone

`http://<scope>:8080/r` is a remote for the scope's settings: it shows no waveform and never opens
anything on the scope's screen (see [Phone remote](#phone-remote)).

| | | |
|---|---|---|
| ![Phone remote Home: big tiles for time/div, trigger level and V/div, the workspace chips, Run/Stop, Single and Auto](docs/images/remote-home.png) | ![Phone remote in BUS: the match bytes and serial trigger tiles](docs/images/remote-bus.png) | ![Phone remote Menus: every settings tab, with search](docs/images/remote-menus.png) |
| **Home** | **BUS:** match bytes, serial trigger | **Menus:** every tab, with search |
| ![Phone remote typed entry: a keypad with units for time/div](docs/images/remote-sheet.png) | ![Phone remote History: each change with who made it, and undo](docs/images/remote-history.png) | ![Phone remote Keys: the knob pushes and keys, and the phone's keep-awake and fullscreen](docs/images/remote-keys.png) |
| **Typed values** with units | **History** with undo | **Keys** and the phone's settings |
| ![Phone remote Sound tab: the tuner with cents, the level meters and the Listen button](docs/images/remote-sound.png) | | |
| **Sound:** tuner, meters and Listen | | |

## At a glance

| | |
|---|---|
| **Channels** | 2, sampled together |
| **Sample rate** | **500 MS/s per channel**, real time, on both channels at once (1 GS/s aggregate) |
| **Record length** | **1,048,576 samples per channel** at full rate, captured on every acquisition; the full record is read on STOP or SINGLE in 0.2 to 0.4 s |
| **Live display** | **45–53 frames/s** on the LCD from 10 ns/div to 100 µs/div, protocol decode included (36 at 1 ms/div; the panel refreshes at 52 Hz); with a web page open, 50–62 frames/s for the page at 1 µs/div and faster, the LCD at 20 |
| **Precision mode** | 16-bit filtered samples; decimation ×16 to ×1,048,576 for long records |
| **Super-resolution** | up to a **×64 time grid** (31.25 ps), in software or stacked in the FPGA |
| **Timebase** | 1 ns/div to 50 s/div |
| **Roll mode** | AUTO at 10 ms/div and slower scrolls continuously, from captures or as a gap-free stream buffered in the scope's SRAM |
| **Vertical** | 2 mV/div to 10 V/div; DC, AC or GND (AC and GND applied to the samples); 20 MHz bandwidth limit; probe ×1, ×10 or ×100 |
| **Serial protocols** | **52**, each with a hardware trigger, decode and AUTO detection ([list](#serial-protocols)) |
| **Streaming decode** | **continuous, gap-free protocol decoding**, not one capture at a time: each line sliced in the FPGA and streamed at up to 2.61M words a second; 921,600-baud UART ran for 3 minutes without losing a byte |
| **Analysis** | 18 automatic measurements, cursors, math, FFT, spectrogram, Bode, eye diagram and jitter, mask test, bus layers, BUS Visual, SOUND (audio meters, tuner, envelope, modulation, harmonics, Listen) |
| **Interfaces** | web UI in any browser, phone remote, SCPI over VXI-11 (LAN), the scope's own LCD and front panel |

## Acquisition

* **Full-depth capture on both channels.** Every acquisition fills the scope's 2 MiB SRAM: 1 M
  samples per channel at 500 MS/s, 2.1 ms. While running, only a display-sized view of it is read,
  so the screen keeps up; **STOP or SINGLE reads the full record** (0.2 to 0.4 s, with a loading
  indicator), which can then be zoomed, measured, decoded and exported at full resolution. SINGLE
  shows its capture at once, as a live frame, and stops; the full record of that same capture
  follows. In Average, ERES and Precision, SINGLE completes one result as the live view does
  and stops on it, and STOP keeps the result shown.
  Changing V/div, offset or timebase on a stopped record redraws it from memory.
* **Fast readout.** An isolated bus worker reads the FPGA by DMA into a ring shared with the app,
  about 11 MB/s: a full record reaches the screen at 8 to 9 MB/s. For the live view the FPGA sends
  an 8 KiB min/max envelope instead of the 2 MiB record, while the full record stays in the SRAM for
  STOP, SINGLE and detailed review.
* **Calibrated five-way interleaving.** Each channel runs five 100 MS/s converters in turn. Per-core
  correction removes the interleave pattern (the comb drops from 11 codes to 0.15).
* **Converters checked.** About one FPGA load in a hundred leaves some converters not converting,
  which would put a fake pattern at a fifth of the sample rate under every capture. The scope restarts
  the converters' encode clocks after every load and reads the ten cores; it reads them again
  every minute and whenever a full-rate record shows a core holding one code, and brings stuck
  ones back (an encode restart, then reloading the image). If they stay stuck, a notice on the LCD and the
  web UI's status line names them ("ADC fault: converter E2 not converting - restart the scope") and
  `/api/status` carries `adc_fault`; `adc_recoveries` counts the ones brought back.
* **Precision mode.** At slow timebases the FPGA decimates by a power of two, ×16 to ×1,048,576,
  through a CIC filter. The ARM applies a 63-tap compensation filter and keeps 16-bit samples, so a
  slow record gains resolution instead of aliasing. The UI shows the resulting bandwidth and bit
  gain. The precision filter, the rounding to codes, the extremes of a record and of each drawn
  column and the interleave correction run on the CPU's NEON unit.
* **Modes:** normal, average (a running average of up to 256 captures, aligned on the trigger),
  enhanced resolution and precision.
* **Live min/max envelope:** the FPGA reduces the screen to 4,096 min/max points, so narrow events
  stay visible at any zoom. Zoomed in until fewer of those points than pixels are shown (the LCD
  from 10×, the web page alike), the zoomed stretch of the same capture is read again at full
  detail, a min/max pair per column: a 1 MHz square at 200 µs/div zoomed 50× shows its 40 cycles,
  not the screen's points stretched. The points are put back in time order, so protocol decode,
  masks and trigger placement run on live frames too. Average, ERES and precision use a decimated
  live capture of 8,192 samples instead (their filtering needs true samples); the FFT takes 8,192 to
  16,384 filtered samples, or up to 32,768 at the full rate at 5 µs/div and faster; eye diagram and
  super-resolution read raw samples while in use.
* **Roll mode.** In AUTO at 10 ms/div and slower the view scrolls instead of waiting for a
  trigger. By default (Roll: Chunked) it is built from back-to-back decimated captures, so a
  timebase change responds at once; the gaps between captures hold the last value, drawn dimmed,
  and are left out of measurements. Roll: Stream (HORIZ on the scope; SCOPE, Horizontal on the web)
  switches to **gap-free streaming** on the FPGA's `stream` image: every averaged (or min/max)
  sample reaches the ARM, with the scope's SRAM as the stream's buffer, so a pause of the host
  loses nothing; a loss is reported, never silent.
* **Responsive controls:** front-panel keys and lamps answer within tens of milliseconds, even
  during a long readout; a LOADING indicator (LCD and web) shows while a record is being read.
* **The LCD and the acquisition share one core, by who is looking.** The panel refreshes at
  52 Hz, so the LCD draws at most 52 pictures a second. With no web page open on the scope the LCD
  is the only view and comes first: as many pictures as the core allows, a new spectrum in each,
  the acquisition slowed (never under 20 frames a second) only while the LCD falls behind it. With
  a web page showing the scope the acquisition comes first, for the page: the LCD gives way, but
  never below 20 pictures a second.

## Triggering

* **Every trigger is decided in the FPGA**, at every timebase; the app only places the trigger point
  exactly on the event. The edge trigger, the qualifiers and the protocol receivers read the
  converters' raw samples whatever the record's decimation. On a decimated record an edge must hold
  for a sample of every converter (10 ns), so a level just above a flat top does not fire on the
  converters' offsets.
* **Edge** with sub-sample placement: 0.19 ns peak-to-peak edge wander at 5 ns/div.
* **Pulse width, slope and video** (PAL / NTSC, line select): the `qualify` FPGA image measures the
  width or slope time in 4 ns ticks and counts video lines, and fires on the event.
* **Auto, normal and single** modes; holdoff up to 10 s; force trigger.
* **Protocol triggers** for all fifty-two protocols. Match a byte, word, address, ID or frame
  pattern, with masks and wildcards. The first ten have their own receivers in the FPGA (MIDI uses
  the UART's); the other forty-one share one programmable block in the `qualify` image (the symbol
  trigger), which times each run of a line, classifies it through a table the app writes and matches
  a 64-bit window.
* **Sequence trigger:** chain up to **32 protocol events**, including **trigger on error**.
* **Zone trigger:** draw up to four boxes on the screen that the waveform must cross or avoid; the
  FPGA keeps a history of where the signal was and decides each edge.
* **Mask test:** build a golden mask from live frames with time and voltage tolerances. Count
  failures or stop on the first one, and browse the failure gallery.

## Serial protocols

Fifty-two protocols, each with an FPGA trigger, a decoder (on the scope and, the same, in the web
UI), AUTO detection and a decoded stream:

UART, I²C, SPI, CAN / CAN FD, FlexRay, ARINC 429, MIL-STD-1553, SENT, Manchester, USB low and full
speed, LIN, DMX512, MIDI, Modbus RTU, 1-Wire, WS281x, DHT / AM230x, HDMI-CEC, SAE J1850 VPW, IR NEC,
IR Sony SIRC, IR RC-5, IR RC-6, DALI, USB Power Delivery, S/PDIF, PS/2, SWD, MDIO, Wiegand, quadrature
encoder, stepper STEP/DIR, PWM, PT2262 / EV1527 remote codes, Oregon Scientific OOK, ISO 14443 Miller,
Morse, DCF77, EM4100, EM4305, T55xx, STM8 SWIM, SDQ, PJON (PJDL), DSI, Qi, SD card (SD mode), AVR PDI,
cJTAG, Maple bus, digital caliper, IR multi-protocol (Samsung, JVC, NEC16/42, Kaseikyo, Matsushita,
Denon).

The scope loads the FPGA image a protocol needs by itself (about 1 s). The first ten have receivers of
their own:

| Protocol | FPGA image | Highlights |
|---|---|---|
| UART | general; fast past 5 MBd or not 8N1 | 5 to 9 data bits, parity; to 62.5 MBd decoded on the samples |
| I²C | general; fast in high-speed mode | 7-bit address, direction, data bytes; to 3.4 MHz on the samples |
| SPI | general; fast past 20 MHz | all four modes, both bit orders; to 104 MHz on the samples, with a data delay |
| CAN / CAN FD | packet | Classic CAN CRC check; CAN FD ID/data decode (CRC not checked) |
| FlexRay | packet | header and payload; header CRC |
| ARINC 429 | packet | masked 32-bit word |
| MIL-STD-1553 | frame | 16-bit words; parity |
| SENT | frame | tick-calibrated nibbles; CRC |
| Manchester | line | IEEE 802.3 and Thomas conventions |
| USB low-speed | frame | PID and payload bytes |

USB full speed uses the USB receiver and MIDI the UART's; the other forty-one are on the `qualify`
image's symbol trigger.

* **Auto** detects the bus once from a sampled capture at any timebase and fills the protocol, channel
  roles, rate, SPI mode and bit order into the decoder settings, where the LCD, the web page and the
  phone show and edit them; then it steps to a timebase whose live view reads the bus, and says so.
  Everything (the decode, the packet list, the stream, the serial trigger) runs on those settings. When
  they stop decoding for about two seconds at the timebase they decoded at (the probes moved to another
  bus), Auto detects again. It looks at a decimated capture first, so UART, I2C, SPI, SENT and ARINC 429
  are named in under a second and a half; buses that need the full sample rate (Manchester, MIL-1553,
  CAN, USB, FlexRay) take a few seconds.
* **AUTO in BUS** scans for the bus anew and frames it for reading: the vertical fitted with the
  threshold midway, the hardware trigger on the start of each frame (I2C's START, SPI's first word after
  its idle, CAN's SOF, a MIL-1553 command word, the first UART character after an idle gap, ...), the
  trigger near the left and whole messages on screen with every bit resolved. AUTO names a bus from
  Default setup in about 4 to 9 s (5.2 s on average over 60 bench buses of all 52 protocols), and
  arms its trigger about 1 s later when the protocol's FPGA image has to load.
* **Decode before frame rate in BUS:** where the live view is too coarse for the bus (an envelope
  at a slow timebase, or the roll view: a 115200 Bd UART at 10 ms/div), BUS reads records sampled
  just finely enough for the bus instead and decodes them (a record every 0.35 s at 10 ms/div); the
  timebase or the bus changing tries the fast live view again first.
* **Bus layers:** application decoders stacked on a decoded bus, as sigrok's: SPI flash, SD card, AVR ISP,
  radios, network chips, converters and sensors on SPI (22); EDID, clocks, EEPROMs, port expanders and
  sensors on I²C (16, with by-address and address-filter views); S.BUS, ARM trace, a Bluetooth module and
  Amulet displays on UART (6); the 1-Wire ROM layer and three 1-Wire devices; CFP modules on MDIO; USB
  requests. The BUS sheet's Layer row picks one, or Auto names it from the packets.
* Hex, ASCII and combined views, and a watch buffer with regex search.

## Decoded streams

BUS's third view (Stream) decodes the bus continuously, not one capture at a time, into a scrolling
transcript on the LCD, the web page and the phone; leaving the view ends the stream. It stops on a
decode error, on a value, or by hand, and then shows the full-rate record around the event.

* **The first ten, MIDI and USB full speed** stream from their FPGA receivers' decoded events, with
  the full-rate record kept for every frame. UART at **921,600 baud ran lossless for 3 minutes**
  (16.6 M bytes); at higher rates a lost event is marked, never silently dropped.
* **The symbol trigger's buses** stream on the `bits` image: each line sliced at its level in the
  FPGA, sixteen periods a word, at a period of any whole number of 4 ns raw words, down to 24 ns a
  period on both lines at the bus's ceiling of 2.61M words a second. The SRAM holds the stream's
  backlog, so the host's pauses lose nothing. A protocol streams **gap-free** where the scope's CPU
  decodes it as fast as it comes, and as **captures** (back-to-back records armed on the symbol
  trigger, with the share of the signal time they read) where it cannot: the dense ones (SWD, MDIO
  and Maple at a 1 MHz clock, WS281x, SWIM). The BUS sheet's Stream mode row, the LCD and the web
  page say which. Carrier lines (IR remotes, RFID tags) stream as min/max words.
* **A stream stopped on a value shows the event itself.** On the slower buses (DMX512, 1-Wire, LIN,
  J1850, Modbus, CEC) the FPGA keeps an analog history ring of 0.5 to 2.1 s beside the stream; the
  stop freezes it with the trigger on the decoded frame. Faster buses show the full-rate record of
  the value's next occurrence. These buses stop on a value or by hand, not on an error: the symbol
  trigger has no error token.
* **Decode in this browser** (a checkbox on the web page's Stream view): the scope sends the digital
  stream's words and the page decodes them in a web worker, frame for frame as the scope does, so
  the dense buses stream gap-free too. The scope's transcript, LCD and phone show the page's frames,
  and the scope decodes again a few seconds after the page lets go.
* **BUS Visual** (the fourth view) draws the bus as what it carries: MIDI as a piano, the notes
  falling onto its keys and sounding in the browser (and recorded to a `.mid` file); digital audio
  (S/PDIF, I2S) with its spectrum, distortion and noise; DMX levels; a PS/2 keyboard or mouse; an LED
  strip; an IR remote's buttons; a PWM gauge or servo; an encoder's or stepper's dial; a caliper's
  reading; the DCF77 time; LIN and CAN frames (CAN signals from a DBC file); the USB PD contract;
  Wiegand cards; a bus layer's values. A bus streamed gap-free is followed whole; one streamed as
  captures shows what the captures hold.

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

All three run in one `fast` image. The general image's receivers stay for everything they read: UART to
about 5 MBd 8N1, I²C to fast-mode plus, SPI to about 20 MHz. Past that, or with **Receiver = Samples**
on the BUS sheet, the scope loads the fast image by itself. Detection names these buses too: a 62.5 MBd
UART and high-speed I²C.

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
* **FFT** with a peak table (up to 64 peaks; a peak you pick on the web follows its tone within a few
  bins; when the tone is gone it reads greyed as lost for 3 s, then is cleared), harmonics and THD.
  The LCD takes two 8,192-point spectra for each picture (about 21 pictures a second at 1 µs/div,
  12 at 100 µs/div).
* **Spectrogram:** the spectrum over time as a waterfall; each capture paints a row for every
  2048-point stretch of its record (about 11 pictures a second).
* **Bode:** gain and phase of a circuit against a log frequency axis, from its input on one channel
  and its output on the other while a source steps through the frequencies.
* **Eye diagram and jitter:** software clock recovery; TIE rms and peak-to-peak, RJ / DJ split,
  period and cycle-to-cycle jitter, eye height and width; TIE histogram and spectrum.
* **Super-resolution:** stacks repeated edges on a time grid up to ×64 finer than the sample period,
  with interpolating, cubic or drizzle kernels, or on a UART byte the FPGA triggers on. The **FPGA
  stacking image** accumulates the hits in hardware, streaming the record through its scan four
  samples a clock, using the same captured records across every tile to avoid acquisition-dependent
  seams. Phase-coherent equivalent-time folding resolves clocks above the trigger comparator's range.
* **SOUND** (SPECTRUM's fourth view on the LCD, its own workspace on the web page, the Sound tab on
  the phone): both channels streamed gap-free at 61 kSa/s; level meters against dBu, dBV or a preset
  reference (Eurorack, line levels, guitar); a tuner with cents, strobe and pitch over time, V/oct
  tracking and a drift log; envelope (ADSR) capture; tremolo, vibrato and FM index; the audio spectrum
  with harmonics and THD; input against output (gain, phase, delay, added distortion, XY transfer); a
  pitch-locked cycle view on the FPGA's edge marks; and Listen, the stream played in the browser.
  STOP freezes a take of up to 15 s to scroll, measure and replay; SINGLE and NORMAL take one around
  an edge the FPGA marks. The stream and the other workspaces share one acquisition: while a web or
  phone SOUND page holds it, the LCD says so, and AUTO (or any other key) takes it back.
* **Reference waveforms** (two), **persistence**, **XY mode** and **freeze**. REF A and B live on the
  scope: one stored on the LCD, the web page or the phone shows on all three, and the web's difference
  readout compares the live trace with the scope's references.
* **AUTO frames the signal for the workspace:** SCOPE two cycles with the swing on about six
  divisions; MEASURE at least ten cycles, with the cursors on one period and on the top and base, and
  SPECTRUM at least 50 (the marker on the fundamental),
  both with the largest swing that stays on the screen; COMPARE takes the setup of the mask or
  reference so the live trace sits on it; BUS frames the bus (above). A trace left past the screen's
  edge steps to a coarser V/div, and the notice says what AUTO did ("AUTO: 2 cycles, C1 500mV/div").
  AUTO on the web page frames for the workspace the page shows, whichever the LCD shows.

## Connect and control

* **On the scope itself:** the LCD and the real front-panel keys and knobs, as five task workspaces
  (SCOPE, BUS, SPECTRUM, MEASURE, COMPARE) on F1 to F5, each workspace's key again showing its next view;
  MENU and the printed keys open full-screen settings sheets with every option written out, and Help
  opens the SYS tab (system settings, the version and a guide to the workspace). Horizontal
  Position moves the trigger in every workspace; its push switches it to the workspace's second job
  (BUS: next match, the eye's sample point or the transcript; SPECTRUM: fine marker or Bode point;
  MEASURE: cursor B) and back. In BUS, push Adjust to pick a byte from the packets and push it again
  to trigger on it. Settings are kept across restarts.
* **Web UI** on port 8080 in any browser, with WebGL rendering and every feature above. It is laid
  out as the scope's five task workspaces plus SOUND: SCOPE, BUS (decode with a packet table, trigger
  on bytes, eye diagram, stream, visual), SPECTRUM (FFT, harmonics and THD, spectrogram, Bode),
  MEASURE (statistics, cursors, mask test), COMPARE (references, math, difference readout, stacked
  master) and SOUND. Alt+1 to Alt+6 switch, and the shown workspace's key again shows its next view.
  A strip with run control, timebase, channels and trigger stays at the bottom, and All settings
  (Ctrl+,) lists every setting and shows where it lives.
* **Phone remote** at `/r` on port 8080 (below).
* **Export:** PNG, CSV, sigrok `.sr` (opens in PulseView, with thresholded logic channels), VCD and
  WAV. A channel displayed inverted exports as shown (its volts negated, named in the CSV and VCD
  headers); SCPI `WF?` keeps the acquired volts, and `Cn:INVS?` tells a script the display is
  inverted.
* **SCPI over VXI-11** (LAN): Siglent / LeCroy short-form commands, including `WF?` waveform
  transfer with a WAVEDESC header and `SCDP` screenshots, so existing Siglent scripts and VISA tools
  work. `*IDN?` answers as the factory firmware does.
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
* **Sound:** the tuner, the level meters and Listen, from SOUND's stream (the scope's captures pause while it
  shows).
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
* **streaming decode**: protocol traffic decoded continuously, without the gaps between captures,
  and drawn as what it carries;
* zone triggering, eye-diagram and jitter analysis, a spectrogram, Bode plots, SOUND, precision mode
  and super-resolution;
* a complete browser interface, a phone remote, sigrok export, and open source you can extend.

## Install, update and remove

**Requirements:** a Siglent SDS1000CML+ (developed and validated on the SDS1102CML+), a USB stick,
the scope on a LAN for the web UI, the phone remote and SCPI, and a browser with WebGL.
Nothing is written to the scope's own flash: the firmware runs from the stick, and its settings are
kept on the stick.

**Install** from a [release](https://github.com/labeth/open-sds1000cml/releases), no build needed:

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
power-cycle. Settings stay (they live in `ota/scope-settings.json` on the stick, which the zip does not
contain); `ota/agent.env` is the zip's again, so make any edits to it again. Or update over the
network with `otactl`, the host controller in the release for Linux, macOS and Windows:
`otactl -tcp <scope-ip>:5900 update-app agent-slots/A/app` (the app from a release zip) installs it
into the other of two app slots and goes back to the previous one if the new one fails its health
check.

**Roll back:** `otactl -tcp <scope-ip>:5900 activate A` (or `B`) runs the app in the other slot; after
a zip install both slots hold the same app, so the earlier release's zip is the way back.

**Remove:** take the stick out and power-cycle. The scope boots the factory firmware, unchanged.

**Network:** the web UI (8080), SCPI (VXI-11) and the agent's control port (5900) have no
authentication: anyone who can reach the scope can control it, and port 5900 runs commands on it as
root. Use the scope on a trusted network. `OTA_LISTEN=127.0.0.1:5900` in `ota/agent.env` closes port
5900 to the network (updates then go by the stick).

**Version:** the scope's SYS settings tab (the Help key) names it, the web UI's status line ends with
it, and `/api/status` has it (`version`). `otactl version` and `otactl -tcp <scope-ip>:5900 status`
name the controller's and the agent's. Every `otactl` command (status, logs, takeover, updates) is in
[`ota/README.md`](ota/README.md).

**Problems:** open an [issue](https://github.com/labeth/open-sds1000cml/issues) with the scope model,
the firmware version, what happened, and the logs from the stick (`ota/logs/agent.log`,
`ota/logs/boot.log`, `ota/logs/firstboot.log`).

## Other SDS1000 models

The firmware supports the SDS1000CML+ (tested on the SDS1102CML+). To help support another model
(an SDS1000DL+ or another population of the same board), use the diagnostics stick,
`open-sds1000cml-<version>-diagnostics.zip` from the release: extract it onto a FAT32 stick, clip
both probes to the scope's 1 kHz probe compensation output, boot the scope with the stick, wait about
half an hour until the screen says DONE, and send the `sds-diag-….tar.gz` it leaves on the stick. It
installs nothing and writes nothing but the stick; its `README.txt` says how to use it and what the
file holds. In one boot, each stage only after checks that the one before found what it expects:

1. **Read-only:** the board's processor, kernel, devices and pin multiplexing, the factory app's
   devices and memory map, the factory firmware files (with the FPGA image and calibration), the
   factory app's settings and its screen.
2. **Active:** which FPGA registers and SPI words the factory firmware writes for each setting,
   while the stick steps the factory app through every V/div, coupling, offset, timebase, trigger and
   acquisition setting; then the settings go back.
3. **Takeover:** the stick stops the factory app and loads a probe image into the FPGA. It then
   tests every part of the board that this firmware uses, and says whether each is fitted:
   - the converters and their clocks;
   - each channel's gain DAC, range relay, AC coupling, bandwidth limit, offset DAC and trigger
     comparator, measured on the 1 kHz square;
   - the sample memory;
   - the panel's keys and key interrupt;
   - the processor's devices;
   - every other FPGA ball, against the SDS1102CML+.

   Then it runs this firmware's app for a minute and a half. The FPGA images last until the power
   goes off.

## Documentation

The engineering model is the documentation: requirements, architecture, behaviour and design
decisions are in [`model/`](model/) (start with `requirements.yml` and `decisions.yml`). The
behavioural specifications the implementation was written from are in [`specs/`](specs/).

## Layout

| Path | What it is |
|---|---|
| [`app/`](app/README.md) | The scope application (Go, ARMv7): acquisition engine, triggers, decoders, LCD, front panel, web UI, SCPI/VXI-11. Embeds the nine FPGA images. |
| [`fpga/`](fpga/README.md) | The FPGA images (Verilog, Cyclone IV EP4CE10): what is common, what each image adds, the shipping bitstreams and the build. |
| [`ota/`](ota/README.md) | The on-device agent and `otactl`: install, launch, health, A/B slots, USB stick builder. |
| `tools/deploy.sh` | Build the app and install it on a scope over the network. |
| `tools/sigrok-oracle/` | A `sigrok-cli` stand-in over libsigrokdecode's decoders, for the decoder cross-checks in CI. |
| [`model/`](model/README.md) | The engineering model (documentation). |
| [`specs/`](specs/README.md) | Behavioural specifications. |
| `docs/` | Release notes and the pictures above. |

## Build, test, deploy

```sh
make test                      # app and ota test suites
make -C app app                # the ARMv7 app with the nine FPGA images embedded
make -C ota dist               # agent + otactl, and the USB stick tree
fpga/sim.sh                    # self-checking RTL testbenches (iverilog)
tools/deploy.sh                # build the app and install it on the scope (DEV=<scope-ip>)
```

Each part's README has the details: [app](app/README.md), [fpga](fpga/README.md) (the image build
with Quartus), [ota](ota/README.md) (the stick and `otactl`). CI (`.github/workflows/ci.yml`) runs
vet and the tests of both modules, golangci-lint (`.golangci.yml`) and govulncheck, the browser and
JS-parity tests with Playwright, the sigrok cross-checks of the decoders, the RTL testbenches and
the images' top-level simulations, the engineering model's validation and the ARMv7 cross-build. A
`v*` tag builds the release (`.github/workflows/release.yml`).

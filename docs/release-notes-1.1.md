# open-sds1000cml 1.1

Open replacement firmware for the Siglent SDS1000CML+ two-channel oscilloscope, developed and
validated on the SDS1102CML+. It runs from a USB stick in place of the factory application and the
FPGA images that application loads. Nothing is written to the scope's flash: take the stick out,
power-cycle, and the factory firmware is back.

> Experimental firmware with no warranty. It drives the scope's relays, DACs and acquisition bus.
> Read [SAFETY.txt](../SAFETY.txt) before installing.

## What is new since 1.0

* **52 serial protocols, each with a hardware trigger, decode and AUTO.** Forty-two join the first
  ten: LIN, DMX512, MIDI, Modbus RTU, 1-Wire, WS281x, DHT / AM230x, HDMI-CEC, SAE J1850 VPW, the IR
  codes (NEC, Sony SIRC, RC-5, RC-6, and Samsung, JVC, NEC16/42, Kaseikyo, Matsushita, Denon), DALI,
  DSI, USB Power Delivery, S/PDIF, PS/2, SWD, MDIO, Wiegand, USB full speed, quadrature encoders,
  stepper STEP/DIR, PWM, PT2262 / EV1527 remote codes, Oregon Scientific OOK, ISO 14443 Miller,
  Morse, DCF77, EM4100, EM4305, T55xx, STM8 SWIM, SDQ, PJON, Qi, SD card (SD mode), AVR PDI, cJTAG,
  Maple bus and digital calipers. Each decodes on the scope and in the web UI alike and agrees with
  sigrok's decoders. Their triggers are decided in the FPGA by one programmable block in the existing
  `qualify` image (MIDI and USB full speed use the existing UART and USB receivers): no new image.
* **AUTO from a standard trigger.** AUTO in BUS finds the bus on an edge-triggered capture, names it,
  sets the decoder and arms the protocol's frame-start trigger in the FPGA: about 4 to 9 s from
  Default setup (5.2 s on average over 60 bench buses).
* **49 bus layers:** the chip or protocol a decoded bus carries, read from its packets as sigrok's
  stacked decoders do: SPI flash, SD card, radios, network chips and sensors on SPI; EEPROMs, clocks,
  EDID and port expanders on I²C; ARM ITM / TPIU / ETM and Futaba S.BUS on UART; 1-Wire devices, CFP
  on MDIO and USB requests. The BUS sheet's Layer row picks one, or Auto names it from the packets.
* **Decoded streams for all 52 protocols**, gap-free where the protocol is slow enough for the
  continuous stream (now including DMX512, 1-Wire and Modbus to 230,400 Bd), and as back-to-back
  captures, with the share of the signal time they read, where it is not.
* **SOUND**, for analog sound design: SPECTRUM's fourth view on the LCD, its own tab on the web page
  (Alt+6) and the phone. Listen to C1, C2, stereo or A−B in the browser, through adjustable high-
  and low-pass filters; level meters in V, dBu and dBV;
  a tuner with cents and a pitch strip; V/oct tracking; envelope (ADSR) times; tremolo, vibrato and
  FM index; harmonics and THD; input against output (gain, phase, transfer curve); a pitch-locked
  cycle view. STOP freezes a take of up to 15 s to scroll, zoom, measure with cursors and replay
  (also at half or quarter speed); SINGLE and NORMAL capture a take around a trigger the FPGA marks.
* **Faster and lighter.** With no web page open, the LCD shows 45 to 53 frames a second from
  10 ns/div to 100 µs/div and 36 at 1 ms/div (16 to 24 in 1.0); the panel refreshes at 52 Hz, and
  the LCD never draws faster. With a web page open, the acquisition comes first, for the page:
  50 to 62 frames a second at 1 µs/div and faster, the LCD at 20. Measurements, decoding, the
  LCD's drawing, the web page and the phone remote do the same work in less time. With the scope
  stopped, the app uses 13 to 43 % of the CPU across the workspaces (41 to 92 % in 1.0); reading
  the full record on STOP or SINGLE takes 0.2 to 0.4 s (about 4 s in 1.0); an FPGA image switch
  takes about 1 s (the images ship compressed); AUTO in BUS takes 4.5 s on average over 60 bench
  buses, and the scope shows frames about 2 s after the app starts. During a full-record read, an
  image switch and AUTO the LCD draws only their progress; streams keep their full display.
* **The LCD and the acquisition share one core, by who is looking.** With no web page open the
  LCD is the only view and comes first: as many pictures as the core allows, a new spectrum in
  each, the acquisition slowed (never under 20 frames a second) only while the LCD falls behind
  it. With a page showing the scope the acquisition comes first, for the page, and the LCD gives
  way, never below 20 pictures a second.
* **Fixes from four bug hunts**, among them: a long session no longer slows the stopped scope down
  (memory was held and the collector ran without pause); a BUS stream of a fast SPI bus no longer
  starves the app; streams that start faulted reload their FPGA image and run; the phone's
  clockwise quick buttons were rebuilt and laid out again on every status update; AUTO in BUS
  picks a timebase whose live view decodes the bus with its trigger armed (9600 Bd Modbus was
  left at 5 ms/div, too coarse to decode); SPI sent as short transfers is named SPI, not I2C or
  SWD; while a trigger the FPGA decides waits, the LCD and the web page say so, which trigger and
  for how long, instead of "ACQUIRING"; after AUTO the BUS view decodes WS281x, Wiegand, T55xx and
  SWIM too (a whole Wiegand frame on screen, a T55xx command's end no longer cut by the trigger's
  room before it, a WS281x latch or SWIM transaction cut by the screen's edge read as cut, not as an
  error).
* **BUS trades frame rate for decoding.** Where the live view is too coarse for the bus (a slow
  timebase's envelope, or the roll view: a 115200 Bd UART at 10 ms/div), BUS reads records sampled
  just finely enough for the bus and decodes them, a record every 0.35 s at 10 ms/div and 1.5 s at
  50 ms/div.

## Install

Download `open-sds1000cml-v1.1.0-usb.zip` from the release, format a USB stick as MBR + FAT32 (one
primary partition; a GPT stick is not seen), extract the zip onto the root of the stick so that
`startup.sh`, `ota/` and `agent-slots/` are at the top, plug it into the scope and power-cycle. The
web UI is at `http://<scope-ip>:8080/`; the LCD shows the address. `SHA256SUMS` covers every file of
the release.

With the stick in, the scope runs this firmware at every boot; without it, the factory firmware.

## Update from 1.0, roll back, remove

* **Update:** extract the 1.1 zip onto the stick, replacing the files, and power-cycle. Settings
  stay: they are kept in `scope-settings.json` on the stick. Edits to `ota/agent.env` are replaced by
  the zip's, so make them again. Over the network,
  `otactl -tcp <scope-ip>:5900 update-app <app>` installs an app into the other slot.
* **Roll back:** `otactl -tcp <scope-ip>:5900 activate A` (or `B`), or the 1.0 release's zip.
* **Remove:** take the stick out and power-cycle.

`otactl` for Linux, macOS and Windows is part of the release. The version shows on the scope's SYS
settings tab (the Help key), at the end of the web UI's status line and in `/api/status`.

## Known limitations

* **Hardware:** two channels. The analog bandwidth is the model's own (100 MHz on the SDS1102CML+);
  the 20 MHz limit is the front end's. Only the SDS1102CML+ has been tested.
* **AC and GND coupling** are applied to the samples, not by the input relay: AC removes the mean of
  the trace, so a small signal on a large DC level is limited by the range that holds the DC.
* **Vertical accuracy** has not been checked against a calibrated reference.
* **Raw-only images:** the `fast`, packet and line images record raw samples only (2.1 ms at the full
  rate, no precision decimation), so while one of them is loaded the timebase stays at 200 µs/div or
  faster. Pulse width, slope and video triggers fire on full-rate captures only, with the same limit.
* **Serial triggers:** a value stop is refused, with the reason, where the trigger cannot match it (a
  value past the symbol trigger's 64-bit window, or among units of other lengths), and the symbol
  trigger has no error token, so the protocols on it stop on a value or by hand, not on an error.
  SWD has no frame leader, so a match can also land inside a data phase. CAN FD frames are decoded but
  their CRC is not checked. ARINC 429 has not been tested on a physical ARINC 429 bus.
* **Streams:** should a load of the stream image start its streams faulted, the scope reloads the
  image and the stream runs after a gap of about 1 s.
* **SOUND:** Listen plays about 0.5 to 0.7 s behind the input: the page is served over plain http,
  where the browser plays it on the page's main thread with a larger buffer.
  The distortion floor is the 8-bit converters' (about -45 to -55 dB, 0.2 to 0.5 % THD). Modulation
  readouts follow a change in the source within two 4 s windows. SOUND and the other workspaces share
  one acquisition, and the last to act holds it: while a web or phone SOUND page holds it, the LCD
  says so and AUTO (or any other key) takes it back; the page then waits for its "Take the scope"
  button.
* **SPECTRUM** takes two 8,192-point spectra for each LCD picture, about 20 pictures a second at
  1 µs/div (17 with a web page open), and from 5 µs/div it reads 6,000 to 16,000 samples a
  spectrum at about 2.5 µs a word: 10 frames a second at 100 µs/div.
* **Software-decoded streams** (the protocols past the first ten that stream gap-free, such as
  Modbus and 1-Wire) count the frame they start inside as one or more errors at the stream's start.
* **Roll:** the default Chunked roll averages each chunk, so a signal much faster than a column
  (a 1 MHz square at 20 ms/div) shows as a flat line; Roll in Stream shows its full band.
* **Converters:** about one FPGA load in a hundred leaves a converter not converting. The scope
  restarts the converters after every load and checks them every minute; one it cannot bring back
  is named in a notice on the LCD and in the web UI's status line ("ADC fault: converter E2 not
  converting - restart the scope") and in `/api/status` (`adc_fault`). Restart the scope when it
  appears.
* **SCPI identity:** `*IDN?` answers as the factory firmware does, so Siglent scripts that check it
  keep working; the firmware's own version is on the SYS tab and in `/api/status`.
* **Network security:** the web UI (8080), SCPI (VXI-11) and the agent's control port (5900) have
  no authentication. Anyone who can reach the scope can control it (the web server includes
  diagnostic pages that read and write the FPGA's registers), and port 5900 runs commands on it as
  root. Use the scope on a trusted network only. `OTA_LISTEN=127.0.0.1:5900` in
  `ota/agent.env` closes port 5900 to the network (updates then go by the stick).

## Reporting issues

Open an issue at <https://github.com/labeth/open-sds1000cml/issues> with:

* the scope model and the firmware version (the SYS tab, the web UI's status line, or `version` in
  `/api/status`);
* what you did, what you expected and what happened, with a screenshot (the web UI's PNG export, or
  `SCDP` over SCPI) where it shows the problem;
* the logs from the stick: `ota/logs/agent.log`, `ota/logs/boot.log` and `ota/logs/firstboot.log`.

## Licence

MIT (see [LICENSE](../LICENSE)). The binaries contain third-party code under its own licences, listed
in [THIRD_PARTY_LICENSES.txt](../THIRD_PARTY_LICENSES.txt).

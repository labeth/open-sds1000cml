# open-sds1000cml 1.2

Open replacement firmware for the Siglent SDS1000CML+ two-channel oscilloscope, developed and
validated on the SDS1102CML+. It runs from a USB stick in place of the factory application and the
FPGA images that application loads. Nothing is written to the scope's flash: take the stick out,
power-cycle, and the factory firmware is back.

> Experimental firmware with no warranty. It drives the scope's relays, DACs and acquisition bus.
> Read [SAFETY.txt](../SAFETY.txt) before installing.

## What is new since 1.1

* **BUS Visual: the bus drawn as what it carries.** BUS's fourth view (F2 again), on the LCD, the
  web page and the phone: MIDI as a piano with the notes falling onto its keys and sounding in the
  browser (and recorded to a `.mid` file), digital audio (S/PDIF, I2S) with its spectrum, distortion
  and noise, LIN, CAN and DCF77 as their frames and fields. Every bus the scope streams gap-free is
  followed whole; a bus streamed as captures shows what the captures hold.
* **Streams at the bus's rate.** Decoded streams run on a new digital stream image: each line sliced
  at its level in the FPGA, sixteen periods a word, at a period of any whole number of 4 ns raw
  words - down to 24 ns a period on both lines at the bus's ceiling of 2.61M words a second. The
  stream's backlog rides in the SRAM, which the FPGA now rewinds to reach, so a pause of the host
  (20 to 30 ms now and then on this kernel) drains at the bus's rate. PDI, USB PD, PWM, SDQ, Miller
  and cJTAG now stream gap-free instead of as captures (10 to 31 times the decoded units a second);
  33 protocols decode from their lines' edges, which costs the CPU far less (DMX512 88 % to 51 %).
* **A stream stopped on a value shows the event itself.** On the slower buses (DMX512, 1-Wire, LIN,
  J1850, Modbus, CEC) the FPGA keeps an analog history ring of 0.5 to 2.1 s beside the stream; the
  stop freezes it with the trigger on the decoded frame, with no image load and no wait for the value
  to come again. Faster buses keep the full-rate record of the value's next occurrence.
* **A browser can decode the stream.** A web page can take the digital stream and decode it in its
  own worker, frame for frame as the scope does; the scope keeps its own stream and gets it back when
  the page lets go.
* **Records and stacking at the bus's rate.** A record is read into the shared ring by the bus worker
  (about 11 MB/s of DMA, 8 to 9 MB/s to the screen, from 1.75 MB/s): a full record on STOP or SINGLE
  shows in 0.2 to 0.4 s from 1 µs/div to 10 ms/div. The FPGA's super-resolution stacking streams the
  record through its scan, four samples a clock: a sparse five-record stack scans in 0.7 s (8.9 s in
  1.1), and the moments download in 0.16 s (3.5 s).
* **Faster processing.** The precision filter, the record's rounding to codes, the extremes of a
  record and of each drawn column and the interleave correction run on the CPU's NEON unit (the
  precision filter in under a quarter of the time); spectrum levels, the spectrogram, the tuner and
  the aperture correction take less: SPECTRUM draws about 30 % more pictures (12 a second at
  100 µs/div), the spectrogram 11, and a full record at 1 ms/div shows on the web page in 0.7 s
  (1.2 s).
* **Triggers compare raw samples at every timebase.** The edge trigger and the qualifiers read the
  converters' raw samples even when the record is decimated: an edge must hold for a sample of every
  converter, so a level just above a flat top no longer fires on the converters' offsets.
* **AUTO** finds more buses, faster, and names fewer wrong ones: a software fuzzing campaign over
  216,000 mutated bench signals and a hardware fuzz with random relay and trigger states (105 of 105
  named right) drove its checks of each protocol's timing and content.
* **Fixes from three bug hunts and a code scan**, among them: BUS comes back on its Stream or Visual
  view after a restart or a recalled setup; a WS281x strip longer than eight LEDs no longer crashes
  the decoder; an SD card record cut inside a token decodes the tokens after it; a gap-free stream
  no longer counts the frame it starts inside as errors; `/api/record.bin` serves records finer than
  32 ns at 20 to 200 µs/div; about one stream start in a hundred no longer ends in the FPGA's FIFO
  fault (the stream's FPGA image goes straight back to its write head when a due trip finds the
  host holding both banks); typed match text keeps an escaped quote as one quote.
* **Hardening from a review of all the Go code** against the Go community's coding guidelines
  (golangci-lint now runs in CI and finds nothing in either module): the web server bounds a
  request's headers to 10 s, so a client that never finishes one no longer holds a connection; a
  SCPI (VXI-11) client writing without end no longer grows the app's memory; an oversized VXI-11
  read count or reply length can no longer crash the agent; a knob turn sent from the web page with
  a huge step count turns the right way; a failed write of the agent's takeover state no longer
  takes the bus without the watchdog; four races in the agent (uploads, restore-factory, the
  watchdog's start, the status reads) are closed.
* **COMPARE's master and mask on real signals.** The repeat test reads each capture as column
  extremes, so a probe's ringing at an edge no longer makes a steady signal read as not repeating
  (a 1 MHz square wave was refused a master), and it no longer judges the record's ends, where an
  edge on the window's edge had no partner. A master's mask is built from the live raw captures
  after it, as the MASK row builds one; the average stays REF A. A mask drawn around the average
  failed every raw capture, whose ringing the averaging had flattened.
* **One app, one path.** The app has no alternative capture modes, environment switches or
  diagnostic pages any more, and the stick carries no emergency app: what ships is the path the
  scope runs and the tests cover.

## Install

Download `open-sds1000cml-v1.2.0-usb.zip` from the release, format a USB stick as MBR + FAT32 (one
primary partition; a GPT stick is not seen), extract the zip onto the root of the stick so that
`startup.sh`, `ota/` and `agent-slots/` are at the top, plug it into the scope and power-cycle. The
web UI is at `http://<scope-ip>:8080/`; the LCD shows the address. `SHA256SUMS` covers every file of
the release.

With the stick in, the scope runs this firmware at every boot; without it, the factory firmware.

## Update from 1.1, roll back, remove

* **Update:** extract the 1.2 zip onto the stick, replacing the files, and power-cycle. Settings
  stay: they are kept in `scope-settings.json` on the stick. Edits to `ota/agent.env` are replaced by
  the zip's, so make them again. Over the network,
  `otactl -tcp <scope-ip>:5900 update-app <app>` installs an app into the other slot.
* **Roll back:** `otactl -tcp <scope-ip>:5900 activate A` (or `B`), or the 1.1 release's zip.
* **Remove:** take the stick out and power-cycle.

`otactl` for Linux, macOS and Windows is part of the release. The version shows on the scope's SYS
settings tab (the Help key), at the end of the web UI's status line and in `/api/status`.

## Known limitations

* **Hardware:** two channels. The analog bandwidth is the model's own (100 MHz on the SDS1102CML+);
  the 20 MHz limit is the front end's. Only the SDS1102CML+ has been tested.
* **AC and GND coupling** are applied to the samples, not by the input relay: AC removes the mean of
  the trace, so a small signal on a large DC level is limited by the range that holds the DC.
* **Vertical accuracy** has not been checked against a calibrated reference.
* **Box and picked decimation:** the packet, frame and line images decimate by box averages and the
  fast image by picking every 2^n-th sample, not with the precision filter (no room for it): their
  records alias what the filter would attenuate, and the Precision acquisition mode is not offered
  while one of them is loaded. The qualify image's BUS records pick samples too, while a serial
  trigger is armed. Past these timebases a bus's shortest feature spans under three of the record's
  samples and BUS asks for a faster timebase: S/PDIF past 1 ms/div, USB full speed past 200 µs/div,
  USB low speed past 5 ms/div, WS281x and SWIM past 10 ms/div; a clocked bus where its clock gets
  under three samples (a 10 MHz SPI bus past 200 µs/div, 400 kHz I2C past 20 ms/div).
* **Serial triggers:** a value stop is refused, with the reason, where the trigger cannot match it (a
  value past the symbol trigger's 64-bit window, or among units of other lengths), and the symbol
  trigger has no error token, so the protocols on it stop on a value or by hand, not on an error.
  SWD has no frame leader, so a match can also land inside a data phase. CAN FD frames are decoded but
  their CRC is not checked. ARINC 429 has not been tested on a physical ARINC 429 bus.
* **Streams at the bus's ceiling:** at the fastest digital stream (2.61M words a second) about one
  3 s stream in five hundred loses words when the host pauses for most of a second (the FPGA's ring
  and the app's together hold about 0.9 s); the loss is always reported, never silent. Slower
  streams have the same headroom for longer pauses.
* **Dense 1 MHz buses stream as captures:** SWD, MDIO, Maple and SWIM at a 1 MHz clock carry about
  two million edges a second a line, more than the single core decodes in real time, so they stream
  as back-to-back captures (about 2 % of the signal time read), each frame in them decoded whole.
* **I2C at 1 MHz** streams gap-free from the FPGA's decoded events but loses about 0.5 % of its units
  at 72,000 a second: the event FIFO holds 4 to 5 ms against the host's 20 to 30 ms pauses. The
  losses are counted and shown. 100 and 400 kHz I2C lose none.
* **Dense stacking:** on a signal with tens of thousands of crossings a record, the FPGA's stacking
  takes about 4 ms a hit after the scan (about 55 s for five records of a dense tone).
* **Streams:** should a load of the stream image start its streams faulted, the scope reloads the
  image and the stream runs after a gap of about 1 s. An FPGA image switch holds the core for its
  0.9 s: the LCD and the web page pause meanwhile.
* **SOUND:** Listen plays about 0.5 to 0.7 s behind the input: the page is served over plain http,
  where the browser plays it on the page's main thread with a larger buffer.
  The distortion floor is the 8-bit converters' (about -45 to -55 dB, 0.2 to 0.5 % THD). Modulation
  readouts follow a change in the source within two 4 s windows. SOUND and the other workspaces share
  one acquisition, and the last to act holds it: while a web or phone SOUND page holds it, the LCD
  says so and AUTO (or any other key) takes it back; the page then waits for its "Take the scope"
  button.
* **SPECTRUM** takes two 8,192-point spectra for each LCD picture, about 21 pictures a second at
  1 µs/div, and from 5 µs/div it reads 6,000 to 16,000 samples a spectrum: 12 frames a second at
  100 µs/div.
* **Roll:** the default Chunked roll averages each chunk, so a signal much faster than a column
  (a 1 MHz square at 20 ms/div) shows as a flat line; Roll in Stream shows its full band.
* **Converters:** about one FPGA load in a hundred leaves a converter not converting. The scope
  restarts the converters after every load and checks them every minute; one it cannot bring back
  is named in a notice on the LCD and in the web UI's status line ("ADC fault: converter E2 not
  converting - restart the scope") and in `/api/status` (`adc_fault`). Restart the scope when it
  appears.
* **Acquisition worker:** rarely (about once in seventy bus changes on the bench) the acquisition
  worker stops answering while a stream starts; the agent sees the health signal go stale and
  restarts the app, which is back with its settings within seconds.
* **SCPI identity:** `*IDN?` answers as the factory firmware does, so Siglent scripts that check it
  keep working; the firmware's own version is on the SYS tab and in `/api/status`.
* **Network security:** the web UI (8080), SCPI (VXI-11) and the agent's control port (5900) have
  no authentication. Anyone who can reach the scope can control it, a web page open in a browser
  on the same network included, and port 5900 runs commands on it as root. Use the scope on a trusted network only. `OTA_LISTEN=127.0.0.1:5900` in
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

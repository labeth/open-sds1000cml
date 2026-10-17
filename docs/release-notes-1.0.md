# open-sds1000cml 1.0

Open replacement firmware for the Siglent SDS1000CML+ two-channel oscilloscope, developed and
validated on the SDS1102CML+. It runs from a USB stick in place of the factory application and the
FPGA images that application loads. Nothing is written to the scope's flash: take the stick out,
power-cycle, and the factory firmware is back.

> Experimental firmware with no warranty. It drives the scope's relays, DACs and acquisition bus.
> Read [SAFETY.txt](../SAFETY.txt) before installing.

## What it does

* **Acquisition:** both channels at 500 MS/s into a 1,048,576-sample record on every acquisition,
  16 to 24 live frames a second from 1 µs/div to 1 ms/div, the full record on STOP or SINGLE.
  Calibrated five-way interleaving, a live min/max envelope, normal, average, ERES and precision
  (16-bit, CIC-decimated) modes, roll mode and gap-free streaming roll.
* **Triggers decided in the FPGA:** edge, pulse width, slope, video and zone triggers, and hardware
  triggers on ten serial protocols (UART, I²C, SPI, CAN / CAN FD, FlexRay, ARINC 429, MIL-STD-1553,
  SENT, Manchester, USB low-speed), with masks, wildcards, sequences of up to 32 events and trigger on
  error. The app places the trigger point exactly on the event.
* **Protocol decoding** of all ten protocols, continuous and gap-free in the BUS stream view, with
  automatic bus detection. The `fast` FPGA image decodes SPI to 104 MHz, UART to 62.5 MBd and I²C in
  high-speed mode from the converters' samples, with an SPI data delay read from the data's eye.
* **Analysis:** 18 automatic measurements with statistics, cursors that stay on the signal, math,
  FFT with peaks, harmonics and THD, spectrogram, Bode plot, eye diagram and jitter, mask testing,
  reference waveforms, super-resolution with FPGA stacking, and AUTO that frames the signal for the
  workspace in use.
* **Three ways to use it:** the scope's own LCD and front panel (five task workspaces on F1 to F5,
  full-screen settings sheets), a WebGL web UI on port 8080, and a phone remote at `/r`. SCPI over
  VXI-11 with Siglent short-form commands, `WF?` and `SCDP`. Export to PNG, CSV, sigrok `.sr`, VCD
  and WAV.
* **Safe updates:** an on-scope agent keeps two app slots and goes back to the previous app when a
  new one fails its health check.

The [README](../README.md) describes each function with screenshots.

## Install

Download `open-sds1000cml-v1.0.0-usb.zip` from the release, format a USB stick as MBR + FAT32 (one
primary partition; a GPT stick is not seen), extract the zip onto the root of the stick so that
`startup.sh`, `ota/` and `agent-slots/` are at the top, plug it into the scope and power-cycle. The
web UI is at `http://<scope-ip>:8080/`; the LCD shows the address. `SHA256SUMS` covers every file of
the release.

To boot the stick without taking over the scope, set `OTA_AUTO_TAKEOVER=0` in `ota/agent.env` on
the stick and delete `ota/state.json` from it if it is there: once the stick has taken over a scope,
that file makes it take over at every boot.

## Update, roll back, remove

* **Update:** extract a newer release's zip onto the stick, replacing the files, and power-cycle.
  Settings stay: they are kept in `scope-settings.json` on the stick. Edits to `ota/agent.env` are
  replaced by the zip's, so make them again. Over the network,
  `otactl -tcp <scope-ip>:5900 update-app <app>` installs an app into the other slot.
* **Roll back:** `otactl -tcp <scope-ip>:5900 activate A` (or `B`), or the earlier release's zip.
* **Remove:** take the stick out and power-cycle.

`otactl` for Linux, macOS and Windows is part of the release. The version shows on the scope's SYS
settings tab (the Help key), at the end of the web UI's status line and in `/api/status`.

## Known limitations

* **Hardware:** two channels. The analog bandwidth is the model's own (100 MHz on the SDS1102CML+);
  the 20 MHz limit is the front end's. Only the SDS1102CML+ has been tested.
* **AC and GND coupling** are applied to the samples, not by the input relay: AC removes the mean of
  the trace, so a small signal on a large DC level is limited by the range that holds the DC.
* **Vertical accuracy** has not been checked against a calibrated reference.
* **Image switches:** the scope loads the FPGA image a function needs; a switch takes about 4 s,
  and AUTO in BUS on a packet or line-code bus 11 to 15 s, as it fits the bus on the general image
  first. The `fast`, packet and line images record raw samples only (2.1 ms at the full rate, no
  precision decimation), so while one of them is loaded the timebase stays at 200 µs/div or faster.
* **Pulse width, slope and video triggers** fire on full-rate captures only, so with one of them the
  timebase stays at 200 µs/div or faster, and Average, ERES and Precision update slowly (Average at
  200 µs/div about 0.3 captures a second).
* **Full record:** reading the 1 M-sample record on STOP or SINGLE takes about 4 s.
* **Streaming:** gap-free roll is loss-free to about 978,000 samples a second per channel
  (decimation ×512, 3.9 MB/s); the SRAM covers a stall of the ARM of about 0.5 s at ×512 and 1 s at
  ×1024. A longer stall is reported as a gap. Streaming decode kept a 921,600-baud UART for
  3 minutes without loss; at higher rates a lost event is marked.
* **Protocols:** CAN FD frames are decoded but their CRC is not checked. ARINC 429 has not been
  tested on a physical ARINC 429 bus.
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

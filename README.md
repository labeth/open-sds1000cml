# open-sds1000cml

Clean-room replacement firmware for the **Siglent SDS1000CML+** series 2-channel
digital storage oscilloscope (developed and validated on the **SDS1102CML+**).

It replaces the vendor scope application with an open, from-scratch implementation
that drives the instrument's real hardware — acquisition engine, 1 ns–50 s/div
timebase, triggering, vertical analog front end, LCD, and front panel — to
oscilloscope-grade behaviour, and adds a live **web UI** and a **SCPI/VXI-11**
host interface on top.

Everything here was written clean-room from behavioural specifications
([`specs/`](specs/)) — no vendor firmware code. The specs describe *what the
hardware does and how the firmware must behave*; the implementation in
[`app/`](app/) is built from them and validated on a real unit.

> ⚠️ **Safety / use at your own risk.** This is experimental firmware that takes
> over a mains-powered instrument and drives its real relays, gain/offset DACs and
> memory-mapped acquisition bus. A bug can wedge the acquisition engine or leave the
> front end in an unexpected state. Recovery is normally automatic (the on-device
> supervisor relaunches the app) or a takeover-revert; the last resort is a mains
> power-cycle. It is written for the **SDS1000CML+ series only** — do not run it on
> other hardware. There is **no warranty** (see [LICENSE](LICENSE)); you are
> responsible for your instrument.

![The web UI driving a real SDS1102CML+](docs/images/host-yt.png)
<sub>The web UI (Y-T mode, cursors on, live auto-measurements) driving a real SDS1102CML+.</sub>

## Highlights

- **Full acquisition engine** — single-owner GPMC design (one goroutine owns the
  inherited `/dev/Gpmc` fd), per-frame arm → capture-halt → drain → re-arm, triple-
  buffered publish, wedge-recovery ladder.
- **33-detent timebase** 1 ns – 50 s/div: native-fast, decimated, slow-envelope,
  roll, and opt-in equivalent-time sampling (ETS).
- **Triggering** — EDGE with software slope validation, plus PULSE / SLOPE / VIDEO
  qualifiers, and **trigger holdoff**.
- **Acquisition modes** — NORMAL, AVERAGE, ERES, PEAK.
- **Vertical front end** — 12-detent V/div ladder (SPI relay + gain DAC),
  calibrated offset DAC, **probe attenuation (×1/×10/×100)**, and
  **AC / DC / GND coupling** (software-modelled — see the design note below).
- **Measurements** — a shared measurement core (`internal/measure`) computes
  Vpp/Vmax/Vmin/Vmean/Vrms, histogram Vtop/Vbase/Vamplitude, and timing
  (frequency, period, duty, 10–90 % rise/fall, ± pulse width, over/preshoot),
  identical on the web UI and the device LCD.
- **Web UI** (`http://<device>:8080/`) — responsive canvas display, Y-T / X-Y / FFT
  (per-channel), auto-measurements, draggable time/voltage cursors, waveform
  persistence, math (C1±C2, C1×C2, FFT-carrier subtraction), **reference
  waveforms (REF A/B)**, autoset, protocol decode (UART/I²C/SPI) with auto-detect,
  a clipping indicator, and PNG / calibrated-CSV export.
- **On-device UI** — LCD renderer with a softkey menu system, a calibrated
  **on-screen MEASURE panel**, **on-screen cursors**, and per-channel
  coupling/probe pages driven from the front panel.
- **Host interface** — SCPI over VXI-11 (ONC-RPC): `*IDN?`, timebase / vertical /
  trigger control, byte-exact `Cn:WF?` waveform transfers, `SCDP` screenshot.
- **Over-the-air ops** — an on-device supervisor with A/B slots + health rollback,
  and a host controller (`otactl`) for status, file transfer, updates, factory
  takeover/revert, and (optional) Shelly mains-power control.

### Design note: coupling on this clone

On this hardware, AC coupling has no effective hardware high-pass and the GND relay
bit alone does not reliably ground the input (it needs a factory-firmware config-plane
write that is unsafe to reproduce; re-emitting the relay word from the boot state can
also collapse the other channel's gain). This firmware therefore models coupling
**in software** — AC removes the DC component, GND shows a flat ground trace, DC
passes through — which is safe, always works, and matches on both the web and the LCD.
See [`specs/06-vertical-and-analog.md`](specs/06-vertical-and-analog.md) §6.

## The host UI (web app)

Served at `http://<device>:8080/` — a responsive, single-page control surface for
the whole instrument. Y-T with direct-manipulation cursors and live measurements
(the [screenshot above](docs/images/host-yt.png)), an X-Y mode, a per-channel FFT
with clickable peak markers, waveform math and reference overlays, and one-click
protocol decode with auto-detection.

| Per-channel FFT | Protocol decode (auto-detected) |
|---|---|
| [![FFT view](docs/images/host-fft.png)](docs/images/host-fft.png) | [![Protocol decode](docs/images/host-decode.png)](docs/images/host-decode.png) |
| Spectra of a 20 kHz square — its odd-harmonic comb, with the strongest peaks tagged. | UART/I²C/SPI decode; here it auto-detected SPI (CLK=C2, DATA=C1), with per-channel FFT peak lists alongside. |

Every acquisition control lives in the footer (run/stop, trigger, timebase,
per-channel V/div · coupling · probe · offset, acquire mode + memory depth), and
PNG / calibrated-CSV export is one click away.

## On the instrument (LCD + front panel)

The firmware isn't only a web front end — it drives the scope's own 800×480 LCD and
front-panel matrix, so it's a self-contained instrument. Front-panel softkeys drive
a menu system, a calibrated on-screen MEASURE panel and on-screen cursors (both from
the *same* measurement core as the web UI), and per-channel coupling/probe pages.

| On-screen MEASURE panel | On-screen cursors | Front-panel menu |
|---|---|---|
| [![LCD MEASURE panel](docs/images/lcd-measure.png)](docs/images/lcd-measure.png) | [![LCD cursors](docs/images/lcd-cursors.png)](docs/images/lcd-cursors.png) | [![LCD channel menu](docs/images/lcd-menu.png)](docs/images/lcd-menu.png) |
| Calibrated Vpp/Vamp/Vrms/Vavg + timing, matching the web. | Time cursors with a live Δt / 1÷Δt readout. | The CHANNEL softkey page — coupling + probe per channel, with the "10×"/"AC" HUD tags. |

## Quick start

Requires Go (see [`app/go.mod`](app/go.mod)) and an **MBR-partitioned, FAT32 USB
stick** — the stock firmware runs `startup.sh` from it at boot, which is how the
agent gets in and takes over (no firmware modification). See [`ota/`](ota/) for
the full model.

```sh
# 0) On your computer, prepare the USB boot stick (MBR + FAT32) once. The helper
#    formats + populates it, or copies onto an already-mounted FAT32 stick:
sudo ota/mkstick.sh --format /dev/sdX      # DESTRUCTIVE, heavily guarded
#    edit <stick>/ota/agent.env, then move the stick to the scope and REBOOT it
#    (the firmware runs startup.sh from the stick only at boot), then confirm:
ota/checkdev.sh <device-ip>                # stick + agent + app are healthy

# 1) Build the ARM app binary (version-stamped via git):
cd app && make app                 # → app/dist/app-arm

# 2) Run the full offline test suite (no hardware needed — scripted fake bus):
make test

# 3) Stage + deploy to the device (device /tmp is read-only, so stage to the stick):
../ota/dist/otactl -tcp <device>:5900 \
  -stage /usr/bin/siglent/usr/media/U-disk0/agent-slots/staging \
  update-app dist/app-arm

# 4) One-time cutover from the factory app, then browse the UI:
../ota/dist/otactl -tcp <device>:5900 takeover
#   -> http://<device>:8080/    (or talk SCPI via any VXI-11 client)
```

Recovery: `otactl untakeover` restores the factory app; the last resort is a mains
power-cycle. See [`ota/README.md`](ota/README.md) for the full supervisor/OTA model.

## Layout

| Path | What |
|---|---|
| [`app/`](app/) | The clean-room scope application (Go, ARMv7). Engine, front end, LCD, panel, web UI, SCPI. |
| [`ota/`](ota/) | On-device supervisor (`agent`), host controller (`otactl`), and the USB boot anchor. |
| [`specs/`](specs/) | The behavioural specifications the firmware is built from. Start with `specs/README.md`. |

## Contributing

Contributions are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md). In short: keep
changes validated (the offline `make test` suite plus, where relevant, the web
end-to-end harness), match the surrounding code, and don't break the load-bearing
architecture rules (single GPMC owner, inherit-don't-open, absolute front-end writes).

## License

[MIT](LICENSE).

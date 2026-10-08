# app — the scope application

The application that runs on the scope's ARM processor (`cmd/app`), launched by
the OTA agent (`../ota`). It owns acquisition over the GPMC bus, loads the FPGA
images it embeds, and serves the LCD, the front panel, the web UI (port 8080)
and SCPI over VXI-11.

The model ([`../model/`](../model/)) documents
its requirements, behaviour and decisions.

```sh
make app-release   # dist/app-arm with ../fpga/bitstreams/*.rbf embedded
make app           # without FPGA images (verifies a preloaded image only)
go test ./...      # unit, RTL (iverilog), browser (node + Playwright) and parity tests
GOARCH=386 go test ./...   # the device is 32-bit
```

`internal/` holds the packages: `engine` (acquisition), `sramcapture` and
`bus` (FPGA access), `analog` (front end), `decode` and `decodedlines`
(protocols), `measure`, `dsp`, `superres`, `lcd`, `panel`, `web`, `scpi` and
`vxi11srv`, `settings`, `fpgaload`, `streamview`, `cal`, `diag`, `iface`,
`frames`, `buildinfo`, and `testenv` (test helpers).

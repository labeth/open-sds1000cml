# app — the scope application

The application that runs on the scope's ARM processor, module
`github.com/labeth/open-sds1000cml/app`. The OTA agent ([`../ota`](../ota/README.md)) starts it and
hands it the GPMC device, the health token's path and the stick's `ota/` directory (`SCOPE_GPMC`,
`OTA_HEALTH_PATH`, `OTA_DIR`; the contract is in the OTA README); it refuses to start without them.
It loads the FPGA images it embeds, owns acquisition over the GPMC bus, and serves the LCD, the front
panel, the web UI and phone remote (port 8080) and SCPI over VXI-11. It has one way to run: no
alternative capture modes, environment switches or diagnostic pages (ADR-ONE-APP in the
[model](../model/)).

```sh
make app                   # dist/app-arm with the nine ../fpga/bitstreams/*.rbf embedded (app-release: the same)
make test                  # go test -timeout 30m ./...: unit, RTL (iverilog), browser (node + Playwright) and parity tests
GOARCH=386 go test ./...   # the device is 32-bit
```

`make app` refuses to build without every image. To try a new FPGA build, point one image at it
(`PACKET_RBF=...`, see the Makefile). Browser and node tests skip without node and Playwright;
`CI_REQUIRE_BROWSER=1` turns the skips into failures, as in CI. Install a build on a scope with
`../tools/deploy.sh` or `otactl update-app dist/app-arm` ([OTA README](../ota/README.md)).

`cmd/app` only starts `internal/app`, which wires the parts together; the same binary started with
`--acquisition-worker` is the bus worker that owns the GPMC transfers. The other packages under
`internal/`:

| Package | Holds |
|---|---|
| `board/acq` | the acquisition HAL: planning, SRAM readout, hardware triggers, image switching, roll, stacking, converter health and panel scanning |
| `board/streams` | FPGA-decoded and software-decoded streams, their history ring and the web client decode tap, served as sessions by the core acquisition loop |
| `board`, `scopeapi` | board settings and trigger-level conversion; the scope vocabulary and timebase ladder shared by the adapters |
| `bus`, `sramcapture`, `fpgaload`, `iface` | GPMC access and the bus worker (DMA), the SRAM record protocol, FPGA configuration and the fabric register interface |
| `analog`, `cal` | the vertical front end and per-unit calibration |
| `lcd`, `panel`, `settings` | display and front-panel adapters, board-specific persisted settings |
| `webscope`, `scpi` | board extensions for the shared web server and the CML SCPI dialect |
| `buildinfo` | version stamp |

The application is a board-support package on `open-dso-core`'s acquisition loop. The core owns
instrument state, acquisition scheduling, signal processing, decoders, the UI model, rendering,
web UI and SCPI transport. `internal/app` composes that loop with this board's HAL and adapters;
there is no separate CML acquisition engine.

`open-dso-core/webclient/e2e/` drives the web UI on a live scope.
`internal/bus/dcinv/` is the D-cache invalidate kernel module the EDMA drain loads
([README](internal/bus/dcinv/README.md)).

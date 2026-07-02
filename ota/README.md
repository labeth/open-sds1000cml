# OTA — device supervisor + host controller

The remote-access and over-the-air update system for the open-sds1000cml unit. Two Go programs plus
the USB boot anchor:

| Piece | Where it runs | Role |
|---|---|---|
| `cmd/agent` | on the scope (ARMv7) | boot-tree supervisor: holds the inherited `/dev/Gpmc` + `/dev/fpga_key` fds, owns the hardware watchdog after takeover, launches/supervises the clean-room app across A/B slots with health rollback, and serves the remote control/OTA endpoint (NATS + a local TCP listener). |
| `cmd/otactl` | on your laptop (amd64) | host controller: status/probe/exec, chunked file transfer, app + agent OTA, factory takeover, and Shelly mains power control. Can run an embedded NATS broker (`otactl serve`). |
| `boot/startup.sh` | on the scope, from the USB stick | the never-OTA'd boot anchor: runs an optional `commands` file, then the agent A/B respawn loop with crash-loop revert. |

There is **no on-device shell/login service**. All control is through the agent and `otactl`; recovery
of last resort is the external mains power-cycle (`otactl power`, via the Shelly plug).

## The load-bearing rules (from specs 01 / 09)

- **Inherited fds only.** `/dev/Gpmc` and `/dev/fpga_key` are opened once by the boot chain and inherited
  down `startup.sh → agent → app`. A fresh `open()` faults; closing the inherited fd frees the FPGA chip
  select for the whole tree. The agent keeps them as **raw ints**, never wrapped in an `*os.File`, so no
  finalizer/Close path can touch them.
- **The agent is the permanent fd holder; the app is a tenant.** Each app launch (crash restart or OTA)
  inherits the same live fds — that is why a wedge is recoverable by relaunching the app without a reboot.
- **The agent never drives the GPMC bus while the app runs.** The only bus reads it does are during
  takeover: plain, always-complete registers (version `0x12`, fill `0x46`, status `0x38`), read while the
  factory app is still alive post-STOP, to confirm the idle landing. It never writes the bus and never
  reads a sample port.
- **Watchdog is the agent's, for the device's whole life.** After the factory kill nothing else pets
  `/dev/watchdog`; an unserviced watchdog warm-resets the SoC in ~60 s and drops USB hotplug (→ physical
  power-cycle). Clean stop disarms with the magic byte `'V'`.
- **Takeover lands at idle, never mid-frame.** `takeover` drives the factory app to `STOP` over its own
  VXI-11 SCPI, confirms `0x12==0x0052` + a frozen `0x46`, persists `taken_over=true` **before** the kill
  (so a crashed successor re-acquires the watchdog immediately), then SIGKILLs the factory tree.

## Quick start

```sh
# on the laptop: run a broker (optional — TCP works without it)
otactl serve

# talk to the unit directly over TCP (agent's OTA_LISTEN, default :5900)
otactl -tcp 192.168.1.209:5900 status
otactl -tcp 192.168.1.209:5900 probe --gpmc      # read-only device fingerprint
otactl -tcp 192.168.1.209:5900 takeover --dry-run
otactl -tcp 192.168.1.209:5900 takeover          # inherit-then-kill the factory app
otactl -tcp 192.168.1.209:5900 update-app  ./app-arm
otactl -tcp 192.168.1.209:5900 update-agent ./agent-arm

# hard power-cycle if the unit wedges (only real recovery from a bus wedge)
otactl power -shelly 192.168.1.223 cycle
```

## Build

```sh
# device agent — static ARMv7
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build ./cmd/agent
# host controller
go build ./cmd/otactl
```

`make -C ota dist` cross-compiles both and lays out the USB stick tree under `dist/`.

## USB stick layout

```
<stick>/
  startup.sh              boot anchor (never OTA'd)
  commands                optional root-exec fallback run at boot
  ota/
    agent.A  agent.B      A/B agent binaries
    agent.active          pointer: A|B
    agent.confirmed       last agent slot that ran stably
    agent.env             per-deployment env (OTA_NATS=, OTA_DEVICE_ID=, …)
    logs/                 boot.log, agent.log (size-capped)
  agent-slots/
    A/app  B/app          A/B clean-room app binaries
    emergency/app         known-good backstop
    active  confirmed      app slot pointers
    staging/              upload staging
```

## Env contract (set in `ota/agent.env`, exported by `startup.sh`)

`OTA_NATS`, `OTA_DEVICE_ID`, `OTA_LISTEN` (default `:5900`), `OTA_HEALTH_DIR` (default `/dev`),
`OTA_WD_PET`, `OTA_STABLE`, `OTA_MAXFAILS`, `OTA_APP_GRACE`, `OTA_HEALTH_TIMEOUT`,
`OTA_AUTO_TAKEOVER` (set to auto-take-over after boot), `OTA_FACTORY_NAMES` (extra process-name hints).
The agent derives all paths from `OTA_DIR` / `OTA_SLOT_ROOT`.

## Tests

`go test ./...` covers the boot anchor (real `startup.sh` driven with stub agents: respawn, confirm,
A/B self-update activation, crash-loop revert, commands file), the slot store, `/proc` fd discovery,
the GPMC ioctl encoding, the VXI-11 framing (send/query against a fake instrument), the health-file
contract, and RPC dispatch (panic containment + the takeover fd-gate).

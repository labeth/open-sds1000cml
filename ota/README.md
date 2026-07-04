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

## The app ↔ OTA contract (build the app against THIS, never touch the OTA)

The OTA agent owns everything operational — takeover, the watchdog, A/B slots,
health rollback, remote transport. The clean-room app is a plain binary that
implements only this surface (see `cmd/stubapp`, the reference/validation app):

1. **It is `exec`'d by the agent as a direct child**, so it *inherits* the boot
   fds. Discover them by scanning `/proc/self/fd` for `/dev/Gpmc` and
   `/dev/fpga_key`; **never fresh-open them, never close them** (spec 01 §5).
2. **Env the agent exports**: `OTA_HEALTH_PATH` (health token to write),
   `SCOPE_GPMC` (`/dev/Gpmc`), `SCOPE_LCD` (`/dev/fb0`), `SCOPE_MMAP_DRAIN`.
3. **Health = frame-advance liveness**: after ≥3 genuine coherent frames,
   re-write `OTA_HEALTH_PATH` whenever the engine advances (throttle ~500 ms).
   Do **not** write it before the first real capture. The agent marks the app
   healthy on the first change and unhealthy → relaunch if it stalls > ~3 s.
4. **Exit cleanly (0) on SIGTERM** so the agent's stop path is clean.

That's the whole contract. The app needs zero knowledge of takeover, the
watchdog, slots, or rollback. To ship a new app: `otactl update-app ./app-arm`
(stages it into the inactive slot; a stable run confirms it, a crash-loop rolls
back). The takeover cutover is `otactl takeover` — no app change required.

**Validated end-to-end on the real unit (SDS1102CML+):** takeover drove the
factory `SDS1000_arm.app` to STOP over VXI-11, confirmed the idle landing
(`version=0x0052`, fill `0x46` frozen, `status 0x38≈0x8a`), killed it, claimed
and pet `/dev/watchdog`, and launched the reference app — which reported
`inherited /dev/Gpmc fd=5 /dev/fpga_key fd=6` and went healthy. `untakeover`
released control; a power-cycle restored the factory app. Agent self-update over
the network (`update-agent`) was validated in the same run.

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

## Preparing the USB boot stick

**How takeover starts:** the stock SDS1000CML+ firmware, at boot, mounts a USB
stick and **runs `startup.sh` from its root** (the vendor's own script hook).
That is our entire entry point — `startup.sh` launches the agent, which then
takes over the instrument. No firmware modification, no on-device login.

`mkstick.sh` makes a **ready-to-run** stick: it pre-loads the clean-room app into
both app slots plus a known-good emergency backstop, and ships `agent.env` with
auto-takeover ON — so a fresh stick boots **straight into the working scope + web
UI**, no network deploy required. (You still update the app later over the network
with `otactl update-app`.)

For the firmware to see the stick it **must** be:

- **MBR-partitioned** (msdos/DOS partition table — **not GPT**);
- a **single primary FAT32 partition**, type `0x0c` (W95 FAT32 LBA);
- that partition is what appears as **`/dev/sda1`**, mounted at
  `/usr/bin/siglent/usr/media/U-disk0`, on the instrument.

The stick is prepared **on your computer**, then physically moved to the scope,
which reads it **only at boot** — so the last step is a reboot:

**1. On your computer** — format + populate the stick (the helper runs `make dist`
and writes the tree to the stick):

```sh
# A) format a blank stick correctly (MBR + FAT32) and populate it, in one go:
sudo ota/mkstick.sh --format /dev/sdX      # DESTRUCTIVE; guards: removable + size + type-to-confirm

# B) or copy the tree onto an already-mounted FAT32 stick:
ota/mkstick.sh /run/media/you/OPENSDS
ota/mkstick.sh --verify /run/media/you/OPENSDS   # just check the layout

# then edit  <stick>/ota/agent.env  (device id, OTA_LISTEN, OTA_NATS, takeover policy)
```

**2. Move the stick to the scope** and **reboot it** (power-cycle) — the firmware
runs `startup.sh` from the stick as it comes up. A stick inserted into an
already-running scope does nothing until the next boot.

**3. Back on your computer** — confirm the chain came up (agent reachable, stick
mounted FAT32, boot.log reached the agent, app serving):

```sh
ota/checkdev.sh <ip>        # exits non-zero if any check fails
```

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

`mkstick.sh` populates the `agent-slots/` app binaries + pointers for you; plain
`make dist` only lays out the empty slot dirs (the app is pushed later via OTA).

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

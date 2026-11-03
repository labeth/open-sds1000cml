# OTA — device supervisor + host controller

The remote-access and over-the-air update system for the scope, module
`github.com/labeth/open-sds1000cml/ota`: two Go programs and the USB boot anchor. Installing a release
on a scope, updating and removing it are in the top [README](../README.md#install-update-and-remove);
this file is for working on the OTA system and for every `otactl` command.

| Piece | Where it runs | Role |
|---|---|---|
| `cmd/agent` | on the scope (ARMv7) | boot-tree supervisor: holds the inherited `/dev/Gpmc` + `/dev/fpga_key` fds, owns the hardware watchdog after takeover, launches and supervises the app across A/B slots with health rollback, and serves the remote control/OTA endpoint (a local TCP listener, and NATS when configured). |
| `cmd/otactl` | on your computer | host controller: status/probe/exec, logs, chunked file transfer, app + agent OTA, factory takeover, raw SCPI, and Shelly mains power control. Can run an embedded NATS broker (`otactl serve`). |
| `boot/startup.sh` | on the scope, from the USB stick | the never-OTA'd boot anchor: runs the stick's `commands` file, then the agent A/B respawn loop with crash-loop revert. |

There is **no on-device shell/login service**. All control is through the agent and `otactl`; recovery
of last resort is a mains power-cycle.

## The app ↔ OTA contract

The OTA agent owns everything operational — takeover, the watchdog, A/B slots,
health rollback, remote transport. The app ([`../app`](../app/README.md)) is a
plain binary that implements only this surface:

1. **It is `exec`'d by the agent as a direct child**, so it *inherits* the boot
   fds. It finds them by scanning `/proc/self/fd` for `/dev/Gpmc` and
   `/dev/fpga_key`; it **never fresh-opens them and never closes them** (spec 01 §5).
2. **Env**: the agent exports `OTA_HEALTH_PATH` (the health token to write) and
   `SCOPE_GPMC` (`/dev/Gpmc`); `startup.sh` exports `OTA_DIR` (the stick's
   `ota/` directory, where the app keeps its settings). The app refuses to start
   without all three.
3. **Health = frame-advance liveness**: after ≥3 coherent frames, the app
   re-writes `OTA_HEALTH_PATH` whenever the engine advances (throttled to about
   500 ms). It does **not** write it before the first real capture. The agent
   marks the app healthy on the first change and relaunches it if it stalls past
   `OTA_HEALTH_TIMEOUT`.
4. **Exit cleanly (0) on SIGTERM** so the agent's stop path is clean.

The app needs no knowledge of takeover, the watchdog, slots, or rollback.
`otactl update-app <app>` stages a new app into the inactive slot; a stable run
(`OTA_STABLE`) confirms it, a crash-loop (`OTA_MAXFAILS` failures) rolls back to
the confirmed slot. When the confirmed slot itself crash-loops the agent keeps
retrying it and stays reachable for an update.

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
  VXI-11 SCPI, confirms `0x12==0x0052` + a frozen `0x46`, persists `taken_over=true` in `ota/state.json`
  **before** the kill (so a crashed successor re-acquires the watchdog immediately), then SIGKILLs the
  factory tree.

## otactl

```sh
otactl -tcp <scope-ip>:5900 status                 # device, app, slots, watchdog
otactl -tcp <scope-ip>:5900 logs agent             # or boot
otactl -tcp <scope-ip>:5900 probe --gpmc           # read-only device fingerprint
otactl -tcp <scope-ip>:5900 takeover --dry-run
otactl -tcp <scope-ip>:5900 takeover               # inherit-then-kill the factory app
otactl -tcp <scope-ip>:5900 update-app  ./app-arm  # into the inactive slot
otactl -tcp <scope-ip>:5900 activate A             # or B: run that slot
otactl -tcp <scope-ip>:5900 update-agent ./agent-arm
otactl -tcp <scope-ip>:5900 scpi '*IDN?'           # raw VXI-11 SCPI to the instrument
otactl serve                                       # embedded NATS broker (optional; TCP needs none)
otactl power -shelly <plug-ip> cycle               # mains power-cycle through a Shelly plug
```

`otactl` with no command lists them all (`exec`, `sh`, `put`, `get`, `app start|stop|restart`,
`untakeover`, `reboot`, `discover`, `watch`, `version`) and the global flags
(`-tcp`, `-nats`/`-device`, `-shelly`, `-stage`, `-timeout`). Uploads stage on the stick
(`agent-slots/staging`), as the scope's root file system, `/tmp` included, is read-only.

## Build

```sh
make -C ota agent    # dist/agent-arm (static ARMv7)
make -C ota otactl   # dist/otactl for this computer
make -C ota dist     # both, and the stick tree under dist/stick (agent slots, no app)
make -C ota diag-zip # the diagnostics stick zip (sdsdiag + diagstick/), see below
make -C ota test
```

## The diagnostics stick

`cmd/sdsdiag` (`internal/diag`) with `diagstick/` is a stick of its own for other SDS1000 variants,
never on this firmware's stick (ADR-DIAG-STICK). The factory firmware runs its `startup.sh`, which
starts `sds-diag/sdsdiag` in the background 20 s after boot; it writes one
`sds-diag-<mac>-<mode>-<n>.tar.gz`, a summary and `sds-diag-status.txt` to the stick's root.

- **Passive** (`sds-diag/MODE` = `passive`, the default) only reads: `/proc`, the boot log, sysfs and
  debugfs (pin mux, GPIO), `/dev`, every process with its descriptors and maps, the factory firmware
  tree (files up to 1 MiB whole, larger ones by their head and SHA-256), the FPGA's plain registers
  through the inherited `/dev/Gpmc`, the factory app's settings over VXI-11, and the screen.
- **Active** (`MODE` = `active`) adds the SoC's pad, GPMC and clock configuration through read-only
  `/dev/mem` mappings (each region in a child process: a gated block's bus error ends only that child)
  and a ptrace trace of the factory app's device syscalls (`/dev/Gpmc` reads and writes decoded,
  opens, mmap2, SPI/I2C reads and writes) while the stick steps the factory app through every V/div,
  coupling, offset, bandwidth limit, timebase, trigger and acquisition setting over SCPI; it then
  sends the saved settings back. The tracer is a child process: when it exits, the kernel detaches
  and resumes the factory app.

## Making a boot stick

The stock firmware, at boot, mounts a USB stick and **runs `startup.sh` from its root** (the vendor's
own script hook). That is the entire entry point: `startup.sh` launches the agent, which then takes
over the instrument. No firmware modification, no on-device login. The firmware reads the stick only
at boot, and sees it only as **MBR** with a **single primary FAT32 partition** (type `0x0c`), which
comes up as `/dev/sda1`, mounted at `/usr/bin/siglent/usr/media/U-disk0`.

`mkstick.sh` builds the agent and the app and makes a ready-to-run stick: the app in both app slots and
`agent.env` with auto-takeover on, so a fresh stick boots straight into the scope and its web UI.

```sh
sudo ota/mkstick.sh --format /dev/sdX            # DESTRUCTIVE: MBR + FAT32, then populate; guards: removable + size + type-to-confirm
ota/mkstick.sh /run/media/you/OPENSDS            # populate an already-mounted FAT32 stick
ota/mkstick.sh --verify /run/media/you/OPENSDS   # check a stick's layout
ota/mkstick.sh --zip [out.zip]                   # the release zip (what release.yml publishes)
```

Edit `<stick>/ota/agent.env` if needed, move the stick to the scope and power-cycle it. Then
`ota/checkdev.sh <ip>` confirms the chain came up (agent reachable, stick mounted FAT32, boot.log
reached the agent, app serving); it exits non-zero if any check fails.

## USB stick layout

```
<stick>/
  startup.sh              boot anchor (never OTA'd)
  commands                read-only first-boot diagnostic, run before the agent (logs/firstboot.log)
  LICENSE.txt  SAFETY.txt  THIRD_PARTY_LICENSES.txt
  ota/
    agent.A  agent.B      A/B agent binaries
    agent.active          pointer: A|B
    agent.confirmed       last agent slot that ran stably
    agent.env             per-deployment env (OTA_LISTEN=, OTA_AUTO_TAKEOVER=, ...)
    state.json            the agent's state (taken_over)
    scope-settings.json   the app's settings
    logs/                 boot.log, agent.log (rotated at 1 MiB), firstboot.log
  agent-slots/
    A/app  B/app          A/B app binaries
    active  confirmed     app slot pointers
    staging/              upload staging
```

## Env (set in `ota/agent.env`, exported by `startup.sh`)

`boot/agent.env.example` documents the common ones:

- transport: `OTA_LISTEN` (default `:5900`), `OTA_NATS` (empty: TCP only), `OTA_DEVICE_ID`
  (default `sds-<mac tail>`), `OTA_NKEY`, `OTA_CREDS`, `OTA_CA` (NATS auth), `OTA_HB_INTERVAL` (default 10 s);
- takeover: `OTA_AUTO_TAKEOVER` (the stick ships it on; a stick that has taken over before also remembers it
  in `ota/state.json`), `OTA_TAKEOVER_DELAY`, `OTA_FACTORY_NAMES` (extra process-name hints);
- supervision, in seconds: `OTA_WD_PET` (15, must stay under 60), `OTA_STABLE` (30), `OTA_MAXFAILS` (3),
  `OTA_APP_GRACE` (30), `OTA_HEALTH_TIMEOUT` (3), and `OTA_HEALTH_DIR` (default `/dev`, RAM-backed);
- devices and paths, normally left alone: `OTA_GPMC`, `OTA_FPGA_KEY`, `OTA_WD_DEV`, `OTA_DIR`, `OTA_SLOT_ROOT`.

## Tests

`go test ./...` covers the boot anchor (real `startup.sh` driven with stub agents: respawn, confirm,
A/B self-update activation, crash-loop revert, commands file), the slot store, `/proc` fd discovery,
the GPMC ioctl encoding, the VXI-11 framing (send/query against a fake instrument), the health-file
contract, and RPC dispatch (panic containment + the takeover fd-gate).

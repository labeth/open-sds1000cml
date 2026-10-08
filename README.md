# open-sds1000cml

Clean-room replacement firmware for the **Siglent SDS1000CML+** two-channel
oscilloscope (developed on the SDS1102CML+): an application for the scope's ARM
processor and the FPGA images it loads, plus the on-device agent that installs
and runs it.

> ⚠️ **Use at your own risk.** This firmware takes over a mains-powered
> instrument and drives its relays, DACs and acquisition bus. It is for the
> SDS1000CML+ series only. There is no warranty (see [LICENSE](LICENSE) and
> [SAFETY.txt](SAFETY.txt)).

## Documentation

The engineering model is the documentation: requirements, architecture,
behaviour and design decisions are in
[`model/`](model/) (start with
`requirements.yml` and `decisions.yml`). The behavioural specifications the
implementation was written from are in [`specs/`](specs/).

## Layout

| Path | What it is |
|---|---|
| `app/` | The scope application (Go, ARMv7): acquisition engine, triggers, decoders, LCD, front panel, web UI, SCPI/VXI-11. Embeds the four FPGA images. |
| `fpga/` | The FPGA images (Verilog, Cyclone IV EP4CE10): what is common, what each image adds, the shipping bitstreams and the build. |
| `ota/` | The on-device agent and `otactl`: install, launch, health, A/B slots, USB stick builder. |
| `tools/deploy.sh` | Build the release app and install it on a scope over the network. |
| `model/` | The engineering model (documentation). |
| `specs/` | Behavioural specifications. |

## Build, test, deploy

```sh
make test                      # app and ota test suites
fpga/sim.sh                    # self-checking RTL testbenches (iverilog)
make -C app app-release        # ARMv7 app with the four FPGA images embedded
tools/deploy.sh                # build and install on the scope (DEV=192.168.1.209)
node --experimental-strip-types fpga/build.ts --image=general   # rebuild an FPGA image (Quartus 21.1 Lite)
```

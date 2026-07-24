# Owned bitstream — bench bring-up & HW-verification

Reproducible procedure for flashing the owned `acq.rbf` onto the SDS1000CML's Cyclone IV
and verifying it, plus the hardware facts this proved (2026-07). The scope is controlled
over the network with `otactl` (agent at `192.168.1.209:5900`); power via a Shelly plug at
`192.168.1.223`. JTAG boundary-scan (Atmel-ICE on **J13**, `openocd -f tools/jtag/scan.cfg`)
observes Cyclone pins independently of the bus.

> **SAFETY: always power-cycle after a flash session** — `otactl -shelly 192.168.1.223 power
> cycle`. The bitstream is volatile CRAM; a power-cycle reloads the factory image from NAND.
> A bad load only black-screens acquisition; it never writes config flash.

## Build
```
cd fpga && make bitstream          # -> fpga/standard/acq.rbf (exactly 368011 bytes)
sh fpga/standard/sim/run.sh        # offline testbenches (adcif + acq GPMC slave) — must PASS
```

## Flash (proven path — CS3 0x07 bit-bang, NOT spidev)
This unit's SPI-route capability bit (CS3 0x03 b3) is 0, so the loader **bit-bangs
DATA0/DCLK over CS3 0x07** (`tools/fpga_reload`, and the app's `fpgaload.bitbangLoader`).
```
otactl put fpga/standard/acq.rbf .../fpgaflash/acq.rbf     # (remount U-disk rw first)
otactl takeover ; otactl app stop                          # free /dev/Gpmc
otactl exec .../fpga_reload -bitrev=true .../acq.rbf        # -> "CONF_DONE asserted"
```
`-bitrev=true` is required (Cyclone IV passive-serial shifts LSB-first; the raw .rbf is MSB).

## Verify the register slave (HW-CONFIRMED WORKING)
Read/write with **factory GPMC timing** — do **NOT** `gpmc_probe relax` (its tighter
CONFIG2..6 is too fast for the combinational read path → returns 0x0000). Our fabric holds
`gpmc_wait`=1 so there is no WAIT wedge at factory timing.
```
otactl exec .../gpmc_probe rd 1 0x10     # BUILDID_LO  (matches iface build-ID low word)
otactl exec .../gpmc_probe rd 1 0x14     # BUILDID_HI
otactl exec .../gpmc_probe rd 1 0x18     # VERSION = 0x0052
otactl exec .../gpmc_probe wr 1 0x24 0x0005 ; ... rd 1 0x24   # RUN write-back = 0x0005
```
Confirmed stable + exact (build-ID `0xc2f6eb5f`): `0x10=eb5f 0x14=c2f6 0x18=0052`; writes to
RUN/DECIM/PRETRIG read back exact. **The complete GPMC slave — handshake, read, write — is
HW-verified.**

## Hardware facts this established (correcting earlier "verified" guesses)
- **GPMC address lines: only A3,A4,A5,A6,A7 (sel[6:2]) are reliably wired to the Cyclone
  AND stable.** A2 (sel[1]) floats; **A1 (sel[0], ball M2) carries a CLOCK** (M2 toggles
  ~50% at rest under JTAG — the old "A1=M2" was a clock mis-ID). The owned CS1 map is
  therefore laid out on **multiples of 4** (bit0=bit1=0) and the fabric masks bits 0/1/7,
  so decode uses only A3..A7. CS3 (the MAX V's plane) is unchanged.
- **clk = ball C2** (a real ~80 MHz clock; toggles under JTAG). ENCODE gen (adc_encode)
  drives correctly (B16 toggles), and `gpmc_wait` is held ready — both JTAG-confirmed.

## Remaining to full streaming (functional bench-tune)
1. **ENCODE ball** — the Cyclone must clock the ADCs. `adc_encode` on the B16 candidate
   drives, but the ADC data lanes stayed static (wrong ball) → sweep the clock/control
   candidates until the ADC data toggles (converting).
2. **The 4 missing ADC lane balls** (constant-MSB / via-hidden) + core grouping — board trace.
3. **De-interleave / bit-order** in `adcif.v` — tune against a known input once converting.

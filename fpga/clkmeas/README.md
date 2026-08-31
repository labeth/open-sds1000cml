# `clkmeas` — the absolute clock-measurement fabric

Owner: `fpga-specs/takeover/18-clocks-and-timebase.md` **C1.1 / C1.2 / C1.3**
(= master-plan Phase 1 step **1.4**, campaign blocker **CB-4**).

Neither reference clock on this board has ever been edge-counted. `clkmeas` counts
15 independent clock domains with 32-bit counters and an **atomic latch**, so C2
and M2 can be measured absolutely over host gates of 0.1 / 1 / 10 s.

## Build

```
./BUILD.sh                     # -> clkmeas.rbf, exactly 368,011 bytes
```

⚠ **Do NOT build this design with `go run ./cmd/buildacq -design clkmeas`.**
`buildacq` copies only `*.v` and the `` `include ``-d headers into its scratch work
dir; it does **not** copy `.sdc` files, even when the QSF names one. Measured
2026-08-20 on exactly this design:

```
Critical Warning (332012): Synopsys Design Constraints File file not found: 'clkmeas.sdc'.
Info (332105): create_clock -period 1.000 -name clk clk        <-- 1 GHz on ball C2
Info (332105): create_clock -period 1.000 -name trig_sense trig_sense
... all twelve counted balls auto-clocked at 1.000 ns ...
Critical Warning (332148): Timing requirements not met
```

That is the exact failure `standard/acq.sdc`'s own header was written to stop, and
it applies to **every** design in this tree that `make -C fpga bitstream` builds.
`BUILD.sh` runs the same map → fit → sta → asm → cpf flow *with* the SDC and
aborts if Quartus ever reports `332012`.

Never run two Quartus flows at once; `BUILD.sh` refuses to start a second one.

⚠ `BUILD.sh` writes `clkmeas/clkmeas.rbf` only. It does **not** touch
`fpga/standard/acq.rbf`, the shared gitignored deploy slot with two other writers.

## Loading it

Volatile SRAM configuration only:

```
fpga_reload clkmeas.rbf          # NEVER pass a flash option; a mains cycle must undo this
```

The container is native Quartus bit order (`0x20..0x28 = 6a f7 f7 f7 f7 f7 f7 f3 fb`,
byte-identical to `standard/acq.rbf`), so step 0.1's auto-detect resolves it to
`bitrev: true — auto — native Quartus order`. Do not override with `-bitrev=false`.

Then **read CS1 `0x00` and log it**: `0xC1EA` = `clkmeas`, `0x5CA0` = `sramcap`,
anything else = neither. The `IFACE_BUILD_ID` handshake cannot tell the fabrics
apart — the `0x00` word is the only discriminator (precondition P2).

## Register map, self-checks and host protocol

All of it is in the header comment of `clkmeas.v`, which is the single source of
truth. The three things that must not be skipped:

1. **Atomicity check** (C1.1 pass predicate): with `CTRL.run = 0`, two successive
   `LATCH` writes must return identical words for every domain.
2. **Double shadow**: after one `LATCH`, `shadow B − shadow A` must be exactly `1`
   for every live domain and exactly `0` for every dead one — never anything else.
3. **IDX echo**: after writing `IDX` (`0x34`), read it back and confirm before
   reading `DATA_LO`/`DATA_HI`. A GPMC write commits ~4 `clk` after `nWE` rises but
   the read path decodes the selector combinationally, so a fast host can
   otherwise read the *previous* domain's word with no error indication.
   `0x48`–`0x54` (C2 and M2 direct) are immune and are what step 1.4 should use.

## Validity ceilings — quote these with any result

From the STA of the shipped build (Slow 1200mV 85C, per-clock Fmax):

| Domain | Counter Fmax | Ceiling that actually binds |
|---|---|---|
| `mclk_in` (M2) | 252.84 MHz | **250 MHz** — device max I/O toggle rate |
| `clk` (C2) | 180.41 MHz | 180.41 MHz |
| `trig_sense` (A12) | 134.64 MHz | 134.64 MHz (comparator rolls off ~1 MHz) |
| the ten unknown balls | 177.4 – 249.7 MHz | 250 MHz max I/O toggle rate |
| `u_pll200` c0 tap | 236.41 MHz | valid while f_M2 ≤ 189 MHz |
| `u_m2pll` c4 tap (f_M2/16) | 190.4 MHz | valid while f_M2 ≤ 3.0 GHz |

**No ball-clocked counter in this design is valid above 250 MHz**, and that is a
device I/O limit, not a logic limit — it cannot be fixed by a better fit. M2's
quoted PLL lock window runs to 260.08 MHz, so the top ~4 % of the M2 candidate
range is out of reach here. If M2 measures near 250 MHz, do not report the number:
cross-check it with the `u_m2pll` c4 tap (domain 13), whose ceiling is 16× higher.

## What it drives

`gpmc_d` (only on a CS1 read outside the panel window `0x64`/`0x68`) and
`gpmc_wait`. Nothing else. No ENCODE, no ADC controls, no SRAM pins.

## Files

| File | What |
|---|---|
| `clkmeas.v` | RTL + the full register map and host protocol |
| `clkmeas.qsf` | device, pins, weak pull-ups where the device allows them |
| `clkmeas.sdc` | per-domain clocks, async groups, GPMC access budget |
| `BUILD.sh` | the build flow that actually carries the SDC |
| `clkmeas.rbf` | the built bitstream (368,011 bytes) |

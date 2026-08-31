# [3-BUILD] Quartus compile of sramcap (acq_sram) — RESULT: SUCCESS

Toolchain: Quartus Prime Lite 21.1 (`/home/labeth/intelFPGA_lite/21.1/quartus`).
Flow: `quartus_map` -> `quartus_fit` -> `quartus_asm` -> `quartus_cpf`. Nothing committed.

## Bottom line
- **`acq_sram.rbf` = EXACTLY 368011 bytes** (matches acq.rbf / sramdump.rbf; `bitstream_compression=off`). PASS.
- 0 synthesis errors, 0 fit errors, 0 assembler errors/warnings.
- All 27 external-SRAM balls + D2 + D14 read clock + P6 status + 18 DQ + full acq pinset placed; 0 unassigned pins; 0 pin conflicts.
- build-ID `0xc2f6eb5f` and VERSION `0x0052` preserved (regs.vh/regmux.vh byte-identical to acq).
- Fmax 59.12 MHz (slow 1200mV 85C) on the single `clk` domain — comfortably above the GPMC clock and the divided SRAM sample clocks.

## Deliverables
- `/home/labeth/ws/open-sds1000cml/fpga/sramcap/acq_sram.rbf` (368011 bytes) — DROP-IN for `fpga/standard/acq.rbf`.
- `/home/labeth/ws/open-sds1000cml/fpga/sramcap/output_files/acq_sram.sof` (358700 bytes, pre-conversion).
- Fit report: `/home/labeth/ws/open-sds1000cml/fpga/sramcap/output_files/acq_sram.fit.rpt`
- Pin report: `/home/labeth/ws/open-sds1000cml/fpga/sramcap/output_files/acq_sram.pin`
- STA report: `/home/labeth/ws/open-sds1000cml/fpga/sramcap/output_files/acq_sram.sta.rpt`

## Fixes applied (QSF-only; NO RTL changes were needed)
The [2-IMPL] Verilog compiled clean (map = 0 errors). Two fit-blocking issues, both in `acq_sram.qsf`,
both about the D2 dual-purpose configuration pin (nCSO / FLASH_nCE), fixed using the proven `srambf.qsf`
pattern (the working D2-driving design):

1. **`Error (176310)/(169125)`: PIN_D2 collides with `~ALTERA_FLASH_nCE_nCSO~`.** D2 is a dedicated
   Active-Serial config pin. Added
   `set_global_assignment -name RESERVE_FLASH_NCE_AFTER_CONFIGURATION "USE AS REGULAR IO"`
   to reclaim it as the MAX-V mode lever (verbatim from srambf.qsf).
2. **`Error (169187)`: invalid feature on d2 in ACTIVE_SERIAL scheme.** The reclaimed FLASH_nCE pin
   rejects a custom `CURRENT_STRENGTH_NEW` setting. Removed the `... MINIMUM CURRENT -to d2` instance
   assignment (srambf puts zero instance assignments on its `d2_ce`; default drive is used). `sck_rd`
   (D14, an ordinary I/O) keeps its MINIMUM CURRENT setting.

Also added (cosmetic, silences a fit warning, gentle on the shared box): `NUM_PARALLEL_PROCESSORS 4`.

No `inout` DQ tri-state fix, unused-pin fix, or timing-constraint fix was required.

## Verification detail
### (1) Byte count
```
acq_sram.rbf = 368011 bytes  (expected 368011) — PASS
```
`quartus_cpf -c -o bitstream_compression=off output_files/acq_sram.sof acq_sram.rbf` — 0 errors, 0 warnings.

### (2) Pin placement (from output_files/acq_sram.pin; 0 unassigned)
| Signal group | count placed | notes |
|---|---|---|
| `sram_a[0..17]` | 18 | ADDRESS balls |
| `sram_c[0..5]`  | 6  | CONTROL balls (CS#/WE#/load) |
| `sram_k[0..2]`  | 3  | write CLOCK balls (F2/J2/K2) |
| **SRAM write balls total** | **27** | matches RE map |
| `sram_dq[0..17]` | 18 | dedicated DQ read inputs |
| shared DQ on adc_lane | 4 | A13=adc_lane[14], B12=adc_lane[2], G15=adc_lane[25], G16=adc_lane[26] (PATH A shared bus) |
| `adc_lane` | 33 | ADC data bus |
| `adc_enc` / `adc_ctl_hi` / `adc_ctl_lo` | 8 / 4 / 3 | ADC drive |
| `gpmc_d` | 16 | GPMC data (bidir) |
| `d2` | 1 | **placed at PIN_D2** (nCSO mode lever) |
| `sck_rd` | 1 | **placed at PIN_D14** (proven drain read clock) |
| `p6` | 1 | placed at PIN_P6 (MAX-V status, input) |

I/O pins used: **125 / 180 (69%)**. Logic: 2261 LE (22%), 997 registers (10%), 64 M9K RAM segments.

### (3) build-ID / VERSION
- `regs.vh:12  \`define IFACE_BUILD_ID 32'hc2f6eb5f`
- `regmux.vh:53-54  { PLANE_CS1, SEL_BUILDID_LO/HI }: rmux_rdata = IFACE_BUILD_ID_LO/HI` — read path intact.
- `regmux.vh:55  { PLANE_CS1, SEL_VERSION }: rmux_rdata = 16'h0052` — VERSION 0x0052 intact.
- regs.vh / regmux.vh copied byte-for-byte from acq; top entity is `acq`. App-facing identity unchanged.

### (4) Pin conflicts
`Fitter was successful. 0 errors`. No "multiple pins assigned" / "can't place" errors after the D2 fix.

### Timing
No SDC in the project (same as the reference acq design). STA's default 1 ns clock produces meaningless
negative slack; the real number is the Fmax table: **59.12 MHz on `clk` (slow 1200mV 85C)**, 59.12 MHz
restricted. The SRAM write/read sample clocks are `clk`/clkdiv (default clkdiv=25), i.e. low single-digit
MHz — far inside the 59 MHz envelope. If a bench needs a slower SRAM clock, the runtime `clkdiv`/`rd_clkdiv`
CS1 debug registers cover it without recompiling. No timing relaxation was required.

## Reproduce
```
cd /home/labeth/ws/open-sds1000cml/fpga/sramcap
export PATH=/home/labeth/intelFPGA_lite/21.1/quartus/bin:$PATH
quartus_map acq_sram && quartus_fit acq_sram && quartus_asm acq_sram
quartus_cpf -c -o bitstream_compression=off output_files/acq_sram.sof acq_sram.rbf
stat -c%s acq_sram.rbf   # -> 368011
```

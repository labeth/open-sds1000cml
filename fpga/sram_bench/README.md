# Volatile SRAM speed/capacity bench

This independent test image uses the factory MAX V logic and measured SRAM controls:
Cyclone K1 = observed write/read mode, G1 = transaction/address enable, K2 = clock,
and 32 DQ balls in the same arbitrary bit order as the boundary-scan proof.
It does not sample ADCs. It never programs MAX V or internal persistent storage.

The FPGA writes a full 524,288-word sequence, releases DQ, and reads/checks a full
524,288-word sequence. The 32-bit LFSR sequence is unique over the tested address
span. Writes naturally wrap the MAX V address counter, so no external address
reset or known initial pointer is needed. Clock measurement uses a free-running
FPGA counter latched by ARM requests, with timestamp uncertainty recorded.

The read checker has a configurable pipeline delay. The checker
warms the read pipeline by 16 words and verifies those early words after the
address counter wraps: every address remains checked, including the first word.
The fixed `+32` read clocks provide room for warm-up and pipeline latency.
A 16-clock constant-data write preamble establishes steady operation before the
full payload. The final payload word is held for eight idle clocks before DQ
is released. The DDR clock-output input is pipelined explicitly; both half-cycle
inputs are latched on the positive edge by the real Altera primitive.

Above 200 MHz the optional 32-word debug head is disabled, so its internal M9K
clock-period limit does not restrict the external SRAM experiment. Every payload
word still passes through the same full-depth comparator. The runner sweeps all
eight whole-clock latency settings and retains every result before independently
checking additional seeds.

## Build and offline verification

```
python3 fpga/sram_bench/build.py 200 1250
iverilog -g2012 -DBENCH_MHZ=10 -s tb -o /tmp/sram_bench_sim \
  fpga/sram_bench/sim/tb.v fpga/sram_bench/bench.v fpga/common/gpmc_slave.v
vvp /tmp/sram_bench_sim
vvp /tmp/sram_bench_sim +alias
```

Build arguments are nominal MHz and read-sampling phase in picoseconds. The PLL
uses the measured 100 MHz M2 reference. The host runner also measures the actual
output clock on each image. Quartus runs under the repository-wide lock, with
two worker threads. `audit.py` checks actual pin placement and all reported
setup/hold and minimum-pulse-width corners before the hardware runner will load an image.

**The audit certifies internal FPGA timing only.** SRAM/PCB timing is exercised on hardware,
not statically closed by the current SDC. A clean cold-board experiment is not
an across-temperature or production timing guarantee.

## Hardware runner

Build `app/cmd/srambench` for ARM into `/tmp/srambench.arm`, then:

```
python3 tools/hw/sram_speed/run.py fpga/sram_bench/out/200mhz-p1250/bench.rbf
```

The runner power-cycles the scope, verifies the factory app, suspends it, uploads
only to `/dev` tmpfs, loads Cyclone CRAM via CS3 selector 7, measures the clock,
and runs three different full-depth patterns after a complete latency sweep if needed. It never accesses CS3 selector 8.
The OTA agent retains inherited GPMC fd 5. It archives the exact binary, source,
pin/timing audit, command output, and result summaries on the host.

The runner deliberately leaves the tester loaded and the factory app suspended
for diagnosis or a longer stress run. **Do not resume the factory app against
the test image.** Mains-cycle to restore the factory FPGA configuration first.
The OTA agent should remain `taken_over=false` and `auto_takeover=false`.

ARM helper commands: `load FILE`, `status`, `measure`, `run SEED LATENCY`,
`stress SEED LATENCY PASSES`. A stress run stops at the first bad or incomplete
sweep. Register identity is `0x5b51`; revision is selector 21. Full register
mapping is in `bench.v`.

All 32 DQ lines use explicitly assigned 8 mA output drive in the current build.
Earlier archived trials used 4 mA on every line, or 8 mA on DQ12/F3 alone.
The audit verifies the actual fitted drive strengths, not just QSF requests.

The current measured results and limitations are recorded separately under
`the acq2 analysis branch/`; do not infer a speed result from a
successful compile alone.

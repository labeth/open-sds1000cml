# Burst-mode drain — investigation and verdict

**Branch: `burst-drain` (local only). Verdict: DISPROVEN — the current 5-port
round-robin single-read mmap drain is at the hardware floor for this FPGA
interface. No userspace burst path improves it.**

The drain reads the 20,480-word frozen deep record from FPGA sample ports
0x30–0x34 (5-port round-robin, hi byte C1 / lo byte C2). A CPU profile of the
running scope attributes ~48% of all CPU to it (`DrainInto` + `load16`), so it
was the last remaining lever for lower CPU / higher fps. This branch tried to
move it onto the AM335x GPMC "burst"/prefetch machinery and measured the result
on the real unit.

## What the hardware actually is (read-only GPMC controller dump)

`gpmcprobe` (read-only mmap of the GPMC config port at phys 0x50000000):

- GPMC_REVISION 0x60 → AM335x GPMC.
- **CS0** 0x08000000, 8-bit **async single** → NAND (rootfs/usr UBIFS).
- **CS1** 0x01000000, 16-bit **async single** → FPGA register plane (drain window).
- **CS2** 0x02000000, 16-bit **async single**, identical config → the kernel-named
  "FPGA burst" aperture (same async timings — *not* synchronous).
- **CS3** 0x03000000, 16-bit **async single** → CS3 trigger/config plane.
- CS4–7 disabled. **Prefetch engine idle** (CONFIG1/CONTROL/STATUS all 0).
- CS1 timing (CONFIG5=0x010d141f, GPMC_FCLK 100 MHz): **RDCYCLETIME=31 (310 ns)**,
  RDACCESSTIME=13 (data valid @130 ns). No WAIT monitoring enabled.

**No chip-select is configured for synchronous/burst reads.** Classic GPMC burst
(READTYPE=sync + READMULTIPLE) requires the FPGA fabric to implement the sync
protocol (drive GPMC_CLK, honor WAIT). The inherited vendor bitstream is
async-only and cannot be changed (bitstream generation is out of scope). So the
"burst CS" the kernel names is just a second async aperture, not a burst engine.

## The three levers, tried and measured on hardware

Both experiment knobs live behind `/api/debug/tune` (`drain_mode`, `rd_cycle`),
default OFF so the shipped behaviour is unchanged.

1. **Prefetch / EDMA fixed-address offload.** The GPMC prefetch engine (and the
   simplest EDMA use) reads one fixed address repeatedly into a FIFO — ideal for
   a NAND-style single data port, offloading the CPU entirely. Gate test
   (`drain_mode=1`, read port 0x30 for the whole record): valid_depth collapsed
   to **~4069 ≈ cols/5**. The 5-port round-robin is **structural** — port 0x30
   alone yields only every 5th sample. A fixed-address reader cannot reproduce
   the stream. **Disproven.**

2. **Tighten the async read cycle** (`rd_cycle`, rewrites CS1 RDCYCLETIME,
   revertible). RDACCESSTIME=13 said data is valid at 130 ns while the cycle runs
   to 310 ns — apparently conservative. Sweep 31→24→20→16→14 (records stayed
   full and correct at every step, so the FPGA tolerates it), drain time:

   | RDCYCLETIME | drain_ms (3 trials) |
   |---|---|
   | 31 (stock) | 17.7 / 17.8 / 18.0 |
   | 14 | 22.0 / 22.1 / 22.1 |

   Tightening the cycle makes the drain **reproducibly slower**, not faster. The
   async read cost is set by the FPGA's real port recovery time, not the
   programmed RDCYCLETIME; the stock value is already at (or below) the natural
   cycle, and shortening it only adds re-synchronisation cost. **Disproven.**

3. **EDMA with a 5-strided source descriptor** (A=2 bytes, C=5 ports wrapping)
   could reproduce the round-robin off-CPU. Not pursued: it offloads the CPU but
   not the bus time (levers 1–2 show the bus/port time is the real cost and is at
   its floor), it needs userspace programming of the shared EDMA engine plus
   DMA-buffer cache coherency, and it is blocked by the project's safety rules
   (no kernel module; never contend a shared engine that the NAND path uses). The
   only correct home for it is a kernel driver, which is out of scope here.

## Conclusion

The drain is bus/port-bound at ~310 ns/sample by the FPGA's async sample-port
interface, the 5-port round-robin forbids any fixed-address DMA offload, and no
synchronous-burst CS exists (nor could without changing the bitstream). The
current single-owner mmap round-robin drain is the right and near-optimal design
for this hardware. **The performance ceiling is the FPGA interface, not the
firmware** — the only real path to a faster drain would be a different FPGA
bitstream (a linear burst-readable sample FIFO), which this project does not own.

Experiment scaffolding (`drain_mode`, `rd_cycle`, `cmd/gpmcprobe`) is retained on
this branch as the reproducible record; it is default-off and never merged.

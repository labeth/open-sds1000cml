# The AM335x GPMC "FPGA burst" chip-select — is it real, configured, used?

**Verdict (kill-test): DISPROVEN as a usable fast path.** The "FPGA burst"
chip-select (**GPMC CS2**) is *real and is claimed by the driver* (a distinct
16 MB window with its own error string), but it is **NOT configured as a
synchronous/burst window, is NOT read via the GPMC prefetch engine, and the vendor
application never touches it.** It carries the *identical asynchronous single-read*
timing as the plain FPGA CS; the prefetch enable/reset functions are called **only
by the NAND driver**; and across every runtime capture the userspace op-byte that
would select CS2 is used **zero** times. There is **no vendor burst-drain path** to
reproduce — the vendor drains sample memory exactly the way our clone does: one
16-bit `ioctl`/`readw` per sample. "FPGA burst" is plumbing that was wired up
(CS reserved, timing programmed) but never used.

This is confirmed **twice over**: statically, from the vendor kernel + app (Part A),
and empirically, from a live GPMC controller dump on the real unit (Part B) — both
independently conclude "async-only, CS2 unused, no burst engine."

---

# Part A — Static RE of the vendor firmware

Citations: vendor kernel `work/kernel/decomp/vmlinux.bin` (ARM, load base
`0xC0008000`, disassembled with `kvdis.py`) and vendor app
`work/extracted/config_full/SDS1000_arm.app` (`vdis.py`).

## A1. AM335x GPMC controller — base and register offsets (TRM SPRUH73)

**Controller register base (physical): `0x50000000`** (size 0x1000). Present as
literal words inside the omap-gpmc init code (`grep` the word `0x50000000` in
`vmlinux.bin` → VA `0xc02eb0f6`, `0xc02eb72d`, `0xc02ecee1`, `0xc02eda05`, …).

Offsets from `0x50000000` (AM335x TRM values):

| Register | Offset |
|----------|--------|
| GPMC_REVISION | 0x000 |
| GPMC_SYSCONFIG | 0x010 |
| GPMC_SYSSTATUS | 0x014 |
| GPMC_IRQSTATUS | 0x018 |
| GPMC_IRQENABLE | 0x01C |
| GPMC_TIMEOUT_CONTROL | 0x040 |
| GPMC_ERR_ADDRESS | 0x044 |
| GPMC_ERR_TYPE | 0x048 |
| GPMC_CONFIG | 0x050 |
| GPMC_STATUS | 0x054 |
| **GPMC_CONFIG1_i** | **0x060 + 0x30·i** |
| GPMC_CONFIG2_i | 0x064 + 0x30·i |
| GPMC_CONFIG3_i | 0x068 + 0x30·i |
| GPMC_CONFIG4_i | 0x06C + 0x30·i |
| GPMC_CONFIG5_i | 0x070 + 0x30·i |
| GPMC_CONFIG6_i | 0x074 + 0x30·i |
| **GPMC_CONFIG7_i** | **0x078 + 0x30·i** |
| **GPMC_PREFETCH_CONFIG1** | **0x1E0** |
| **GPMC_PREFETCH_CONFIG2** | **0x1E4** |
| **GPMC_PREFETCH_CONTROL** | **0x1EC** |
| **GPMC_PREFETCH_STATUS** | **0x1F0** |
| GPMC_ECC_CONFIG | 0x1F4 |
| GPMC_ECC_CONTROL | 0x1F8 |
| GPMC_ECC_SIZE_CONFIG | 0x1FC |

`i` = chip-select 0..7; per-CS CONFIG block stride = 0x30. The per-CS math is
proven in `gpmc_cs_write_reg` (VA `0xc001a370`):

```
0xc001a384: add r0, r0, r0, lsl #1   ; r0 = cs*3
0xc001a38c: add r1, r1, r0, lsl #4   ; r1 = reg + cs*0x30
0xc001a390: add r1, r1, #0x60        ; r1 = reg + cs*0x30 + 0x60  (= CONFIG1_i)
0xc001a398: str r2, [r3, r1]         ; r3 = ioremap(0x50000000)
```

## A2. Which chip-selects the kernel requests, and for what

The built-in **`/dev/Gpmc`** misc driver's `open()` (VA `0xc01c1c64`) requests
**three** chip-selects, each with a distinct kernel error string (rodata VA shown):

| String VA | Text |
|-----------|------|
| `0xc03a3680` | `Failed to request GPMC mem for FPGA` |
| `0xc03a36e0` | `Failed to request GPMC mem for FPGA burst` |
| `0xc03a373c` | `Failed to request GPMC mem for CPLD` |
| `0xc03a36a8` | `gpmc_mem` (name for `request_mem_region`) |
| `0xc03a36b4/3710/3764` | `#######gpmc request_mem_region 0/1/2 failed` |

`gpmc_cs_request(cs, size, &phys)` = `bl 0xc001aa80`. Mapping from the `open()`
disassembly, with the **live physical bases from Part B's `gpmcprobe`** filled in:

| CS | Purpose | request call (`open()`) | Size | Phys base (live CONFIG7) | ioremap slot |
|----|---------|-------------------------|------|--------------------------|--------------|
| **CS1** | **FPGA** (register + sample-memory window; the drain window) | `0xc01c1e08`: `r0=1, r1=0x1000000` | **16 MB** | **`0x01000000`** | priv+0x1c |
| **CS2** | **FPGA burst** | `0xc01c1e60`: `r0=2, r1=0x1000000` | **16 MB** | **`0x02000000`** | priv+0x20 |
| **CS3** | **CPLD** (trigger/analog config plane) | `0xc01c1eb8`: `r0=3, r1=0x1000000` | **16 MB** | **`0x03000000`** | priv+0x24 |
| CS0 | NAND (board init, *not* this driver) | `0xc0027bcc`/`0xc02e0d9c`/`0xc02e2b40` | — | `0x08000000` (8-bit) | — |

Each of CS1/CS2/CS3 is requested at `0x1000000` = **16 MB**. `gpmc_cs_request`
programs **CONFIG7** itself (BASEADDRESS = `base>>24`, 16 MB granularity;
CSVALID bit 6):

```
0xc001ab78: ubfx r3, r7, #0x18, #6   ; BASEADDRESS = base>>24
0xc001ab80: orr  r3, r3, #0x40       ; CSVALID
0xc001ab90: bl   0xc001a370          ; gpmc_cs_write_reg(cs, 0x18=CONFIG7, val)
```

> The app's nominal FPGA base constant `0x20200000` is **masked to its low 16 bits
> by the driver** (`reg_word = (addr & 0xFFFF) >> 1`, §A5) — it is NOT the physical
> base. The real CS1 window is `0x01000000` (Part B), so `0x20200000` is legacy /
> cosmetic. NAND is a **separate CS** (CS0, 8-bit) set up by arch/board code, not
> by this driver.

## A3. Is `gpmc_prefetch_enable` / `gpmc_prefetch_reset` called for the burst CS?

**No — they are called ONLY by the NAND driver, never for any FPGA/burst CS.**

Addresses resolved from the kernel `__ksymtab`:
`gpmc_prefetch_enable = 0xc001a100`, `gpmc_prefetch_reset = 0xc0019e10`,
`gpmc_nand_read = 0xc001a270`, `gpmc_nand_write = 0xc001a1a0`.

Every `BL` caller (full branch scan of `vmlinux.bin`):

```
gpmc_prefetch_enable : 0xc01e6b84 0xc01e6cec 0xc01e6fe0 0xc01e71e4 0xc01e72e0 0xc01e74a8
gpmc_prefetch_reset  : 0xc01e7118 0xc01e75e0
gpmc_nand_write/read : 0xc01e76a4 / 0xc01e76dc
```

All prefetch callers sit in the **`0xc01e6xxx–0xc01e7xxx`** block — the same
function region that calls `gpmc_nand_read/write`, i.e. the **OMAP2 NAND driver**.
The `/dev/Gpmc` FPGA driver (region `0xc01c1xxx`) contains **no** branch to
`0xc001a100`/`0xc0019e10`; it only calls `gpmc_cs_request` and `gpmc_cs_write_reg`.
The prefetch engine is a NAND-only feature.

## A4. FPGA CS vs FPGA-burst CS timing (CONFIG1..6)

**All three chip-selects get the identical config — and it is asynchronous
single-read.** `open()` writes the same six values to **CS1, CS2 and CS3** (three
identical blocks at `0xc01c1ca4…`, `0xc01c1d14…`, `0xc01c1d84…`, guarded by the
one-time init flag `*(u32*)0xc048f098`):

| reg | value | note |
|-----|-------|------|
| CONFIG1 (0x00) | `0x00001001` | decoded below |
| CONFIG2 (0x04) | `0x00141400` | async CS timing (CSRDOFFTIME=20, CSWROFFTIME=20) |
| CONFIG3 (0x08) | `0x00020201` | ADV timing |
| CONFIG4 (0x0C) | `0x10041004` | OE/WE strobe timing |
| CONFIG5 (0x10) | `0x010d141f` | RDCYCLETIME=31, WRCYCLETIME=20, RDACCESSTIME=13 |
| CONFIG6 (0x14) | `0x00000f80` | bus turnaround / cycle2cycle |

**CONFIG1 = `0x00001001` — the sync-vs-async determinant:**

| field | bits | value | meaning |
|-------|------|-------|---------|
| WRAPBURST | 31 | 0 | — |
| **READMULTIPLE** | 30 | **0** | **single read (no burst)** |
| **READTYPE** | 29 | **0** | **ASYNCHRONOUS read** |
| WRITEMULTIPLE | 28 | 0 | single write |
| **WRITETYPE** | 27 | **0** | **ASYNCHRONOUS write** |
| CLKACTIVATIONTIME | 25:24 | 0 | (sync-only; unused) |
| ATTACHEDDEVICEPAGELENGTH | 23:22 | 0 | — |
| **DEVICESIZE** | 13:12 | **1** | **16-bit device** |
| DEVICETYPE | 11:10 | 0 | NOR-like (not NAND) |
| MUXADDDATA | 9:8 | 0 | non-multiplexed |
| GPMCFCLKDIVIDER | 1:0 | 1 | (sync-only) |

So **all three CSes — including "FPGA burst" CS2 — are 16-bit, non-multiplexed,
fully asynchronous, single (non-burst) read/write.** `READTYPE=0`,
`READMULTIPLE=0`, `CLKACTIVATIONTIME=0`, `WRAPBURST=0`: no synchronous/burst
configuration anywhere. The "burst" CS differs from the plain FPGA CS in nothing
but its 16 MB address window and its error string.

## A5. How the vendor APP reads the deep sample record (0x30–0x34)

**One `ioctl` per 16-bit sample — exactly like our clone. No mmap of GPMC, no
second device, no `/dev/mem`, no burst window.**

- App opens only `/dev/Gpmc` (class `CHWAccess::gpmc`), plus `/dev/fpga_key`,
  `/dev/fb0`, `/dev/mtd0..11`, `/dev/spidev1.x`, `/dev/ubi1_0`. **No `/dev/mem`
  string exists in the binary.** The only `mmap` is the framebuffer `/dev/fb0`
  (`work/ghidra/spec03-evidence.md:13`). The one "Burst" string is `Burstlänge`
  (German UI label for *burst length* measurement) — unrelated to GPMC.
- Drain = `FUN_001af1fc` (VA `0x1af1fc`, `specs/04c-channel-split-and-scaling.md:30`):
  `for n in 0..4096: for s in 0..5: word = read16(0x20200060 + s*2)` — one 16-bit
  read per sample, 20480 total. `read16` = helper `0x1b3534` → `bl 0xcc224`.
- `FUN_000cc224` (VA `0xcc224`) is the read builder: **hard-codes op = 1** and
  issues one ioctl `0x80026700`:
  ```
  0xcc25c: mov r3,#1 ; 0xcc260: strb r3,[fp,#-0x1c]  ; buf[0]=op=1 -> CS1 (FPGA)
  0xcc270: lsr r3,r3,#1                               ; reg_word=(addr&0xFFFF)>>1
  0xcc290: ldr r1,[pc,#0x28]                          ; cmd=0x80026700
  0xcc298: bl  0xcb5c                                 ; ioctl(gpmc_fd,...)
  ```
  Write builder `FUN_000cc2c4` (VA `0xcc2c4`) also sets op = 1. A second write
  builder at `0xcc074` sets **op = 3** (CPLD/CS3). **No builder sets op = 2.**
- Kernel ioctl (`0xc01c1b38`) confirms a **single `ldrh`** (no readiness poll),
  base selected by op: `base = priv[0x1c + (op-1)*4]` → op1=CS1, op2=CS2(burst),
  op3=CS3:
  ```
  0xc01c1c1c: ldr  r2, [r2, #0x1c]   ; base
  0xc01c1c24: ldrh r3, [r2, r3]      ; single 16-bit read
  ```
  Even if op=2 were sent, it is still one `ldrh` per call — no burst/prefetch on
  the ioctl path.

### Runtime proof: CS2 is never used

`/dev/Gpmc` op-byte captures (`work/*reglog*.txt`) tag each access with `csN` (the
op byte). Distribution — **cs2 is absent everywhere** (`grep -r cs2` = nothing):

| capture | cs1 (FPGA) | cs2 (burst) | cs3 (CPLD) |
|---------|-----------:|:-----------:|-----------:|
| vendor-boot | 3083 | **0** | 274 |
| ofst | 1403 | **0** | 642 |
| sweep | 2590 | **0** | 1002 |
| vendor-nativefast | 2052 | **0** | 334 |
| vendor-nativefast-notrig | 59598 | **0** | 334 |
| vendor-sim | 1192 | **0** | 334 |

Even the fast native-acquisition capture (59,598 CS1 ops) never issues a single
CS2 access. The sample drain (selectors `0x30–0x34`) goes through the op=1 read
builder → **CS1**.

---

# Part B — Live hardware experiment (clone unit, prior investigation)

*(Retained verbatim from the clone-side `burst-drain` branch. Independently reaches
the same verdict from a live GPMC controller dump — and supplies the actual
CONFIG7 physical bases folded into Part A's table.)*

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

---

## Consequences for the clone / replacement firmware

- **There is no vendor "burst drain" to match.** The vendor reads sample memory
  the same slow way we do: 20480 single-word `ioctl`/`readw`s per frame
  (`specs/04b §6.1`; `specs/04-fpga-acquisition.md:220`: *"no DMA, no bulk ioctl,
  and no `mmap`"*). Our clone is already at parity with the vendor read path.
- **CS2 "FPGA burst" is dead plumbing** — reserved, timed as async single-read,
  never addressed. A sync-burst read of CS2 as-configured would not work without
  re-programming CONFIG1..6 (sync timing) *and* the FPGA fabric — neither of which
  the vendor firmware does, and Part B measured that the bus/port time is already
  at its async floor.
- Both static RE (Part A) and live hardware (Part B) agree: **the ceiling is the
  FPGA async interface, not software.** A genuinely faster drain is new engineering
  (a different bitstream with a linear burst FIFO), not activation of an existing
  vendor capability.

### Source pointers
- `work/kernel/decomp/vmlinux.bin` — `open()` `0xc01c1c64`, ioctl `0xc01c1b38`,
  `gpmc_cs_request` `0xc001aa80`, `gpmc_cs_write_reg` `0xc001a370`,
  `gpmc_prefetch_enable` `0xc001a100`, `gpmc_prefetch_reset` `0xc0019e10`.
- `work/extracted/config_full/SDS1000_arm.app` — drain `0x1af1fc`, read16 `0x1b3534`,
  read ioctl `0xcc224`, write ioctl `0xcc2c4`, CPLD(op3) write `0xcc074`.
- `GPMC_DRIVER_RE.md`; `specs/04-fpga-acquisition.md`,
  `specs/04b-fpga-acquisition-sequence.md`, `specs/04c-channel-split-and-scaling.md`.
- `work/*reglog*.txt` — runtime op-byte captures (no `cs2` anywhere).

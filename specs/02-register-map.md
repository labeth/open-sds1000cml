# 02 — Register Map

The firmware talks to **our owned acquisition FPGA** over the OMAP **GPMC** bus through the
`/dev/Gpmc` character driver. Every acquisition, trigger, front-panel and calibration action reduces
to reads and writes of the registers our fabric exposes. The register **table itself is generated**
from the interface schema; this document is the human reference for the bus/addressing model and the
block layout, and it points at the generated map as the single source of truth.

This is a **clean replacement**, not a compatibility layer: there is no vendor factory register map,
no divisor/class/run-word opcodes, no round-robin sample ports, no re-trigger strobe, no force pulse.
The app drives **only** our fabric and refuses to drive anything else (the build-ID handshake, §4).

---

## 1. Bus & addressing model

### 1.1 Chip-select planes

The FPGA presents **two** register windows, selected by the chip-select (CS) field of the ioctl:

| Plane | Register/bus base | mmap physical base | Purpose | Access |
|---|---|---|---|---|
| **CS1** | `0x20200000` | `0x01000000` | Acquisition / read plane: capture opcode + program, status, the auto-inc BURST drain port, the streaming spine + envelope result channel | ioctl (read + write); the post-halt drain may use the mmap fast path |
| **CS3** | `0x20100000` | — (not mmapped) | Config / control plane: `CONF_DONE`, trigger-level DAC, offset DACs, LED latch | ioctl only (read + write); `CONF_DONE` is read via the CS3 ioctl config port |

A third chip-select (CS2) is allocated by the kernel bring-up but is **not used** by the acquisition
firmware; the LCD framebuffer is `/dev/fb0` (spec 07), not a GPMC plane.

**Two addressing axes are in play; do not conflate them.**

- The **register/bus-address base** is what a register's byte address is computed from:
  `byte_addr = register_base + (sel << 1)`. CS1 registers are at `0x20200000 + sel·2`, CS3 registers
  at `0x20100000 + sel·2`.
- The **mmap physical base** is the `/dev/mem` mapping address for the syscall-free drain/read fast
  path (§1.4): only CS1 maps, at `0x01000000`. CS3 is not mmapped — `CONF_DONE` is read via the
  CS3 ioctl config port, never through mmap.

A **register selector** `sel` addresses the 16-bit FPGA **word** `sel << 1` within its plane. On the
ioctl path the driver shifts `<<1` internally — pass the **selector**, never the pre-shifted byte
address. Selectors in the generated bindings and in `fpga/standard/docs/REGISTER-MAP.md` are already
**unshifted**.

### 1.2 Obtaining the `/dev/Gpmc` fd (inherited, do not open fresh)

The `/dev/Gpmc` driver enforces a **single-open** guard and performs its chip-select ioremap init
only in the boot process context. A fresh `open("/dev/Gpmc", O_RDWR)` therefore either fails with
`EPERM` while the boot holder's fd is live, or succeeds but lacks the boot-time chip-select init so
the first reads **wedge the bus for seconds**. The firmware MUST instead reuse the descriptor the
boot process already opened and passed down the exec tree:

1. Scan `/proc/self/fd`; match each symlink target against `"/dev/Gpmc"`. The matching entry's
   numeric name is the inherited fd.
2. Wrap that fd **number directly** without duplicating it (same open file description as the boot
   holder). Do **not** `dup()`.
3. **Never `Close()` it**, and clear any GC finalizer on the wrapper. Closing this fd closes the
   single-open device for the whole process tree and kills acquisition.

Every other section assumes you already hold this working inherited fd.

### 1.3 ioctl encoding

Register access uses a 6-byte ioctl struct on the inherited `/dev/Gpmc` fd:

```
b[0] = plane (chip-select): 1 = CS1, 3 = CS3      # index into the kernel's ioremap base table
b[1] = 0
b[2] = sel & 0xff                                  # low byte of selector (NOT pre-shifted)
b[3] = (sel >> 8) & 0xff
b[4] = val & 0xff                                  # 16-bit value, little-endian
b[5] = (val >> 8) & 0xff
```

ioctl request codes:

| Direction | Request code |
|---|---|
| Read  | `0x80026700` |
| Write | `0x40026701` |

**Plane 0 is illegal.** Plane 0 underflows the driver's ioremap base index and stalls the bus for
seconds; the bus layer rejects any plane that is not 1 (CS1) or 3 (CS3) *before* the syscall.

### 1.4 The mmap `O_SYNC` single-16-bit-load fast path

The post-halt record drain is the only high-rate transfer. It may bypass the ioctl by mapping the
CS1 window from `/dev/mem` opened `O_RDWR|O_SYNC` at physical base `0x01000000`, length one page.
Reads then go through a **single, un-inlined, aligned 16-bit load** (`load16`, `//go:noinline`) so
the compiler can never split, hoist, or CSE the access — an **auto-increment** port (§3, `BURST`,
`ENV_DATA`) pops exactly one word per bus transaction, so a coalesced or speculated read corrupts the
stream. Before trusting the mapping the driver re-reads `VERSION` through it (the double-shift trap):
a value other than `0x0052` means the addressing is wrong and it falls back to the ioctl drain.

### 1.5 Single-owner discipline

Every register touch happens on the **single engine-owner goroutine** (spec 01 §3, spec 03). The bus
layer does not enforce this; the engine does. Reads of an auto-increment port are **mutations** and
must never be deduplicated, speculated, or reordered.

---

## 2. The write guard is schema-derived

The bus layer permits a write **iff the schema marks the register writable** (`iface.Writable(plane,
sel)`): it is false for every read-only register (`CONF_DONE`, all status/drain/channel ports) and
for any selector the schema does not define. There are **no hand-maintained forbidden ranges** — the
guard cannot drift from the fabric because it is generated from the same schema the fabric is.

Writing `CONF_DONE` (CS3) collapses the config engine and is refused. Reads are not guarded (a read of
an undefined selector simply returns whatever the fabric drives).

---

## 3. The register table (generated — single source of truth)

The complete register table is **generated** from `codegen/ifacedef` and lives at
[`../fpga/standard/docs/REGISTER-MAP.md`](../fpga/standard/docs/REGISTER-MAP.md) (and, for the app, in
the compiled `app/internal/iface` bindings). Do **not** hand-maintain a register table here — edit the
schema and regenerate. Provenance is clean-room: this behavioral spec (and spec 03) → `ifacedef`
(schema) → {`regs.vh`, `regmux.vh`, `iface.go`, the generated map doc}.

The owned CS1 plane is organized into **blocks** by selector range; the CS3 plane carries config +
the analog front end. Each register declares its **access** (R/W/RW) and **behavioral semantics**
(normal, strobe, auto-inc-port, read-after-halt, level-status, wait-guarded), which the engine and
the bus layer honor.

| Block | Plane · range | Role |
|---|---|---|
| `meta` | CS1 `0x10..0x17` | identity: `BUILDID_LO/HI`, `VERSION` (build-ID handshake, §4) |
| `capture` | CS1 `0x20..0x2f` | `OPCODE` strobe (GO/HALT/RESET), `RUN` (mode+run), `DECIM_LO/HI`, `PRETRIG_LO/HI`, `POSTTRIG_LO/HI` |
| `drain` | CS1 `0x30..0x3f` | the single fixed auto-inc `BURST` port + `BURST_REMAIN` (words-remaining / DMA-ready) |
| `status` | CS1 `0x40..0x4f` | `STATUS_A` (VALID/TRIG/DONE level), `TRIGPOS_LO/HI` (interpolating position), `FILL` |
| `spine` | CS1 `0x50..0x5f` | `XFORM_CTRL` (transform-stage bypass), `ENV_COLS` (envelope column count) |
| `channels` | CS1 `0x60..0x7f` | result/event channels; the wired one is `envelope` (`ENV_DATA`/`ENV_COUNT`/`ENV_RESET`) |
| `trigger`/`measure`/`decode` | CS1 `0x80..0xdf` | **reserved** ranges for the v1/v2 fabric taps — no registers yet |
| `config` | CS3 `0x00..0x08` | `CONF_DONE` (nCONFIG/config-status; **never written**) |
| `frontend` | CS3 `0x09..0x3f` | `LED_*`, per-channel offset DACs (`OFF_C1/C2_*`), trigger-level DAC lanes (`LVL_A/B_*`; the hi write self-latches + loads the serializer) |

Field layouts (e.g. `STATUS_A.{VALID,TRIG,DONE}`, `RUN.{MODE,RUN}`, `TRIGPOS_HI.IDX`,
`BURST_REMAIN.{READY,REMAIN}`, `ENV_COUNT.{COUNT,OVERFLOW}`, `CONF_DONE.DONE`) and the packed channel
record codecs (`EnvelopeRecord`, …) are in the generated map / bindings — use the generated field
accessors, never hand-packed masks.

### 3.1 Behavioral semantics that constrain access

- **strobe** (`OPCODE`, `ENV_RESET`, `LVL_A/B_HI`, `LED_STROBE`): a write is an event, not a value.
- **auto-inc-port** (`BURST`, `ENV_DATA`): each read pops one word; never dedup/speculate/CSE.
- **read-after-halt** (`BURST`, `BURST_REMAIN`, `TRIGPOS_*`, `ENV_DATA`): only valid after a HALT; the
  engine reads these solely on a frozen record.
- **level-status** (`STATUS_A`, `FILL`, `BURST_REMAIN`, `ENV_COUNT`, `CONF_DONE`): a live level, not a
  sticky latch — re-read for the current value.

---

## 4. Build-ID handshake (refuse a mispaired fabric)

Because the app **generates** the bitstream from the same schema it compiles against, a mismatch is a
build error, not a negotiation. At bring-up the bus layer runs `iface.Verify`:

1. read `VERSION` (CS1 `0x12`); it must read `0x0052` (a cheap addressing sanity check);
2. read `BUILDID_LO`/`BUILDID_HI` (CS1 `0x10`/`0x11`); `(HI<<16)|LO` must equal the compiled
   `iface.BuildID` (the schema fingerprint).

Any mismatch → **refuse to drive** (no dual mode, no fallback). The engine re-checks the same
handshake at the top of its run loop so a re-flashed or wedged fabric is caught before the first arm.

# [1-INTEG] — Swapping acq's M9K capture buffer for the external SRAM

Goal recap: replace `standard/acq.rbf` with a drop-in that presents the **same** CS1/CS3
register interface, the **same** build-ID `0xc2f6eb5f`, VERSION `0x0052`, and the **same**
368011-byte `.rbf`, while the *capture medium* changes from on-chip M9K
(`capture.v: mem[0:REC_DEPTH-1]`) to the external S7A163630M SRAM written the vendor way
(ADC -> shared DQ bus -> SRAM, MAX-V sequencing the strobes). Only `capture.v` is touched;
`acq.v` / `spine.v` / `drain.v` / `adcif.v` / `envelope.v` / `regmux.vh` / `regs.vh` stay
byte-identical (one tiny read-mux tap added in `acq.v` for debug regs — see (c)).

---

## (a) capture.v's black-box boundary — the port contract to PRESERVE

`acq.v` instantiates `capture u_capture(...)`. The replacement module (`capsram.v`) must keep
**this exact port list, widths, and semantics** so `acq.v` is wired unchanged:

INPUTS (from acq/spine):
| port | width | meaning — must keep |
|---|---|---|
| `clk` | 1 | fabric ref (ball C2, ~80 MHz) |
| `arm` | 1 | `op_go` — clean single-cycle re-arm -> ST_FILL |
| `halt` | 1 | `op_halt` — manual freeze |
| `rst` | 1 | `op_reset` — -> idle/clear |
| `pre_work_w` | 16 | arm-time clamped pre depth (latch @arm) |
| `post_work_w` | 16 | arm-time clamped post depth (latch @arm) |
| `cap_word` | 16 | canonical decimated sample, hi=CH1 lo=CH2 (from spine) |
| `cap_tick` | 1 | spine `valid` — the commit strobe |
| `mode_norm` | 1 | 0=AUTO / 1=NORM |
| `trig_rise` | 1 | synchronized comparator rising edge |
| `trig_level`| 8 | CH1 level in sample units (interp) |
| `rd_addr` | `ADDR_W`=15 | drain-driven record read address |

OUTPUTS (consumed by acq/drain/envelope):
| port | width | meaning — must keep |
|---|---|---|
| `rd_data` | 16 | **registered** read data at `rd_addr` (2-cycle latency; drives `rdata_BURST`) |
| `filling` | 1 | ST_FILL — gates spine decimator (`spine.filling`) |
| `smp_valid` | 1 | committed-sample strobe — envelope fold enable |
| `r_valid` | 1 | STATUS_A.VALID |
| `r_trig` | 1 | STATUS_A.TRIG |
| `r_done` | 1 | STATUS_A.DONE |
| `coherent` | 1 | frozen coherent record present — **gates the drain** (`rdata_BURST`, drain pops) |
| `fill_out` | 11 | FILL.COUNT (freezes at halt) |
| `trig_idx` | 15 | TRIGPOS.IDX (physical index of trigger sample) |
| `trig_frac` | 16 | TRIGPOS.FRAC (Q16 sub-sample) |
| `rec_len` | 16 | frozen captured length -> drain BURST_REMAIN |
| `frame_done`| 1 | 1-cycle pulse at finalize — envelope flush |

**The write interface today** (what we re-implement over SRAM):
- Commit predicate: `wr_commit = filling && cap_tick && !post_full` (exact-post finalize).
- Atomic single-cycle write: `if (wr_commit) mem[waddr] <= cap_word;` with `waddr` advancing
  the same edge (circular, wrap at `REC_LAST`=20479).
- `wrote_count` / `post_count` bookkeeping, trigger accept (`trig_fire`), interpolation
  divider, exact finalize `post_full = triggered && (post_count == posttrig_work)` ->
  `state<=ST_HALT; coherent<=1; r_done<=1; frame_done<=1`.

**The read interface today** (what the drain uses, keep identical):
- `mem` is single-write / **registered-read** simple-dual-port: `rd_data <= mem[rd_addr];`
  in the same clocked block. Drain drives `rd_addr=burst_addr`; 2-cycle registered latency.

### The minimal-change architecture (SRAM as capture medium, M9K becomes the drain-serve buffer)

REC_DEPTH=20480 x16 = 40 KB already fits the EP4CE10 M9K, so the swap is about **emulating the
vendor capture path**, not about depth. Keep `mem[]` — but change *what fills it*:

1. **ST_FILL (capture):** the ADC drives the shared DQ bus; a new **SRAM write controller**
   drives the 27 addr/ctrl/clk balls (+ D2 lever) so the fixed MAX-V latches each committed
   sample into external SRAM. Our fabric **never drives DQ**. The existing live bookkeeping
   (`wrote_count`/`post_count`/`trig_fire`/interp/`post_full`) is unchanged and still runs off
   `cap_word`/`cap_tick` — so trig_idx/trig_frac/fill/envelope are all still correct.
   `mem[]` is **not** written during fill.
2. **post_full / halt -> new ST_DRAIN_SRAM:** run the **proven sramdump read** (drive **only**
   D14, tri-state the 27, capture DQ each D14 edge) to slurp `rec_len` words SRAM->`mem[]`
   sequentially (`waddr` reused as the load pointer). `frame_done` may still pulse at
   post_full (envelope is live, independent of the slurp).
3. **slurp done -> ST_HALT:** assert `coherent`/`r_valid`/`r_done`. From here the existing
   registered `rd_addr->rd_data` port serves the drain from `mem[]` exactly as today — so
   `drain.v` and `acq.v`'s `rdata_BURST` path are untouched.

Net: `capture.v`'s **ports are byte-identical**; only the *source* of `mem[]` (external SRAM
via a post-freeze sequential slurp instead of live cap_word) and an added FILL-time SRAM write
controller change. `coherent` is simply deferred until the slurp completes so the drain never
sees a stale record.

## (b) how drain reads + the BURST port

- `drain.v` owns only the **address + remaining count**: `burst_addr` (0..rec_len-1, saturating),
  `rdata_burst_remain = coherent ? {1'b1, remain[14:0]} : 0`. One pop per nOE-rise
  (`burst_rd_done`), advances only while `coherent`.
- `acq.v` wires `burst_addr -> capture.rd_addr` and gates the word:
  `rdata_BURST = coherent ? rec_rd_data : 0`, `rec_rd_data = capture.rd_data`.
- **Nothing in drain changes.** It reads the same registered port; `rec_len` still comes from
  capture's frozen `wrote_count`. Because we defer `coherent` until the SRAM->mem slurp finishes,
  the 2-cycle registered read semantics the drain relies on are preserved.

## (c) FREE register-spine selectors for the runtime write-tuning debug regs

Selector decode is `wr_sel = {1'b0, sel_q2[6:2], 2'b00}` -> only **multiples of 4, 0x00..0x7c**
are reachable (bit7 forced 0: the reserved 0x80/0xa0/0xc0 blocks are NOT addressable). Used CS1
selectors: 0x10,0x14,0x18,0x20,0x24,0x28,0x2c,0x30,0x34,0x38,0x3c,0x40,0x44,0x50,0x54,0x58,0x5c,
0x60,0x64,0x70,0x74,0x78.

**FREE & reachable:** `0x00 0x04 0x08 0x0c 0x1c 0x48 0x4c 0x68 0x6c 0x7c`.

Suggested allocation for the write-controller tuning (fields frozen in task [2/3], not here):
| sel | name | dir | purpose |
|---|---|---|---|
| 0x48 | DBG_WDIV | W | write-strobe clock divider (like sramdump `clkdiv`) |
| 0x4c | DBG_WPHASE | W | CS/WE assert phase + addr-advance edge select |
| 0x68 | DBG_WSTROBE | W | load-strobe **ball selection** (which of the 6 CONTROL balls is WE/ADSC) + polarity |
| 0x6c | DBG_WMISC | W | D2 level override, controller enable, manual-write test kick |
| 0x7c | DBG_STATUS | R | write-ctlr state + SRAM-slurp progress + DQ peek |

These are decoded **directly from `we_commit`/`wr_sel`/`sel` in `capsram.v`** (not in the
generated `regmux.vh`), so no generated selector case is added and the schema/build-ID is
untouched. Because the generated read-mux returns 0 for these selectors, the **only** `acq.v`
edit is the single-line read tap:
`assign gpmc_d = read_active ? (dbg_rd_hit ? dbg_rdata : rmux_rdata) : 16'hzzzz;`
(`dbg_rd_hit`/`dbg_rdata` exported by `capsram`). This adds no register the app touches and
cannot shift any existing selector, so BUILDID/VERSION reads are bit-identical.

## (d) build-ID / VERSION plumbing to preserve (do NOT touch)

- `regs.vh`: `` `IFACE_BUILD_ID 32'hc2f6eb5f `` / `` `_LO 16'heb5f `` / `` `_HI 16'hc2f6 ``.
- `regmux.vh` read-mux cases (unchanged): `SEL_BUILDID_LO -> _LO`, `SEL_BUILDID_HI -> _HI`,
  `SEL_VERSION -> 16'h0052`. These are constant literals independent of any other logic.
- The app validates the fabric by reading BUILDID_LO/HI (`0xeb5f`/`0xc2f6`) and VERSION
  (`0x0052`). As long as `regs.vh`/`regmux.vh` are byte-identical and the debug tap only *adds*
  an override on unused selectors, every app-visible read is bit-identical.
- **Keep `bitstream_compression=off` + device `EP4CE10F17C8`** -> the uncompressed frame count
  is fixed -> `.rbf` = 368011 bytes regardless of added logic.

## (e) pin budget & conflict resolution (scripted cross-check: **0 conflicts**)

Existing acq balls (unchanged): 16 gpmc_d, nCS1/nOE/nWE/gpmc_wait, clk, trig_sense, 7 sel,
8 adc_enc, 7 adc_ctl, 33 adc_lane. **New balls added:**

- **27 SRAM addr/ctrl/clk (write path, RE-decode-proven, `merged_sram_roles.json`)** — all
  currently FREE in acq, **zero conflicts**:
  - CLOCK(3): F2 J2 K2
  - CONTROL(6): L2 M6 N1 N5 R6 T5
  - ADDRESS(18): L1 N2 P1 P2 R1 J6 K5 L3 N3 N6 P3 R7 R3 R4 R5 T3 T4 T6
  - These are **tri-stateable outputs**: driven during ST_FILL; **Hi-Z during ST_DRAIN_SRAM**
    (so the MAX-V owns the address on read, per the proven non-contending pattern).
- **D14 = SRAM read clock** (proven `sramdump` `sck_pin`) — FREE, output, driven **only** during
  the drain slurp. (Note: `merged_sram_roles` flags F2/J2/K2 as the *acquisition-region* CLKs
  and D14/C14 as the ADC-sample-clock band; the PROVEN sramdump read empirically advances the
  MAX-V read counter via D14, so D14 is used for the read slurp exactly as proven. Reconciling
  D14-vs-{F2/J2/K2} roles is deferred to the write-controller bring-up, not this integration.)
- **D2 = nCSO MAX-V mode lever** — FREE, output, **static-LOW** throughout (SAMPLE-confirmed).
- **P6 = MAX-V status** — FREE, optional INPUT for debug (`DBG_STATUS`); not required.
- **DQ read lanes (drain), reuse proven `sramdump dq[21:0]`** — 22 balls:
  - **4 are the SAME balls as acq's adc_lane inputs** (the shared bus tap): **A13 B12 G15 G16**
    — same input direction, **no conflict**; they read the ADC codes during capture and the
    SRAM output during drain (Path A shared bus).
  - **18 are new FREE input balls:** A14 A15 B13 B14 B16 C15 C16 D9 D11 D15 D16 F15 J16 L7 P8
    R8 R16 T8 — added as tri-stated inputs.
  - Word mapping (proven): hi byte = CH1 = dq[15:8], lo byte = CH2 = dq[7:0].

**Resolution summary:** no ball is claimed twice. The write path adds 27+D2 outputs; the read
path adds D14 output + 22 DQ inputs (4 shared with adc_lane, 18 new). All driven SRAM outputs get
`MAXIMUM CURRENT` (drive the board net, per sramrw.qsf); D2/D14 minimum. During drain the 27 are
Hi-Z (RESERVE-tri-state semantics enforced by the controller OE, not the global reserve).

### Open items handed to task [2/3] (parameterize, don't freeze)
- Exact per-cycle CS/WE/CLK/D2 **write** timing the MAX-V expects (clkdiv, assert phase,
  addr-advance edge, load-strobe **ball** among the 6 CONTROL balls) — runtime-settable via
  the DBG_* regs above.
- A0..A18 order within the 4 address bands, and CONTROL strobe identity — brute-forceable on HW
  through the same debug regs (sramrw/sramgold2 role-sweep pattern), never guess-and-freeze.

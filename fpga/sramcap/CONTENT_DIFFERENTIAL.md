# Vendor content differential — the SRAM DQ bus is the ADC-lane bus (2026-08-03)

## Method
Ran the vendor factory bitstream, drove the analog input to two **confirmed-distinct**
constant SRAM contents, and JTAG boundary-scan SAMPLE'd all 84 candidate balls in each
state, then diffed the per-ball input duty (`p1`).

- Content **A**: `C1:OFST -1V`  @ VDIV 500MV/TDIV 100NS -> mean **246** (0xF6)  [/tmp -> A2.txt]
- Content **B**: `C1:OFST -0.4V` -> mean **21** (0x15)  [B2.txt]
- `|dp1| > 0.35` = the ball's level tracks the SRAM/ADC content = a **data** ball.

## Result — assumptions were WRONG in a specific, fixable way
| group (as labelled in acq_sram.v) | balls that carry the content |
|---|---|
| `adc_lane` | **D8 C8 A13 L16 L14 R10 M10** (adc_lane[1,10,14,15,16,17,29]) |
| `sram_dq` (A14…T8) | **NONE** |
| bidir (F3 F5 G5 D3 F7) | F3 D3 |
| address | 10 balls flip (sweep-phase aliasing under RUN) |
| control | N1 T5 |
| clock (F2 J2 K2) | none |

**The `sram_dq` balls never move with content — they are not the SRAM DQ.**
The real wide data bus is the **shared ADC-lane bus**. The prior `sram_dq` map was
ADC-contaminated (matches the sram-interface-re conclusion, reached here from a fresh angle).

`acq_sram.v` `dqv[]` was rerouted accordingly: `dqv[7:0]` = the proven CH1 de-interleave
byte (adc_lane[3,0,1,11,9,12,6,5]); `dqv[21:8]` = the flipped + neighbouring data lanes.

## But this does NOT yield a faithful SRAM read — the MAX-V wall is real
With the corrected routing + full app (SPI analog up, ADC driving):
- **`prove` (eng_enable 0->1)**: no change to the output (coherent, valid_depth=6144,
  last_ptp=255 both ways). Per the fabric's own design contract, this means **data does
  not traverse the external SRAM** — the coherent frames are the on-chip **M9K** path.
- Under our *minimal* fabric (adcfind, no app/SPI) the ADC never drives the bus at all
  (all data balls pinned p1=1.00, tog=0.00 for every ADC-control config) — so the
  standby-control hunt is moot; there is nothing to silence.
- read_only SRAM-drain modes (0x58 bit10/13 + P6 handshake) change mem[] contents but
  there is **no known-written ground truth** to call any of it faithful, and we cannot
  write known data because eng_enable (the fill strobes) has no observable effect.

## Why it cannot be cracked remotely (verified blockers)
1. The **MAX-V 5M240ZT** co-owns the SRAM command strobes (ADSP#/ADSC#/ADV#/OE#/BW#/GW#)
   and sequences every real read/write. It is **off the JTAG chain** (unobservable via
   boundary-scan) and **no `.pof` exists** anywhere in the workspace or device firmware
   (`firmdata0/` has only Siglent_CML.cfg + calibration) — so its FSM cannot be decoded.
2. We are fully remote (JTAG SAMPLE + GPMC only) — no logic analyzer on the SRAM strobes.
3. Physical capture depth is already delivered by the owned M9K design (6144/frame here,
   20480/ch ceiling) — the SRAM does not add depth; it is a bench-only target.

**Honest status: no faithful our-fabric SRAM fill+read achieved. The remaining blocker is
the MAX-V read/write predicate, which is not observable or decodable remotely.**

## 2026-08-03 (cont.) — FACTORY-PRIME → OUR-READ test: GROUND-TRUTH NEGATIVE (decisive)
Made the factory SRAM prime survive the flaky auto-takeover by (a) building acq_sram with
`read_only=1` DEFAULT so our fabric can NEVER fill/overwrite, and (b) `sync`ing the vfat before
the Shelly power-cut so `untakeover` (taken_over=false, auto_takeover=false) actually persists —
factory then booted STABLE (no delayed takeover, 84s+ verified).

Procedure: factory @ TDIV 100NS (fast → external SRAM exercised), railed C1:OFST +3V →
SRAM filled with a **verified constant 0xFF** (factory's own VXI `C1:WF? DAT2` = 20480 samples,
mean 253.8, ptp 3, values 252-255). Then takeover → force-load our read-only fabric (SRAM 0xFF
survives, read-only can't overwrite) → drain → read.

**Result: our drain reads mean~117, uniq~19, a fixed 0x3D/0xBD float artifact — 0/6144 samples
≥ 250, i.e. NOT ONE sample reflects the 0xFF that is verifiably in the SRAM.** Swept
clk_mode/d2_rd/read_sync/drain_p6_wait/pol/rd_clkdiv with the KNOWN 0xFF target to score against:
every config = 0/6144 hits. The read is completely independent of both the SRAM content and the
handshake knobs.

CONCLUSION (experiment-backed, ground-truth): **our fabric cannot read the external SRAM even when
it is primed with known content.** The only proven read (sramdump/draintest) did capture+drain in
the SAME factory fabric with NO reconfigure between; the reconfigure required to load our read
fabric breaks the MAX-V read (the MAX-V does not stream for our D14/D2 stimulus post-reconfigure).
Together with the write blocker (900-config sweep = 0 commits), BOTH directions are closed for our
fabric remotely — the SRAM round-trip is inextricably factory-fabric-bound without bench access to
the MAX-V. read_only default reverted to 0 after the test.

## 2026-08-03 (cont.³) — DECOMPILE REVISIT + vendor-style-read (vread): decode-grounded, HW-verified NEGATIVE
Re-mined the vendor Cyclone decompile (re_workflows/out/{acqpath,datapath,sramf,sramnet,vendorctl,maxv})
with the corrected DQ ground truth. Two payoffs + a decisive test:

**Decompile corrected 3 wrong moves in our old D14-slurp/tri-state read model:**
1. D14 is the ADC *sample* clock, NOT the SRAM read clock — the SRAM read is clocked on **F2/J2/K2**
   (free-running). (`hundred/sramwr`, `jtag/1_harness.md` flags D14-as-read-clock as superseded scaffolding.)
2. D2 (nCSO) is **static LOW = MAX-V CE** through capture AND drain (HW-SAMPLE: 0 flips) — there is NO
   "read-mode level". Moving D2 off 0 drops CE. (`vendorctl/B_fsm.md`.)
3. The address counter is **Cyclone-owned ARITH and must keep WALKING during read** (do NOT tri-state);
   the MAX-V latches the presented address (ADSC#) + asserts OE# when CS#=low & WE#=high. (`acqpath/2_fsm.md`,
   `datapath/SRAM_DATAPATH_VERDICT.md`; specimen FSM `maxv/3_fsm.md`: OE#.next = reg(~cs_n & we_n).)

**Also: my earlier "no MAX-V .pof / bench-only" was too strong** — a COMPLETE MAX-V FSM decode toolchain
exists and is validated bit-exact on fresh designs (`maxv/MAXV_VERDICT3.md`, pof_cfm→cone_decode→fsm_sim);
it only lacks the vendor CPLD's config DUMP as input (the CPLD is off-JTAG, never dumped).

**Implemented the corrected read as `vread` (capsram.v 0x58 bit14):** during drain, keep driving the address
counter (walking), control balls in READ posture (CS# low via low_mask, all WE/load HIGH), F2/J2/K2 clocked,
D2 static low, capture adc-lane DQ. **JTAG-VERIFIED the fabric drives EXACTLY that posture** (L2=CS# p1=0.00,
N1/M6/N5/R6/T5 p1=1.00, address balls L1/N2/P1 drv=1.00 tog~0.5 walking). Tested vs the verified 0xFF prime,
swept every CS#-ball selection (low_mask 0x01..0x3f): **0/6144 samples read 0xFF** — the SRAM never drives.
Then tested the full corrected ROUND-TRIP (our fabric capture WE#-low + vread drain, one session, no
reconfigure): eng_enable 0↔1 = NO change (coherent climbs both ways) → write does not commit AND read returns
the float artifact.

**DECISIVE, decode-grounded + HW-verified: the MAX-V does NOT act on ANY Cyclone-boundary stimulus we can
produce**, even when we drive the exact posture the decompile prescribes. This empirically confirms
`sramcensus/CENSUS_RECIPE.md`: "D2=nCSO alone does not drive a read; ADSP#/ADSC#/OE# are MAX-V-owned and this
lever does not reach them." The blocker is the MAX-V internal FSM, which is unreadable from the Cyclone rbf
(LEIM wall) and un-decodable without the vendor CPLD dump. **Single remaining unlock: dump the vendor MAX-V
config (JTAG lead to its TAP at the bench) → the ready toolchain decodes the strobe FSM bit-exact.**
vread left in capsram.v (default off) for when a MAX-V dump makes it testable.

# In-fabric DECODE + TRIGGER — consolidated reference

The owned Cyclone IV E (`EP4CE10F17C8`) decodes UART / I2C / SPI / 100BASE-TX **in
fabric** and produces a hardware trigger into `capture.v`, byte-for-byte agreeing
with the app's software oracle — so the FPGA does the decoding and the triggering,
losslessly to the host, instead of the AM3352 pulling every record and decoding on
the CPU. This file is everything about the decode+trigger register interface, the
four trigger modes, the arm recipes, and what is proven vs bench-owed.

**Legend:** ✅ verified in source + self-checking sim/test · ◐ derived / clean-vector
only · ❓ bench-owed (needs a live-signal scope run) · ⚠️ caveat / footgun.

**Traces to source** (owned-fpga, `fpga/standard/`): `acq.v` (wiring),
`dec_trigger.v` (4-mode engine), `uart_decode.v`, `i2c_decode.v`, `spi_decode.v`,
`eth100_decode_lr.v`+`eth_framer.v`, `byte_fifo.v`, `regs.vh`/`regmux.vh` (schema).
Host driver: `app/internal/engine/fabrictrig.go`. Oracles: `app/internal/decode/`,
`app/internal/engine/serialtrig.go`, `app/internal/eth100tx/`.

---

## 0. Hard invariants (why this block is schema-invariant)

- **Build-ID is untouched.** `IFACE_BUILD_ID = 0xc2f6eb5f` (`regs.vh:12`). The whole
  decode block lives on **hand-decoded spare selectors that are deliberately NOT in
  `regs.vh`/`regmux.vh`** — `regmux.vh` never generates a `we_`/`rmux` case for them
  (the `decode` schema block is still `RESERVED (v2)`, `REGISTER-MAP.md:94`). So the
  codegen fingerprint cannot change. The writes go through `bus.WriteSpare` (the
  narrow escape hatch), the reads through unguarded `bus.Read`. ✅
- **Mode-0 / decode-off is byte-identical to today.** `trig_mode==0` routes the
  UNTOUCHED per-module comparator pulse (`dec_trigger.v:212–216`); `dec_en==0` forces
  `decode_trig=0` and clears the sticky (`acq.v` gates every engine on `dec_en`). At
  reset `dec_cfg=0x0000` (`acq.v:232`) ⇒ proto=UART, `dec_en=0`, `trig_mode=0` ⇒ the
  fabric is fully inert and every legacy mode is unchanged. ✅
- **Additive only.** Every decode field is packed into previously-FREE / mode-
  reinterpreted bits of EXISTING selectors. No new selector, no schema/codegen edit.

---

## 1. Dataflow

```
                     CS1 GPMC writes (WriteSpare 0x04/08/0c/1c/48/68)
                                 │  (latched in acq.v)
        cap_word / cap_tick      ▼
   (decimated column stream) → dec_cfg / dec_thr / dec_spb / dec_match / dec_tg
        │                                 │  (per-proto config fan-out)
        ├──────────────► uart_decode ─┐   │
        ├──► dec_a/dec_b ► i2c_decode ─┤   │  exactly ONE engine hot
        ├──► dec_a/dec_b ► spi_decode ─┤   │  (dec_en & sel_<proto>)
        └──► c1a/b/c_p ► eth100_decode_lr ─┘
                                 │  PROTOCOL DISPATCH mux (acq.v:759–771)
                                 ▼
        SHARED emit bus:  dec_emit_stb / dec_emit_byte / dec_emit_idx
                          dec_emit_flags8 (8b, ETH) / dec_trig_pulse (legacy)
                                 │
              ┌──────────────────┴───────────────────┐
              ▼                                       ▼
        dec_trigger.v  (4 modes)               byte_fifo.v (32-deep, logic)
         │  decode_trig → capture.v             │  head → 0x7c drain (auto-inc pop)
         │  matched (sticky) → 0x4c[14]         └─ fill/ovf/empty → 0x4c
         └  matched_byte     → 0x6c
```

- Engines tap **`cap_word`/`cap_tick`** — the SAME decimated column stream capture /
  envelope consume — so SPB / gap are in host `colTimeS` units and the decoded bytes
  match `decode.Decode*().Bytes` (`acq.v:597–608`). "Column" == one `cap_tick`.
- The three serial engines are mutually exclusive; ETH taps the interleave per-core
  phase captures `c1a_p/c1b_p/c1c_p` at 600 MSa/s and runs its chain at 80 MHz
  (`acq.v:708–755`).

---

## 2. Register map (hand-decoded spare selectors, PLANE_CS1, 16-bit)

Selectors are decoded on the masked `sel[6:2]` lines, like the generated mux
(`acq.v:917`). All writes are `we_commit & (wr_plane==PLANE_CS1) & (wr_sel==sel)`
(`acq.v:225–230`); reads 0x4c/0x6c/0x7c are intercepted at the single tri-state
driver (`acq.v:917–921`).

### 2.1 WRITE — configuration

| sel | reg | reset | field layout (bit ranges are from `acq.v:246–273`) |
|---|---|---|---|
| `0x04` | **CFG** (`dec_cfg`) | `0x0000` | `[0]` en · `[1]` src/chan · `[5:2]` UART databits (0⇒8) · `[7:6]` UART parity (0 none,1 even,2 odd) · `[8]` trig_en · `[9]` tg_en · `[11:10]` proto (0 UART,1 I2C,2 SPI,3 ETH) · `[13:12]` trig_mode (0 byte,1 err,2 seq,3 addr) · `[15:14]` seqlen (N = seqlen+1) |
| `0x08` | **THR** (`dec_thr`) | `0x0080` | `[7:0]` thrA = SCL/CLK slice (== UART thr8) · `[15:8]` thrB = SDA/DATA slice |
| `0x0c` | **SPB_LO** (`dec_spb_lo`) | `0x0000` | samples-per-bit `[15:0]`, Q16.8 |
| `0x1c` | **SPB_HI** (`dec_spb_hi`) | `0x00` | samples-per-bit `[23:16]` ⇒ `dec_spb = {hi,lo}` 24-bit Q16.8 |
| `0x48` | **MATCH** (`dec_match`) | `0x0000` | `[7:0]` pattern · `[15:8]` mask — **reinterpreted per trig_mode** (§4) |
| `0x68` | **TESTGEN** (`dec_tg`) | `0x0000` | `[7:0]` tg_byte/tg_word / mode-2 seq1 · `[15:8]` mode-2 seq2 |

**CFG `[5:2]` / `[7:6]` are per-protocol reinterpreted** (same physical bits):

| CFG bits | UART | I2C | SPI |
|---|---|---|---|
| `[1]` | source channel (0=C1,1=C2) | channel swap | channel swap |
| `[2]` | databits.0 | — | CPOL |
| `[3]` | databits.1 | — | CPHA |
| `[4]` | databits.2 | — | MSB-first |
| `[5]` | databits.3 | — | — |
| `[7:6]` | parity | — | — |

`dec_a = SCL/CLK`, `dec_b = SDA/DATA` (swapped by CFG`[1]`); UART taps its single
source channel via CFG`[1]` (`acq.v:606–608`). `dec_spb` doubles as SPI `gapReset`
(integer columns) and seeds the mode-2 adjacency window (§4).

### 2.2 READ — status / results (non-schema, tri-state override)

| sel | reg | layout | pop? |
|---|---|---|---|
| `0x4c` | **STATUS** | `[15]` overflow · `[14]` matched (sticky) · `[13]` busy (FIFO non-empty) · `[12:11]` 0 · `[10:0]` fill (0..32) | no |
| `0x6c` | **MATCHED** | `[7:0]` matched_byte · `[15:8]` **0** (matched is data-only) | no |
| `0x7c` | **BYTE** | non-ETH: `[7:0]` data · `[8]` flags[0] · `[9]` flags[1] · `[15:10]` 0.  ETH: `[7:0]` data · `[15:8]` flags8 | **yes** — one FIFO pop per `nOE` rise (`acq.v:146–150`) |

Source: STATUS/MATCHED/BYTE words `acq.v:844–851`. FIFO overflow clears on
`op_reset` OR any CFG write (`clr_overflow = op_reset | we_DEC_CFG`, `acq.v:840`) —
so re-arming (which rewrites CFG) always starts from a clean FIFO + sticky.

> ⚠️ **0x6c upper byte is hardwired 0.** `dec_matched = {6'd0,2'b00,matched_byte}`
> (`acq.v:846`) — the matched byte is data-only, so `[9:8]` are always 0. The host
> `Poll()` still reads `FrameErr=mb&0x0200 / ParityErr=mb&0x0100`
> (`fabrictrig.go:356–357`); those bits are **always false**. Per-byte error flags
> come only from the **0x7c** drain word (`DrainBytes`, `fabrictrig.go:387–388`).

---

## 3. Per-protocol decode behavior

All engines: one decode step per `cap_tick`; pure-threshold sample (`code >= thr8`
== oracle `logicAt` float `code >= Thr` because `thr8 = ceil(Thr)`); `en=0` ⇒ fully
inert, sticky cleared. Each carries an on-chip TEST-GEN loopback (CFG`[9]`), which
**must never be set when arming a real trigger** (`fabrictrig.go:65`).

### 3.1 UART — `uart_decode.v` ↔ `decode.DecodeUART`
- Falling-edge start hunt, then fractional-accumulator sampling: `acc=0.5*SPB`,
  `tgt=round(acc)`, `acc+=SPB` per phase — rounds the full sum, never re-rounds per
  bit (`uart_decode.v:234–265`), matching `DecodeUART`'s `round(S+(p+0.5)*SPB)`.
- Emits a byte for **every confirmed frame including parity/frame-error frames**
  (oracle appends those too). Only start-confirm-fail aborts.
- `emit_flags = {frame_err, parity_err}` (`uart_decode.v:272`).
- Host param: **SPB is required** (Q16.8), computed from baud + capture interval
  (`fabricSPB`); the fabric has no auto-baud, so auto-baud UART stays on the software
  path (`fabrictrig.go:266–279, 437–449`). ✅ clean-path bit-exact.

### 3.2 I2C — `i2c_decode.v` ↔ `decode.DecodeI2C`
- Per-column FSM, priority START > STOP > SCL-rising-sample, MSB-first, 8 data bits +
  ACK clock. `Result.Bytes` = **DATA payload only**; addresses are excluded
  (`i2c_decode.v:44–47`).
- Emit deferred to the 9th/ACK clock so ACK/NAK rides `emit_flags[0]`; a completed-
  but-un-ACKed byte is flushed on the next START/STOP (`i2c_decode.v:262–296`).
- `emit_flags = {KIND(1=addr), ACK(1=NAK)}`. `emit_byte` for an address = `{addr7,rw}`
  ⇒ host recovers `addr=byte>>1, rw=byte&1` (`i2c_decode.v:64–70`).
- No host timing param needed (edge-clocked). ◐ clean-vector-exact (hysteresis note,
  `i2c_decode.v:21–28`).

### 3.3 SPI — `spi_decode.v` ↔ `decode.DecodeSPI`
- `sampleRising = (CPOL==CPHA)`; MSB/LSB per CFG`[4]`; **8-bit word hardcoded** (SPICfg
  has no length field — must be 8 to be Bytes-exact, `spi_decode.v:40–42`).
- Word reframe: a sampling edge after a `> gapReset` idle with 1..7 bits pending
  discards the partial word. `gapReset` reuses `dec_spb`; host should load
  `floor(1.5*period)` (`spi_decode.v:49–59`).
- DATA words emit `emit_flags=2'b00`. **A mid-word gap that discards a 1..7-bit
  partial emits a NON-DATA framing-fault marker `emit_flags=2'b01`** (`spi_decode.v:
  258–265`) — never on clean traffic (`bc==0` at every clean gap).
- ◐ clean-signal-verified; RISK-1 near-threshold, RISK-2 gapReset floor, RISK-4 word
  length (`spi_decode.v:18,54–59,42`).

### 3.4 100BASE-TX — `eth100_decode_lr.v` (+`eth_framer.v`) ↔ `app/internal/eth100tx/`
- Chain: gearbox (200→80 CDC) → slicer/CDR → descramble → 4b/5b → framer (+ CRC-32
  FCS). WRITE side at 200 MHz interleave taps, READ side at 80 MHz.
- `emit_flags8` map (`eth_framer.v:59–65`): `[7]` START (first body octet) · `[6]`
  END (last/4th FCS octet) · `[5]` FCS (this octet is one of the 4 FCS octets) · `[4]`
  FCS-ok (valid at END) · `[3]` FCS-err (valid at END) · `[2:0]` reserved.
- Trigger source is **SFD** (frame start), gated by `dec_trigen` (`acq.v:756–757`).
- The framer/4b5b/CRC path is golden-vector exact (`eth100tx` IEEE vectors). ❓ the
  **analog taps** (`c1a_p` re-center + ×8 scale, slicer thresholds) are **bench-cal**,
  same status as the interleave taps (`acq.v:715–725`).

---

## 4. The four trigger modes (`dec_trigger.v`)

Selected by CFG`[13:12]`. `en=dec_en`; `trig_en=CFG[8]`. Output `decode_trig` is a
1-clk pulse into `capture.v`; `matched` is sticky (0x4c[14]); `matched_byte` is 0x6c.
Verified end-to-end by `sim/tb_dec_trigger.v` (HIT + MISS + sticky + disarm for every
mode — `== tb_dec_trigger: ALL CHECKS PASSED ==`). ✅

### Field reinterpretation of MATCH (0x48) / TESTGEN (0x68) by mode

| mode | name | MATCH`[7:0]` | MATCH`[15:8]` | TESTGEN`[7:0]` | TESTGEN`[15:8]` | seqlen |
|---|---|---|---|---|---|---|
| 0 | BYTE | pattern | mask | — | — | — |
| 1 | ERROR | — | **err_mask** | — | — | — |
| 2 | SEQUENCE | seq0 (first) | seq3 (last, N=4) | seq1 | seq2 | N−1 |
| 3 | ADDR | addr_field | addr_mask | — | — | — |

### Mode 0 — BYTE (pass-through, byte-identical to legacy)
Routes the per-module comparator (`legacy_trig/_matched/_byte`, `dec_trigger.v:212`).
Per engine: UART `clean_w&&match_w`, I2C data-only `pend_match`, SPI word match, ETH
SFD. Match predicate `(byte & mask)==(pattern & mask)`. Nothing re-implemented here.

### Mode 1 — ERROR (flag-mask)
`decode_trig` on ANY emit where `(emit_flags8 & err_mask)!=0`, `err_mask=MATCH[15:8]`
(`dec_trigger.v:121`). Fires on the anchoring symbol. Capability the software trigger
does **not** have.

### Mode 2 — SEQUENCE (2..4 contiguous DATA bytes)
Mirrors `serialtrig.matchBytes`: **data-only** (non-data symbols not pushed) + an
adjacency (idx-gap) check that rejects a sequence bridging a marker/idle gap; fires on
the **last** byte (`dec_trigger.v:124–166`). 4-deep `{byte,idx}` history; transmit
order is `seqv0..seqv{N-1}`.

**CORRECTED sequence packing** (`dec_trigger.v:137–140`, confirmed by
`fabrictrig.go:153–160` and `fabrictrig_test.go`):

```
seq0 = MATCH[7:0]   seq1 = TESTGEN[7:0]   seq2 = TESTGEN[15:8]   seq3 = MATCH[15:8]
```

| N | bytes {b0..} | MATCH | TESTGEN | seqlen(=N−1) |
|---|---|---|---|---|
| 2 | {AA,BB} | `0x00AA` | `0x00BB` | 1 |
| 3 | {AA,BB,CC} | `0x00AA` | `0xCCBB` | 2 |
| 4 | {AA,BB,CC,DD} | `0xDDAA` | `0xCCBB` | 3 |

Adjacency window `adj_win = {dec_spb[17:8], 6'b0}` (~64× integer SPB, `acq.v:788`) — a
generous single-expression threshold, NOT the oracle's exact `≤ 2×byte-width` (RISK-5,
bench-trim knob). ⚠️ testgen-exact ≠ bench-exact.

### Mode 3 — ADDR / FIELD
- **I2C:** on the ADDRESS symbol (`emit_flags[1]==1`) fire when
  `(emit_byte & addr_mask)==(addr_field & addr_mask)`, `addr_field=MATCH[7:0]`,
  `addr_mask=MATCH[15:8]`, `emit_byte={addr7,rw}` (`dec_trigger.v:185–186`). Clear the
  addr bits for addr-any; clear bit0 for RW-any. Mirrors `serialtrig.matchI2C`.
- **ETH:** fire on SFD (== the current ETH trigger; strict superset, no serialtrig
  ETH parity claimed) (`dec_trigger.v:187`).

---

## 5. Per-protocol trigger matrix

| mode | UART | I2C | SPI | ETH |
|---|---|---|---|---|
| 0 BYTE | ✅ data byte match | ✅ data byte match | ✅ word match | ✅ = SFD |
| 1 ERROR | ✅ frame/parity (mask=0x03) | ✅ NAK (mask=**0x01**) | ◐ framing-fault marker only (mask=0x01) | ✅ FCS-err (mask=**0x08**) |
| 2 SEQUENCE | ✅ 2..4 data bytes | ✅ 2..4 data payload bytes | ✅ 2..4 words | ◐ 2..4 non-FCS octets |
| 3 ADDR | — | ✅ addr+RW | — | ✅ = SFD |

⚠️ **Mode-1 mask must be protocol-scoped.** The host defaults `err_mask=0xFF` when
unset (`fabrictrig.go:184–188`). That is fine for UART (only `[1:0]` are error bits)
and SPI, but over-triggers on **I2C** (`[1]=KIND` fires on every address) and **ETH**
(`[7]start/[6]end/[5]fcs/[4]ok` fire on normal octets). Use `err_mask=0x01` (I2C NAK)
and `err_mask=0x08` (ETH FCS-err). The intended per-proto error bits are in §6.

⚠️ **SPI mode-1 is not a true no-op.** `dec_trigger.v:24–25` says "SPI has no error
flag ⇒ mode-1 no-op", and `tb_dec_trigger.v` checks that with flags forced to 0. But
`spi_decode.v` DOES emit a framing-fault marker (`emit_flags=2'b01`) on a mid-word
gap, so mode-1 with mask `0x01` **will** fire on that marker. It never occurs on clean
traffic, so both statements are true under their scope — the header comment is just
over-broad.

⚠️ **I2C addr+data is not one fabric mode.** Mode-3 is addr-only; mode-2 is data-seq-
only. `serialtrig.matchI2C` does address AND a data-byte sequence within the
transaction; the fabric cannot arm both at once — the host encoder picks mode-3 when
`Addr>=0 && no Bytes`, else mode-2 (`fabrictrig.go:189–215`).

---

## 6. Error-flag map per protocol (the mode-1 err bits)

| proto | flags width | bit meanings | error bit(s) for mode-1 |
|---|---|---|---|
| UART | 2b (`emit_flags[1:0]`) | `[1]` frame_err · `[0]` parity_err | `0x03` (either) |
| I2C | 2b | `[1]` KIND (1=addr, **not** an error) · `[0]` ACK (1=NAK) | `0x01` (NAK) |
| SPI | 2b | `[1:0]=00` DATA word · `[0]=1` framing-fault marker | `0x01` (framing fault) |
| ETH | 8b (`emit_flags8`) | `[7]`start `[6]`end `[5]`fcs `[4]`fcs_ok `[3]`fcs_err `[2:0]`rsvd | `0x08` (FCS-err) |

Data-only predicate used by modes 2/3 (`dec_trigger.v:108–110`): I2C `= ~flags[1]`
(KIND==0), ETH `= ~flags[5]` (non-FCS octet), UART/SPI `= (flags[1:0]==0)`.

---

## 7. Oracle mapping (which app function each fabric block mirrors)

| fabric block | app oracle (must agree byte-exact) | agreement basis |
|---|---|---|
| `uart_decode.v` bytes | `decode.DecodeUART().Bytes` | clean-path bit-exact (`uart_decode.v:12–54`) |
| `i2c_decode.v` bytes | `decode.DecodeI2C().Bytes` (data payload only) | clean-vector-exact |
| `spi_decode.v` bytes | `decode.DecodeSPI().Bytes` | clean-signal-verified |
| ETH chain + FCS | `app/internal/eth100tx/` (IEEE golden PHY encoder+decoder+vectors) | golden-vector exact (framer); analog taps bench-cal |
| UART/SPI byte/seq trigger | `engine.matchBytes` (data-only + adjacency) | `serialtrig.go:222–259` |
| I2C addr trigger | `engine.matchI2C` (addr `byte>>1`, rw `byte&1`) | `serialtrig.go:174–212` |
| mode select + MATCH/TESTGEN packing | `engine.encodeFabricSerial` | `fabrictrig.go:161–260`; `fabrictrig_test.go` ✅ |
| Arm / Poll / DrainBytes host driver | `engine.FabricTrig` (`fabrictrig.go`) | — |

`Result.Span{I0,I1,Text,Kind,Val}` and `Result.Bytes` (`decode.go:11–26`) are the
comparison targets; `Kind=="data"` is the data-only gate both sides honor.

---

## 8. GPMC arm / verify recipe

All writes are `bus.WriteSpare(sel, val)` on **CS1**; results are `bus.Read(CS1, sel)`.
Order is load-bearing (`FabricTrig.Arm`, `fabrictrig.go:302–322`):

```
1. WriteSpare 0x04 CFG     = 0x0000   ; inert FIRST — clears sticky (dec_en↓) + FIFO overflow
2. WriteSpare 0x08 THR     = {thrB,thrA}          ; 0x8080 default midscale
3. WriteSpare 0x0c SPB_LO  = SPB[15:0]            ; UART only (Q16.8); SPI: gapReset lo
4. WriteSpare 0x1c SPB_HI  = SPB[23:16]
5. WriteSpare 0x48 MATCH   = per §4 table
6. WriteSpare 0x68 TESTGEN = per §4 table (mode-2 only; else 0)
7. WriteSpare 0x04 CFG     = live value   ; dec_en+trig_en+proto+mode assert ATOMICALLY, LAST
```

CFG assembly (`fabrictrig.go:229–258`): `cfgEn | cfgTrigEn | proto<<10 | mode<<12 |
seqlen<<14`, plus proto extras (UART bits<<2 / parity<<6 / srcch; SPI cpol/cpha/msb/
swap; I2C swap). **Never set `tg_en (CFG[9])` when arming a real trigger.**

Verify (non-popping):

```
Read 0x4c STATUS  → [14] matched? [15] overflow? [10:0] fill (bytes queued)
Read 0x6c MATCHED → [7:0] matched_byte   (upper byte always 0 — see §2.2)
```

Drain the lossless byte stream (pops the FIFO; read `fill` first so you never pop
empty, `fabrictrig.go:371–392`):

```
n = STATUS[10:0]
repeat n:  w = Read 0x7c   → data=w[7:0], parity_err=w[8], frame_err=w[9]  (ETH: flags8=w[15:8])
```

The sticky `matched` holds until the next re-arm; the host counts its rising edge
(`fabrictrig.go:466–470`). Re-arm = rewrite CFG (step 1), which wipes sticky + FIFO,
so the driver re-arms only when the resolved register image or SPB actually changes
(`fabrictrig.go:451–459`) — never mid-capture.

---

## 9. Proven vs bench-owed status (honest)

| claim | status | evidence |
|---|---|---|
| Trigger ENGINE, all 4 modes (HIT/MISS/sticky/disarm) | ✅ proven | `iverilog dec_trigger.v sim/tb_dec_trigger.v` → ALL CHECKS PASSED |
| Host encoder packing (incl. corrected mode-2) + arm order | ✅ proven | `go test ./internal/engine -run Fabric` → ok |
| Mode-0 / decode-off byte-identical to legacy | ✅ structural | pass-through wiring `dec_trigger.v:212–216`; reset `dec_cfg=0` |
| Build-ID unaffected (spare selectors, no schema edit) | ✅ | `regs.vh`/`regmux.vh` have no decode case; `0xc2f6eb5f` |
| UART bytes == DecodeUART (clean) | ◐ clean-exact | oracle audit `uart_decode.v:12–54`; no live-signal run |
| I2C bytes == DecodeI2C (clean full-swing) | ◐ clean-vector | hysteresis-equivalence note `i2c_decode.v:21–28` |
| SPI bytes == DecodeSPI (clean) | ◐ clean-signal | RISK-1/2/4 `spi_decode.v:18,54–59,42` |
| ETH framer/4b5b/CRC == eth100tx golden | ✅ golden-vector | `app/internal/eth100tx/vectors` |
| ETH analog front-end (taps → codes) | ❓ bench-owed | ×8 scale + slice thresholds are bench-cal, `acq.v:715–725` |
| Mode-2 adjacency window vs oracle `2×byte-width` | ◐/❓ | `adj_win` ≈ 64× SPB single-expr threshold, `acq.v:782–788` |
| Live UART/I2C/SPI/ETH error/addr/seq trigger on real signals | ❓ bench-owed | gated scope Verify (`tb_dec_trigger.v:9–10`) |

**Bottom line:** the decode+trigger *logic* (engine, packing, arm/disarm, FIFO,
build-ID invariance) is fully proven in sim + Go test. What remains is a **live-signal
bench run** on the remote scope (real serial/Ethernet traffic through the analog front
end), plus the ETH analog-tap calibration and the mode-2 adjacency-window trim — the
same bench-cal class as the interleave taps.

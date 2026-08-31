# VALIDATE.md — live-scope validation of the external-SRAM capture fabric (`acq_sram.rbf`)

**Deliverable of [V RECIPE].** How to prove, on the real SDS1102CML+ (EP4CE10F17C8), that
**our own** Cyclone bitstream captures the CH1/CH2 **triangle** into the **external
S7A163630M SRAM** the vendor way (ADC → shared DQ bus → MAX-V-sequenced SRAM →
D14 slurp → GPMC drain) and that the **unmodified owned app** renders it — full depth,
coherent, at the right peak-to-peak.

- Bitstream under test: `/home/labeth/ws/open-sds1000cml/fpga/sramcap/acq_sram.rbf` (368011 bytes, build-ID `0xc2f6eb5f`, VERSION `0x0052`).
- Drop-in target it replaces: `/home/labeth/ws/open-sds1000cml/fpga/standard/acq.rbf`.
- Consumer: `open-sds1000cml/app` branch **owned-fpga**, rebuilt as `dist/app-arm` (same build-ID → loads+verifies unchanged).
- Bench: remote via `otactl -tcp 192.168.1.209:5900`; scope HTTP UI at `http://192.168.1.209`; a **triangle wave is present on CH1/CH2**; recovery via Shelly at `192.168.1.223`.
- Companion script: **`RUN.sh`** in this directory does deploy → sweep → readback → restore. This file is the human recipe + the reasoning behind each step.

---

## 0. PASS CRITERION (crisp)

> **Our fabric captures the triangle into the EXTERNAL SRAM and drains it coherently at full
> depth through our app.** Concretely, with the write-timing knobs tuned:
> 1. the fabric loads + verifies (`0xc2f6eb5f`) and reports our debug identity `0x5CA0`;
> 2. the app's `/api/status` shows **`coherent` advancing** frame-over-frame, **`valid_depth` ≈ the
>    drained `cols`** (up to the full **20480** at the deepest band) with **no dead tail**, and
>    **`last_ptp`** equal to the triangle's real peak-to-peak (stable, non-flat);
> 3. `/api/frame.bin?raw=1` returns a **monotone rising/falling triangle** on C1/C2, not noise/flat;
> 4. the **negative control passes**: setting `eng_enable=0` (fabric stops driving the SRAM write
>    strobes) makes the record go flat/incoherent — proving the data really traversed the external
>    SRAM and is **not** an on-chip M9K artefact.
>
> All four → PASS. Any tuned param set that achieves 1–4 is the answer; write it down (§7).

Why (4) is the M9K-vs-SRAM discriminator: `capsram.v` **never writes `mem[]` during fill** — `mem[]`
is filled *only* by the D14 slurp that reads the external SRAM DQ bus. So a correct full-depth triangle
frame is, by construction, data that went out to the external part and came back. `eng_enable=0` removes
the *only* thing that makes the MAX-V latch the ADC data into SRAM; if the triangle survived that, it
would have to be coming from on-chip memory — it does not, so it must vanish. That flip is the proof.

---

## 1. Register surface used here

Two planes, driven with `gpmc_probe` (`wr <plane> <sel> <val>` / `rd <plane> <sel>`), plane **1 = CS1**, **3 = CS3**.

### Schema registers the APP drives (our `regs.vh` map — do not hand-drive these while the app runs)
| sel | name | meaning |
|-----|------|---------|
| CS1 0x10 / 0x14 | BUILDID lo/hi | `0xeb5f` / `0xc2f6` → build-ID `0xc2f6eb5f` |
| CS1 0x18 | VERSION | `0x0052` |
| CS1 0x20 | OPCODE | `OP_RESET=0x0000`, `OP_GO=0x0001` (only while RUN), `OP_HALT=0x0002` |
| CS1 0x24 | RUN | bit2 = run-enable, bits[1:0] = mode (0 AUTO / 1 NORM) |
| CS1 0x28/0x2c | DECIM lo/hi | decimation |
| CS1 0x30/0x34 | PRETRIG lo/hi | pre-trigger work |
| CS1 0x38/0x3c | POSTTRIG lo/hi | post-trigger work |
| CS1 0x40 | BURST | auto-inc frozen-record read port (the drain) |
| CS1 0x44 | BURST_REMAIN | bit15 ready, [14:0] words remaining |
| CS1 0x50 | STATUS_A | bit0 VALID, bit1 TRIG, bit2 DONE |
| CS1 0x54/0x58 | TRIGPOS frac/idx | interpolating trigger position |
| CS1 0x5c | FILL | [10:0] fill count |
| CS3 0x07 | CONF_DONE | bit7 = configured (READ ONLY — never write CS3 0x07) |

### FREE debug selectors decoded in `capsram.v` (outside the schema — the sweep drives these)
These are multiples-of-4 the schema leaves unused; the app never touches them, so poking them from
`gpmc_probe` never collides with the running app. **This is the whole tuning surface.**

**Writes** (`wr 1 <sel> <val>`):
| sel | name | fields | default |
|-----|------|--------|---------|
| 0x48 | DBG_WDIV | `[15:0]` write SRAM-clk divider (bigger = slower write) | 25 |
| 0x4c | DBG_WPHASE | `[3:0]`=we_phase `[7:4]`=load_phase (strobe pulse widths) | 2,2 |
| 0x68 | DBG_WSTROBE | `[2:0]`=load_sel `[6:4]`=we_sel `[13:8]`=low_mask (over ctrl[5:0]) | 3,2,0b000011 |
| 0x6c | DBG_WMISC | `[0]`=eng_enable `[1]`=d2_wr `[2]`=d2_rd `[3]`=d2_idle | 1,0,0,0 |
| 0x0c | DBG_RDDIV | `[15:0]` drain D14 divider (read side — leave 25, proven) | 25 |
| 0x08 | DBG_MAP | `[4:0]`=val `[9:5]`=idx `[11:10]`=tbl (00=addr order `amap`, 01=lane_sel `lmap`) | identity |

**Reads** (`rd 1 <sel>`):
| sel | name | returns |
|-----|------|---------|
| 0x00 | DBG_ID | `0x5CA0` — proves OUR fabric is loaded (vendor never returns this) |
| 0x04 | DBG_RAW_HI | `{p6, 9'd0, dq_lat[21:16]}` — MAX-V status + upper DQ candidates |
| 0x1c | DBG_RAW_LO | `dq_lat[15:0]` — last latched DQ vector from the slurp (lane confirm) |
| 0x7c | DBG_STATUS | `{slurp_addr[7:0], sck_rd, sck_wr, eng_enable, slurp_done, slurp_run, coherent, state[1:0]}` |

The **6 control balls** behind DBG_WSTROBE (RE map order): `ctrl[0..5] = L2 N1 M6 N5 R6 T5`
(`L2,N1` = CS# candidates, `M6,N5,R6,T5` = WE/load candidates). The 3 write-clock balls `F2/J2/K2`
all carry the divided `sck_wr`; D14 is the fixed proven read clock; D2 is the nCSO mode lever.

> **CRAM is volatile.** Every FPGA (re)load resets all debug knobs to the RTL defaults above. Re-apply
> the tuned set after any reconfigure/power-cycle. This is also your unbrickable safety net (§8).

---

## 2. Deploy

```
cd /home/labeth/ws/open-sds1000cml
cp fpga/sramcap/acq_sram.rbf fpga/standard/acq.rbf          # drop-in swap
cd app && make app-release                                   # embeds the new rbf; same build-ID → loads+verifies
otactl -tcp 192.168.1.209:5900 -stage /usr/bin/siglent/usr/media/U-disk0/agent-slots/staging \
       update-app dist/app-arm
otactl -tcp 192.168.1.209:5900 takeover --force
otactl -tcp 192.168.1.209:5900 app start
```

**(1) Confirm the fabric loaded + verified.** Look for the agent log line
`loaded and verified 0xc2f6eb5f`. Then double-confirm live:

```
gpmc_probe rd 3 0x07     # CONF_DONE: expect bit7 set (0x0080)
gpmc_probe rd 1 0x18     # VERSION : expect 0x0052
gpmc_probe rd 1 0x10     # BUILDID lo: expect 0xeb5f
gpmc_probe rd 1 0x14     # BUILDID hi: expect 0xc2f6
gpmc_probe rd 1 0x00     # DBG_ID  : expect 0x5CA0  ← proves OUR fabric, not the vendor image
```

(gpmc_probe runs on-device via `otactl ... sh "cd <fpgaflash-dir>; gpmc_probe ..."`; arbitrate the bus
first with `gpmc_probe relax 1 --force` exactly as the oracle recipe does — HTTP `/api/*` is a separate
path and never needs the bus.)

---

## 3. WRITE-TUNING sweep (the one uncertain thing)

The **read/drain** path is fixed and proven (the `sramdump` D14-only non-contending read, reused
verbatim). The **only** unknown is the per-cycle CS/WE/CLK/D2 **write** micro-timing the fixed MAX-V
expects in order to latch each ADC sample into the SRAM. That is exactly what the DBG_* knobs
parameterise, so we **sweep on hardware, no recompile**.

**Oracle for scoring = the app itself.** With the triangle live on CH1/CH2 the comparator has a real
edge (this bench does *not* have the flat-rail no-trigger problem the vendor oracle hit). So the app
free-runs, arms, waits VALID, drains BURST, and publishes. Poll `http://192.168.1.209/api/status`:

- `coherent` — increments only on a latched+drained coherent frame;
- `valid_depth` — live samples before the dead tail (want ≈ `cols`, i.e. no dead tail);
- `last_ptp` — peak-to-peak of the drained CH1 (want = the triangle's real ptp, stable);
- `dead_runs` / `stuck_suspect` — want 0 / false;
- `band` — put the app on the **deepest** band so `cols`→ up to 20480 (see §4).

A wrong write-timing candidate ⇒ the MAX-V never latches ⇒ the SRAM holds stale/garbage ⇒ the slurp
reads garbage ⇒ the app sees a flat/incoherent frame (`coherent` stalls, `valid_depth` collapses or
`last_ptp` random). A right candidate ⇒ the triangle appears at full depth. That contrast **is** the
sweep signal.

### Sweep order (prioritised; `RUN.sh sweep` automates stages A–B)
Start slow and safe: `clkdiv=64`, `we_phase=load_phase=2`, `d2_wr=0`, maps identity, `eng_enable=1`.

**Stage A — control-ball roles (the big unknown).** Which of `M6/N5/R6/T5` is WE#, which is the
load/ADSC# strobe, and which balls are held CS#-low. Enumerate the natural candidates
(`we_sel,load_sel ∈ {2,3,4,5}`, distinct; `low_mask` base `0b000011` = L2,N1 as CS#, optionally also
pulling the unused WE-group ball low):
```
for each (we_sel, load_sel):
    gpmc_probe wr 1 0x68  <load_sel | (we_sel<<4) | (0x03<<8)>
    (let the app run ~3 s; sample /api/status twice)
    record Δcoherent, valid_depth, last_ptp
```
Keep the triple with the largest `valid_depth` and a sane, stable `last_ptp`.

**Stage B — clock + strobe-phase refine.** Fix the winning roles, then grid:
```
clkdiv     ∈ {8,16,25,40,64,100}   (wr 1 0x48 <clkdiv>)
we_phase   ∈ {1,2,3}               packed with load_phase into 0x4c
load_phase ∈ {1,2,3}
```
Pick the point with **max `valid_depth`** at the correct `last_ptp`. Faster (small clkdiv) is better as
long as depth stays full — it proves margin.

**Stage C — D2 write polarity (only if A/B never lock).** Try `d2_wr=1`:
`gpmc_probe wr 1 0x6c 0x03` (eng_enable=1, d2_wr=1). D2 is SAMPLE-confirmed static-low in normal
operation, so 0 is the expected winner; 1 is the fallback.

**Stage D — lane / address order (only if the triangle is present but bit-scrambled).** If `last_ptp`
is right-ish but the waveform is byte/bit-shuffled, the DQ lane_sel or address bit-order is off. Confirm
with the raw DQ read `rd 1 0x1c` while a slurp runs (it should track the triangle), then remap per lane:
```
lane_sel: wr 1 0x08  <val | (j<<5) | (0x01<<10)>   # word bit j <- dq[val]
addr order: wr 1 0x08 <val | (i<<5) | (0x00<<10)>  # addr ball i <- waddr[val]
```
Defaults are the **proven sramdump order** and identity, so D is rarely needed.

### While sweeping, watch the engine directly (no app needed)
```
gpmc_probe rd 1 0x7c    # DBG_STATUS: bit? sck_wr should toggle during FILL, sck_rd during DRAIN,
                        #             state cycles IDLE→FILL→DRAIN→HALT, coherent asserts post-slurp
gpmc_probe rd 1 0x1c    # DBG_RAW_LO: the live DQ vector captured off the external SRAM bus
gpmc_probe rd 1 0x04    # DBG_RAW_HI: MAX-V P6 status mirror + upper DQ lanes
```

**PASS signal for §3:** a knob set for which `/api/status` shows `coherent` climbing every frame,
`valid_depth` ≈ `cols` (approaching 20480 on the deep band), `last_ptp` = the triangle ptp, and the
DBG_STATUS shows the normal IDLE→FILL→DRAIN→HALT cycle. **Record the winning knob values (§7).**

---

## 4. Read back a drained frame and confirm it's the triangle

Set the app to the **deepest** band so the full record drains (`cols`→ up to 20480):

```
curl -s 'http://192.168.1.209/api/set' -d '{"control":"tdiv","dir":+1,"steps":6}'   # walk to a deep/slow tdiv
# (or drive tdiv from the panel; goal: mem_depth / cols as large as possible for this build)
```

Then pull the raw record and inspect:

```
curl -s 'http://192.168.1.209/api/frame.bin?raw=1&since=0' -o frame.bin
# header: 8-byte magic/len prefix, then JSON {cols, sample_s, ...}, then C1[cols] C2[cols] uint8.
```

Confirm on the bytes:
- **Shape** = a clean triangle: C1 rises linearly to a peak, falls linearly to a trough, repeats;
  monotone runs, symmetric slopes, no flat dead tail past `valid_depth`.
- **Depth** = `valid_depth` from `/api/status` ≈ `cols` (full-record, no half-record).
- **ptp** = `max(C1)-min(C1)` matches `/api/status.last_ptp` and the triangle you see on screen.
- **Word packing** = hi byte CH1 / lo byte CH2 (8-bit unsigned) — CH1 and CH2 both show the triangle.
- **Screenshot** cross-check: `curl -s http://192.168.1.209/api/screen.png -o screen.png` — the rendered
  trace is the triangle.

### PROVE it's the external SRAM, not on-chip M9K (the decisive test)
```
gpmc_probe wr 1 0x6c 0x00     # eng_enable=0  → fabric STOPS driving SRAM write strobes
# let the app run ~3 s, re-read /api/status + /api/frame.bin
#   EXPECT: coherent stalls, valid_depth collapses / frame goes flat|garbage (SRAM never got the data)
gpmc_probe wr 1 0x6c 0x03     # restore eng_enable=1 (+ d2_wr as tuned)  → triangle returns
# let the app run ~3 s
#   EXPECT: coherent climbs again, triangle back at full depth
```
The triangle appears **iff** the fabric is actively writing the external SRAM. `mem[]` is never written
during fill, so this cannot be an M9K artefact. Optionally also confirm bus activity directly:
`rd 1 0x1c` (DBG_RAW_LO) tracks the triangle during a slurp with `eng_enable=1` and freezes/goes stale
with `eng_enable=0`.

**This is the §4 PASS: full-depth triangle out of `/api/frame.bin`, ptp matches, and the eng_enable
toggle switches it on/off — external SRAM proven.**

---

## 5. SAMPLE / scope checks

No external gear required (all in-fabric via DBG_STATUS), plus optional bench confirmation:

- **In-fabric (primary):** `rd 1 0x7c` repeatedly across a frame — `sck_wr` toggles during FILL,
  `sck_rd` toggles during DRAIN, `state` walks IDLE(0)→FILL(1)→DRAIN(2)→HALT(3), `slurp_addr` counts up
  to `rec_len`, `coherent` asserts only after the slurp completes, `eng_enable` reads back what you set.
- **P6 / MAX-V handshake:** `rd 1 0x04` bit15 = live P6 status from the MAX-V (it is cooperating).
- **External LA / SAMPLE (optional, confirms real balls):** on a vendor-driven or our-driven acquisition,
  sample `F2/J2/K2` (write clock toggling during capture), `L2/N1` (CS# low), the tuned WE/load ball
  (`M6/N5/R6/T5`) pulsing per sample, **D2 static-low**, and **D14** toggling during drain. These match
  the SAMPLE-confirmed facts (D2 static-low; D14 the one Cyclone-controllable SRAM clock).
- **Scope UI:** the rendered triangle on `/api/screen.png` is stable, triggered, correct ptp and
  timebase — i.e. the instrument behaves like a scope on our fabric.

---

## 6. Recovery (unbrickable)

CRAM is volatile; the factory bitstream lives in flash. Any hang, wedge, or "make it factory again":

```
otactl -tcp 192.168.1.209:5900 untakeover          # graceful: hand back to the factory app
# hard reset (last resort / on any WAIT-line hang):
otactl -shelly 192.168.1.223 power cycle            # mains cycle → factory bitstream reloads
```
Safety rules preserved throughout: **never write CS3 0x07** (nCONFIG); the app owns the arm/drain
sequencing so we never read the frozen-record BURST port before a proven-coherent halt; the debug pokes
only touch unused CS1 selectors. A power-cycle always returns the vendor scope.

---

## 7. Record the tuned knob set here after a PASS

Fill in the winning values so the next boot can re-apply them (CRAM-volatile → set after every load):

```
DBG_WDIV    (0x48) = ____      # clkdiv
DBG_WPHASE  (0x4c) = ____      # we_phase | load_phase<<4
DBG_WSTROBE (0x68) = ____      # load_sel | we_sel<<4 | low_mask<<8   → WE#=___ load#=___ CS#=___
DBG_WMISC   (0x6c) = ____      # eng_enable=1, d2_wr=___
DBG_RDDIV   (0x0c) = 25        # (proven; change only if drain needs it)
maps               = identity  # unless Stage D remap was needed: ____
```
Once frozen, these can be baked into the `capsram.v` RTL defaults and rebuilt for a knob-free image
(out of scope for this validation run — RTL currently ships the defaults in §1).

---

## 8. One-glance summary

| step | action | PASS check |
|------|--------|-----------|
| deploy | swap rbf, `make app-release`, stage, takeover, app start | agent log `loaded and verified 0xc2f6eb5f`; `rd 1 0x00 = 0x5CA0` |
| sweep | `RUN.sh sweep` over DBG_WSTROBE/WDIV/WPHASE (Stages A–B) | `/api/status`: `coherent`↑, `valid_depth`≈`cols`, `last_ptp`=triangle |
| readback | `/api/frame.bin?raw=1` | clean full-depth triangle, ptp matches |
| prove-SRAM | toggle `eng_enable` 0→1 | triangle vanishes at 0, returns at 1 |
| scope | `rd 1 0x7c`, `/api/screen.png`, optional LA | FSM cycles; balls behave; trace correct |
| restore | `untakeover` / Shelly cycle | factory scope back |

**PASS = all rows green = our fabric captured the triangle into the external SRAM and drained it
coherently at full depth through our unmodified app.**
</content>
</invoke>

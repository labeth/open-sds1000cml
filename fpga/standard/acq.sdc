# =============================================================================
# acq.sdc -- Synopsys Design Constraints for the owned acquisition bitstream
# Device: Altera Cyclone IV E  EP4CE10F17C8   Top: acq   (Quartus 21.1 TimeQuest)
#
# WHY THIS FILE EXISTS
#   The project shipped with NO .sdc, so every prior build hit:
#     Critical Warning (332012): ... 'acq.sdc' ... not found ...
#     Info (332142): No user constrained ... Calling "derive_pll_clocks -create_base_clocks"
#     Info (332105): create_clock -period 1.000 -name clk clk      <-- 1 GHz(!) on the C2 fabric
#   i.e. TimeQuest timed the entire 80 MHz fabric against a bogus 1.000 ns (1 GHz)
#   auto-clock (worst setup slack -16.433 ns, TNS -23355 ns, all on `clk`), and it
#   left the two async oscillator domains (C2 fabric vs the M2/PLL tree) with NO
#   clock-group cut -- so the interleave 200 MHz write paths and the eth_gearbox
#   200->80 CDC were never analyzed against a real requirement. This file supplies
#   the real clocks, the async cuts, and reasonable I/O budgets.
#
# CLOCK INVENTORY (see acq.v / acq.qsf)
#   clk       ball C2  ~80 MHz  free-running fabric reference (main GPMC/datapath domain)
#   mclk_in   ball M2  160 MHz  dedicated PLL-INCLK; reference for BOTH PLLs
#   u_pll200  (inclk=mclk_in) VCO=160*5=800; /4 -> 200 MHz x5 phases:
#             clk[0]=0  clk[1]=120  clk[2]=240  clk[3]=180  clk[4]=90(=cap_clk200)
#   u_m2pll   (inclk=mclk_in) VCO=160*5=800; clk[0]=/2=400 MHz (lock heartbeat),
#             clk[1]=160@90  clk[2]=160@180  clk[3]=160@270  (cap_clk phase source)
#   gpmc_vclk VIRTUAL clock modelling the off-chip AM3352 GPMC controller (I/O only)
#
# DOMAIN / CDC MODEL  (the crux of correct STA here)
#   Group A = { clk }                              -- the C2 physical oscillator
#   Group B = { mclk_in + all u_pll200 + all u_m2pll clocks }
#             All of Group B derives from the single M2 oscillator, so its members
#             are mutually SYNCHRONOUS (phase-related) and MUST be timed together --
#             that is exactly the 200 MHz multi-phase interleave write timing we want
#             proven. Group A (C2) and Group B (M2) are SEPARATE physical crystals
#             => genuinely ASYNCHRONOUS. The one async cut A<->B simultaneously and
#             correctly covers EVERY C2<->M2 crossing in the design:
#               * eth_gearbox    200 MHz wr_clk (pll200 clk[0]) -> 80 MHz clk  read
#               * cbuf ring      cap_clk (m2pll/mclk) write     -> clk read
#               * il_capture     cap_clk200 (pll200 clk[4]) fill -> clk readout (toggle sync)
#               * m2_ctr/pll_hb/lock heartbeats  (mclk_in / pll_c0) -> clk (2-FF sync)
#   Group C = { gpmc_vclk }                        -- off-chip GPMC controller, async
#             The GPMC bus is an asynchronous SRAM-style slave: nCS1/nOE/nWE, sel[],
#             and gpmc_d(in) are multi-flop synchronized into clk (acq.v ~L104), so
#             their port->register paths are a DELIBERATE CDC and are correctly cut
#             by the A/C async grouping. The set_input/output_delay lines below are
#             the board/controller interface budget (documentation + IOB guidance);
#             actual capture is strobe-timed by the AM3352 (multi-GPMCFCLK access,
#             HW-verified byte-exact), which dwarfs any FPGA IOB delay.
# =============================================================================

# ---------------------------------------------------------------------------
# 1) BASE CLOCKS
# ---------------------------------------------------------------------------
# C2 fabric ~80 MHz -> 12.5 ns (slightly conservative vs the measured ~80 MHz).
create_clock -name clk     -period 12.500 -waveform {0.000 6.250}  [get_ports clk]
# M2 dedicated PLL-INCLK 160 MHz -> 6.25 ns (matches the megafunction inclk param,
# 6250 ps, and the value Quartus auto-derived when unconstrained).
create_clock -name mclk_in -period 6.250  -waveform {0.000 3.125}  [get_ports mclk_in]

# Virtual clock for the asynchronous GPMC slave interface (off-chip AM3352).
# Period ~ one GPMCFCLK (100 MHz) tick; access cycles are multiples of this.
create_clock -name gpmc_vclk -period 10.000

# ---------------------------------------------------------------------------
# 2) PLL-GENERATED CLOCKS
# ---------------------------------------------------------------------------
# Auto-creates all u_pll200 (5x200 MHz phases) and u_m2pll (400 + 3x160 MHz phases)
# generated clocks, sourced from the mclk_in base clock above. Names produced:
#   u_pll200|auto_generated|pll1|clk[0..4]   u_m2pll|auto_generated|pll1|clk[0..3]
derive_pll_clocks

# ---------------------------------------------------------------------------
# 3) CLOCK GROUPS  (asynchronous domain cuts)  -- see DOMAIN / CDC MODEL above
# ---------------------------------------------------------------------------
# Wildcard "clk*" matches the bus-indexed phase names clk[0]..clk[N] robustly
# (avoids the get_clocks bracket-as-charclass pitfall).
set_clock_groups -asynchronous \
    -group [get_clocks { clk }] \
    -group [get_clocks { mclk_in \
                         u_pll200|auto_generated|pll1|clk* \
                         u_m2pll|auto_generated|pll1|clk* }] \
    -group [get_clocks { gpmc_vclk }]

# ---------------------------------------------------------------------------
# 4) CLOCK UNCERTAINTY
# ---------------------------------------------------------------------------
derive_clock_uncertainty

# ---------------------------------------------------------------------------
# 5) ASYNCHRONOUS INPUTS  (not part of the GPMC bus)
# ---------------------------------------------------------------------------
# ADC return data: the AD9288 lanes are sampled with an ENCODE we drive but their
# valid window is BENCH-cal (per-core PLL phase + trim), not an STA relationship.
# They fan out to clk (adc_lane_q) AND to the pll200 phase captures (c1a..c2b);
# a -from cut covers every capture clock. Alignment is a bench knob, not timing.
set_false_path -from [get_ports {adc_lane[*]}]
# HW trigger comparator (ball A12): free-running async, 3-FF synchronized (trig_q).
set_false_path -from [get_ports {trig_sense}]

# ---------------------------------------------------------------------------
# 6) ASYNCHRONOUS OUTPUTS  (ADC drive -- bench-cal, not STA-closed)
# ---------------------------------------------------------------------------
# ENCODE clocks / mode holds to the AD9288s. Their phase to the converter is the
# bench-cracked ADC-drive recipe (docs/aux-bus-re.md), calibrated on the bench;
# there is no synchronous capture on our side to constrain them against.
set_false_path -to [get_ports {adc_enc[*] adc_enc2[*] adc_enc3 adc_ctl_hi[*] adc_ctl_lo[*]}]

# ---------------------------------------------------------------------------
# 7) GPMC I/O BUDGET  (interface documentation; internal paths cut in section 3)
# ---------------------------------------------------------------------------
# Reasonable board+controller budgets vs gpmc_vclk. These bound the IOB/board
# path for documentation and placement; the port->synchronizer register paths are
# an intentional CDC (cut by the gpmc_vclk/clk async grouping) -- the AM3352 GPMC
# access width (tens of ns, multi-GPMCFCLK) provides the real setup/hold margin.
set input_ports  [get_ports {nCS1 nOE nWE sel[*] gpmc_d[*]}]
set output_ports [get_ports {gpmc_d[*] gpmc_wait}]
set_input_delay  -clock gpmc_vclk -max 6.0 $input_ports
set_input_delay  -clock gpmc_vclk -min 1.0 $input_ports
set_output_delay -clock gpmc_vclk -max 5.0 $output_ports
set_output_delay -clock gpmc_vclk -min 1.0 $output_ports

# =============================================================================
# NO multicycle constraints are added: every intra-Group-B transfer is a REAL,
# design-intended (sub-)cycle phase relationship (phase-matched interleave
# capture) that must be analyzed as-is, and every cross-domain transfer is an
# async CDC already cut in section 3. Relaxing either with a multicycle would
# either hide a genuine 200 MHz timing risk or misstate the design intent.
# =============================================================================

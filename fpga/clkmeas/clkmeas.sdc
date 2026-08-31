# =============================================================================
# clkmeas.sdc -- constraints for the absolute clock-measurement fabric.
# Device: Cyclone IV E EP4CE10F17C8   Top: clkmeas   (Quartus 21.1 TimeQuest)
#
# WHY THE CONSTRAINTS ARE WRITTEN THE WAY THEY ARE
#   This design's whole purpose is to count edges on clocks whose frequency is
#   UNKNOWN -- that is the blocker (CB-4). So the periods below are not facts;
#   they are the CLAIM each counter is asked to support. A counter that closes
#   at period P is trustworthy for any input up to 1/P and untrustworthy above
#   it, and an over-clocked counter does not error, it UNDERCOUNTS -- which is
#   indistinguishable from a slower clock in the result. Read the per-clock
#   Fmax out of the STA report with the result and quote the validity ceiling.
#
#   * clk (C2): 10.000 ns. The candidate family runs 42 / 80 / 83.33 / 100 MHz;
#     100 MHz is the top of it. acq.sdc uses 12.5 ns (80 MHz) -- this file is
#     deliberately tighter so a C2 of 100 MHz is covered.
#   * mclk_in (M2): 6.250 ns, matching both acq.sdc and the altpll megafunction
#     parameter (inclk0_input_frequency = 6250 ps). The corpus's own upper
#     candidate for M2 is 266 MHz; that is NOT constrained here on purpose,
#     because derive_pll_clocks would then propagate 332 MHz into the PLL taps.
#     The mclk_in Fmax number in the STA report is what settles the ceiling.
#   * the ten unknown balls: 10.000 ns each. If any of them is live and faster
#     than 100 MHz its count is not trustworthy -- but the same reading also
#     immediately re-opens CLK-8, so it is a result either way.
#   * trig_sense (A12): 100.000 ns. The comparator rolls off above ~1 MHz
#     (bench-measured), so 10 MHz is a 10x margin on the gate logic.
#
# DOMAIN MODEL
#   Every counted ball is its own physical source until proven otherwise --
#   that is the open question, so nothing may be assumed synchronous. Each gets
#   its own asynchronous clock group. The ONLY intra-group relationships are
#   inside the M2 group (mclk_in and both PLLs derive from the same reference).
#   Every path that actually crosses a group boundary in this design is a 2-FF
#   toggle synchroniser and MUST be cut; there is no data path across domains
#   at all -- captured counter values never leave their own domain.
# =============================================================================

# ---------------------------------------------------------------------------
# 1) BASE CLOCKS
# ---------------------------------------------------------------------------
create_clock -name c2_clk  -period 10.000 [get_ports clk]
create_clock -name m2_clk  -period  6.250 [get_ports mclk_in]

create_clock -name e1_clk  -period 10.000 [get_ports ball_e1]
create_clock -name m1_clk  -period 10.000 [get_ports ball_m1]
create_clock -name e15_clk -period 10.000 [get_ports ball_e15]
create_clock -name e16_clk -period 10.000 [get_ports ball_e16]
create_clock -name m15_clk -period 10.000 [get_ports ball_m15]
create_clock -name m16_clk -period 10.000 [get_ports ball_m16]

create_clock -name k2_clk  -period 10.000 [get_ports ball_k2]
create_clock -name r4_clk  -period 10.000 [get_ports ball_r4]
create_clock -name f2_clk  -period 10.000 [get_ports ball_f2]
create_clock -name j2_clk  -period 10.000 [get_ports ball_j2]

create_clock -name a12_clk -period 100.000 [get_ports trig_sense]

# ---------------------------------------------------------------------------
# 2) PLL-GENERATED CLOCKS
# ---------------------------------------------------------------------------
# u_m2pll  clk4 = mclk_in x 5/80  (the counted f_M2/16 cross-check tap)
# u_pll200 clk0 = mclk_in x 5/4   (the counted second M2 witness)
derive_pll_clocks

# ---------------------------------------------------------------------------
# 3) CLOCK GROUPS -- one asynchronous group per physical source
# ---------------------------------------------------------------------------
set_clock_groups -asynchronous \
    -group [get_clocks { c2_clk }] \
    -group [get_clocks { m2_clk \
                         u_m2pll|auto_generated|pll1|clk* \
                         u_pll200|auto_generated|pll1|clk* }] \
    -group [get_clocks { e1_clk }]  \
    -group [get_clocks { m1_clk }]  \
    -group [get_clocks { e15_clk }] \
    -group [get_clocks { e16_clk }] \
    -group [get_clocks { m15_clk }] \
    -group [get_clocks { m16_clk }] \
    -group [get_clocks { k2_clk }]  \
    -group [get_clocks { r4_clk }]  \
    -group [get_clocks { f2_clk }]  \
    -group [get_clocks { j2_clk }]  \
    -group [get_clocks { a12_clk }]

# ---------------------------------------------------------------------------
# 4) CLOCK UNCERTAINTY
# ---------------------------------------------------------------------------
derive_clock_uncertainty

# ---------------------------------------------------------------------------
# 5) THE GPMC INTERFACE
# ---------------------------------------------------------------------------
# The GPMC bus is an asynchronous SRAM-style slave. nCS1 / nWE / sel_hi and the
# write half of gpmc_d are multi-flop synchronised into c2_clk, so every
# port -> register path is a DELIBERATE CDC and is cut here rather than being
# timed against a fictional controller clock.
set_false_path -from [get_ports {nCS1 nOE nWE sel_hi[*] gpmc_d[*]}] -to [all_registers]

# The READ path is what the bus actually has to meet, and it is purely
# combinational: selector + strobes -> the read mux -> the tri-state driver.
# Constrain it as an access time rather than inventing a virtual clock. 25 ns is
# a deliberately conservative budget against the AM3352's multi-GPMCFCLK access
# (GPMCFCLK is 100 MHz and the shipped CONFIG programs several ticks of it).
#
# The -to form (not -from) is deliberate and was corrected after the first build:
# a -from form covering only the ports left 1115 register -> gpmc_d paths
# UNCONSTRAINED (the shadow registers and the status/index registers feed the same
# combinational mux), and an unconstrained read path is exactly the kind of hole
# that lets a build be reported as "timing closed" when the thing the host
# actually depends on was never analysed. Constraining the endpoint covers every
# launch point at once.
set_max_delay -to [get_ports {gpmc_d[*]}] 25.000
set_min_delay -to [get_ports {gpmc_d[*]}]  0.000

# gpmc_wait is tied to a constant (held ready, never wedge the bus).
set_false_path -to [get_ports {gpmc_wait}]

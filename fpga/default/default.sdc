# default.sdc -- timing constraints for the acq2 default image (top default_top, EP4CE10F17C8).
#
# THREE ASYNCHRONOUS CLOCK GROUPS (05-WORKPLAN s3; shape after owned-fpga acq.sdc, e2932c3):
#   A  clk        ball C2, the GPMC/register domain (~80 MHz, unmeasured; 12.5 ns is conservative)
#   B  mclk_in    ball M2 (~160 MHz) and EVERYTHING the two PLLs derive from it: the 200 MHz
#                 encode clock ph0, the 200 MHz capture clock, 100 MHz, 50 MHz. Members of B are
#                 phase-related and are timed against each other (the capture write, the 200 MHz
#                 LANEMAP pipeline, the clk50 bus registers all live here).
#   C  gpmc_vclk  virtual clock modelling the off-chip AM3352 GPMC controller (I/O budget only).
# Every A<->B crossing goes through sync_bit / sync_pulse / sync_word / the dual-clock M9Ks
# (default.v header) and is cut by the group declaration.
#
# Inside group B two things are NOT static and are cut explicitly:
#   * the capture clock is dynamically phase-shiftable (the pll_m2 step engine; its CAP_STEP input
#     is held at 0 since v2.2 -- PHASE_SEL/CAP_STEP retired, 06-TIERS s1.7), and since v2.2 every
#     encode pair is clocked by ph0 itself (adc_front.v: the v2.1 4:1 LUT phase mux is gone, so
#     PLL A c1..c3 have no load and derive_pll_clocks creates no clock for them -- they are not
#     in the member list below). The only logic that crosses between cap_clk and ph0 is the
#     2-flop sync g_pair[*].s1/s2 -- cut at s1.
#   * the snapshot clock is a 4:1 LUT mux (clk50/clk100/cap_clk/clk); its inputs are 2-flop synced.
#   A LUT clock mux makes the Timing Analyzer see EVERY mux input as a clock of the registers
#   behind it, so it times launch-on-clock-X / latch-on-clock-Y pairs that can never happen
#   (fit iteration 4: -8.2 ns "clk50 -> cap_clk" inside the snapshot writer, -3.3 ns "ph0 -> ph90"
#   inside one encode pair). Every register transfer between two different members of the PLL
#   group goes through a sync_bit / sync_pulse / sync_word (cut below at the first stage), so
#   the cross-member pairs are declared false paths pairwise; the same-clock paths through each
#   mux stay timed at that clock's own period (5 ns for cap_clk / the phases).
# The ADC lanes are calibrated, not STA-closed: false path from the lane ports (v2.2 keeps the
# v2.1 reset alignment: PHASE_SEL = 0, CAP_STEP = 0).

# ---- base clocks ----------------------------------------------------------------
create_clock -name clk       -period 12.500 -waveform {0.000 6.250} [get_ports clk]
create_clock -name mclk_in   -period 6.250  -waveform {0.000 3.125} [get_ports mclk_in]
create_clock -name gpmc_vclk -period 10.000

# ---- PLL outputs (u_pll|u_pll_a: c0 = ph0 (200 MHz encode), c1..c3 unloaded, c4 = capture clock;
#      u_pll|u_pll_b: c0 = 100 MHz, c1 = 50 MHz) ----
derive_pll_clocks
derive_clock_uncertainty

# ---- asynchronous groups ----------------------------------------------------------
set_clock_groups -asynchronous \
    -group [get_clocks {clk}] \
    -group [get_clocks {mclk_in *u_pll_a* *u_pll_b*}] \
    -group [get_clocks {gpmc_vclk}]

# ---- PLL-group members that meet only through LUT clock muxes or synchronizers ------------
#   ph0 = u_pll_a clk[0], cap_clk = u_pll_a clk[4], clk100/clk50 = u_pll_b clk[0..1]
#   (clk[1..3] = ph90/180/270 have no load since v2.2; listing them only produced "empty
#   collection" warnings -- add them back here if a phase counter gets a load again)
set pll_members [list \
    {*u_pll_a*clk[0]} {*u_pll_a*clk[4]} \
    {*u_pll_b*clk[0]} {*u_pll_b*clk[1]}]
foreach a $pll_members {
    foreach b $pll_members {
        if {$a ne $b} {
            set_false_path -from [get_clocks $a] -to [get_clocks $b]
        }
    }
}

# ---- GPMC I/O budget (documentation of the board interface; the strobes are synchronized
#      in gpmc_slave.v and the real margin is the AM3352 multi-GPMCFCLK access width) ----
set gpmc_in  [get_ports {nCS1 nOE nWE sel[*] gpmc_d[*] gpmc_a2 gpmc_b1}]
set gpmc_out [get_ports {gpmc_d[*]}]
set_input_delay  -clock gpmc_vclk -max 6.0 $gpmc_in
set_input_delay  -clock gpmc_vclk -min 1.0 $gpmc_in
set_output_delay -clock gpmc_vclk -max 5.0 $gpmc_out
set_output_delay -clock gpmc_vclk -min 1.0 $gpmc_out
# The asynchronous READ is a combinational pad-to-pad path: {nCS1, nOE, sel, A2, B1} -> read mux
# -> gpmc_d (gpmc_slave.v: data is driven while nCS1&nOE are low, from the LIVE 7-bit selector;
# since schema v3 balls A2/B1 are selector bits 1/0 and sit in the same path). Against the
# 10 ns virtual clock that path can never close (fit iteration 4: -16.0 ns, 15.0 ns of logic).
# Its real requirement is the GPMC read access time: factory CS1 timing (fpga-specs/10 s4.3) is
# OEONTIME = 4, RDACCESSTIME = 13 GPMC_FCLK ticks (100 MHz), i.e. the AM3352 samples gpmc_d
# 90 ns after nOE falls (130 ns after the address). The budget below (30 ns = 3 ticks, incl. the
# 6 + 5 ns external delays above) leaves the app free to shorten RDACCESSTIME to OEONTIME + 3
# and is the number a faster GPMC configuration (WP2) must respect; the prior treated the same
# path as an "async-I/O modelling artifact" and shipped with the violation (03-PRIOR-ART).
set_max_delay -from [get_ports {nCS1 nOE sel[*] gpmc_a2 gpmc_b1}] -to $gpmc_out 30.0

# ---- asynchronous / bench-calibrated pads --------------------------------------------
# ADC lanes: valid window calibrated (reset alignment in v2.2), not by STA.
set_false_path -from [get_ports {lane[*]}]
# comparator, MAX-V mirror, unknown-function inputs, the bus and singles when read back
set_false_path -from [get_ports {trig_sense p6 d1 bus[*] single[*]}]
# static levels, forwarded clocks and the bus/singles drive (no synchronous receiver on our side)
set_false_path -to [get_ports {enc_p[*] enc_n[*] a11 f1 g1 g2 k1 d1 d2 k2 f2 j2 bus[*] single[*]}]

# ---- synchronizer first stages (metastability filters; the source is another clock) -----
set_false_path -to [get_registers {*|meta[*]}]                  ;# sync_bit (W=1 is still meta[0])
set_false_path -to [get_registers {*|s[0]}]                     ;# sync_pulse
set_false_path -to [get_registers {*|req_s[0] *|ack_s[0]}]      ;# sync_word handshake
set_false_path -to [get_registers {*g_pair[*].s1[*]}]           ;# encode pair re-timing (generate scope = '.')
set_false_path -to [get_registers {*|u_hw|q[0]}]                ;# A12 stretch3
# sync_word payload: hold is written by the source and read by the destination only after the
# handshake, so the hold -> q_dst path is safe by protocol.
set_false_path -from [get_registers {*|hold[*]}] -to [get_registers {*|q_dst[*]}]

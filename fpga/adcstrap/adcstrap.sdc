# =============================================================================
# adcstrap.sdc -- constraints for the ADC static-control strap kit (EP4CE10F17C8)
#
# WHY A PERIOD OF 10.000 ns FOR A "~80 MHz" CLOCK
#   Ball C2 has NEVER been edge-counted. The corpus's three arithmetic paths give
#   ~80, ~100 and ~42 MHz, and closing that is takeover Phase 1 step 1.4
#   (`18` C1.1-C1.3). Until it lands, constraining at the NOMINAL 12.5 ns would
#   report closure this design might not actually have if C2 is the 100 MHz
#   candidate. So this file constrains the FASTEST candidate, 100 MHz / 10.000 ns:
#   closure here is closure at every candidate. Do not relax it to 12.5 to buy
#   slack -- report the failure instead (that is the finding).
#
# DOMAINS
#   clk        ball C2, the only real clock in the design; the whole datapath, the
#              GPMC slave and the ENCODE divider live in it.
#   gpmc_vclk  VIRTUAL clock modelling the off-chip AM3352 GPMC controller. The
#              GPMC bus is an asynchronous SRAM-style interface: nCS1/nOE/nWE, sel[]
#              and gpmc_d(in) are multi-flop synchronized into clk, so their
#              port->register paths are a DELIBERATE CDC and are cut below.
#   The ADC data lanes are inputs whose launch edge is the ENCODE we generate, but
#   the converter's output delay is unmeasured and the capture is deliberately
#   registered-then-decimated (the recorder is a statistical instrument, not a
#   source-synchronous receiver), so they are cut too. Anything that depends on ADC
#   setup/hold is out of this kit's scope by construction.
#
# There is no PLL in this kit. That is deliberate: the S2.1/S2.2 measurements do
# not need one, and a PLL-free kit is one fewer thing to blame for a null result.
# =============================================================================

create_clock -name clk       -period 10.000 -waveform {0.000 5.000} [get_ports clk]
create_clock -name gpmc_vclk -period 10.000

derive_clock_uncertainty

# clk (C2 crystal) and the AM3352 GPMC controller are genuinely asynchronous.
set_clock_groups -asynchronous -group {clk} -group {gpmc_vclk}

# ---- asynchronous / deliberately-cut boundaries -----------------------------
set_false_path -from [get_ports {nCS1 nOE nWE}]
set_false_path -from [get_ports {sel[*]}]
set_false_path -from [get_ports {adc_lane[*]}]
set_false_path -from [get_ports {gpmc_d[*]}]
set_false_path -to   [get_ports {gpmc_d[*]}]
set_false_path -to   [get_ports {gpmc_wait}]

# The ENCODE outputs are a clock we generate for the converters; their timing
# requirement is a frequency, not a data-path arrival, and there is no return path.
set_false_path -to [get_ports {adc_enc[*] adc_enc_c14 adc_enc_d14}]

# The seven static-control balls are DC levels held for milliseconds at a time.
set_false_path -to [get_ports {strap_f1 strap_g1 strap_g2 strap_k1}]

# In the MAIN build L4/T2/T7 are outputs; in the two pre-check builds they are
# tri-stated inputs. Constrain whichever exists (SDC is Tcl).
foreach p {strap_l4 strap_t2 strap_t7} {
    set op [get_ports -nowarn $p]
    if {[get_collection_size $op] > 0} {
        catch { set_false_path -from $op }
        catch { set_false_path -to   $op }
    }
}

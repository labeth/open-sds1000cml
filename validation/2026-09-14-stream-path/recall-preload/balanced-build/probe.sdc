create_clock -name core -period 4.0 [get_ports core_clk]
create_clock -name sample -period 4.0 -waveform {0.4 2.4} [get_ports sample_clk]
create_clock -name ram -period 8.0 [get_ports ram_clk]
create_clock -name host -period 10.0 [get_ports host_clk]
create_clock -name adc_ref -period 10.0 [get_ports clk100]
derive_pll_clocks
derive_clock_uncertainty
set_false_path -from [get_ports reset]
source stream_path_cdc.sdc
source acquisition_config_cdc.sdc

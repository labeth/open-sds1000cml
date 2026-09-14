create_clock -name core -period 4.0 [get_ports core_clk]
create_clock -name ram -period 8.0 [get_ports ram_clk]
create_clock -name host -period 10.0 [get_ports host_clk]
derive_clock_uncertainty
set_false_path -from [get_ports reset]
source host_path_cdc.sdc

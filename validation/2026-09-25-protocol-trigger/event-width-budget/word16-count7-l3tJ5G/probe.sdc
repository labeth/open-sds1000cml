create_clock -name receiver -period 8.0 [get_ports source_clk]
create_clock -name cpu -period 12.5 [get_ports host_clk]
derive_clock_uncertainty
set_max_delay 8.0 -from [get_clocks receiver] -to [get_clocks cpu]
set_max_delay 8.0 -from [get_clocks cpu] -to [get_clocks receiver]
set_min_delay 0.0 -from [get_clocks receiver] -to [get_clocks cpu]
set_min_delay 0.0 -from [get_clocks cpu] -to [get_clocks receiver]
set_false_path -to [get_registers {*events*request_sync[0] *events*ack_sync[0]}]

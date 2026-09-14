create_clock -name ram_write -period 8.0 [get_ports write_clk]
create_clock -name host_read -period 10.0 [get_ports read_clk]
derive_clock_uncertainty

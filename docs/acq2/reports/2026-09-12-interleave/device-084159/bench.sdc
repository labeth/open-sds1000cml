create_clock -name mref -period 10.0 [get_ports mclk_in]
create_clock -name cpu -period 12.5 [get_ports clk]
derive_pll_clocks
derive_clock_uncertainty
set_clock_groups -asynchronous -group [get_clocks cpu] -group [get_clocks {*pll* mref}]
# External SRAM timing is measured separately; do not interpret core slack as board closure.
# Synchronizer first-stage paths only; data clocks remain timed.
set_false_path -to [get_registers {*enable_s[0] *overflow_s[0] *snap_req_s[0] *snap_ack_s[0]}]
# Snapshot payload is held before a three-stage acknowledgement reaches memory clock.
set_max_delay 8.0 -from [get_registers {*snap_payload[*]}] -to [get_registers {snapshot[*]}]
# DCFIFO read/write ACLR synchronizers assert asynchronously; only their reset-source paths are excepted. Internal release and data paths remain timed.
set_false_path -from [get_registers {*record|running}] -to [get_registers {*wraclr* *rdaclr*}]

create_clock -name mref -period 10.0 [get_ports mclk_in]
create_clock -name cpu -period 12.5 [get_ports clk]
derive_pll_clocks
derive_clock_uncertainty
# CPU and acquisition clocks have no phase relationship. Bound crossing data
# routing instead of cutting every path: a broad clock-group exception would
# also hide the held decoded-event payload and epoch bus. Mailbox controls use
# synchronizers and payloads remain stable through their handshake latency.
set_max_delay 8.0 -from [get_clocks cpu] -to [get_clocks {*pll* mref}]
set_max_delay 8.0 -from [get_clocks {*pll* mref}] -to [get_clocks cpu]
set_min_delay 0.0 -from [get_clocks cpu] -to [get_clocks {*pll* mref}]
set_min_delay 0.0 -from [get_clocks {*pll* mref}] -to [get_clocks cpu]
# Only the first metastability-catching stage is excepted. Subsequent stages
# retain their normal destination-clock timing checks.
set_false_path -to [get_registers {*events*request_sync[0] *events*ack_sync[0] event_run_sync[0] event_core_sync[0]}]
# External SRAM timing is measured separately; do not interpret core slack as board closure.
# Synchronizer first-stage paths only; data clocks remain timed.
set_false_path -to [get_registers {*enable_s[0] *overflow_s[0] *snap_req_s[0] *snap_ack_s[0]}]
# Snapshot payload is held before a three-stage acknowledgement reaches memory clock.
set_max_delay 8.0 -from [get_registers {*snap_payload[*]}] -to [get_registers {snapshot[*]}]
# DCFIFO read/write ACLR synchronizers assert asynchronously; only their reset-source paths are excepted. Internal release and data paths remain timed.
set_false_path -from [get_registers {frontend_run}] -to [get_registers {*wraclr* *rdaclr*}]
# Precision pipeline FIFO reset assertion, releases are internally synchronized.
set_false_path -from [get_registers {precision_enable}] -to [get_registers {*precision*wraclr* *precision*rdaclr*}]
# Common precision reset asserts asynchronously. The config-domain shift
# registers release locally over three clocks; only the reset source is cut.
set_false_path -from [get_registers {precision_enable}] -to [get_registers {*precision*enable_s[*] *precision*pack_enable[*]}]

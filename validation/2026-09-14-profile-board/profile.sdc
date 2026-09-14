create_clock -name mref -period 10.0 [get_ports mclk_in]
create_clock -name host -period 10.0 [get_ports clk]
derive_pll_clocks
derive_clock_uncertainty
set enable_stream 0
source stream_path_cdc.sdc
source acquisition_config_cdc.sdc
# Existing board GPMC combinational access budget; pad delays remain unqualified.
set_max_delay 30.0 -from [get_ports {nCS1 nOE sel[*] gpmc_a2 gpmc_b1}] -to [get_ports {gpmc_d[*]}]
# Held command/status bundles cross behind synchronized request tokens.
foreach instance {bridge response} {
 set held [get_registers "*commands|${instance}|held_payload*"]
 set sampled [get_registers "*commands|${instance}|core_payload*"]
 if {[get_collection_size $held]==0 || [get_collection_size $sampled]==0} {error "Missing command mailbox payload"}
 set_max_delay 6.0 -from $held -to $sampled
 set_min_delay 0.0 -from $held -to $sampled
 foreach name {request_sync ack_sync} {
  set first [get_registers [format {*commands|%s|%s[0]} $instance $name]]
  if {[get_collection_size $first]!=1} {error "Missing command mailbox synchronizer"}
  set_max_delay 4.0 -to $first
  set_min_delay 0.0 -to $first
 }
}
set fault_first [get_registers {*profile|acquisition_fault_host[0]}]
if {[get_collection_size $fault_first]!=1} {error "Missing profile fault synchronizer"}
set_max_delay 4.0 -to $fault_first
set_min_delay 0.0 -to $fault_first

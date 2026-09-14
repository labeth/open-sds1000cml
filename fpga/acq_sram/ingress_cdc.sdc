# Common epoch assertion is asynchronous; synchronized local release stays timed.
set_false_path -from [get_ports reset]
# Bundled payload is held until destination consumption is acknowledged.
# Two request synchronizer stages plus capture ensure at least two destination
# periods of settling. 6 ns is below that minimum for the 250 MHz destination.
foreach bridge {incoming outgoing} {
 set sources [get_registers "*${bridge}|payload\[*\]"]
 set targets [get_registers "*${bridge}|dest_data\[*\]"]
 if {[get_collection_size $sources]==0 || [get_collection_size $targets]==0} {error "Missing $bridge mailbox endpoints"}
 set_max_delay 6.0 -from $sources -to $targets
 set_min_delay 0.0 -from $sources -to $targets
}
# Do not apply a broad asynchronous clock cut. Token synchronizer first stages
# are bounded physically as well; later stages retain ordinary clock timing.
set ingress_tokens [get_registers {*|request_sync[0] *|ack_sync[0] *|queue_fault_sync[0]}]
if {[get_collection_size $ingress_tokens]<5} {error "Missing ingress token synchronizers"}
set_max_delay 4.0 -to $ingress_tokens
set_min_delay 0.0 -to $ingress_tokens

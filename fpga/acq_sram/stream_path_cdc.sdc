# Scope: sram_stream_path, containing host and ingress instances.
# Only common external reset assertion is false-pathed. Local release is timed.
set_false_path -from [get_ports reset]
proc host_checked_registers {pattern expected} {
 set points [get_registers $pattern]
 if {[get_collection_size $points]!=$expected} {error "Host CDC endpoints $pattern: expected $expected, got [get_collection_size $points]"}
 return $points
}
set host_slots [host_checked_registers {*fifo|slots*} 640]
set host_fifo_output [host_checked_registers {*fifo|dest_data*} 80]
# FIFO slot held until the acknowledged read; write Gray pointer crosses two
# stages and empty detection before prefetch. Same bound as original FIFO.
set_max_delay 6.0 -from $host_slots -to $host_fifo_output
set_min_delay 0.0 -from $host_slots -to $host_fifo_output
foreach name {wgray_meta rgray_meta} {
 set points [host_checked_registers "*fifo|${name}*" 4]
 set_max_delay 4.0 -to $points
 set_min_delay 0.0 -to $points
}
# Ownership payload held until release. Two payload stages precede visibility
# through three token stages. Bound first payload stage rather than cutting it.
foreach bank {0 1} {
 set held [host_checked_registers "*ownership|held${bank}*" 76]
 set meta [host_checked_registers "*ownership|meta${bank}*" 76]
 set_max_delay 6.0 -from $held -to $meta
 set_min_delay 0.0 -from $held -to $meta
}
foreach name {ack1 pub1 rel1} {
 set points [host_checked_registers "*ownership|${name}*" 2]
 # Bound the asynchronous token's D input, not the local reset input.
 # Whole-register exceptions also change recovery requirements on clrn.
 set data_pins [get_pins -compatibility_mode "*|ownership|${name}*|d"]
 if {[get_collection_size $data_pins]!=2} {error "Ownership token data pins $name"}
 set_max_delay 4.0 -to $data_pins
 set_min_delay 0.0 -to $data_pins
}
# Sticky fault crossings: only first synchronizer stage gets an override.
foreach name {source_bad_ram ram_bad_core bad_host} {
 set pattern [format {*host|%s[0]} $name]
 set points [host_checked_registers $pattern 1]
 set_max_delay 4.0 -to $points
 set_min_delay 0.0 -to $points
}
# Stage-to-stage synchronizer paths remain on ordinary clock timing.
# This file alone is not CDC qualification: all endpoint counts/routes and
# maximum delays must be audited in every supported operating corner.

# Explicit build profile: default preserves the unified/streaming checks.
if {![info exists enable_stream]} {set enable_stream 1}
if {$enable_stream} {
# Controller consumes only sample bits 31:0; marker bits 35:32 are
# optimized away. Netlist inspection verifies exactly the retained 32 bits.
# Ingress mailboxes hold each payload until consumed.
foreach bridge {incoming outgoing} {
 set sources [host_checked_registers "*${bridge}|payload*" 32]
 set targets [host_checked_registers "*${bridge}|dest_data*" 32]
 set_max_delay 6.0 -from $sources -to $targets
 set_min_delay 0.0 -from $sources -to $targets
 foreach name {request_sync ack_sync} {
  set points [host_checked_registers [format {*%s|%s[0]} $bridge $name] 1]
  set_max_delay 4.0 -to $points
  set_min_delay 0.0 -to $points
 }
}
set queue_fault [host_checked_registers {*ingress|queue_fault_sync[0]} 1]
set_max_delay 4.0 -to $queue_fault
set_min_delay 0.0 -to $queue_fault
} else {
 # Deep-only must actually remove the stream mailboxes and ingress FIFO.
 foreach pattern {*incoming|* *outgoing|* *ingress|*} {
  host_checked_registers $pattern 0
 }
}

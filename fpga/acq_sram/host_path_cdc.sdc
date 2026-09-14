# Scope: sram_host_path as top-level; integration must adapt/check hierarchy.
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
 set_max_delay 4.0 -to $points
 set_min_delay 0.0 -to $points
}
# Sticky fault crossings: only first synchronizer stage gets an override.
foreach name {source_bad_ram ram_bad_core bad_host} {
 set pattern [format {%s[0]} $name]
 set points [host_checked_registers $pattern 1]
 set_max_delay 4.0 -to $points
 set_min_delay 0.0 -to $points
}
# Stage-to-stage synchronizer paths remain on ordinary clock timing.
# This file alone is not CDC qualification: all endpoint counts/routes and
# maximum delays must be audited in every supported operating corner.

# Standalone stack_page_transfer scope; check hierarchy when integrating.
# Reset is a common asynchronous epoch abort; local reset release remains timed.
set_false_path -from [get_ports reset]
proc stack_page_endpoints {pattern expected} {
 set points [get_registers $pattern]
 if {[get_collection_size $points]!=$expected} {error "Stack page CDC endpoints $pattern: expected $expected, got [get_collection_size $points]"}
 return $points
}
# Slots cannot change until the read pointer acknowledges their consumption.
set slots [stack_page_endpoints {*fifo|slots*} 640]
set payload [stack_page_endpoints {*fifo|dest_data*} 80]
set_max_delay 6.0 -from $slots -to $payload
set_min_delay 0.0 -from $slots -to $payload
foreach name {wgray_meta rgray_meta} {
 set points [stack_page_endpoints "*fifo|${name}*" 4]
 set_max_delay 4.0 -to $points
 set_min_delay 0.0 -to $points
}
set releases [stack_page_endpoints {release_meta*} 2]
set_max_delay 4.0 -to $releases
set_min_delay 0.0 -to $releases
foreach name {source_bad_memory memory_bad_core} {
 set points [stack_page_endpoints [format {%s[0]} $name] 1]
 set_max_delay 4.0 -to $points
 set_min_delay 0.0 -to $points
}
# Ordinary clock timing applies between synchronizer stages. No whole-domain
# false paths; bound and inspect crossing routes in all operating corners.

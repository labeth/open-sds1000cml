# Standalone stack_record_cache scope; adapt and check hierarchy at integration.
set_false_path -from [get_ports reset]
proc cache_checked_registers {pattern expected} {
 set points [get_registers $pattern]
 if {[get_collection_size $points]!=$expected} {error "Cache CDC endpoints $pattern: expected $expected, got [get_collection_size $points]"}
 return $points
}
set slots [cache_checked_registers {*fifo|slots*} 640]
set payload [cache_checked_registers {*fifo|dest_data*} 80]
set_max_delay 6.0 -from $slots -to $payload
set_min_delay 0.0 -from $slots -to $payload
foreach {name source_clock} {wgray_meta transport rgray_meta memory} {
 set points [cache_checked_registers "*fifo|${name}*" 4]
 set_max_delay 4.0 -from [get_clocks $source_clock] -to $points
 set_min_delay 0.0 -from [get_clocks $source_clock] -to $points
}
set releases [cache_checked_registers {*transfer|release_meta*} 2]
set_max_delay 4.0 -from [get_clocks memory] -to $releases
set_min_delay 0.0 -from [get_clocks memory] -to $releases
foreach {name source_clock} {source_bad_memory transport memory_bad_core memory} {
 set points [cache_checked_registers [format {*transfer|%s[0]} $name] 1]
 set_max_delay 4.0 -from [get_clocks $source_clock] -to $points
 set_min_delay 0.0 -from [get_clocks $source_clock] -to $points
}
# Refill geometry is held until the completion toggle returns. Transport
# snapshots it only after the two-stage request synchronizer has settled.
# Counts target AW=19, CACHE_AW=8. Offset bits 7:0 and 19 are
# constant; bit 8 shares the bank register. Length only needs bits 8:0.
# Geometry changes require rechecking endpoints.
foreach {source target count} {held_start core_start 19 held_bias core_bias 19 held_words core_words 20 refill_offset core_offset 10 refill_length core_length 9 refill_bank core_bank 1 core_failed refill_error 1} {
 set origins [cache_checked_registers $source* $count]
 set targets [cache_checked_registers $target* $count]
 set_max_delay 6.0 -from $origins -to $targets
 set_min_delay 0.0 -from $origins -to $targets
}
# Bound only incoming CDC data, not local reset recovery or other local paths.
# The standalone interface leaves arbitrary data I/O unconstrained, but these
# explicit asynchronous control inputs still receive first-stage route bounds.
foreach {name source_clock input_ports} {request_sync memory {} abort_sync memory {owned frozen raw8} completion_sync transport {} idle_sync {} {transport_ready} grant_sync {} {transport_owned} available_sync transport {transport_owned}} {
 set points [cache_checked_registers [format {%s[0]} $name] 1]
 if {$source_clock ne ""} {
  set_max_delay 4.0 -from [get_clocks $source_clock] -to $points
  set_min_delay 0.0 -from [get_clocks $source_clock] -to $points
 }
 if {$input_ports ne ""} {
  set_max_delay 4.0 -from [get_ports $input_ports] -to $points
  set_min_delay 0.0 -from [get_ports $input_ports] -to $points
 }
}
# Subsequent synchronizer stages and local reset recovery remain clock-timed.

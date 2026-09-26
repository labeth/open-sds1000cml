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
foreach name {wgray_meta rgray_meta} {
 set points [cache_checked_registers "*fifo|${name}*" 4]
 set_max_delay 4.0 -to $points
 set_min_delay 0.0 -to $points
}
set releases [cache_checked_registers {*transfer|release_meta*} 2]
set_max_delay 4.0 -to $releases
set_min_delay 0.0 -to $releases
foreach name {source_bad_memory memory_bad_core} {
 set points [cache_checked_registers [format {*transfer|%s[0]} $name] 1]
 set_max_delay 4.0 -to $points
 set_min_delay 0.0 -to $points
}
# Bundled addresses/results are held through the synchronized toggle handshake.
# Default CACHE_AW=8; endpoint count intentionally fails if geometry changes.
set addresses [cache_checked_registers {*storage|held_word*} 9]
set memory_addresses [cache_checked_registers {*storage|memory_word*} 9]
set_max_delay 6.0 -from $addresses -to $memory_addresses
set_min_delay 0.0 -from $addresses -to $memory_addresses
set results [cache_checked_registers {*storage|held_response*} 32]
set returned [cache_checked_registers {*storage|response_data*} 32]
set_max_delay 6.0 -from $results -to $returned
set_min_delay 0.0 -from $results -to $returned
foreach name {request_sync response_sync} {
 set points [cache_checked_registers [format {*storage|%s[0]} $name] 1]
 set_max_delay 4.0 -to $points
 set_min_delay 0.0 -to $points
}
# Subsequent synchronizer stages remain subject to ordinary clock timing.

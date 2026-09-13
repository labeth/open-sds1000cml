# Verify that every surviving bundled-data endpoint is actually timed. A broad
# asynchronous cut would produce zero paths and fail this audit, not pass it.
project_open bench
create_timing_netlist
read_sdc
set f [open stream_cdc.tsv w]
puts $f "group\tcorner\tendpoints\tminimum_slack_ns\tmaximum_data_delay_ns"
set failures 0
foreach_in_collection corner [get_available_operating_conditions] {
 set_operating_conditions $corner
 update_timing_netlist
 foreach group {descriptor payload} {
  if {$group eq "descriptor"} {
   set sources [get_registers {*stream_queue|base0[*] *stream_queue|base1[*] *stream_queue|count0[*] *stream_queue|count1[*] *stream_queue|single_last[*]}]
   set targets [get_registers {*stream_queue|base0_q[*] *stream_queue|base1_q[*] *stream_queue|count0_q[*] *stream_queue|count1_q[*] *stream_queue|single_q[*]}]
   set minimum_count 100
  } else {
   set sources [get_registers {*stream_pack|payload[*] *stream_pack|payload_valid *stream_pack|payload_single *stream_pack|payload_seal}]
   set targets [get_registers {*stream_pack|packet_data[*] *stream_pack|packet_valid *stream_pack|packet_single *stream_pack|packet_seal}]
   set minimum_count 64
  }
  set count [get_collection_size $targets]
  if {$count < $minimum_count} {error "Missing $group endpoints: $count"}
  set worst_slack 1000.0
  set worst_delay 0.0
  foreach_in_collection target $targets {
   set paths [get_timing_paths -setup -from $sources -to $target -npaths 1]
   if {[get_collection_size $paths] == 0} {
    error "Untimed $group endpoint: [get_node_info -name $target]"
   }
   foreach_in_collection path $paths {
    set slack [get_path_info -slack $path]
    set delay [get_path_info -data_delay $path]
    set worst_slack [expr {min($worst_slack,$slack)}]
    set worst_delay [expr {max($worst_delay,$delay)}]
   }
  }
  puts $f "$group\t[get_operating_conditions_info -display_name $corner]\t$count\t$worst_slack\t$worst_delay"
  # Check physical data delay independently of clock skew in max-delay slack.
  if {$worst_slack < 0 || $worst_delay > 8.0} {incr failures}
 }
}
close $f
if {$failures != 0} {error "Bundled CDC timing failed at $failures corner/group combinations"}
puts "STREAM_CDC_PASS: every bundled-data endpoint timed, physical delay <= 8 ns"

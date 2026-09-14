project_open probe
create_timing_netlist
read_sdc
set f [open cdc.tsv w]
puts $f "bridge\tcorner\tendpoints\tminimum_slack_ns\tmaximum_data_delay_ns"
set failures 0
foreach_in_collection corner [get_available_operating_conditions] {
 set_operating_conditions $corner
 update_timing_netlist
 foreach bridge {incoming outgoing} {
  foreach signal {request_sync ack_sync} {
   set first [get_registers "*${bridge}|${signal}\[0\]"]
   set second [get_registers "*${bridge}|${signal}\[1\]"]
   if {[get_collection_size $first]!=1 || [get_collection_size $second]!=1} {error "Missing $bridge $signal synchronizer stage"}
   set stages [get_timing_paths -setup -from $first -to $second -npaths 1]
   if {[get_collection_size $stages]!=1} {error "Untimed $bridge $signal synchronizer"}
   foreach_in_collection stage $stages {
    if {[get_path_info -slack $stage]<0} {error "Failed $bridge $signal stage timing"}
   }
  }
  set sources [get_registers "*${bridge}|payload\[*\]"]
  set targets [get_registers "*${bridge}|dest_data\[*\]"]
  if {[get_collection_size $sources]!=36 || [get_collection_size $targets]!=36} {error "Missing 36-bit $bridge payload"}
  set slack 1000.0;set delay 0.0
  foreach_in_collection target $targets {
   set paths [get_timing_paths -setup -from $sources -to $target -npaths 1]
   if {[get_collection_size $paths]==0} {error "Untimed $bridge payload endpoint"}
   foreach_in_collection path $paths {
    set slack [expr {min($slack,[get_path_info -slack $path])}]
    set delay [expr {max($delay,[get_path_info -data_delay $path])}]
   }
  }
  puts $f "$bridge\t[get_operating_conditions_info -display_name $corner]\t[get_collection_size $targets]\t$slack\t$delay"
  if {$slack<0 || $delay>6.0} {incr failures}
 }
}
close $f
if {$failures} {error "CDC timing failed: $failures corner/direction cases"}
puts "PASS all 36 payload bits in both directions are timed within 6 ns"

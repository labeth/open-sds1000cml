project_open probe
create_timing_netlist
read_sdc
set f [open cdc.tsv w]
puts $f "group\tcorner\tendpoints\tminimum_slack_ns\tmaximum_data_delay_ns"
proc inspect {f label corner points others from_points limit} {
 set slack 1000.0;set delay 0.0
 foreach_in_collection point $points {
  if {$from_points} {set paths [get_timing_paths -setup -from $point -to $others -npaths 1]} else {set paths [get_timing_paths -setup -from $others -to $point -npaths 1]}
  if {[get_collection_size $paths]!=1} {error "Untimed $label endpoint"}
  foreach_in_collection path $paths {
   set slack [expr {min($slack,[get_path_info -slack $path])}]
   set delay [expr {max($delay,[get_path_info -data_delay $path])}]
  }
 }
 puts $f "$label\t[get_operating_conditions_info -display_name $corner]\t[get_collection_size $points]\t$slack\t$delay"
 flush $f
 if {$slack<0 || $delay>$limit} {error "$label violates timing"}
}
foreach_in_collection corner [get_available_operating_conditions] {
 set_operating_conditions $corner
 update_timing_netlist
 set slots [get_registers {*dut|slots*}]
 set output [get_registers {*dut|dest_data*}]
 if {[get_collection_size $slots]!=640 || [get_collection_size $output]!=80} {error "FIFO payload register count"}
 inspect $f slots $corner $slots $output 1 6.0
 inspect $f output $corner $output $slots 0 6.0
 foreach direction {wgray rgray} {
  set first [get_registers "*dut|${direction}_meta*"]
  set second [get_registers "*dut|${direction}_sync*"]
  if {[get_collection_size $first]!=4 || [get_collection_size $second]!=4} {error "Missing $direction synchronization stages"}
  # Every first-stage input must be timed; only those inputs have max-delay
  # overrides. Check every second-stage bit with ordinary clock constraints.
  set sources [all_registers]
  inspect $f ${direction}_first $corner $first $sources 0 4.0
  inspect $f ${direction}_second $corner $second $first 0 8.0
 }
}
close $f
puts "PASS host FIFO: 640 stored bits, 80 outputs, all Gray synchronizer stages timed"

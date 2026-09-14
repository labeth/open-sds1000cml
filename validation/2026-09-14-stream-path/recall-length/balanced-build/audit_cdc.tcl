project_open probe
create_timing_netlist
read_sdc
set f [open cdc.tsv w]
puts $f "group\tcorner\tendpoints\tminimum_slack_ns\tmaximum_data_delay_ns"
set failed 0
proc inspect {f label corner points others from_points limit} {
 global failed
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
 if {$slack<0 || $delay>$limit} {set failed 1}
}
foreach_in_collection corner [get_available_operating_conditions] {
 set_operating_conditions $corner
 update_timing_netlist
 set all [all_registers]
 if {[info exists adc_config_source]} {
  inspect $f adc_config_source $corner $adc_config_source $adc_config_dest 1 6.0
  inspect $f adc_config_dest $corner $adc_config_dest $adc_config_source 0 6.0
  inspect $f adc_encode_source $corner $adc_encode_source $adc_encode_meta 1 4.0
  inspect $f adc_encode_meta $corner $adc_encode_meta $adc_encode_source 0 4.0
  inspect $f adc_encode_settled $corner $adc_encode_settled $adc_encode_meta 0 10.0
 }
 inspect $f fifo_slots $corner $host_slots $host_fifo_output 1 6.0
 inspect $f fifo_output $corner $host_fifo_output $host_slots 0 6.0
 foreach direction {wgray rgray} {
  set first [host_checked_registers "*fifo|${direction}_meta*" 4]
  set second [host_checked_registers "*fifo|${direction}_sync*" 4]
  inspect $f ${direction}_first $corner $first $all 0 4.0
  inspect $f ${direction}_second $corner $second $first 0 8.0
 }
 foreach bank {0 1} {
  set held [host_checked_registers "*ownership|held${bank}*" 76]
  set meta [host_checked_registers "*ownership|meta${bank}*" 76]
  set settled [host_checked_registers "*ownership|settled${bank}*" 76]
  inspect $f held$bank $corner $held $meta 1 6.0
  inspect $f meta$bank $corner $meta $held 0 6.0
  inspect $f settled$bank $corner $settled $meta 0 10.0
 }
 foreach {a b period} {ack1 ack2 8.0 pub1 pub2 10.0 pub2 pub3 10.0 rel1 rel2 4.0} {
  set first [host_checked_registers "*ownership|${a}*" 2]
  set second [host_checked_registers "*ownership|${b}*" 2]
  if {$a!="pub2"} {inspect $f $a $corner $first $all 0 4.0}
  inspect $f $b $corner $second $first 0 $period
 }
 foreach {name period} {source_bad_ram 8.0 ram_bad_core 4.0 bad_host 10.0} {
  set first [host_checked_registers [format {*host|%s[0]} $name] 1]
  set second [host_checked_registers [format {*host|%s[1]} $name] 1]
  inspect $f ${name}_first $corner $first $all 0 4.0
  inspect $f ${name}_second $corner $second $first 0 $period
 }
 foreach bridge {incoming outgoing} {
  set payload [host_checked_registers "*${bridge}|payload*" 32]
  set dest [host_checked_registers "*${bridge}|dest_data*" 32]
  foreach points [list $payload $dest] {
   foreach_in_collection point $points {
    set name [get_node_info -name $point]
    if {![regexp {\[([0-9]+)\]$} $name -> bit] || $bit>31} {error "Unexpected ingress payload bit $name"}
   }
  }
  inspect $f ${bridge}_payload $corner $payload $dest 1 6.0
  inspect $f ${bridge}_dest $corner $dest $payload 0 6.0
  foreach signal {request_sync ack_sync} {
   set first [host_checked_registers [format {*%s|%s[0]} $bridge $signal] 1]
   set second [host_checked_registers [format {*%s|%s[1]} $bridge $signal] 1]
   inspect $f ${bridge}_${signal}_first $corner $first $all 0 4.0
   inspect $f ${bridge}_${signal}_second $corner $second $first 0 8.0
  }
 }
 set first [host_checked_registers {*ingress|queue_fault_sync[0]} 1]
 set second [host_checked_registers {*ingress|queue_fault_sync[1]} 1]
 inspect $f ingress_fault_first $corner $first $all 0 4.0
 inspect $f ingress_fault_second $corner $second $first 0 4.0
 set third [host_checked_registers {*ingress|queue_fault_sync[2]} 1]
 inspect $f ingress_fault_third $corner $third $second 0 4.0
}
close $f
if {$failed} {error "Host CDC timing bound failed; inspect cdc.tsv"}
puts "PASS combined host and ingress CDC routes at all corners"

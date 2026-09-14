# Shared precision configuration bundle. decim_l is held before precision
# enable rises and throughout the epoch. enable_s asserts reset asynchronously
# and releases after three clk100 edges. decim_100 refreshes while reset remains
# asserted: the final capture is at least two clk100 periods after the earliest
# enable sampling edge. Bound data routing to 6 ns, well inside that window.
# No data-path or asynchronous reset exceptions are introduced here.
set adc_config_source [host_checked_registers {*source|decim_l*} 5]
set adc_config_dest [host_checked_registers {*precision|config_domain.decim_100*} 5]
foreach points [list $adc_config_source $adc_config_dest] {
 set bits {}
 foreach_in_collection point $points {
  set name [get_node_info -name $point]
  if {![regexp {\[([0-9]+)\]$} $name -> bit]} {error "Unexpected configuration endpoint $name"}
  lappend bits $bit
 }
 if {[lsort -integer $bits] ne {0 1 2 3 4}} {error "Configuration bundle bit coverage: $bits"}
}
set_max_delay 6.0 -from $adc_config_source -to $adc_config_dest
set_min_delay 0.0 -from $adc_config_source -to $adc_config_dest

# Independent encode-enable bits enter two-register local phase synchronizers.
# Bound first-stage routing, keeping meta -> settled and settled -> DDIO timed
# by their real phase clock. These are independent enables, not an atomic bus.
set adc_encode_source [host_checked_registers {*source|encode_l*} 10]
set adc_encode_meta [host_checked_registers {*frontend|*synchronized_encode.meta*} 10]
set adc_encode_settled [host_checked_registers {*frontend|*synchronized_encode.settled*} 10]
set_max_delay 4.0 -from $adc_encode_source -to $adc_encode_meta
set_min_delay 0.0 -from $adc_encode_source -to $adc_encode_meta

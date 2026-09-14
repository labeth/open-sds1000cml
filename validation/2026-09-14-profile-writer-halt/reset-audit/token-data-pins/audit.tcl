project_open profile
create_timing_netlist
read_sdc /tmp/acq-host-reset-profile.sdc
# Precision disable asserts asynchronously; three destination-clock stages
# control release. Cut only their clear pins, never their D inputs or outputs.
set precision_clear [get_pins -compatibility_mode {*|precision|enable_s*|clrn *|precision|pack_enable*|clrn}]
if {[get_collection_size $precision_clear]!=6} {error "Expected six precision reset-release clear pins"}
set_false_path -to $precision_clear
# Fixed SYNC_ENCODE=1 frontend: seven warm-up stages and three pack stages.
foreach {pattern expected} {
 {*|frontend|enable_s*|clrn} 7
 {*|frontend|pack_control.enable_s*|clrn} 3
} {
 set clear [get_pins -compatibility_mode $pattern]
 if {[get_collection_size $clear]!=$expected} {error "Frontend reset-release pin count $pattern"}
 set_false_path -to $clear
}
# Generated DCFIFO rdaclr/wraclr each contain two release registers. Their
# outputs reset working FIFO state; those downstream paths stay timed.
foreach fifo {frontend|queue precision|input_queue precision|output_queue} {
 foreach domain {rdaclr wraclr} {
  foreach stage {dffe12a dffe13a} {
   set pattern [format {*|source|%s|auto_generated|%s|%s[0]|clrn} $fifo $domain $stage]
   # Escape the bit index for Tcl string matching in compatibility mode.
   set pattern [string map {[ \[ ] \]} $pattern]
   set clear [get_pins -compatibility_mode $pattern]
   if {[get_collection_size $clear]!=1} {error "Missing FIFO reset-release pin $pattern"}
   set_false_path -to $clear
  }
 }
}

update_timing_netlist
report_timing -recovery -npaths 4 -detail full_path -file /tmp/acq-front-recovery.rpt

project_open probe
create_timing_netlist
read_sdc
update_timing_netlist
report_timing -setup -from $adc_encode_source -to $adc_encode_meta -npaths 10 -detail full_path -file /tmp/acq-encode-path.rpt

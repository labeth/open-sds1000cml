project_open probe
create_timing_netlist
read_sdc
foreach bridge {incoming outgoing} {
 foreach name {payload dest_data} {
  set points [get_registers "*${bridge}|${name}*"]
  puts "ENDPOINT $bridge $name [get_collection_size $points]"
  foreach_in_collection p $points {puts [get_node_info -name $p]}
 }
}

# CPU and acquisition clocks are asynchronous. Keep the existing exceptions for
# legacy crossings, but DO NOT cut the new bundled descriptor paths.
set stream_cpu_sinks [get_fanouts -no_logic [get_ports clk]]
set stream_metadata [get_registers {*stream_queue|base0_q[*] *stream_queue|base1_q[*] *stream_queue|count0_q[*] *stream_queue|count1_q[*] *stream_queue|single_q[*]}]
if {[get_collection_size $stream_cpu_sinks] < 100 || [get_collection_size $stream_metadata] < 10} {
 error "stream CDC endpoint discovery failed"
}
set_false_path -from [get_clocks cpu] -to [get_clocks {*pll* mref}]
set_false_path -from [get_clocks {*pll* mref}] -to [remove_from_collection $stream_cpu_sinks $stream_metadata]

# Descriptor buses reach the first capture stage within 8 ns (< one 12.5 ns
# CPU clock). Two metadata stages settle before the three-stage ready token.
set stream_descriptor_sources [get_registers {*stream_queue|base0[*] *stream_queue|base1[*] *stream_queue|count0[*] *stream_queue|count1[*] *stream_queue|single_last[*]}]
set_max_delay 8.0 -from $stream_descriptor_sources -to $stream_metadata
set_min_delay 0.0 -from $stream_descriptor_sources -to $stream_metadata

# The packet mailbox is held until acknowledgement. Its request traverses
# three packet-clock stages before the destination captures this payload.
set stream_payload_sources [get_registers {*stream_pack|payload[*] *stream_pack|payload_valid *stream_pack|payload_single *stream_pack|payload_seal}]
set stream_payload_targets [get_registers {*stream_pack|packet_data[*] *stream_pack|packet_valid *stream_pack|packet_single *stream_pack|packet_seal}]
set_max_delay 8.0 -from $stream_payload_sources -to $stream_payload_targets
set_min_delay 0.0 -from $stream_payload_sources -to $stream_payload_targets

# Only the asynchronous epoch assertion into the local reset synchronizers is
# excepted. Their release stages and all local reset recovery/removal stay timed.
set_false_path -from [get_registers {stream_mode[*] priming arm}] -to [get_registers {*stream_pack|word_reset[*] *stream_pack|packet_reset[*] *stream_queue|producer_reset[*] *stream_queue|host_reset[*]}]

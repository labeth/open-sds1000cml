create_clock -name source -period 4.0 [get_ports source_clk]
create_clock -name dest -period 8.0 [get_ports dest_clk]
derive_clock_uncertainty
# Common reset assertion is asynchronous; local synchronized release is timed.
set_false_path -from [get_ports reset]
# The write pointer crosses two stages plus registered empty detection before
# a slot can be fetched. Slots remain held until read-pointer acknowledgement.
# Bound this bundled path, rather than cutting the two clocks asynchronously.
set fifo_slots [get_registers {*dut|slots*}]
set fifo_output [get_registers {*dut|dest_data*}]
if {[get_collection_size $fifo_slots]!=640 || [get_collection_size $fifo_output]!=80} {error "Host FIFO payload endpoints missing"}
set_max_delay 6.0 -from $fifo_slots -to $fifo_output
set_min_delay 0.0 -from $fifo_slots -to $fifo_output
# A Gray pointer may change each source clock. Keep first-stage routes within
# the faster 4 ns period; synchronizer stage-to-stage paths stay normally timed.
set fifo_meta [get_registers {*dut|wgray_meta* *dut|rgray_meta*}]
if {[get_collection_size $fifo_meta]!=8} {error "Host FIFO pointer synchronizers missing"}
set_max_delay 4.0 -to $fifo_meta
set_min_delay 0.0 -to $fifo_meta

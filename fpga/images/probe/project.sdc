create_clock -name mref -period 10.0 [get_ports mclk_in]
create_clock -name cpu -period 12.5 [get_ports clk]
derive_pll_clocks
derive_clock_uncertainty
# The channels count each ball on its own edges; their counts are read on clk
# only after the gate has stopped them, and the gate reaches them through two
# synchronizer stages.
set_false_path -from [get_registers {*chan*e[*]}] -to [get_clocks cpu]
set_false_path -from [get_registers {clear running}] -to [get_registers {*chan*r1 *chan*r2 *chan*e[*]}]
# The encode legs are set by the host and held: a leg starts on the PLL clock
# a few cycles after the write.
set_false_path -from [get_registers {legs[*]}]
# The reference ball is sampled on clk like every other ball; only the first
# synchronizer stage is cut.
set_false_path -to [get_registers {s1[*] *chan*h1}]
# The held codes are copied on a pair's capture clock after a toggle from clk
# passes two synchronizer stages, and read on clk well after: the toggle's
# first stage and the held codes' path to the host are cut.
set_false_path -to [get_registers {*hold*t[0]}]
set_false_path -from [get_registers {*hold*h[*]}] -to [get_clocks cpu]
# The edge capture: the arm toggle and the trigger ball enter pair E1's
# capture clock through synchronizers; its memory and write pointer are read
# on clk only after the capture is done.
set_false_path -to [get_registers {ct[0] tr[0] cd[0]}]
set_false_path -from [get_registers {wa[*] cap_ball[*]}]
set_false_path -to [get_registers {cword[*]}]

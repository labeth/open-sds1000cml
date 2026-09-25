module bench_pll(input refclk,output c0,c1,locked,halfclk);
wire [4:0] clocks;
altpll #(.inclk0_input_frequency(10000),.intended_device_family("Cyclone IV E"),
.clk0_multiply_by(250),.clk0_divide_by(100),.clk0_duty_cycle(50),.clk0_phase_shift("0"),
.clk1_multiply_by(250),.clk1_divide_by(100),.clk1_duty_cycle(50),.clk1_phase_shift("2000"),
.clk2_multiply_by(250),.clk2_divide_by(200),.clk2_phase_shift("0"),.port_clk2("PORT_USED"),
.compensate_clock("CLK0"),.operation_mode("NORMAL"),.width_clock(5),
.port_inclk0("PORT_USED"),.port_clk0("PORT_USED"),.port_clk1("PORT_USED"),.port_locked("PORT_USED")) pll(
.inclk({1'b0,refclk}),.clk(clocks),.locked(locked),.areset(1'b0),.pfdena(1'b1),.pllena(1'b1),
.clkena(6'b111111),.extclkena(4'b1111),.fbin(1'b1),.clkswitch(1'b0),.scanclk(1'b0),
.scanclkena(1'b0),.scandata(1'b0),.configupdate(1'b0),.phasecounterselect(4'b0),
.phasestep(1'b0),.phaseupdown(1'b0),.scanaclr(1'b0),.scanread(1'b0),.scanwrite(1'b0));
assign c0=clocks[0];assign c1=clocks[1];assign halfclk=clocks[2];endmodule

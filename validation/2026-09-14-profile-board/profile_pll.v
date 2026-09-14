// Existing board clock plan: 100 MHz M2 reference, 250 MHz core,
// 250 MHz SRAM input sampling at +1 ns, and 125 MHz host-buffer writes.
module acq_profile_pll(input refclk,output core,sample_clk,ram_clk,locked);
 wire [4:0] clocks;
 altpll #(.inclk0_input_frequency(10000),.intended_device_family("Cyclone IV E"),
  .clk0_multiply_by(5),.clk0_divide_by(2),.clk0_duty_cycle(50),.clk0_phase_shift("0"),
  .clk1_multiply_by(5),.clk1_divide_by(2),.clk1_duty_cycle(50),.clk1_phase_shift("1000"),
  .clk2_multiply_by(5),.clk2_divide_by(4),.clk2_duty_cycle(50),.clk2_phase_shift("0"),
  .compensate_clock("CLK0"),.operation_mode("NORMAL"),.width_clock(5),
  .port_inclk0("PORT_USED"),.port_clk0("PORT_USED"),.port_clk1("PORT_USED"),
  .port_clk2("PORT_USED"),.port_locked("PORT_USED")) pll(
  .inclk({1'b0,refclk}),.clk(clocks),.locked(locked),.areset(1'b0),.pfdena(1'b1),.pllena(1'b1),
  .clkena(6'b111111),.extclkena(4'b1111),.fbin(1'b1),.clkswitch(1'b0),.scanclk(1'b0),
  .scanclkena(1'b0),.scandata(1'b0),.configupdate(1'b0),.phasecounterselect(4'b0),
  .phasestep(1'b0),.phaseupdown(1'b0),.scanaclr(1'b0),.scanread(1'b0),.scanwrite(1'b0));
 assign core=clocks[0];assign sample_clk=clocks[1];assign ram_clk=clocks[2];
endmodule

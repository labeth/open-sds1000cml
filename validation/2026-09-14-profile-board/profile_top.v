// Physical board wrapper. SRAM control constants and pins follow acq_sram/top.v.
// No internal flash programming interface is present.
module acq_profile_top #(parameter ENABLE_STREAM=0,BUILD_ID=32'b0)(
 input wire clk,mclk_in,nCS1,nOE,nWE,input wire [6:2] sel,
 input wire gpmc_a2,gpmc_b1,inout wire [15:0] gpmc_d,
 inout wire [31:0] dq,input wire [79:0] lane,
 output wire k1,k2,g1,g2,d1,d2,f1,f2,j2,a11,output wire [4:0] enc_p,enc_n
);
 wire core,sample_clk,ram_clk,locked;
 acq_profile_pll clocks(mclk_in,core,sample_clk,ram_clk,locked);
 // Stretch startup/lock-loss reset across every domain. Internal release
 // paths remain visible to timing analysis; this is not a CDC exception.
 reg [7:0] reset_hold=8'hff;
 always @(posedge mclk_in or negedge locked)
  if(!locked)reset_hold<=8'hff;else reset_hold<={reset_hold[6:0],1'b0};
 wire reset=reset_hold[7] || !locked;
 assign g2=0;assign d1=0;assign d2=0;
 assign f1=1;assign f2=1;assign j2=1;assign a11=1;
 acq_profile_core #(.ENABLE_STREAM(ENABLE_STREAM),.BUILD_ID(BUILD_ID)) profile(
  .reset(reset),.locked(locked),.core_clk(core),.sample_clk(sample_clk),
  .ram_clk(ram_clk),.host_clk(clk),.clk100(mclk_in),
  .nCS1(nCS1),.nOE(nOE),.nWE(nWE),.sel({sel,gpmc_a2,gpmc_b1}),
  .gpmc_d(gpmc_d),.lane(lane),.dq(dq),.k1(k1),.k2(k2),.g1(g1),.enc_p(enc_p),.enc_n(enc_n));
endmodule

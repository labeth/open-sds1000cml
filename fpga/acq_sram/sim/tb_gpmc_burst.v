`timescale 1ns/1ps
module bench_pll(input refclk,output reg c0=0,output c1,output locked,output reg halfclk=0);
 always #2 c0=~c0;initial begin #2;forever #4 halfclk=~halfclk;end assign #1 c1=c0;assign locked=1;
endmodule
module adc_interleave(input refclk,memclk,packclk,enable,input [79:0] lane,input [9:0] encode_enable,
 input snapshot_request,output snapshot_ack,input consume,output [31:0] word_data,output valid,fault,output [79:0] snapshot,output [4:0] enc_p,enc_n,output locked);
 assign snapshot_ack=snapshot_request;assign word_data=0;assign valid=0;assign fault=0;assign snapshot=0;assign enc_p=0;assign enc_n=0;assign locked=1;
endmodule
module tb;
 reg clk=0,mclk_in=0;always #6.25 clk=~clk;always #5 mclk_in=~mclk_in;
 reg cs=1,oe=1;wire [15:0] gpmc_d;wire [31:0] dq;wire k1,k2,g1;
 acq_sram_top dut(.clk(clk),.mclk_in(mclk_in),.nCS1(cs),.nOE(oe),.nWE(1'b1),.sel(5'd6),
  .gpmc_a2(1'b0),.gpmc_b1(1'b1),.gpmc_d(gpmc_d),.dq(dq),.lane(80'b0),.k1(k1),.k2(k2),.g1(g1));
 reg [31:0] mem[0:524287];reg [18:0] address=0;reg [31:0] s1=0,s2=0;
 assign dq=k1&&g1?s2:32'bz;
 always @(posedge k2) if(g1)begin
  if(!k1)mem[address]<=dq;else begin s1<=mem[address];s2<=#4.5 s1; /* Modeled board read-response latency exceeds one 4 ns period. */end
  address<=address+1'b1;
 end
 integer i;
 task command(input integer code);
 begin
  @(negedge clk);dut.opcode=code;dut.request=~dut.request;
  wait(dut.ack==dut.request);#100;
  if(dut.command_error)$fatal(1,"command rejected %0d",code);
 end endtask
 reg pop=0;
 initial begin
  #100;command(1);wait(dut.record_done && dut.ready);#10;
  dut.count_cfg=4096;command(2);wait(dut.ready);#40;
  if(dut.received!=4096)$fatal(1,"large buffer count");
  for(i=0;i<4096;i=i+1)if((i%2 ? dut.buffer_mem64[i/2][63:32] : dut.buffer_mem64[i/2][31:0])!==i)$fatal(1,"large buffer %0d",i);
  for(i=0;i<8192;i=i+1)begin
   #80;cs=0;#40;oe=0;#40;
   if(gpmc_d !== (i%2 ? 16'd0 : i/2))$fatal(1,"GPMC half %0d got %h",i,gpmc_d);
   #20;
   if(i%2)begin cs=1;#8;oe=1;end
   else begin oe=1;#8;cs=1;end
  end
  #100;if(dut.burst_index!=0)$fatal(1,"burst wrap");
  $display("PASS full GPMC read path: 8192 halfwords, skewed CS/OE release, pointer wrap");$finish;
 end
 initial begin #30000000;$fatal(1,"timeout");end
endmodule

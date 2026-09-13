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
 wire [15:0] gpmc_d;wire [31:0] dq;wire k1,k2,g1;
 acq_sram_top dut(.clk(clk),.mclk_in(mclk_in),.nCS1(1'b1),.nOE(1'b1),.nWE(1'b1),.sel(5'b0),
  .gpmc_a2(1'b0),.gpmc_b1(1'b0),.gpmc_d(gpmc_d),.dq(dq),.lane(80'b0),.k1(k1),.k2(k2),.g1(g1));
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
 integer start=0,n,j;
 initial begin
  for(i=0;i<1024;i=i+1)mem[i]=i;
  force dut.record_done=1;
  #100;dut.count_cfg=32;command(2);wait(dut.ready);#40;
  start=32;
  for(j=0;j<4;j=j+1)begin
   n=j==0?1:j==1?4:16;
   dut.count_cfg=n;command(7);wait(dut.ready);#40;
   for(i=0;i<n;i=i+1)begin
    $display("continue start=%0d index=%0d value=%0d",start,i,i%2 ? dut.buffer_mem64[i/2][63:32] : dut.buffer_mem64[i/2][31:0]);
   end
   start=start+n;
  end
  $finish;
 end

 initial begin #30000000;$fatal(1,"timeout");end
endmodule

// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-040
module tb;
 reg clk=0,enable=0,valid=0;always #2 clk=~clk;
 reg signed [27:0] a=0,b=0;
 wire ov;wire signed [27:0] lo,hi;
 cic_pair_integrator dut(clk,enable,valid,a,b,ov,lo,hi);
 reg [27:0] state=0,expected_lo[0:4095],expected_hi[0:4095];
 reg [31:0] rng=32'h51465319;
 integer written=0,read=0,i,epoch;
 task cycle(input v,input [27:0] x,y);
 begin
  @(negedge clk);valid=v;a=x;b=y;
  if(v)begin
   expected_lo[written]=state+x;
   expected_hi[written]=state+x+y;
   state=state+x+y;written=written+1;
  end
  @(posedge clk);#0.01;
  if(ov)begin
   if(read>=written || lo!==expected_lo[read] || hi!==expected_hi[read])
    $fatal(1,"pair %0d got %h/%h expected %h/%h",read,lo,hi,expected_lo[read],expected_hi[read]);
   read=read+1;
  end
 end endtask
 initial begin
  repeat(4)cycle(0,0,0);
  for(epoch=0;epoch<8;epoch=epoch+1)begin
   enable=1;repeat(4)cycle(0,0,0);
   for(i=0;i<1024;i=i+1)begin
    rng=rng*1664525+1013904223;
    cycle(i<256 || rng[31:30]!=0,rng[27:0],{rng[12:0],rng[31:17]});
   end
   repeat(6)cycle(0,0,0);
   if(read!=written)$fatal(1,"missing output");
   enable=0;repeat(6)cycle(0,0,0);state=0;read=0;written=0;
  end
  $display("PASS split integrator: signed full-width wrap, continuous tokens, stalls, eight rearms");$finish;
 end
endmodule

// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-041, REQ-SDS-043, REQ-SDS-044, REQ-SDS-083
module bench_pll(input refclk,output reg c0=0,output c1,output locked,output reg halfclk=0);
 always #2 c0=~c0;initial begin #2;forever #4 halfclk=~halfclk;end assign #1 c1=c0;assign locked=1;
endmodule
// TRLC-LINKS: REQ-SDS-041, REQ-SDS-043, REQ-SDS-044, REQ-SDS-083
module adc_interleave(input refclk,memclk,packclk,enable,input [79:0] lane,input [9:0] encode_enable,
 input snapshot_request,output snapshot_ack,input consume,output [31:0] word_data,output valid,fault,output [79:0] snapshot,output [4:0] enc_p,enc_n,output locked);
 assign snapshot_ack=snapshot_request;assign word_data=0;assign valid=0;assign fault=0;assign snapshot=0;assign enc_p=0;assign enc_n=0;assign locked=1;
endmodule
// TRLC-LINKS: REQ-SDS-041, REQ-SDS-043, REQ-SDS-044, REQ-SDS-083
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
 initial begin
  #100;command(1);wait(dut.record_done && dut.ready);#10;
  if(dut.record_length!=524288 || dut.origin!=17 || dut.position!=17)$fatal(1,"capture metadata");
  for(i=0;i<524288;i=i+1) if(mem[(i+17)%524288]!==i)$fatal(1,"capture write %0d got %h",i,mem[(i+17)%524288]);
  command(2);wait(dut.ready);#10;
  if(dut.received!=512 || dut.position!=530)$fatal(1,"buffer/position count");
  for(i=0;i<512;i=i+1)if((i%2 ? dut.buffer_mem64[i/2][63:32] : dut.buffer_mem64[i/2][31:0])!==i)$fatal(1,"first recall %0d",i);
  dut.count_cfg=524287;command(3);wait(dut.ready);#10;
  if(dut.position!=529)$fatal(1,"seek after flush");
  dut.count_cfg=512;command(2);wait(dut.ready);#10;
  for(i=0;i<512;i=i+1)if((i%2 ? dut.buffer_mem64[i/2][63:32] : dut.buffer_mem64[i/2][31:0])!==i+512)$fatal(1,"second recall %0d",i);
  dut.count_cfg=1;command(7);wait(dut.ready);#40;
  if(dut.buffer_mem64[0][31:0]!==1024)$fatal(1,"odd-sized buffer flush");
  $display("PASS interleave memory path: odd/full buffer and integrated full-depth capture and consecutive recall windows across counter wrap");$finish;
 end
 initial begin #30000000;$fatal(1,"timeout");end
endmodule

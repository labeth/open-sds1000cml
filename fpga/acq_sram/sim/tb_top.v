`timescale 1ns/1ps
module bench_pll(input refclk,output c0,c1,locked);
 assign c0=refclk;assign #1 c1=refclk;assign locked=1;
endmodule
module tb;
 reg clk=0,mclk_in=0;always #6.25 clk=~clk;always #5 mclk_in=~mclk_in;
 wire [15:0] gpmc_d;wire [31:0] dq;wire k1,k2,g1;
 acq_sram_top dut(.clk(clk),.mclk_in(mclk_in),.nCS1(1'b1),.nOE(1'b1),.nWE(1'b1),.sel(5'b0),
  .gpmc_a2(1'b0),.gpmc_b1(1'b0),.gpmc_d(gpmc_d),.dq(dq),.lane(80'b0),.k1(k1),.k2(k2),.g1(g1));
 reg [31:0] mem[0:524287];reg [18:0] address=0;reg [31:0] s1=0,s2=0;
 assign dq=k1&&g1?s2:32'bz;
 always @(posedge k2) if(g1)begin
  if(!k1)mem[address]<=dq;else begin s1<=mem[address];s2<=s1;end
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
  if(dut.record_length!=524288 || dut.origin!=16 || dut.position!=16)$fatal(1,"capture metadata");
  for(i=0;i<524288;i=i+1) if(mem[(i+16)%524288]!==i)$fatal(1,"capture write %0d got %h",i,mem[(i+16)%524288]);
  command(2);wait(dut.ready);#10;
  if(dut.received!=512 || dut.position!=529)$fatal(1,"buffer/position count");
  for(i=0;i<512;i=i+1)if(dut.buffer_mem[i]!==i)$fatal(1,"first recall %0d",i);
  dut.count_cfg=524287;command(3);wait(dut.ready);#10;
  if(dut.position!=528)$fatal(1,"seek after flush");
  dut.count_cfg=512;command(2);wait(dut.ready);#10;
  for(i=0;i<512;i=i+1)if(dut.buffer_mem[i]!==i+512)$fatal(1,"second recall %0d",i);
  $display("PASS integrated full-depth capture and consecutive recall windows across counter wrap");$finish;
 end
 initial begin #30000000;$fatal(1,"timeout");end
endmodule

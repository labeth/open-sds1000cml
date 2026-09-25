// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-044
module tb;
 localparam AW=19,N=1<<AW;
 reg clk=0;always #5 clk=~clk;
 wire sample_clk;assign #1 sample_clk=clk;
 reg reset=1,locked=1,command=0,command_read=0,command_discard=0,command_continue=0,write_valid=0,write_stop=0;
 reg [AW:0] command_count=0;
 reg [31:0] write_data=0;
 wire ready,write_ready,read_valid,done,k1,k2,g1;
 wire [31:0] read_data,dq;wire [AW-1:0] position;
 sram_transport #(.AW(AW)) dut(.*);
 reg [31:0] mem[0:N-1];reg [AW-1:0] address=0;
 reg [31:0] stage1=0,stage2=0;
 integer writes=0,reads=0,received=0,expected=0,i;
 assign dq=k1 && g1 ? stage2 : 32'bz;
 always @(posedge k2) if(g1) begin
  if(!k1) begin mem[address]<=dq;writes=writes+1;end
  else begin stage1<=mem[address];stage2<=stage1;reads=reads+1;end
  address<=address+1'b1;
 end
 always @(negedge clk) if(read_valid) begin
  if(read_data!==32'h12300000+expected)
   $fatal(1,"read %0d got %h expected %h",received,read_data,32'h12300000+expected);
  expected=(expected+1)%N;received=received+1;
 end
 task tick;begin @(posedge clk);#2;@(negedge clk);#1;end endtask
 task launch(input integer rd,input integer n);
 begin
  wait(ready);command_read=rd;command_count=n;command=1;tick;command=0;
 end endtask
 task readburst(input integer n);
 begin
  expected=position;launch(1,n);wait(done);tick;
  if(position!==address)$fatal(1,"position drift after read");
 end endtask
 initial begin
  tick;reset=0;
  launch(1,0);repeat(4)tick;
  if(!ready || writes!=0 || reads!=0)$fatal(1,"zero-length read was not rejected");
  launch(1,N+1);repeat(4)tick;
  if(!ready || writes!=0 || reads!=0)$fatal(1,"oversized read was not rejected");
  launch(0,N+1);repeat(4)tick;
  if(!ready || writes!=0 || reads!=0)$fatal(1,"oversized write was not rejected");
  launch(0,N);wait(write_ready);@(negedge clk);#1;
  for(i=0;i<N;i=i+1) begin
   write_data=32'h12300000+i;write_valid=1;tick;
   if(i%997==0) begin write_valid=0;repeat(3)tick;end
  end
  write_valid=0;wait(done);tick;
  if(writes!=N || position!=0 || address!=0)$fatal(1,"write count/wrap");
  readburst(N);readburst(1);readburst(255);readburst(257);readburst(N);
  if(received!=2*N+513 || reads!=received+5)$fatal(1,"read/flush count");
  command_discard=1;launch(1,1);wait(done);tick;command_discard=0;
  if(reads!=received+6 || position!==address)$fatal(1,"one-step seek");
  readburst(3);
  // Continue from the prefetched word, with no extra flush or counter wrap.
  command_continue=1;expected=(position+N-1)%N;
  launch(1,1);wait(done);tick;
  expected=(position+N-1)%N;launch(1,512);wait(done);tick;
  if(reads!=received+7 || position!==address)$fatal(1,"continued read accounting");
  $display("PASS full-capacity SRAM transport, stalled writes, repeated reads and exact flush pointer (%0d words)",received);
  $finish;
 end
 initial begin #30000000;$fatal(1,"timeout");end
endmodule

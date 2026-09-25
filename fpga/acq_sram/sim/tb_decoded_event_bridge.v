// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-058
module tb_decoded_event_bridge;
 reg source_clk=0,host_clk=0;
 always #4 source_clk=~source_clk;
 always #7 host_clk=~host_clk;
 reg reset=1,sv=0,hr=0;
 reg [255:0] sd=0;wire sr,hv;wire [255:0] hd;
 decoded_event_bridge dut(reset,source_clk,host_clk,sv,sd,sr,hv,hd,hr);
 integer i;
// TRLC-LINKS: REQ-SDS-013
 task send(input [31:0] value);begin
  wait(sr);@(negedge source_clk);sd={8{value}};sv=1;
  @(negedge source_clk);sv=0;sd=~sd;
 end endtask
// TRLC-LINKS: REQ-SDS-013
 task receive(input [31:0] value);begin
  wait(hv);
  repeat(5)begin @(negedge host_clk);if(!hv || hd!={8{value}} || sr)$fatal(1,"payload changed during host stall");end
  hr=1;@(negedge host_clk);hr=0;
  if(hv)$fatal(1,"accepted event remained valid");
 end endtask
 initial begin
  #25;reset=0;
  for(i=0;i<12;i=i+1)begin send(32'hf00d0000+i);receive(32'hf00d0000+i);end
  send(99);wait(hv);
  #2;reset=1;#35;reset=0;
  repeat(8)@(negedge host_clk);
  if(hv)$fatal(1,"old epoch survived reset");
  send(100);receive(100);
  $display("PASS event CDC: asynchronous clocks, host stalls, ordering, reset in flight");$finish;
 end
 initial begin #20000;$fatal(1,"timeout");end
endmodule

// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
`include "lanemap_seed.vh"
// TRLC-LINKS: REQ-SDS-038
module tb;
 reg [79:0] lanes=0;wire [79:0] cores;
 adc_unpack dut(lanes,cores);
 integer i;reg [79:0] want;
 initial begin
  for(i=0;i<80;i=i+1) begin
   lanes=80'b1 << `LANEMAP_ENTRY(i);want=80'b1 << i;#1;
   if(cores!==want)$fatal(1,"ADC bit %0d misplaced: %h expected %h",i,cores,want);
  end
  lanes=~80'b0;#1;if(cores!==~80'b0)$fatal(1,"all bits");
  $display("PASS: all 80 physical lane bits unpack into measured pair/channel/bit positions");$finish;
 end
endmodule

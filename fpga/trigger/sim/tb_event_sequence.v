// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// Feeds "kind value gap" events after writing "kind mask value" elements;
// prints "M n" when a match pulses, n being the number of events fed so far.
// TRLC-LINKS: REQ-SDS-013
module tb_event_sequence;
 reg clk=0;always #4 clk=~clk;
 reg wr_clk=0;always #6.25 wr_clk=~wr_clk;
 reg reset=1,qualify=0,end_bad_value=0,in_valid=0,wr_en=0;
 reg [5:0] length=0;reg [7:0] in_kind=0;reg [31:0] in_value=0;
 reg [4:0] wr_addr=0;reg [66:0] wr_data=0;
 wire match;wire [15:0] overflows;
 event_sequence dut(.*);
 reg [31:0] events[0:3*65535];reg [31:0] elements[0:3*31+2];
 reg [4095:0] event_file,element_file;
 integer n,m,i,fed=0,ignored,q=0,e=0;
 always @(posedge clk)if(match)$display("M %0d",fed);
 initial begin
  if(!$value$plusargs("events=%s",event_file) || !$value$plusargs("n=%d",n) ||
     !$value$plusargs("elements=%s",element_file) || !$value$plusargs("m=%d",m))$fatal(1,"missing fixture");
  ignored=$value$plusargs("qualify=%d",q);ignored=$value$plusargs("endbad=%d",e);
  qualify=q!=0;end_bad_value=e!=0;
  $readmemh(event_file,events,0,3*n-1);$readmemh(element_file,elements,0,3*m-1);
  for(i=0;i<m;i=i+1)begin
   @(negedge wr_clk);wr_en=1;wr_addr=i;wr_data={elements[3*i][2:0],elements[3*i+1],elements[3*i+2]};
  end
  @(negedge wr_clk);wr_en=0;
  repeat(4)@(negedge clk);length=m;reset=0;repeat(2)@(negedge clk);
  for(i=0;i<n;i=i+1)begin
   in_valid=1;in_kind=events[3*i];in_value=events[3*i+1];fed=i+1;
   @(negedge clk);in_valid=0;
   repeat(events[3*i+2])@(negedge clk);
  end
  repeat(40*256)@(negedge clk);
  $display("O %0d",overflows);$finish(0);
 end
endmodule

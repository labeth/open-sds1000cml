// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-046
module tb;
 parameter BANKS=9,ROWS=512;
 localparam DEPTH=BANKS*ROWS;
 reg clk=0;always #2 clk=~clk;
 reg reset=1,push=0,pop=0;reg [31:0] data_in=0;
 wire [31:0] data_out;wire data_valid,empty,full,overflow,underflow;
 wire [$clog2(DEPTH+1)-1:0] count;
 sram_ingress_fifo #(.BANKS(BANKS),.ROWS(ROWS)) dut(.*);
 reg [31:0] model[0:DEPTH-1];
 integer head=0,tail=0,used=0,accepted=0,returned=0,i;
 integer seed=32'h73ab8011;
 reg expected_valid,expected_overflow=0,expected_underflow=0;
 reg [31:0] expected_data,delayed_data,delayed_data2,delayed_data3;reg delayed_valid=0,delayed_valid2=0,delayed_valid3=0;
 always @(posedge clk)begin
  expected_valid=0;
  if(reset)begin head=0;tail=0;used=0;expected_overflow=0;expected_underflow=0;delayed_valid=0;delayed_valid2=0;delayed_valid3=0;end
  else begin
   expected_valid=pop && used!=0;
   if(push && used==DEPTH)expected_overflow=1;
   if(pop && used==0)expected_underflow=1;
   if(expected_valid)begin expected_data=model[tail];tail=(tail+1)%DEPTH;returned=returned+1;end
   if(push && used<DEPTH)begin
    model[head]=data_in;head=(head+1)%DEPTH;accepted=accepted+1;
    if(!expected_valid)used=used+1;
   end else if(expected_valid)used=used-1;
  end
  #0.5;
  if(count!=used || empty!=(used==0) || full!=(used==DEPTH))$fatal(1,"occupancy mismatch");
  if(data_valid!==delayed_valid3)$fatal(1,"response validity mismatch");
  if(data_valid && data_out!==delayed_data3)$fatal(1,"payload order mismatch got=%h want=%h",data_out,delayed_data3);
  delayed_valid3=delayed_valid2;delayed_data3=delayed_data2;
  delayed_valid2=delayed_valid;delayed_data2=delayed_data;
  delayed_valid=expected_valid;delayed_data=expected_data;
  if(overflow!==expected_overflow || underflow!==expected_underflow)$fatal(1,"sticky fault mismatch");
 end
 task cycle(input wr,input rd);
 begin @(negedge clk);push=wr;pop=rd;data_in=$random(seed);@(posedge clk);#1;end
 endtask
 initial begin
  cycle(0,0);reset=0;
  for(i=0;i<DEPTH;i=i+1)cycle(1,0);
  cycle(1,1); // Full collision: pop succeeds, push fails explicitly.
  for(i=0;i<DEPTH-1;i=i+1)cycle(0,1);
  cycle(1,1); // Empty collision: push succeeds, pop faults; no bypass.
  cycle(0,1);
  reset=1;cycle(1,1);reset=0;
  for(i=0;i<DEPTH/2;i=i+1)cycle(1,0);
  for(i=0;i<DEPTH*8;i=i+1)cycle(1,1); // Multiple bank and full-ring wraps.
  for(i=0;i<DEPTH*20;i=i+1)cycle(($random(seed)&3)!=0,($random(seed)&3)!=0);
  while(used!=0)cycle(0,1);
  repeat(4)cycle(0,0);
  $display("PASS ingress banks=%0d rows=%0d accepted=%0d returned=%0d",BANKS,ROWS,accepted,returned);
  $finish;
 end
 initial begin #10000000;$fatal(1,"timeout");end
endmodule

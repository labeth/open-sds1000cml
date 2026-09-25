// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-013
module tb_decoded_event_reader;
 reg clk=0;always #5 clk=~clk;
 reg reset=1,valid=0,pop=0,rewind=0,clear=0;
 reg [255:0] payload=0;wire ready,available,error;wire [3:0] cursor;wire [15:0] data;
 decoded_event_reader dut(clk,reset,valid,payload,ready,pop,rewind,clear,available,cursor,data,error,);
 integer i;
// TRLC-LINKS: REQ-SDS-013
 task step;begin @(posedge clk);#1;end endtask
 initial begin
  step;@(negedge clk);reset=0;
  for(i=0;i<16;i=i+1)payload[i*16+:16]=16'h1000+i;
  valid=1;step;
  @(negedge clk);payload=256'hffffffff; // producer changes, held event must not
  repeat(3)step;
  if(!available || ready || data!=16'h1000)$fatal(1,"event not held across stall");
  @(negedge clk);pop=1;step;step;
  @(negedge clk);pop=0;rewind=1;step;
  if(cursor!=0 || data!=16'h1000)$fatal(1,"partial read retry failed");
  @(negedge clk);rewind=0;pop=1;
  for(i=0;i<16;i=i+1)begin
   if(data!=16'h1000+i || cursor!=i)$fatal(1,"torn or reordered event at %0d",i);
   step;
  end
  if(available || !ready)$fatal(1,"last halfword did not release event");
  @(negedge clk);pop=0;step; // next event may now enter
  if(!available || data!=16'hffff)$fatal(1,"next event missing");
  @(negedge clk);pop=1;rewind=1;step;
  if(!error || cursor!=0)$fatal(1,"conflicting commands advanced cursor");
  @(negedge clk);reset=1;step;
  if(available || error || data!=0)$fatal(1,"reset exposed stale event");
  $display("PASS event readout: stable partial reads, rewind, ordering, release, reset");$finish;
 end
 initial begin #10000;$fatal(1,"timeout");end
endmodule

// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-013
module tb_decoded_event_port;
 reg clk=0;always #5 clk=~clk;
 reg reset=1,write=0,read_pop=0;reg [7:0] ws=0,rs=0;reg [15:0] wd=0;
 wire enabled,pop,rewind,clear,hit;wire [31:0] epoch;wire [15:0] data;
 reg [15:0] complete_count=127;
 decoded_event_port dut(clk,reset,write,read_pop,ws,rs,wd,1'b1,1'b0,4'd3,16'h1234,enabled,epoch,pop,rewind,clear,hit,data,complete_count);
// TRLC-LINKS: REQ-SDS-013
 task put(input [7:0] s,input [15:0] d);begin
  @(negedge clk);write=1;ws=s;wd=d;@(negedge clk);write=0;
 end endtask
 initial begin
  @(negedge clk);reset=0;rs=59;read_pop=1;#1;
  if(pop || data!=0)$fatal(1,"disabled port exposed event");
  put(57,1);#1;if(!enabled || epoch!=1 || !pop || data!=16'h1234)$fatal(1,"enable/read failed");
  put(57,1);if(epoch!=1)$fatal(1,"idempotent enable changed epoch");
  put(57,3);if(!enabled || epoch!=1)$fatal(1,"malformed control changed capture");
  rs=58;#1;if(!data[2])$fatal(1,"malformed control not reported");
  put(60,2);#1;if(data[2])$fatal(1,"error clear failed");
  put(57,0);put(57,1);if(epoch!=2)$fatal(1,"new stream reused epoch");
  rs=63;#1;if(data!=16'h4501)$fatal(1,"capability missing");
  rs=74;#1;if(data!=16'h4503)$fatal(1,"reservation capability missing");
  put(76,1);rs=75;#1;if(data!=127)$fatal(1,"reservation not captured");
  complete_count=128;repeat(3)@(negedge clk);#1;
  if(data!=127)$fatal(1,"live binary count leaked into asynchronous read");
  put(76,1);#1;if(data!=128)$fatal(1,"reservation did not refresh");
  put(57,0);#1;if(data!=0)$fatal(1,"disabled stream retained reservation");
  rs=48;#1;if(hit)$fatal(1,"port aliases existing UART register");
  $display("PASS event registers: enable, epoch, invalid commands, data gating, capability");$finish;
 end
endmodule

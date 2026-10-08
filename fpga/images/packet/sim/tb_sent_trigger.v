// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
module tb;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,enable=1,line=1,inverted=0,pause_pulse=0;
 reg [23:0] tick_ticks=8;reg [6:0] nibbles=8;
 reg [31:0] pattern=32'h050a;reg [2:0] pattern_len=2;
 wire match,event_valid;wire [7:0] event_kind,event_data;wire [6:0] event_index;
 sent_trigger dut(.tick(1'b1),.*);
 integer j;
 integer hit_count=0,ends=0,errors=0,starts=0,data_count=0;
 always @(posedge clk)begin
  if(match)begin
   if(!event_valid || event_kind!=3)$fatal(1,"trigger before verified CRC");
   hit_count=hit_count+1;
  end
  if(event_valid)case(event_kind)
   1:starts=starts+1;
   2:begin if(event_data>15 || event_index>=nibbles-1)$fatal(1,"invalid nibble event");data_count=data_count+1;end
   3:ends=ends+1;
   4:errors=errors+1;
  endcase
 end
 task clocks(input integer n);begin repeat(n)@(negedge clk);end endtask
 task pulse(input integer width);begin line=inverted;clocks(5*8);line=!inverted;clocks((width-5)*8);end endtask
 // status=0, data=1 2 5 A 3 4, CRC=1 (independently calculated fixture).
 task frame(input integer bad);begin
  pulse(56);pulse(12);pulse(13);pulse(14);pulse(17);pulse(22);pulse(15);pulse(16);pulse(bad?14:13);
 end endtask
 initial begin
  clocks(3);reset=0;clocks(3);
  frame(0);frame(1);frame(0);pulse(56);clocks(2);
  if(hit_count!=2 || ends!=2 || errors!=1 || data_count!=21)$fatal(1,"CRC gating hit_count=%d ends=%d errors=%d data=%d",hit_count,ends,errors,data_count);
  pattern=32'h0e0f;frame(0);pulse(56);clocks(2);
  if(hit_count!=2)$fatal(1,"absent payload matched");
  reset=1;clocks(3);reset=0;inverted=1;line=0;pattern=32'h050a;clocks(3);
  frame(0);pulse(56);clocks(2);
  if(hit_count!=3)$fatal(1,"inverted SENT did not match");
  // A truncated packet must not carry a provisional suffix into the next.
  reset=1;clocks(3);reset=0;clocks(3);
  pulse(56);pulse(12);pulse(17);pulse(22);pulse(56);pulse(12);pulse(13);pulse(14);pulse(15);pulse(16);pulse(17);pulse(18);pulse(20);pulse(56);clocks(2);
  if(hit_count!=3)$fatal(1,"truncated payload triggered");
  reset=1;clocks(3);reset=0;nibbles=64;pattern=32'h0e0f;clocks(3);
  pulse(56);for(j=0;j<63;j=j+1)pulse(12+(j%16));pulse(18);pulse(56);clocks(2);
  if(hit_count!=4)$fatal(1,"64-nibble frame or values 0..15 failed");
  reset=1;clocks(3);reset=0;nibbles=1;pattern_len=0;clocks(3);
  pulse(56);pulse(15);pulse(56);clocks(2);
  if(hit_count!=5)$fatal(1,"CRC-only frame failed");
  reset=1;clocks(3);reset=0;nibbles=8;pattern_len=2;pattern=32'h050a;pause_pulse=1;clocks(3);
  frame(0);pulse(40);frame(0);pulse(40);pulse(56);clocks(2);
  if(hit_count!=7)$fatal(1,"pause pulse framing failed");
  $display("PASS SENT nibble events, CRC-gated matching, absent payload, inversion and truncated frame");$finish;
 end
 initial begin #2000000;$fatal(1,"timeout");end
endmodule

// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
module tb_mil1553_trigger;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,enable=1,tick=1,line=0,inverted=0,match_any=0;
 reg [23:0] bit_ticks=20;reg [15:0] pattern=16'ha55a;
 wire match,event_valid,event_command;wire [7:0] event_kind;wire [15:0] event_data;
 mil1553_trigger dut(.*);
 integer good=0,bad=0,hits=0;reg [15:0] last_word=0;reg last_command=0;
 always @(posedge clk)begin
  #1;if(match)hits=hits+1;
  if(event_valid)begin
   if(event_kind==2)good=good+1;else if(event_kind==4)bad=bad+1;else $fatal(1,"unexpected kind");
   last_word=event_data;last_command=event_command;
  end
 end
 // TRLC-LINKS: REQ-SDS-013
 task hold(input value,input integer cycles);integer j;begin
  for(j=0;j<cycles;j=j+1)begin @(negedge clk);line=value^inverted;end
 end endtask
 // TRLC-LINKS: REQ-SDS-013
 task send(input [15:0] value,input cmd,input corrupt,input integer broken);integer k;reg p,b;begin
  hold(!cmd,8*bit_ticks);hold(cmd,bit_ticks+bit_ticks/2);hold(!cmd,bit_ticks+bit_ticks/2);
  p=!(^value)^corrupt;
  for(k=15;k>=-1;k=k-1)begin
   b=k<0?p:value[k];hold(b,bit_ticks/2);hold(k==broken?b:!b,bit_ticks-bit_ticks/2);
  end
  hold(!cmd,8*bit_ticks);
 end endtask
 initial begin
  hold(0,4);reset=0;hold(0,4);
  // Starting inside a sync-like level run can produce an explicit coding
  // error before even the first payload bit. It must never produce DATA/hit.
  hold(1,30);hold(0,100);
  if(good!=0 || hits!=0 || bad!=1 || last_word!=0)$fatal(1,"partial startup word was not rejected");
  bad=0;
  send(16'ha55a,1,0,-2);
  if(good!=1 || hits!=1 || last_word!=16'ha55a || !last_command)$fatal(1,"valid command word");
  send(16'ha55a,0,1,-2);
  if(bad!=1 || hits!=1 || last_word!=16'ha55a || last_command)$fatal(1,"bad parity triggered");
  send(16'h5aa5,0,0,-2);
  if(good!=2 || hits!=1 || last_word!=16'h5aa5 || last_command)$fatal(1,"data sync or absent pattern");
  send(16'ha55a,1,0,7);
  if(bad!=2 || hits!=1)$fatal(1,"coding violation triggered");
  inverted=1;bit_ticks=21;hold(0,5);match_any=1;
  send(16'h0000,0,0,-2);send(16'hffff,1,0,-2);
  if(good!=4 || hits!=3 || last_word!=16'hffff)$fatal(1,"inversion, odd ticks, or full-word data");
  // An epoch reset in the middle of a word must discard it completely.
  fork
   send(16'ha55a,1,0,-2);
   begin wait(dut.state==2);@(negedge clk);reset=1;repeat(3)@(negedge clk);reset=0;end
  join
  if(good!=4 || hits!=3)$fatal(1,"reset word leaked a match/event");
  enable=0;send(16'ha55a,1,0,-2);enable=1;
  if(good!=4 || hits!=3)$fatal(1,"disabled receiver emitted a word");
  send(16'ha55a,1,0,-2);
  if(good!=5 || hits!=4)$fatal(1,"receiver did not recover after reset/disable");
  $display("PASS MIL1553: sync, full words, parity/coding rejection, polarity, odd ticks, reset and disable");$finish;
 end
 initial begin #200000;$fatal(1,"timeout");end
endmodule

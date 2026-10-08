// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
module tb_manchester_boundaries;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,start=0,finish=0,cell_valid=0,first_level=0,second_level=1,ieee=1,msb=1;
 reg [4:0] word_bits=8;
 wire event_valid,event_error,done,overflow;
 wire [15:0] event_data,event_cell;wire signed [19:0] score;wire [16:0] good;
 manchester_hypothesis dut(.*);
 integer words=0,errors=0,k;reg [15:0] last_data=0,last_cell=0;
 always @(posedge clk)begin
  #1;if(event_valid)begin
   if(event_error)errors=errors+1;else words=words+1;
   last_data=event_data;last_cell=event_cell;
  end
 end
 // TRLC-LINKS: REQ-SDS-013
 task begin_frame(input [4:0] width);begin
  @(negedge clk);word_bits=width;start=1;cell_valid=0;finish=0;
  @(negedge clk);start=0;words=0;errors=0;
 end endtask
 // TRLC-LINKS: REQ-SDS-013
 task offer_cell(input value,input invalid);begin
  @(negedge clk);cell_valid=1;first_level=!value;second_level=invalid ? !value : value;
  @(negedge clk);cell_valid=0;repeat(2)@(negedge clk);
 end endtask
 // TRLC-LINKS: REQ-SDS-013
 task end_frame;begin
  @(negedge clk);finish=1;@(negedge clk);finish=0;repeat(3)@(negedge clk);
 end endtask
 initial begin
  repeat(4)@(negedge clk);reset=0;
  begin_frame(1);offer_cell(1,0);offer_cell(0,1);offer_cell(0,0);end_frame;
  if(words!=2 || errors!=1 || score!=-2 || good!=2 || !done)$fatal(1,"one-bit word/error serialization");
  begin_frame(8);offer_cell(1,0);offer_cell(0,0);offer_cell(1,0);offer_cell(0,1);offer_cell(0,1);
  if(words!=0 || errors!=1 || score!=3 || good!=3 || !done || last_cell!=2)$fatal(1,"idle must discard provisional violation and mark partial word");
  begin_frame(8);offer_cell(1,0);offer_cell(0,0);
  @(negedge clk);reset=1;@(negedge clk);reset=0;
  repeat(3)@(negedge clk);if(words || errors || !done)$fatal(1,"reset leaked partial word");
  begin_frame(8);for(k=7;k>=0;k=k-1)offer_cell((8'ha5>>k)&1,0);end_frame;
  if(words!=1 || errors || last_data!=16'ha5 || last_cell!=7)$fatal(1,"restart word or tail");
  begin_frame(8);offer_cell(1,0);end_frame;
  if(words || errors!=1 || last_cell!=0 || score!=1)$fatal(1,"explicit finish partial");
  begin_frame(0);offer_cell(1,0);if(!done || words || errors)$fatal(1,"invalid width accepted");
  begin_frame(16);
  for(k=0;k<65536;k=k+1)offer_cell(1,0);
  if(words!=4096 || errors || overflow || good!=65536 || score!=65536 || last_cell!=16'hffff)$fatal(1,"full cell ordinal range");
  offer_cell(1,0);if(!overflow || !done || words!=4096)$fatal(1,"cell ordinal wrapped silently");
  begin_frame(1);offer_cell(0,0);end_frame;
  if(overflow || words!=1 || errors || last_cell!=0)$fatal(1,"overflow not cleared by new frame");
  $display("PASS Manchester boundaries: word widths, errors, idle, reset, finish, ordinal overflow and recovery");$finish(0);
 end
 initial begin #3000000;$fatal(1,"timeout");end
endmodule

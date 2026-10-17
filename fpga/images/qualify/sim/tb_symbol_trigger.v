// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// symbol_trigger driven clock by clock from a file of line levels (one line a
// clock: "ab", 0/1 each), its table from +table (2048 hex bytes) and its
// configuration from plusargs; prints "M <clock>" for each clock after which
// match is high and "F <clocks>" at the end. The app's Go test
// (decode/symbol_trigger_rtl_test.go) compares it with the Go model.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
module tb_symbol_trigger;
 reg clk=0;
 reg reset=1,a=1,b=1;
 reg inv_a=0,inv_b=0,and_ab=0,clk_fall=0;reg [1:0] mode=0;reg [2:0] lead_len=0,prescale=0;
 reg [27:0] unit_q8=2048;reg [5:0] frame_units=0,period=0,phase0=0;reg [9:0] k=0;
 reg [63:0] pattern=0,mask=0;
 reg wr_en=0;reg [10:0] wr_addr=0;reg [7:0] wr_data=0;
 wire match;
 symbol_trigger dut(.clk(clk),.reset(reset),.a(a),.b(b),.inv_a(inv_a),.inv_b(inv_b),.and_ab(and_ab),.clk_fall(clk_fall),
  .mode(mode),.lead_len(lead_len),.prescale(prescale),.unit_q8(unit_q8),.frame_units(frame_units),.period(period),.phase0(phase0),
  .k(k),.pattern(pattern),.mask(mask),.wr_clk(clk),.wr_en(wr_en),.wr_addr(wr_addr),.wr_data(wr_data),.match(match));
 reg [7:0] tbl[0:2047];
 reg [1:0] lines[0:1048575];
 integer length,resets,i,v;
 reg [8*256-1:0] input_path,table_path;
 initial begin
  if(!$value$plusargs("input=%s",input_path) || !$value$plusargs("table=%s",table_path) || !$value$plusargs("length=%d",length))
   $fatal(1,"usage: +input= +table= +length=");
  if(!$value$plusargs("resets=%d",resets))resets=8;
  if($value$plusargs("inv_a=%d",v))inv_a=v;if($value$plusargs("inv_b=%d",v))inv_b=v;
  if($value$plusargs("and_ab=%d",v))and_ab=v;if($value$plusargs("clk_fall=%d",v))clk_fall=v;
  if($value$plusargs("mode=%d",v))mode=v;if($value$plusargs("lead_len=%d",v))lead_len=v;
  if($value$plusargs("prescale=%d",v))prescale=v;if($value$plusargs("unit_q8=%d",v))unit_q8=v;
  if($value$plusargs("frame_units=%d",v))frame_units=v;if($value$plusargs("period=%d",v))period=v;
  if($value$plusargs("phase0=%d",v))phase0=v;if($value$plusargs("k=%d",v))k=v;
  if(!$value$plusargs("pattern=%h",pattern))pattern=0;if(!$value$plusargs("mask=%h",mask))mask=0;
  $readmemh(table_path,tbl);
  $readmemb(input_path,lines,0,length-1);
  // the host writes the table while idle
  for(i=0;i<2048;i=i+1)begin
   wr_en=1;wr_addr=i;wr_data=tbl[i];#2 clk=1;#2 clk=0;
  end
  wr_en=0;
  for(i=0;i<length;i=i+1)begin
   reset=i<resets;a=lines[i][1];b=lines[i][0];
   #2 clk=1;#1;
   if(match)$display("M %0d",i);
   #1 clk=0;
  end
  $display("F %0d",length);
  $finish;
 end
endmodule

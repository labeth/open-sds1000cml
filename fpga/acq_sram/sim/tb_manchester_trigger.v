// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018, REQ-SDS-039
module tb_manchester_trigger #(parameter AW=7,EXTERNAL_RAM=1);
 reg clk=0;always #4 clk=~clk;
 reg reset=1,enable=1,tick=1,line=1,inverted=0,ieee=1,msb=1;
 reg [23:0] bit_ticks=20;reg [4:0] word_bits=8;
 reg [63:0] pattern=64'h00aa00b3;reg [2:0] pattern_len=2;
 reg [63:0] sample=0;
 wire match,event_valid,overflow;wire [7:0] event_kind;wire [15:0] event_data;wire [63:0] event_sample;
 wire [1:0] scratch_write;wire [AW:0] scratch_wr_a,scratch_wr_b,scratch_rd;
 wire [32:0] scratch_data_a,scratch_data_b,scratch_q_a,scratch_q_b;
 manchester_trigger #(.AW(AW),.EXTERNAL_RAM(EXTERNAL_RAM)) dut(.*);
 protocol_scratch ram(.clk(clk),.reset(reset),.manchester(1'b1),
  .usb_write(1'b0),.usb_wr(4'd0),.usb_rd(4'd0),.usb_data(64'd0),.usb_q(),
  .man_write(scratch_write),.man_wr_a(scratch_wr_a),.man_wr_b(scratch_wr_b),.man_rd(scratch_rd),
  .man_data_a(scratch_data_a),.man_data_b(scratch_data_b),.man_q_a(scratch_q_a),.man_q_b(scratch_q_b));
 reg samples[0:131071];reg [4095:0] input_file;
 integer length,period,width,convention,order,invert,i;
 reg [63:0] last_sample=0;
 always @(posedge clk)begin
  if(tick)sample<=sample+4;
  #1;if(event_valid)begin
   if(event_sample<last_sample || event_sample>sample)$fatal(1,"non-monotonic or future sample");
   last_sample=event_sample;
   $display("E %0d %0d %0d %0d",event_kind,event_data,event_sample,match);
  end else if(match)$fatal(1,"match without qualified END");
 end
 initial begin
  if(!$value$plusargs("input=%s",input_file) || !$value$plusargs("length=%d",length) ||
     !$value$plusargs("period=%d",period) || !$value$plusargs("width=%d",width) ||
     !$value$plusargs("ieee=%d",convention) || !$value$plusargs("msb=%d",order) ||
     !$value$plusargs("invert=%d",invert) || !$value$plusargs("pattern=%h",pattern))$fatal(1,"missing fixture");
  $readmemh(input_file,samples,0,length-1);
  bit_ticks=period;word_bits=width;ieee=convention;msb=order;inverted=invert;
  repeat(4)@(negedge clk);reset=0;sample=64'hfffffff0;
  for(i=0;i<length;i=i+1)begin
   // Odd-period cases also pause accepted ticks. Raw ordinals and receiver
   // time stop together, while frame publication may continue on clk.
   if(period==9 && i%97==0)begin tick=0;repeat(3)@(negedge clk);tick=1;end
   line=samples[i]^inverted;@(negedge clk);
  end
  repeat(8*period)@(negedge clk);
  $display("F %0d",overflow);$finish(0);
 end
 initial begin #5000000;$fatal(1,"timeout");end
endmodule

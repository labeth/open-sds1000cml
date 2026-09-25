// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018, REQ-SDS-039
module tb_manchester_replay_rate;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,begin_frame=0,close_frame=0;
 reg [31:0] gap=24;reg [63:0] sample=64'h100000000;
 reg [23:0] bit_ticks=8;reg [4:0] word_bits=1;
 reg [1:0] candidate_valid=0,candidate_error=0,receiver_overflow=0;
 reg [31:0] candidate_data=0,candidate_cell=0;
 reg signed [19:0] score_a=128,score_b=1;
 reg [16:0] good_a=128,good_b=128;
 reg [63:0] pattern=0;reg [2:0] pattern_len=0;
 wire match,event_valid,overflow;wire [7:0] event_kind;
 wire [15:0] event_data;wire [63:0] event_sample;
 wire [1:0] scratch_write;wire [7:0] scratch_wr_a,scratch_wr_b,scratch_rd;
 wire [32:0] scratch_data_a,scratch_data_b,scratch_q_a,scratch_q_b;
 manchester_frames #(.EXTERNAL_RAM(1)) dut(.*);
 protocol_scratch ram(.clk(clk),.reset(reset),.manchester(1'b1),
  .usb_write(1'b0),.usb_wr(4'd0),.usb_rd(4'd0),.usb_data(64'd0),.usb_q(),
  .man_write(scratch_write),.man_wr_a(scratch_wr_a),.man_wr_b(scratch_wr_b),.man_rd(scratch_rd),
  .man_data_a(scratch_data_a),.man_data_b(scratch_data_b),.man_q_a(scratch_q_a),.man_q_b(scratch_q_b));
 integer frame=0,index=0,done=0,hits=0,sending,k;
 reg in_frame=0;reg [63:0] expected_anchor=0,last_payload=0;
 always @(posedge clk)begin
  #1;
  if(overflow)$fatal(1,"replay cannot keep up with minimum-period reception");
  if(match)begin
   if(!event_valid || event_kind!=3)$fatal(1,"match outside END");
   hits=hits+1;
  end
  if(event_valid)case(event_kind)
   1:begin
    if(in_frame)$fatal(1,"interleaved frame publication");
    expected_anchor=64'h100000000+frame*64'd8192;
    if(event_sample!==expected_anchor-(frame%2 ? 64'd16 : 64'd0))$fatal(1,"START ordinal");
    in_frame=1;index=0;
   end
   2:begin
    if(!in_frame || index>=128 || event_data!==(index%2) ||
       event_sample!==expected_anchor+index*64'd32+(frame%2 ? 64'd8 : 64'd24))
     $fatal(1,"replay data/order/timestamp frame=%0d index=%0d",frame,index);
    last_payload=event_sample;index=index+1;
   end
   3:begin
    if(!in_frame || index!=128 || event_sample!==last_payload || event_data!=0 || !match)
     $fatal(1,"incomplete frame or END timestamp");
    in_frame=0;frame=frame+1;done=done+1;
   end
   default:$fatal(1,"unexpected replay event %0d",event_kind);
  endcase
 end
 initial begin
  repeat(4)@(negedge clk);reset=0;
  for(sending=0;sending<12;sending=sending+1)begin
   sample=64'h100000000+sending*64'd8192;
   begin_frame=1;@(negedge clk);begin_frame=0;
   // Full 128-record frames, one word each eight receiver clocks. The next
   // frame is received into the alternate bank while the prior one replays.
   for(k=0;k<128;k=k+1)begin
    candidate_valid=3;candidate_data={15'd0,k[0],15'd0,k[0]};
    candidate_cell={k[15:0],k[15:0]};
    @(negedge clk);candidate_valid=0;repeat(7)@(negedge clk);
   end
   score_a=sending%2 ? 1 : 128;score_b=sending%2 ? 128 : 1;
   close_frame=1;@(negedge clk);close_frame=0;
   repeat(24)@(negedge clk);
  end
  repeat(1000)@(negedge clk);
  if(done!=12 || hits!=12 || in_frame)$fatal(1,"lost sustained frames done=%0d hits=%0d",done,hits);
  $display("PASS Manchester replay: 12 full frames, 1536 words, minimum period, both phases and concurrent capture");
  $finish;
 end
 initial begin #200000;$fatal(1,"timeout");end
endmodule

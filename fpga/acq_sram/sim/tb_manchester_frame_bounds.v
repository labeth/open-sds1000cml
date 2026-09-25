// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018, REQ-SDS-039
module tb_manchester_frame_bounds;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,begin_frame=0,close_frame=0;
 reg [31:0] gap=100;reg [63:0] sample=64'h100000000;
 reg [23:0] bit_ticks=20;reg [4:0] word_bits=8;
 reg [1:0] candidate_valid=0,candidate_error=0,receiver_overflow=0;
 reg [31:0] candidate_data=0,candidate_cell=0;
 reg signed [19:0] score_a=8,score_b=1;reg [16:0] good_a=8,good_b=8;
 reg [63:0] pattern=64'h1234;reg [2:0] pattern_len=1;
 wire match,event_valid,overflow;wire [7:0] event_kind;wire [15:0] event_data;wire [63:0] event_sample;
 wire [1:0] scratch_write;wire [2:0] scratch_wr_a,scratch_wr_b,scratch_rd;
 wire [32:0] scratch_data_a,scratch_data_b,scratch_q_a,scratch_q_b;
 manchester_frames #(.AW(2),.EXTERNAL_RAM(1)) dut(.*);
 protocol_scratch ram(.clk(clk),.reset(reset),.manchester(1'b1),
  .usb_write(1'b0),.usb_wr(4'd0),.usb_rd(4'd0),.usb_data(64'd0),.usb_q(),
  .man_write(scratch_write),.man_wr_a({5'd0,scratch_wr_a}),.man_wr_b({5'd0,scratch_wr_b}),.man_rd({5'd0,scratch_rd}),
  .man_data_a(scratch_data_a),.man_data_b(scratch_data_b),.man_q_a(scratch_q_a),.man_q_b(scratch_q_b));
 integer hits=0,losses=0,words=0,errors=0,starts=0,ends=0,k;
 reg [15:0] last_word=0;reg [63:0] data_sample=0,payload_sample=0,start_sample=0;
 always @(posedge clk)begin
  #1;if(match)begin if(!event_valid || event_kind!=3)$fatal(1,"unqualified match");hits=hits+1;end
  if(event_valid)case(event_kind)
   1:begin starts=starts+1;start_sample=event_sample;end
   2:begin words=words+1;last_word=event_data;data_sample=event_sample;payload_sample=event_sample;end
   3:begin
    if(event_sample!==payload_sample)$fatal(1,"END lost final DATA/ERROR timestamp");
    ends=ends+1;
   end
   4:begin errors=errors+1;payload_sample=event_sample;end
   5:losses=losses+1;
   default:$fatal(1,"unexpected kind");
  endcase
 end
 // TRLC-LINKS: REQ-SDS-013
 task clear_epoch;begin
  @(negedge clk);reset=1;begin_frame=0;close_frame=0;candidate_valid=0;receiver_overflow=0;
  repeat(3)@(negedge clk);reset=0;hits=0;losses=0;words=0;errors=0;starts=0;ends=0;
 end endtask
 // TRLC-LINKS: REQ-SDS-013
 task open_buffer(input [31:0] lead);begin
  @(negedge clk);gap=lead;begin_frame=1;@(negedge clk);begin_frame=0;
 end endtask
 // TRLC-LINKS: REQ-SDS-013
 task offer(input [15:0] a,b,input [1:0] bad,input [15:0] index);begin
  @(negedge clk);candidate_valid=3;candidate_error=bad;candidate_data={b,a};candidate_cell={index,index};
  @(negedge clk);candidate_valid=0;repeat(2)@(negedge clk);
 end endtask
 // TRLC-LINKS: REQ-SDS-013
 task commit(input signed [19:0] a,b,input [31:0] trailing);begin
  @(negedge clk);score_a=a;score_b=b;gap=trailing;close_frame=1;@(negedge clk);close_frame=0;
 end endtask
 initial begin
  clear_epoch;open_buffer(100);offer(16'h1234,16'h9999,0,7);commit(8,1,60);
  repeat(30)@(negedge clk);
  if(overflow || hits!=1 || words!=1 || last_word!=16'h1234 || starts!=1 || ends!=1 || data_sample!=64'h10000026c)$fatal(1,"selected frame, match or 64-bit timestamp");
  clear_epoch;open_buffer(100);offer(16'h1111,16'h1234,0,7);commit(8,8,40);
  repeat(12)@(negedge clk);if(event_valid || starts || hits)$fatal(1,"unresolved phase escaped");
  sample=64'h100010000;open_buffer(60);commit(-1,-1,100);
  repeat(30)@(negedge clk);
  if(overflow || hits!=1 || words!=1 || last_word!=16'h1234)$fatal(1,"short trailing idle must choose phase B");
  // Resolve a tie on the first clock after close, before a deferred close
  // decision has committed. The next frame must not steal its gap/quality.
  clear_epoch;open_buffer(100);offer(16'h1111,16'h1234,0,7);commit(8,8,40);
  gap=60;begin_frame=1;sample=sample+10000;
  @(negedge clk);begin_frame=0;commit(-1,-1,100);repeat(30)@(negedge clk);
  if(overflow || hits!=1 || words!=1 || last_word!=16'h1234)$fatal(1,"immediate boundary lost pending tie");
  // Close and begin on the same edge: old metadata belongs to the old bank.
  clear_epoch;open_buffer(100);offer(16'h1111,16'h1234,0,7);
  @(negedge clk);score_a=8;score_b=8;gap=60;close_frame=1;begin_frame=1;sample=sample+10000;
  @(negedge clk);close_frame=0;begin_frame=0;commit(-1,-1,100);repeat(30)@(negedge clk);
  if(overflow || hits!=1 || words!=1 || last_word!=16'h1234)$fatal(1,"coincident close/begin mixed banks");
  clear_epoch;open_buffer(100);offer(16'h1234,0,0,7);commit(8,1,60);
  reset=1;repeat(3)@(negedge clk);reset=0;repeat(30)@(negedge clk);
  if(overflow || starts || words || hits)$fatal(1,"reset leaked a pending close decision");
  clear_epoch;open_buffer(100);offer(16'h1111,16'h1234,0,7);commit(8,8,40);
  repeat(4)@(negedge clk);gap=60;begin_frame=1;
  @(negedge clk);begin_frame=0;reset=1;
  repeat(3)@(negedge clk);reset=0;repeat(30)@(negedge clk);
  if(overflow || starts || words || hits)$fatal(1,"reset leaked a resolved but unpublished tie");
  clear_epoch;open_buffer(100);offer(16'h1234,16'h9999,0,7);commit(8,8,40);
  repeat(8)@(negedge clk);gap=100;repeat(30)@(negedge clk);
  if(overflow || hits!=1 || last_word!=16'h1234)$fatal(1,"long trailing idle must choose phase A");
  clear_epoch;good_a=7;good_b=8;
  open_buffer(100);offer(16'h1234,16'h1234,0,7);commit(8,8,40);
  // A later receiver frame must not change the qualification held for this tie.
  good_a=16;good_b=0;repeat(8)@(negedge clk);gap=100;repeat(30)@(negedge clk);
  if(overflow || starts || words || hits)$fatal(1,"tie accepted a phase below word width");
  clear_epoch;good_a=7;good_b=8;
  open_buffer(100);offer(16'h1234,16'h1234,0,7);commit(8,8,40);
  good_a=16;good_b=0;repeat(8)@(negedge clk);open_buffer(60);commit(-1,-1,100);
  repeat(30)@(negedge clk);
  if(overflow || hits!=1 || words!=1)$fatal(1,"tie lost qualified phase B");
  good_a=8;good_b=8;
  clear_epoch;open_buffer(100);offer(16'h1234,0,0,7);offer(0,0,1,8);commit(4,1,60);
  repeat(40)@(negedge clk);
  if(overflow || hits || words!=1 || errors!=1 || starts!=1 || ends!=1)$fatal(1,"late coding error did not veto frame match");
  clear_epoch;open_buffer(100);offer(16'h1234,0,0,7);clear_epoch;
  repeat(30)@(negedge clk);if(starts || words || hits)$fatal(1,"epoch reset leaked an uncommitted frame");
  pattern=64'h1111222233334444;pattern_len=4;
  open_buffer(100);
  offer(16'h1111,0,0,7);offer(16'h2222,0,0,15);offer(16'h3333,0,0,23);offer(16'h4444,0,0,31);
  commit(32,1,60);repeat(40)@(negedge clk);
  if(overflow || hits!=1 || words!=4)$fatal(1,"four full-width word predicate");
  clear_epoch;pattern=64'h1234abcd;pattern_len=2;
  open_buffer(100);offer(16'h1234,0,0,7);commit(8,1,60);repeat(30)@(negedge clk);
  sample=sample+10000;
  open_buffer(100);offer(16'habcd,0,0,7);commit(8,1,60);repeat(30)@(negedge clk);
  if(overflow || hits || words!=2 || ends!=2)$fatal(1,"predicate crossed a frame boundary");
  clear_epoch;pattern_len=0;
  open_buffer(100);offer(16'h8888,0,0,7);commit(8,1,60);repeat(30)@(negedge clk);
  if(overflow || hits!=1 || words!=1)$fatal(1,"any-data predicate");
  pattern=64'h1234;pattern_len=1;
  clear_epoch;open_buffer(100);
  for(k=0;k<5;k=k+1)offer(16'h1234,0,0,k*8+7);
  commit(40,1,60);repeat(30)@(negedge clk);
  if(!overflow || losses!=1 || starts || words || hits)$fatal(1,"frame overflow leaked a partial valid packet");
  open_buffer(100);offer(16'h1234,0,0,7);commit(8,1,60);repeat(20)@(negedge clk);
  if(losses!=1 || words || hits)$fatal(1,"sticky overflow resumed without reset");
  clear_epoch;open_buffer(100);offer(16'h1234,0,0,7);commit(8,1,60);repeat(30)@(negedge clk);
  if(overflow || hits!=1 || words!=1)$fatal(1,"new epoch did not recover");
  // A protocol violation on the DATA or matching END clock must publish only
  // LOSS, even though non-observable scratch registers may update that clock.
  for(k=0;k<2;k=k+1)begin
   clear_epoch;open_buffer(100);offer(16'h1234,0,0,7);commit(8,1,60);
   if(k==0)wait(dut.state==dut.EMIT);else wait(dut.state==dut.END);
   @(negedge clk);close_frame=1;@(negedge clk);close_frame=0;
   repeat(20)@(negedge clk);
   if(!overflow || losses!=1 || hits || ends || words!=k)$fatal(1,"failure did not override publication/match");
  end
  // Upper supported period and cell ordinal exercise carries beyond 32 bits;
  // both phase offsets must survive a near-wrap full-width anchor.
  for(k=0;k<2;k=k+1)begin
   clear_epoch;bit_ticks=24'h3fffff;sample=k==0 ? 64'hfffffffffffffff0 : 64'h100000000;
   open_buffer(100);offer(16'h1234,16'h1234,0,16'hffff);
   if(k==0)commit(8,1,60);else commit(1,8,60);
   repeat(30)@(negedge clk);
   if(overflow || hits!=1 || words!=1 || start_sample!==sample-(k==0 ? 64'd0 : 64'd8388606) || data_sample!=
      (sample + 64'd65535*64'd4194303*4 + (k==0 ? 64'd12582912 : 64'd4194304)))
    $fatal(1,"maximum-period timestamp carry or wrap");
  end
  $display("PASS Manchester frames: phase ties, buffering, timestamps, whole-frame veto, overflow and epoch recovery");$finish(0);
 end
 initial begin #100000;$fatal(1,"timeout");end
endmodule

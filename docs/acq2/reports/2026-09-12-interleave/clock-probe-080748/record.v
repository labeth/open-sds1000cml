// Record bookkeeping for a sequential external SRAM. There is no sample RAM here.
// All inputs are in clk's domain. step means one write was ACCEPTED by the SRAM
// writer, not merely offered by an ADC. The accepted word's logical address is
// write_addr. Physical addressing may have an arbitrary constant origin.
//
// pre_count excludes the triggering word; post_count includes it. Counts are
// AW+1 bits so DEPTH itself is representable. A trigger is accepted only after
// pre_count writes. The record remains frozen until the next valid arm.
// Invalid configuration rejects arm without damaging an existing frozen record.
module sram_record #(parameter AW=19)(
 input wire clk, reset, arm, halt,
 input wire [AW:0] pre_count, post_count,
 input wire step, trigger,
 output reg running=0, done=0, triggered=0, config_error=0,
 output reg [AW-1:0] write_addr=0, record_start=0,
 output reg [AW:0] record_length=0, trigger_index=0, filled=0
);
 localparam [AW:0] DEPTH={1'b1,{AW{1'b0}}};
 reg [AW:0] pre_l=0, post_l=0, remaining=0;
 reg [AW:0] pre_left=0,pre_plus_one=0,post_minus_one=0;
 reg history_ready=0,post_one=0,last_post=0;
 reg [AW-1:0] pre_start=0;
 wire [AW+1:0] requested={1'b0,pre_count}+{1'b0,post_count};
 wire [AW:0] fill_next=filled==DEPTH ? DEPTH : filled+1'b1;
 always @(posedge clk) begin
  if(reset) begin
   running<=0;done<=0;triggered<=0;config_error<=0;
   write_addr<=0;record_start<=0;record_length<=0;trigger_index<=0;filled<=0;
   pre_l<=0;post_l<=0;remaining<=0;pre_left<=0;pre_plus_one<=0;post_minus_one<=0;history_ready<=0;pre_start<=0;post_one<=0;last_post<=0;
  end else if(arm) begin
   if(post_count==0 || requested>{1'b0,DEPTH}) config_error<=1;
   else begin
    running<=1;done<=0;triggered<=0;config_error<=0;
    write_addr<=0;record_start<=0;record_length<=0;trigger_index<=0;filled<=0;
    pre_l<=pre_count;post_l<=post_count;remaining<=0;
    pre_left<=pre_count;history_ready<=pre_count==0;post_one<=post_count==1;last_post<=0;
    pre_plus_one<=pre_count+1'b1;post_minus_one<=post_count-1'b1;
    pre_start<=-pre_count[AW-1:0];
   end
  end else if(running) begin
   // HALT has priority; the caller must not issue a SRAM write on this edge.
   if(halt) begin
    running<=0;done<=1;
    if(!triggered) begin
     record_start<=filled[AW] ? write_addr : {AW{1'b0}};record_length<=filled;
     trigger_index<=filled;
    end
   end else if(step) begin
    write_addr<=write_addr+1'b1;filled<=fill_next;pre_start<=pre_start+1'b1;
    if(!history_ready)begin pre_left<=pre_left-1'b1;if(pre_left==1)history_ready<=1;end
    if(!triggered && trigger && history_ready) begin
     triggered<=1;record_start<=pre_start;
     trigger_index<=pre_l;record_length<=pre_plus_one;
     remaining<=post_minus_one;last_post<=post_l==2;
     if(post_one) begin running<=0;done<=1;end
    end else if(triggered) begin
     record_length<=record_length+1'b1;remaining<=remaining-1'b1;last_post<=remaining==2;
     if(last_post) begin running<=0;done<=1;end
    end
   end
  end
 end
endmodule

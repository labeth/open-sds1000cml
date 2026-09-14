// Record bookkeeping for a sequential external SRAM. There is no sample RAM here.
// All inputs are in clk's domain. step means one write was ACCEPTED by the SRAM
// writer, not merely offered by an ADC. The accepted word's logical address is
// write_addr. Physical addressing may have an arbitrary constant origin.
//
// pre_count excludes the triggering word; post_count includes it. Counts are
// AW+1 bits so DEPTH itself is representable. A trigger is accepted only after
// pre_count writes. The record remains frozen until the next valid arm.
// Invalid configuration rejects arm without damaging an existing frozen record.
module sram_record #(parameter AW=19,CONFIG_VALIDATED=0)(
 input wire clk, reset, arm, halt,
 input wire [AW:0] pre_count, post_count,
 input wire step, trigger,
 output reg running=0, done=0, triggered=0, config_error=0,
 output reg [AW-1:0] write_addr=0, output wire [AW-1:0] record_start,
 output wire [AW:0] record_length, output wire [AW:0] trigger_index, output wire [AW:0] filled
);
 reg [AW:0] post_length=0,halt_length=0;reg post_carry=0;
 reg full=0,write_last=0,write_carry=0,halted=0;
 assign filled=full ? DEPTH : {1'b0,write_addr};
 assign record_length=triggered ? post_length : (halted ? halt_length : {(AW+1){1'b0}});
 assign trigger_index=triggered ? pre_l : (halted ? halt_length : {(AW+1){1'b0}});
 reg [AW-1:0] trigger_start=0,halt_start=0;
 assign record_start=triggered ? trigger_start : (halted ? halt_start : {AW{1'b0}});
 always @(posedge clk)begin
  // Captured speculatively; exposed only when the matching trigger is accepted.
  if(step && trigger && history_ready && !triggered)trigger_start<=pre_start;
  if(halt && !triggered)begin halt_length<=filled;halt_start<=filled[AW]?write_addr:{AW{1'b0}};end
  if(reset)halted<=0;
  else if(arm)begin
   if(CONFIG_VALIDATED || (post_count!=0 && requested<={1'b0,DEPTH}))halted<=0;
  end else if(halt && !triggered)halted<=1;
 end
 always @(posedge clk)begin
  // Preload while untriggered, including the first accepted trigger edge.
  if(!triggered)begin post_length<=pre_plus_one;post_carry<=&pre_plus_one[7:0];end
  else if(running && step && !halt && (!arm || CONFIG_VALIDATED))begin
   if(AW>8)begin post_length[7:0]<=post_length[7:0]+1'b1;post_length[AW:8]<=post_length[AW:8]+post_carry;end
   else post_length<=post_length+1'b1;
   post_carry<=post_length[7:0]==8'hfe;
  end
 end
 localparam [AW:0] DEPTH={1'b1,{AW{1'b0}}};
 reg [AW:0] pre_l=0, post_l=0, remaining=0;
 reg [AW:0] pre_left=0,pre_plus_one=0,post_minus_one=0;
 reg history_ready=0,post_one=0,post_two=0,last_post=0,pre_last=0,pre_carry=0;
 reg [AW-1:0] pre_start=0;
 wire [AW+1:0] requested={1'b0,pre_count}+{1'b0,post_count};
 // This private countdown is reloaded on every trigger. Its idle/pretrigger
 // value is irrelevant; keep running/halt off its wide arithmetic enable.
 always @(posedge clk) begin
  if(reset)last_post<=0;
  else if(arm)begin if(CONFIG_VALIDATED || (post_count!=0 && requested<={1'b0,DEPTH}))last_post<=0;end
  else if(step) begin
   if(!triggered)begin
    if(trigger && history_ready)begin remaining<=post_minus_one;last_post<=post_two;end
    else begin remaining<=remaining-1'b1;last_post<=0;end
   end else begin remaining<=remaining-1'b1;last_post<=remaining==2;end
  end
 end
 always @(posedge clk)begin
  if(arm)begin
   if(CONFIG_VALIDATED || (post_count!=0 && requested<={1'b0,DEPTH}))begin
    pre_left<=pre_count;pre_start<=-pre_count[AW-1:0];pre_last<=pre_count==1;pre_carry<=pre_count[7:0]==1;
   end
  end else if(step)begin
   pre_left<=pre_left-1'b1;pre_last<=pre_left==2;pre_carry<=pre_start[7:0]==8'hfe;
   if(AW>8)begin pre_start[7:0]<=pre_start[7:0]+1'b1;pre_start[AW-1:8]<=pre_start[AW-1:8]+pre_carry;end
   else pre_start<=pre_start+1'b1;
  end
 end
 always @(posedge clk) begin
  if(reset) begin
   running<=0;done<=0;triggered<=0;config_error<=0;
   write_addr<=0;full<=0;write_last<=0;write_carry<=0;
   pre_l<=0;post_l<=0;pre_plus_one<=0;post_minus_one<=0;history_ready<=0;post_one<=0;
  end else if(arm) begin
   if(!CONFIG_VALIDATED && (post_count==0 || requested>{1'b0,DEPTH})) config_error<=1;
   else begin
    running<=1;done<=0;triggered<=0;config_error<=0;
    write_addr<=0;full<=0;write_last<=0;write_carry<=0;
    pre_l<=pre_count;post_l<=post_count;
    history_ready<=pre_count==0;post_one<=post_count==1;post_two<=post_count==2;
    pre_plus_one<=pre_count+1'b1;post_minus_one<=post_count-1'b1;
   end
  end else if(running) begin
   // HALT has priority; the caller must not issue a SRAM write on this edge.
   if(halt) begin
    running<=0;done<=1;
   end else if(step) begin
    if(last_post || (trigger && history_ready && post_one))begin running<=0;done<=1;end
    if(AW>8)begin write_addr[7:0]<=write_addr[7:0]+1'b1;write_addr[AW-1:8]<=write_addr[AW-1:8]+write_carry;end
    else write_addr<=write_addr+1'b1;
    write_carry<=write_addr[7:0]==8'hfe;write_last<=write_addr==DEPTH-2;
    if(write_last)full<=1;
    if(!history_ready && pre_last)history_ready<=1;
    if(!triggered && trigger && history_ready) begin
     triggered<=1;
    end
   end
  end
 end
endmodule

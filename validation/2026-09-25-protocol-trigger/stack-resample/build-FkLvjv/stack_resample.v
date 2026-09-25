// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// One aligned hit -> fine-grid two-channel samples. Memory supplies adjacent
// drift-normalized Q24 values; fitting/normalizing those values is not done here.
// Ineligible bins are explicit zero-mask outputs and never issue memory reads.
// A replacing start drains old memory responses. Reset must also reset adapter.
// Output is held until consumed; no accumulation or retained-memory writes here.
// TRLC-LINKS: REQ-SDS-141
module stack_resample(
 input wire clk,reset,start,
 input wire [31:0] hit_position,bin_count,factor,record_samples,
 input wire signed [24:0] delta,input wire [1:0] channel_mask,input wire odd,
 output wire request_valid,input wire request_ready,output wire [31:0] request_index,
 input wire response_valid,response_error,output wire response_ready,
 input wire [36:0] ch0_left,ch0_right,ch1_left,ch1_right,
 output wire valid,input wire ready,output wire [31:0] bin,
 output wire [36:0] ch0_value,ch1_value,output wire [1:0] result_mask,output wire result_odd,
 output reg busy=0,done=0,invalid=0
);
 localparam IDLE=0,POSITION=1,REQUEST=2,MEMORY=3,INTERPOLATE=4,OUTPUT=5;
 reg [2:0] state=IDLE;
 reg pending=0,discard_pending=0,skip=0;
 reg [1:0] mask=0;reg odd_l=0;
 wire pos_valid,pos_ready,pos_eligible,pos_busy,pos_done,pos_invalid;
 wire signed [33:0] sample_index;wire [23:0] fraction;
 wire interpolate_busy,interpolate_done;
 wire [36:0] value0,value1;wire [1:0] value_mask;
 assign request_valid=state==REQUEST && !pending && busy && !reset && !start;
 assign request_index=sample_index[31:0];
 assign response_ready=pending && !reset;
 wire interpolate_start=response_valid && pending && !discard_pending && state==MEMORY && !response_error && !reset && !start;
 assign valid=state==OUTPUT && busy && !reset && !start;
 assign pos_ready=valid && ready;
 assign ch0_value=skip ? 37'd0:value0;
 assign ch1_value=skip ? 37'd0:value1;
 assign result_mask=skip ? 2'd0:value_mask;
 assign result_odd=odd_l;
 stack_positions positions(.clk(clk),.reset(reset),.start(start),.hit_position(hit_position),
  .bin_count(bin_count),.factor(factor),.record_samples(record_samples),.delta(delta),
  .valid(pos_valid),.ready(pos_ready),.bin(bin),.sample_index(sample_index),.fraction(fraction),
  .eligible(pos_eligible),.busy(pos_busy),.done(pos_done),.invalid(pos_invalid));
 stack_interpolate interpolate(.clk(clk),.reset(reset || start),.start(interpolate_start),
  .ch0_left(ch0_left),.ch0_right(ch0_right),.ch1_left(ch1_left),.ch1_right(ch1_right),
  .fraction(fraction),.channel_mask(mask),.busy(interpolate_busy),.done(interpolate_done),
  .ch0_value(value0),.ch1_value(value1),.result_mask(value_mask));
 always @(posedge clk)begin
  done<=0;
  if(reset)begin
   state<=IDLE;busy<=0;invalid<=0;pending<=0;discard_pending<=0;
  end else if(start)begin
   mask<=channel_mask;odd_l<=odd;state<=POSITION;busy<=1;invalid<=0;
   pending<=pending && !response_valid;discard_pending<=pending && !response_valid;
  end else begin
   if(response_valid && pending)begin
    pending<=0;
    if(discard_pending)discard_pending<=0;
    else if(state==MEMORY)begin
     if(response_error)begin state<=IDLE;busy<=0;done<=1;invalid<=1;end
     else state<=INTERPOLATE;
    end
   end
   if(pos_done && busy)begin state<=IDLE;busy<=0;done<=1;invalid<=pos_invalid;end
   else case(state)
    POSITION:if(pos_valid && !pending)begin
     skip<=!pos_eligible || mask==0;state<=pos_eligible && mask!=0 ? REQUEST:OUTPUT;
    end
    REQUEST:if(request_valid && request_ready)begin pending<=1;state<=MEMORY;end
    INTERPOLATE:if(interpolate_done)state<=OUTPUT;
    OUTPUT:if(ready)state<=POSITION;
   endcase
  end
 end
endmodule

// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Two-channel linear interpolation of nonnegative normalized Q24 samples.
// 37-bit inputs cover [0,8192), including drift-corrected values above ADC 255.
// One serial multiplier serves both channels. Product truncation contributes
// less than one Q24 unit of error. Gain/offset fitting and clipping policy are
// upstream responsibilities; the output mask preserves per-channel eligibility.
// TRLC-LINKS: REQ-SDS-141
module stack_interpolate(
 input wire clk,reset,start,
 input wire [36:0] ch0_left,ch0_right,ch1_left,ch1_right,
 input wire [23:0] fraction,input wire [1:0] channel_mask,
 output reg busy=0,done=0,
 output reg [36:0] ch0_value=0,ch1_value=0,
 output reg [1:0] result_mask=0
);
 localparam IDLE=0,PREP=1,MAGNITUDE=2,MULTIPLY=3,PUBLISH=4;
 reg [2:0] state=IDLE;
 reg [36:0] a0=0,b0=0,a1=0,b1=0,base=0,multiplicand=0;
 reg [23:0] weight=0;
 reg channel=0,negative=0;
 reg signed [37:0] difference=0;
 reg [60:0] product=0;
 reg [4:0] bit_index=0;
 wire [37:0] multiply_sum={1'b0,product[60:24]}+(product[0] ? {1'b0,multiplicand}:38'd0);
 wire [36:0] value=negative ? base-product[60:24]:base+product[60:24];
 always @(posedge clk)begin
  done<=0;
  if(reset)begin state<=IDLE;busy<=0;result_mask<=0;end
  else if(start)begin
   a0<=ch0_left;b0<=ch0_right;a1<=ch1_left;b1<=ch1_right;weight<=fraction;
   result_mask<=channel_mask;channel<=!channel_mask[0];ch0_value<=0;ch1_value<=0;
   busy<=|channel_mask;done<=channel_mask==0;state<=channel_mask==0 ? IDLE:PREP;
  end else case(state)
   PREP:begin
    base<=channel ? a1:a0;
    difference<=$signed({1'b0,channel ? b1:b0})-$signed({1'b0,channel ? a1:a0});
    state<=MAGNITUDE;
   end
   MAGNITUDE:begin
    negative<=difference[37];multiplicand<=difference[37] ? -difference:difference;
    product<={37'd0,weight};bit_index<=23;state<=MULTIPLY;
   end
   MULTIPLY:begin
    product<={multiply_sum,product[23:1]};
    if(bit_index==0)state<=PUBLISH;else bit_index<=bit_index-1'b1;
   end
   PUBLISH:begin
    if(channel)ch1_value<=value;else ch0_value<=value;
    if(!channel && result_mask[1])begin channel<=1;state<=PREP;end
    else begin busy<=0;done<=1;state<=IDLE;end
   end
  endcase
 end
endmodule

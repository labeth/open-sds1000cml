// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// One channel/bin read-modify-write arithmetic. The caller owns memory and
// must serialize updates to each bin. Values/sums use Q24, squares use Q48.
// Output is atomic and held until ready. On overflow all old fields return
// unchanged with invalid asserted; reset/start cancel an unconsumed result.
// A single 36-bit adder is shared by squaring and all accumulator updates.
// TRLC-LINKS: REQ-SDS-141
module stack_accumulate(
 input wire clk,reset,start,enabled,odd,
 input wire [36:0] value,
 input wire [68:0] sum,sum_a,
 input wire [105:0] sum2,
 input wire [31:0] count,count_a,
 output wire valid,input wire ready,
 output reg busy=0,invalid=0,
 output wire [68:0] result_sum,result_sum_a,
 output wire [105:0] result_sum2,
 output wire [31:0] result_count,result_count_a
);
 localparam IDLE=0,SQUARE=1,SHIFT=2,ADD=3,SUM=4,SUM2=5,SUMA=6,FINISH=7,OUTPUT=8;
 reg [3:0] state=IDLE,after_add=IDLE;
 reg odd_l=0;
 reg [36:0] value_l=0,multiplier=0;
 reg [73:0] multiplicand=0;
 reg [5:0] bit_index=0;
 // The adder result is also the running square. Once multiplication ends,
 // its dead multiplicand register retains the exact 74-bit square.
 reg [107:0] a=0,answer=0;
 reg [73:0] b=0;
 reg carry=0;reg [1:0] limb=0;
 wire [36:0] step={1'b0,a[35:0]}+{1'b0,b[35:0]}+carry;
 reg [68:0] old_sum=0,old_sum_a=0,new_sum=0,new_sum_a=0;
 reg [105:0] old_sum2=0,new_sum2=0;
 reg [31:0] old_count=0,old_count_a=0,new_count=0,new_count_a=0;
 assign valid=state==OUTPUT && !reset && !start;
 assign result_sum=invalid ? old_sum:new_sum;
 assign result_sum2=invalid ? old_sum2:new_sum2;
 assign result_sum_a=invalid ? old_sum_a:new_sum_a;
 assign result_count=invalid ? old_count:new_count;
 assign result_count_a=invalid ? old_count_a:new_count_a;
 always @(posedge clk)begin
  if(reset)begin state<=IDLE;busy<=0;invalid<=0;end
  else if(start)begin
   old_sum<=sum;old_sum2<=sum2;old_sum_a<=sum_a;
   old_count<=count;old_count_a<=count_a;
   new_sum<=sum;new_sum2<=sum2;new_sum_a<=sum_a;
   new_count<=count;new_count_a<=count_a;
   odd_l<=odd;value_l<=value;multiplier<=value;multiplicand<={37'd0,value};
   answer<=0;bit_index<=0;busy<=1;invalid<=0;
   if(!enabled)state<=OUTPUT;
   else if(count==32'hffffffff || (odd && count_a==32'hffffffff))begin
    invalid<=1;state<=OUTPUT;
   end else begin
    new_count<=count+1'b1;new_count_a<=count_a+odd;state<=SQUARE;
   end
  end else case(state)
   IDLE:begin end
   SQUARE:begin
    if(multiplier[0])begin
     a<=answer;b<=multiplicand;carry<=0;limb<=0;
     after_add<=SHIFT;state<=ADD;
    end else state<=SHIFT;
   end
   SHIFT:begin
    if(bit_index==36)begin
     multiplicand<=answer[73:0];
     a<={39'd0,old_sum};b<={37'd0,value_l};answer<=0;carry<=0;limb<=0;
     after_add<=SUM;state<=ADD;
    end else begin
     multiplier<=multiplier>>1;multiplicand<=multiplicand<<1;
     bit_index<=bit_index+1'b1;state<=SQUARE;
    end
   end
   ADD:begin
    answer<={step[35:0],answer[107:36]};a<=a>>36;b<=b>>36;carry<=step[36];
    if(limb==2)state<=after_add;else limb<=limb+1'b1;
   end
   SUM:begin
    new_sum<=answer[68:0];if(|answer[107:69] || carry)invalid<=1;
    a<={2'd0,old_sum2};b<=multiplicand;answer<=0;carry<=0;limb<=0;after_add<=SUM2;state<=ADD;
   end
   SUM2:begin
    new_sum2<=answer[105:0];if(|answer[107:106] || carry)invalid<=1;
    if(odd_l)begin
     a<={39'd0,old_sum_a};b<={71'd0,value_l};answer<=0;carry<=0;limb<=0;after_add<=SUMA;state<=ADD;
    end else state<=FINISH;
   end
   SUMA:begin
    new_sum_a<=answer[68:0];if(|answer[107:69] || carry)invalid<=1;
    state<=FINISH;
   end
   FINISH:state<=OUTPUT;
   OUTPUT:if(ready)begin state<=IDLE;busy<=0;end
   default:begin state<=IDLE;busy<=0;invalid<=1;end
  endcase
 end
endmodule

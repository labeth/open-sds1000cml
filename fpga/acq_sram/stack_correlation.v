// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Normalized correlation from exact eight-bit sample moments. Input moments
// are held by start; start/reset abort any pending result. done qualifies score.
// Signed score has 48 fractional bits. Flat/empty windows are undefined.
// One bit-serial multiplier forms centered moments and their energy product;
// digit-by-digit square root and division avoid combinational wide operators.
// The square root carries 48 fractional bits before score division. This is
// score arithmetic only, not peak selection, segment checking or stacking.
// TRLC-LINKS: REQ-SDS-141
module stack_correlation(
 input wire clk,reset,start,
 input wire [31:0] count,
 input wire [39:0] sum_x,sum_y,
 input wire [47:0] sum_xx,sum_yy,sum_xy,
 output reg busy=0,done=0,defined=0,invalid=0,
 output reg signed [49:0] score=0
);
 localparam IDLE=0,LOAD=1,MULTIPLY=2,PRODUCT=3,SQ_LOW=4,SQ_HIGH=5,DIV_LOW=6,DIV_HIGH=7,PUBLISH=8,SQ_COMMIT=9,DIV_COMMIT=10,CHECK_X=11,CHECK_Y=12,MULTIPLY_HIGH=13,CENTER=14,LOW_UPPER=15,HIGH_UPPER=16;
 reg [4:0] state=IDLE;
 reg [2:0] operation=0;
 reg [7:0] step=0;
 reg [31:0] n=0;
 reg [39:0] sx=0,sy=0;
 reg [47:0] sxx=0,syy=0,sxy=0;
 reg [79:0] multiplicand=0,term=0,energy_x=0,energy_y=0;
 reg [159:0] product=0;
 reg signed [80:0] covariance=0;
 reg [80:0] magnitude=0;
 reg bad_energy=0,flat_energy=0,zero_covariance=0;
 reg [39:0] center_low=0;
 reg center_borrow=0;
 wire [40:0] center_low_difference={1'b0,term[39:0]}-{1'b0,product[39:0]};
 wire [40:0] center_high_addition={1'b0,term[79:40]}+{1'b0,~product[79:40]}+!center_borrow;
 wire [80:0] centered_difference={!center_high_addition[40],center_high_addition[39:0],center_low};
 // Covariance settles several products before magnitude is consumed.
 always @(posedge clk)magnitude<=covariance[80] ? -covariance : covariance;
 reg [40:0] multiply_low=0;
 wire [40:0] multiply_low_sum={1'b0,product[119:80]}+(product[0]?{1'b0,multiplicand[39:0]}:41'd0);
 wire [40:0] multiply_high_sum={1'b0,product[159:120]}+(product[0]?{1'b0,multiplicand[79:40]}:41'd0)+multiply_low[40];
 wire [159:0] multiply_next={multiply_high_sum,multiply_low[39:0],product[79:1]};
 reg [255:0] radicand=0;
 reg [127:0] root=0,denominator=0;
 reg [129:0] root_remainder=0;
 reg [175:0] numerator=0;
 reg [128:0] divide_remainder=0;
 reg [48:0] quotient=0;
 reg [63:0] difference_low=0;
 reg [65:0] difference_high=0;
 reg borrow_low=0,borrow_high=0;
 wire [129:0] root_shift={root_remainder[127:0],radicand[255:254]};
 wire [129:0] root_trial={root,2'b01};
 wire [128:0] divide_shift={divide_remainder[127:0],numerator[175]};
 wire [63:0] low_left=state==SQ_LOW ? root_shift[63:0] : divide_shift[63:0];
 wire [63:0] low_right=state==SQ_LOW ? root_trial[63:0] : denominator[63:0];
 reg [31:0] low_upper_left=0,low_upper_right=0;
 reg low_borrow=0,low_is_root=0;
 wire [32:0] low_difference={1'b0,low_left[31:0]}-{1'b0,low_right[31:0]};
 wire [32:0] low_upper_addition={1'b0,low_upper_left}+{1'b0,~low_upper_right}+!low_borrow;
 wire [65:0] high_left=state==SQ_HIGH ? root_shift[129:64] : {1'b0,divide_shift[128:64]};
 wire [65:0] high_right=state==SQ_HIGH ? root_trial[129:64] : {2'b0,denominator[127:64]};
 reg [32:0] high_upper_left=0,high_upper_right=0;
 reg high_borrow=0,high_is_root=0;
 wire [33:0] high_addition={1'b0,high_left[32:0]}+{1'b0,~high_right[32:0]}+!borrow_low;
 wire [33:0] high_upper_addition={1'b0,high_upper_left}+{1'b0,~high_upper_right}+!high_borrow;
 wire subtract_ok=!borrow_high;
 wire [127:0] root_next={root[126:0],subtract_ok};
 wire [48:0] quotient_next={quotient[47:0],subtract_ok};
 always @(posedge clk)begin
  done<=0;
  if(reset)begin state<=IDLE;busy<=0;defined<=0;invalid<=0;score<=0;end
  else if(start)begin
   n<=count;sx<=sum_x;sy<=sum_y;sxx<=sum_xx;syy<=sum_yy;sxy<=sum_xy;
   state<=LOAD;operation<=0;busy<=1;defined<=0;invalid<=0;score<=0;
  end else case(state)
   LOAD:begin
    step<=79;state<=MULTIPLY;
    case(operation)
     0:begin multiplicand<={48'd0,n};product<={112'd0,sxy};end
     1:begin multiplicand<={40'd0,sx};product<={120'd0,sy};end
     2:begin multiplicand<={48'd0,n};product<={112'd0,sxx};end
     3:begin multiplicand<={40'd0,sx};product<={120'd0,sx};end
     4:begin multiplicand<={48'd0,n};product<={112'd0,syy};end
     5:begin multiplicand<={40'd0,sy};product<={120'd0,sy};end
     6:begin multiplicand<=energy_x;product<={80'd0,energy_y};end
    endcase
    if(n==0)begin state<=IDLE;busy<=0;done<=1;end
   end
   MULTIPLY:begin
    multiply_low<=multiply_low_sum;state<=MULTIPLY_HIGH;
   end
   MULTIPLY_HIGH:begin
    product<=multiply_next;
    if(step==0)state<=PRODUCT;else begin step<=step-1'b1;state<=MULTIPLY;end
   end
   PRODUCT:begin
    operation<=operation+1'b1;state<=LOAD;
    case(operation)
     0,2,4:term<=product[79:0];
     1,3,5:begin
      center_low<=center_low_difference[39:0];center_borrow<=center_low_difference[40];
      operation<=operation;state<=CENTER;
     end
     6:begin
      radicand<={product,96'd0};root<=0;root_remainder<=0;
      step<=127;state<=SQ_LOW;
     end
    endcase
   end
   CENTER:begin
    operation<=operation+1'b1;state<=LOAD;
    case(operation)
     1:covariance<=centered_difference;
     3:begin
      energy_x<=centered_difference[79:0];bad_energy<=centered_difference[80];state<=CHECK_X;
     end
     5:begin
      energy_y<=centered_difference[79:0];bad_energy<=centered_difference[80] || magnitude[80];
      flat_energy<=term==product[79:0] || energy_x==0;zero_covariance<=covariance==0;state<=CHECK_Y;
     end
    endcase
   end
   CHECK_X:begin
    if(bad_energy)begin invalid<=1;done<=1;busy<=0;state<=IDLE;end
    else state<=LOAD;
   end
   CHECK_Y:begin
    if(bad_energy)begin invalid<=1;done<=1;busy<=0;state<=IDLE;end
    else if(flat_energy)begin done<=1;busy<=0;state<=IDLE;end
    else if(zero_covariance)begin defined<=1;done<=1;busy<=0;state<=IDLE;end
    else state<=LOAD;
   end
   SQ_LOW,DIV_LOW:begin
    difference_low[31:0]<=low_difference[31:0];low_borrow<=low_difference[32];
    low_upper_left<=low_left[63:32];low_upper_right<=low_right[63:32];
    low_is_root<=state==SQ_LOW;state<=LOW_UPPER;
   end
   LOW_UPPER:begin
    difference_low[63:32]<=low_upper_addition[31:0];borrow_low<=!low_upper_addition[32];
    state<=low_is_root ? SQ_HIGH:DIV_HIGH;
   end
   SQ_HIGH,DIV_HIGH:begin
    difference_high[32:0]<=high_addition[32:0];high_borrow<=!high_addition[33];
    high_upper_left<=high_left[65:33];high_upper_right<=high_right[65:33];
    high_is_root<=state==SQ_HIGH;state<=HIGH_UPPER;
   end
   HIGH_UPPER:begin
    difference_high[65:33]<=high_upper_addition[32:0];borrow_high<=!high_upper_addition[33];
    state<=high_is_root ? SQ_COMMIT:DIV_COMMIT;
   end
   SQ_COMMIT:begin
    root<=root_next;radicand<=radicand<<2;
    root_remainder<=subtract_ok ? {difference_high,difference_low}:root_shift;
    if(step==0)begin
     denominator<=root_next;numerator<={magnitude[79:0],96'd0};
     divide_remainder<=0;quotient<=0;step<=175;state<=DIV_LOW;
    end else begin step<=step-1'b1;state<=SQ_LOW;end
   end
   DIV_COMMIT:begin
    numerator<=numerator<<1;quotient<=quotient_next;
    divide_remainder<=subtract_ok ? {difference_high[64:0],difference_low}:divide_shift;
    if(subtract_ok && step>48)begin invalid<=1;busy<=0;done<=1;state<=IDLE;end
    else if(step==0)state<=PUBLISH;
    else begin step<=step-1'b1;state<=DIV_LOW;end
   end
   PUBLISH:begin
    // Do not cascade the final divide subtract into a signed negate.
    busy<=0;done<=1;state<=IDLE;
    if(quotient>49'h1000000000000)invalid<=1;
    else begin
     defined<=1;score<=covariance[80] ? -$signed({1'b0,quotient}):$signed({1'b0,quotient});
    end
   end
  endcase
 end
endmodule

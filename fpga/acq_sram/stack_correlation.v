// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Normalized correlation from exact eight-bit sample moments. Input moments
// are held by start; start/reset abort any pending result. done qualifies score.
// Signed score has 48 fractional bits. Flat/empty windows are undefined.
// One bit-serial multiplier forms centered moments and their energy product;
// digit-by-digit square root and division avoid combinational wide operators.
// The square root carries 48 fractional bits before score division. This is
// score arithmetic only, not peak selection, segment checking or stacking.
// TRLC-LINKS: REQ-SDS-141
// COUNT_BITS (1..32) bounds internal moment arithmetic. Public inputs stay
// full width so a narrowed configuration rejects overflow instead of truncating.
// The root/division path retains full precision and the existing Q48 contract.
module stack_correlation #(parameter COUNT_BITS=32)(
 input wire clk,reset,start,
 input wire [31:0] count,
 input wire [39:0] sum_x,sum_y,
 input wire [47:0] sum_xx,sum_yy,sum_xy,
 output reg busy=0,done=0,defined=0,invalid=0,
 output reg signed [49:0] score=0
);
 localparam W=2*COUNT_BITS+16,H=W/2;
 // Energy product is 2*W bits. Appending 96 zeros preserves 48 root
 // fractional bits, so the root needs W+48 bits and the numerator W+96.
 // Split the root/divide subtraction into four registered carry sections;
 // odd section widths retain every bit instead of rounding down the halves.
 localparam ROOT_BITS=W+48,NUM_BITS=W+96;
 localparam LOW_BITS=ROOT_BITS/2,HIGH_BITS=ROOT_BITS+2-LOW_BITS;
 localparam LOW_FIRST=LOW_BITS/2,LOW_LAST=LOW_BITS-LOW_FIRST;
 localparam HIGH_FIRST=HIGH_BITS/2,HIGH_LAST=HIGH_BITS-HIGH_FIRST;
 localparam IDLE=0,LOAD=1,MULTIPLY=2,PRODUCT=3,SQ_LOW=4,SQ_HIGH=5,DIV_LOW=6,DIV_HIGH=7,PUBLISH=8,SQ_COMMIT=9,DIV_COMMIT=10,CHECK_X=11,CHECK_Y=12,MULTIPLY_HIGH=13,CENTER=14,LOW_UPPER=15,HIGH_UPPER=16;
 reg [4:0] state=IDLE;
 reg [2:0] operation=0;
 reg [7:0] step=0;
 reg [COUNT_BITS-1:0] n=0;
 reg [COUNT_BITS+7:0] sx=0,sy=0;
 reg [COUNT_BITS+15:0] sxx=0,syy=0,sxy=0;
 reg [W-1:0] multiplicand=0,term=0,energy_x=0,energy_y=0;
 reg [2*W-1:0] product=0;
 reg signed [W:0] covariance=0;
 reg [W:0] magnitude=0;
 reg input_overflow=0;
 reg bad_energy=0,flat_energy=0,zero_covariance=0;
 reg [H-1:0] center_low=0;
 reg center_borrow=0;
 wire [H:0] center_low_difference={1'b0,term[H-1:0]}-{1'b0,product[H-1:0]};
 wire [H:0] center_high_addition={1'b0,term[W-1:H]}+{1'b0,~product[W-1:H]}+!center_borrow;
 wire [W:0] centered_difference={!center_high_addition[H],center_high_addition[H-1:0],center_low};
 // Covariance settles several products before magnitude is consumed.
 always @(posedge clk)magnitude<=covariance[W] ? -covariance : covariance;
 reg [H:0] multiply_low=0;
 wire [H:0] multiply_low_sum={1'b0,product[W+H-1:W]}+(product[0]?{1'b0,multiplicand[H-1:0]}:{(H+1){1'b0}});
 wire [H:0] multiply_high_sum={1'b0,product[2*W-1:W+H]}+(product[0]?{1'b0,multiplicand[W-1:H]}:{(H+1){1'b0}})+multiply_low[H];
 wire [2*W-1:0] multiply_next={multiply_high_sum,multiply_low[H-1:0],product[W-1:1]};
 reg [2*ROOT_BITS-1:0] radicand=0;
 reg [ROOT_BITS-1:0] root=0,denominator=0;
 reg [ROOT_BITS+1:0] root_remainder=0;
 reg [NUM_BITS-1:0] numerator=0;
 reg [ROOT_BITS:0] divide_remainder=0;
 reg [48:0] quotient=0;
 reg [LOW_BITS-1:0] difference_low=0;
 reg [HIGH_BITS-1:0] difference_high=0;
 reg borrow_low=0,borrow_high=0;
 wire [ROOT_BITS+1:0] root_shift={root_remainder[ROOT_BITS-1:0],radicand[2*ROOT_BITS-1:2*ROOT_BITS-2]};
 wire [ROOT_BITS+1:0] root_trial={root,2'b01};
 wire [ROOT_BITS:0] divide_shift={divide_remainder[ROOT_BITS-1:0],numerator[NUM_BITS-1]};
 wire [LOW_BITS-1:0] low_left=state==SQ_LOW ? root_shift[LOW_BITS-1:0] : divide_shift[LOW_BITS-1:0];
 wire [LOW_BITS-1:0] low_right=state==SQ_LOW ? root_trial[LOW_BITS-1:0] : denominator[LOW_BITS-1:0];
 reg [LOW_LAST-1:0] low_upper_left=0,low_upper_right=0;
 reg low_borrow=0,low_is_root=0;
 wire [LOW_FIRST:0] low_difference={1'b0,low_left[LOW_FIRST-1:0]}-{1'b0,low_right[LOW_FIRST-1:0]};
 wire [LOW_LAST:0] low_upper_addition={1'b0,low_upper_left}+{1'b0,~low_upper_right}+!low_borrow;
 wire [HIGH_BITS-1:0] high_left=state==SQ_HIGH ? root_shift[ROOT_BITS+1:LOW_BITS] : {1'b0,divide_shift[ROOT_BITS:LOW_BITS]};
 wire [HIGH_BITS-1:0] high_right=state==SQ_HIGH ? root_trial[ROOT_BITS+1:LOW_BITS] : {2'b0,denominator[ROOT_BITS-1:LOW_BITS]};
 reg [HIGH_LAST-1:0] high_upper_left=0,high_upper_right=0;
 reg high_borrow=0,high_is_root=0;
 wire [HIGH_FIRST:0] high_addition={1'b0,high_left[HIGH_FIRST-1:0]}+{1'b0,~high_right[HIGH_FIRST-1:0]}+!borrow_low;
 wire [HIGH_LAST:0] high_upper_addition={1'b0,high_upper_left}+{1'b0,~high_upper_right}+!high_borrow;
 wire subtract_ok=!borrow_high;
 wire [ROOT_BITS-1:0] root_next={root[ROOT_BITS-2:0],subtract_ok};
 wire [48:0] quotient_next={quotient[47:0],subtract_ok};
 always @(posedge clk)begin
  done<=0;
  if(reset)begin state<=IDLE;busy<=0;defined<=0;invalid<=0;score<=0;end
  else if(start)begin
   input_overflow<=((count >> COUNT_BITS)!=0) || ((sum_x >> (COUNT_BITS+8))!=0) ||
    ((sum_y >> (COUNT_BITS+8))!=0) || ((sum_xx >> (COUNT_BITS+16))!=0) ||
    ((sum_yy >> (COUNT_BITS+16))!=0) || ((sum_xy >> (COUNT_BITS+16))!=0);
   n<=count;sx<=sum_x;sy<=sum_y;sxx<=sum_xx;syy<=sum_yy;sxy<=sum_xy;
   state<=LOAD;operation<=0;busy<=1;defined<=0;invalid<=0;score<=0;
  end else case(state)
   LOAD:begin
    step<=W-1;state<=MULTIPLY;
    case(operation)
     0:begin multiplicand<=n;product<=sxy;end
     1:begin multiplicand<=sx;product<=sy;end
     2:begin multiplicand<=n;product<=sxx;end
     3:begin multiplicand<=sx;product<=sx;end
     4:begin multiplicand<=n;product<=syy;end
     5:begin multiplicand<=sy;product<=sy;end
     6:begin multiplicand<=energy_x;product<=energy_y;end
    endcase
    if(input_overflow)begin invalid<=1;state<=IDLE;busy<=0;done<=1;end
    else if(n==0)begin state<=IDLE;busy<=0;done<=1;end
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
     0,2,4:term<=product[W-1:0];
     1,3,5:begin
      center_low<=center_low_difference[H-1:0];center_borrow<=center_low_difference[H];
      operation<=operation;state<=CENTER;
     end
     6:begin
      radicand<={product,96'd0};root<=0;root_remainder<=0;
      step<=ROOT_BITS-1;state<=SQ_LOW;
     end
    endcase
   end
   CENTER:begin
    operation<=operation+1'b1;state<=LOAD;
    case(operation)
     1:covariance<=centered_difference;
     3:begin
      energy_x<=centered_difference[W-1:0];bad_energy<=centered_difference[W];state<=CHECK_X;
     end
     5:begin
      energy_y<=centered_difference[W-1:0];bad_energy<=centered_difference[W] || magnitude[W];
      flat_energy<=term==product[W-1:0] || energy_x==0;zero_covariance<=covariance==0;state<=CHECK_Y;
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
    difference_low[LOW_FIRST-1:0]<=low_difference[LOW_FIRST-1:0];low_borrow<=low_difference[LOW_FIRST];
    low_upper_left<=low_left[LOW_BITS-1:LOW_FIRST];low_upper_right<=low_right[LOW_BITS-1:LOW_FIRST];
    low_is_root<=state==SQ_LOW;state<=LOW_UPPER;
   end
   LOW_UPPER:begin
    difference_low[LOW_BITS-1:LOW_FIRST]<=low_upper_addition[LOW_LAST-1:0];borrow_low<=!low_upper_addition[LOW_LAST];
    state<=low_is_root ? SQ_HIGH:DIV_HIGH;
   end
   SQ_HIGH,DIV_HIGH:begin
    difference_high[HIGH_FIRST-1:0]<=high_addition[HIGH_FIRST-1:0];high_borrow<=!high_addition[HIGH_FIRST];
    high_upper_left<=high_left[HIGH_BITS-1:HIGH_FIRST];high_upper_right<=high_right[HIGH_BITS-1:HIGH_FIRST];
    high_is_root<=state==SQ_HIGH;state<=HIGH_UPPER;
   end
   HIGH_UPPER:begin
    difference_high[HIGH_BITS-1:HIGH_FIRST]<=high_upper_addition[HIGH_LAST-1:0];borrow_high<=!high_upper_addition[HIGH_LAST];
    state<=high_is_root ? SQ_COMMIT:DIV_COMMIT;
   end
   SQ_COMMIT:begin
    root<=root_next;radicand<=radicand<<2;
    root_remainder<=subtract_ok ? {difference_high,difference_low}:root_shift;
    if(step==0)begin
     denominator<=root_next;numerator<={magnitude[W-1:0],96'd0};
     divide_remainder<=0;quotient<=0;step<=NUM_BITS-1;state<=DIV_LOW;
    end else begin step<=step-1'b1;state<=SQ_LOW;end
   end
   DIV_COMMIT:begin
    numerator<=numerator<<1;quotient<=quotient_next;
    divide_remainder<=subtract_ok ? {difference_high[HIGH_BITS-2:0],difference_low}:divide_shift;
    if(subtract_ok && step>48)begin invalid<=1;busy<=0;done<=1;state<=IDLE;end
    else if(step==0)state<=PUBLISH;
    else begin step<=step-1'b1;state<=DIV_LOW;end
   end
   PUBLISH:begin
    // Do not cascade the final divide subtract into a signed negate.
    busy<=0;done<=1;state<=IDLE;
    if(quotient>49'h1000000000000)invalid<=1;
    else begin
     defined<=1;score<=covariance[W] ? -$signed({1'b0,quotient}):$signed({1'b0,quotient});
    end
   end
  endcase
 end
endmodule

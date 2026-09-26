// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Parabolic sub-sample peak location from three signed Q48 scores.
// delta=(right-left)/(2*(2*center-left-right)), clamped to +/-0.5.
// Missing neighbors or nonnegative software curvature produce zero, as gateFind.
// Output is signed Q24, truncated toward zero. start/reset cancel old work.
// TRLC-LINKS: REQ-SDS-141
module stack_alignment(
 input wire clk,reset,start,left_present,right_present,
 input wire signed [49:0] left_score,center_score,right_score,
 output reg busy=0,done=0,invalid=0,
 output reg signed [24:0] delta=0
);
 localparam IDLE=0,PREP=1,CURVE=2,CHECK=3,LOW=4,HIGH=5,COMMIT=6,PUBLISH=7;
 reg [2:0] state=IDLE;
 reg signed [49:0] l=0,c=0,r=0;
 reg both=0,lp=0,rp=0,bad=0,negative=0;
 reg signed [50:0] neighbors=0,difference=0;
 reg signed [51:0] curvature=0;
 reg [50:0] magnitude=0;
 reg [74:0] numerator=0;
 reg [52:0] denominator=0;
 reg [53:0] remainder=0;
 reg [23:0] quotient=0;
 reg [6:0] step=0;
 reg [26:0] low_difference=0,high_difference=0;
 reg low_borrow=0,high_borrow=0;
 wire [53:0] shifted={remainder[52:0],numerator[74]};
 wire [27:0] low_subtract={1'b0,shifted[26:0]}-{1'b0,denominator[26:0]};
 wire [26:0] high_denominator={1'b0,denominator[52:27]};
 wire [27:0] high_add={1'b0,shifted[53:27]}+{1'b0,~high_denominator}+!low_borrow;
 wire subtract_ok=!high_borrow;
 always @(posedge clk)begin
  done<=0;
  if(reset)begin state<=IDLE;busy<=0;invalid<=0;delta<=0;end
  else if(start)begin
   l<=left_score;c<=center_score;r<=right_score;lp<=left_present;rp<=right_present;
   both<=left_present && right_present;state<=PREP;busy<=1;invalid<=0;delta<=0;
  end else case(state)
   PREP:begin
    neighbors<=$signed({l[49],l})+$signed({r[49],r});
    difference<=$signed({r[49],r})-$signed({l[49],l});
    bad<=c>50'sh1000000000000 || c< -50'sh1000000000000 ||
     (lp && (l>50'sh1000000000000 || l< -50'sh1000000000000)) ||
     (rp && (r>50'sh1000000000000 || r< -50'sh1000000000000));
    state<=CURVE;
   end
   CURVE:begin
    curvature<=$signed({c[49],c,1'b0})-$signed({neighbors[50],neighbors});
    negative<=difference[50];magnitude<=difference[50] ? -difference:difference;
    state<=CHECK;
   end
   CHECK:begin
    // Stage division inputs independently of the wide validity comparisons.
    // Only the state transition authorizes use of these captured values.
    denominator<={curvature[51:0],1'b0};numerator<={magnitude,24'd0};
    remainder<=0;quotient<=0;step<=74;
    if(bad || !both || curvature<=0 || magnitude==0)begin
     invalid<=bad;busy<=0;done<=1;state<=IDLE;
    end else state<=LOW;
   end
   LOW:begin low_difference<=low_subtract[26:0];low_borrow<=low_subtract[27];state<=HIGH;end
   HIGH:begin high_difference<=high_add[26:0];high_borrow<=!high_add[27];state<=COMMIT;end
   COMMIT:begin
    remainder<=subtract_ok ? {high_difference,low_difference}:shifted;
    numerator<=numerator<<1;quotient<={quotient[22:0],subtract_ok};
    if(subtract_ok && step>=23)begin quotient<=24'h800000;state<=PUBLISH;end
    else if(step==0)state<=PUBLISH;
    else begin step<=step-1'b1;state<=LOW;end
   end
   PUBLISH:begin
    delta<=negative ? -$signed({1'b0,quotient}):$signed({1'b0,quotient});
    busy<=0;done<=1;state<=IDLE;
   end
  endcase
 end
endmodule

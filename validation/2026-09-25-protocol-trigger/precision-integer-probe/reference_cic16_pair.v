// Frozen 28-bit oracle from deployed general build-v5T40o; do not optimize.
// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// Two samples per channel per 250 MHz cycle. Each integrator's pair sum is
// registered before the feedback adder, keeping one carry chain per cycle.
// TRLC-LINKS: REQ-SDS-040
module reference_cic_pair_integrator #(parameter W=28)(input clk,enable,valid,input signed [W-1:0] a,b,
 output reg out_valid=0,output reg signed [W-1:0] lo=0,hi=0);
 (* preserve, dont_merge *) reg local_enable=0;
 always @(posedge clk)local_enable<=enable;
 localparam L=W/2,H=W-L;
 reg pending=0,pending_hi=0;
 reg signed [W-1:0] first=0,sum=0;
 reg [L-1:0] state_lo=0,lo_first=0,lo_sum=0;
 reg [H-1:0] state_hi=0,hi_first=0,hi_sum=0;
 reg carry_first=0,carry_sum=0;
 wire [L:0] next_first={1'b0,state_lo}+{1'b0,first[L-1:0]};
 wire [L:0] next_sum={1'b0,state_lo}+{1'b0,sum[L-1:0]};
 wire [H-1:0] next_hi_first=state_hi+hi_first+carry_first;
 wire [H-1:0] next_hi_sum=state_hi+hi_sum+carry_sum;
 // The high accumulator trails the low accumulator by one accepted token.
 // Pipeline its operand and low carry together; stalls preserve alignment.
 always @(posedge clk)begin
  pending<=local_enable && valid;
  pending_hi<=local_enable && pending;
  out_valid<=local_enable && pending_hi;
  if(valid)begin first<=a;sum<=a+b;end
  if(pending)begin
   state_lo<=next_sum[L-1:0];
   lo_first<=next_first[L-1:0];lo_sum<=next_sum[L-1:0];
   carry_first<=next_first[L];carry_sum<=next_sum[L];
   hi_first<=first[W-1:L];hi_sum<=sum[W-1:L];
  end
  if(pending_hi)begin
   state_hi<=next_hi_sum;
   lo<={next_hi_first,lo_first};hi<={next_hi_sum,lo_sum};
  end
  if(!local_enable)begin
   pending<=0;pending_hi<=0;out_valid<=0;
   state_lo<=0;state_hi<=0;lo<=0;hi<=0;
  end
 end
endmodule

// CIC3 /16, signed Q8 input/output. Modulo arithmetic has exactly the
// 12 guard bits required by 16^3 gain. Discard startup outputs explicitly.
// TRLC-LINKS: REQ-SDS-040
module reference_cic16_pair(input clk,enable,valid,input [7:0] a,b,output out_valid,output [15:0] q);
 wire signed [27:0] x0=$signed({a^8'h80,8'b0}),x1=$signed({b^8'h80,8'b0});
 wire v1,v2,v3;wire signed [27:0] a1,b1,a2,b2,a3,b3;
 reference_cic_pair_integrator i1(clk,enable,valid,x0,x1,v1,a1,b1);
 reference_cic_pair_integrator i2(clk,enable,v1,a1,b1,v2,a2,b2);
 reference_cic_pair_integrator i3(clk,enable,v2,a2,b2,v3,a3,b3);
 (* preserve, dont_merge *) reg comb_enable=0;
 always @(posedge clk)comb_enable<=enable;
 reg [2:0] count=0;reg [5:0] cv=0;reg [3:0] warm=0;
 reg signed [27:0] z1=0,z2=0,z3=0,c1=0,c2=0,c3=0;
 reg [14:0] low1=0,low2=0,low3=0;
 reg [13:0] high1=0,high2=0,high3=0;
 // Each sparse comb subtraction takes two short arithmetic stages. The
 // valid token follows all six stages; /16 produces at most one per eight
 // core clocks, so the pipeline retains full throughput with unchanged values.
 always @(posedge clk)begin
  cv<={cv[4:0],v3 && count==7};
  if(v3)begin
   count<=count+1'b1;
   if(count==7)begin
    low1<={1'b0,b3[13:0]}-{1'b0,z1[13:0]};high1<=b3[27:14]-z1[27:14];z1<=b3;
   end
  end
  if(cv[0])c1<={high1-low1[14],low1[13:0]};
  if(cv[1])begin low2<={1'b0,c1[13:0]}-{1'b0,z2[13:0]};high2<=c1[27:14]-z2[27:14];z2<=c1;end
  if(cv[2])c2<={high2-low2[14],low2[13:0]};
  if(cv[3])begin low3<={1'b0,c2[13:0]}-{1'b0,z3[13:0]};high3<=c2[27:14]-z3[27:14];z3<=c2;end
  if(cv[4])c3<={high3-low3[14],low3[13:0]};
  if(cv[5] && warm<8)warm<=warm+1'b1;
  if(!comb_enable)begin count<=0;cv<=0;warm<=0;z1<=0;z2<=0;z3<=0;c1<=0;c2<=0;c3<=0;end
 end
 assign q={~c3[27],c3[26:12]};assign out_valid=enable && cv[5] && warm==8;
endmodule


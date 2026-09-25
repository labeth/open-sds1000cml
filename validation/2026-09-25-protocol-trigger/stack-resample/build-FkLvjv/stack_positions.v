// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Fine-grid positions hit_position + delta + bin/factor. Fraction is Q24;
// a remainder accumulator avoids cumulative drift for non-power-of-two factors.
// Every bin is emitted, including ineligible boundary bins which MUST be skipped.
// start/reset abort pending descriptors. Geometry remains latched for the run.
// TRLC-LINKS: REQ-SDS-141
module stack_positions(
 input wire clk,reset,start,
 input wire [31:0] hit_position,bin_count,factor,record_samples,
 input wire signed [24:0] delta,
 output wire valid,input wire ready,
 output reg [31:0] bin=0,
 output reg signed [33:0] sample_index=0,
 output reg [23:0] fraction=0,
 output wire eligible,
 output reg busy=0,done=0,invalid=0
);
 localparam IDLE=0,DIV_LOW=1,DIV_HIGH=2,DIV_COMMIT=3,EMIT=4,ADVANCE_SUM=5,ADVANCE_CARRY=6,ADVANCE_POSITION=7;
 reg [2:0] state=IDLE;
 reg [31:0] divisor=0,bins_left=0,last_sample=0;
 reg enough_samples=0;
 reg [24:0] dividend=0,quotient=0;
 reg [32:0] remainder=0;
 reg [4:0] bit_index=0;
 reg [15:0] low_difference=0;
 reg [16:0] high_difference=0;
 reg low_borrow=0,high_borrow=0;
 wire [32:0] shifted={remainder[31:0],dividend[24]};
 wire [16:0] low_subtract={1'b0,shifted[15:0]}-{1'b0,divisor[15:0]};
 wire [16:0] high_divisor={1'b0,divisor[31:16]};
 wire [17:0] high_addition={1'b0,shifted[32:16]}+{1'b0,~high_divisor}+!low_borrow;
 wire [32:0] next_remainder=high_borrow ? shifted:{high_difference,low_difference};
 reg [31:0] step_remainder=0,phase_remainder=0;
 reg [32:0] remainder_sum=0;
 reg [25:0] phase_sum=0,phase_next=0;
 assign valid=state==EMIT && !reset && !start;
 assign eligible=enough_samples && sample_index[33:32]==0 && sample_index[31:0]<last_sample;
 always @(posedge clk)begin
  done<=0;
  if(reset)begin state<=IDLE;busy<=0;invalid<=0;end
  else if(start)begin
   divisor<=factor;bins_left<=bin_count;last_sample<=record_samples-1'b1;enough_samples<=record_samples>=2;
   bin<=0;sample_index<=$signed({2'b0,hit_position})-(delta[24] ? 34'sd1:34'sd0);
   fraction<=delta[23:0];phase_remainder<=0;
   dividend<=25'h1000000;quotient<=0;remainder<=0;bit_index<=24;
   state<=DIV_LOW;busy<=1;invalid<=0;
   if(bin_count==0 || factor==0 || delta>25'sh0800000 || delta< -25'sh0800000)begin
    state<=IDLE;busy<=0;done<=1;invalid<=1;
   end
  end else case(state)
   DIV_LOW:begin low_difference<=low_subtract[15:0];low_borrow<=low_subtract[16];state<=DIV_HIGH;end
   DIV_HIGH:begin high_difference<=high_addition[16:0];high_borrow<=!high_addition[17];state<=DIV_COMMIT;end
   DIV_COMMIT:begin
    remainder<=next_remainder;dividend<=dividend<<1;quotient<={quotient[23:0],!high_borrow};
    if(bit_index==0)begin step_remainder<=next_remainder[31:0];state<=EMIT;end
    else begin bit_index<=bit_index-1'b1;state<=DIV_LOW;end
   end
   EMIT:if(ready)begin
    if(bins_left==1)begin busy<=0;done<=1;state<=IDLE;end
    else begin bins_left<=bins_left-1'b1;bin<=bin+1'b1;state<=ADVANCE_SUM;end
   end
   ADVANCE_SUM:begin
    remainder_sum<={1'b0,phase_remainder}+{1'b0,step_remainder};
    phase_sum<={2'b0,fraction}+{1'b0,quotient};state<=ADVANCE_CARRY;
   end
   ADVANCE_CARRY:begin
    phase_remainder<=remainder_sum>={1'b0,divisor} ? remainder_sum-{1'b0,divisor}:remainder_sum[31:0];
    phase_next<=phase_sum+(remainder_sum>={1'b0,divisor});state<=ADVANCE_POSITION;
   end
   ADVANCE_POSITION:begin
    fraction<=phase_next[23:0];sample_index<=sample_index+phase_next[24];state<=EMIT;
   end
  endcase
 end
endmodule

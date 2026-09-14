// 64-bit ordinal with eight independent byte increment lanes. Carry flags
// describe the next increment and hold across arbitrary gaps between steps.
module sram_ordinal_counter(input clk,clear,step,output reg [63:0] value=0);
 reg [6:0] carry=0;
 always @(posedge clk)begin
  if(clear)value[7:0]<=0;
  else if(step)value[7:0]<=value[7:0]+1'b1;
 end
 genvar lane;
 generate for(lane=1;lane<8;lane=lane+1)begin: bytes
  localparam W=8*lane;
  always @(posedge clk)begin
   if(clear)begin value[W+:8]<=0;carry[lane-1]<=0;end
   else if(step)begin
    value[W+:8]<=value[W+:8]+carry[lane-1];
    carry[lane-1]<=value[W-1:0]=={W{1'b1}}-1'b1;
   end
  end
 end endgenerate
endmodule

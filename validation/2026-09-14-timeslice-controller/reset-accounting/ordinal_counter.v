// 64-bit word ordinal with independent 16-bit increment lanes. Carry flags
// describe the next increment, and hold across arbitrary gaps between steps.
module sram_ordinal_counter(input clk,clear,step,output reg [63:0] value=0);
 reg c16=0,c32=0,c48=0;
 always @(posedge clk)begin
  if(clear)begin value<=0;c16<=0;c32<=0;c48<=0;end
  else if(step)begin
   value[15:0]<=value[15:0]+1'b1;
   value[31:16]<=value[31:16]+c16;
   value[47:32]<=value[47:32]+c32;
   value[63:48]<=value[63:48]+c48;
   c16<=value[15:0]==16'hfffe;
   c32<=value[31:0]==32'hfffffffe;
   c48<=value[47:0]==48'hfffffffffffe;
  end
 end
endmodule

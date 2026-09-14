// Byte-lane ordinal with per-byte fullness flags. Prefix carries depend on
// at most seven flags rather than comparing up to 56 value bits in one cycle.
module sram_ordinal_counter(input clk,clear,step,output reg [63:0] value=0);
 reg [7:0] full=0;
 wire [7:0] advance;
 assign advance[0]=step;
 genvar lane;
 generate for(lane=0;lane<8;lane=lane+1)begin: bytes
  if(lane>0)assign advance[lane]=step && &full[lane-1:0];
  always @(posedge clk)begin
   if(clear)begin value[8*lane+:8]<=0;full[lane]<=0;end
   else if(advance[lane])begin
    value[8*lane+:8]<=value[8*lane+:8]+1'b1;
    full[lane]<=value[8*lane+:8]==8'hfe;
   end
  end
 end endgenerate
endmodule

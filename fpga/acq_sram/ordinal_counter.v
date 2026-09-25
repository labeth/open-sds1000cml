// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-ORDINAL-COUNTER
// Four word lanes; carry is a prefix of registered per-lane fullness.
// Full flags track the current value and hold when their lane does not advance.
// TRLC-LINKS: REQ-SDS-047
module sram_ordinal_counter(input clk,clear,step,output reg [63:0] value=0);
 reg [3:0] full=0;
 wire [3:0] advance;
 assign advance[0]=step;
 genvar lane;
 generate for(lane=0;lane<4;lane=lane+1)begin: words
  if(lane>0)assign advance[lane]=step && &full[lane-1:0];
  always @(posedge clk)begin
   if(clear)begin value[16*lane+:16]<=0;full[lane]<=0;end
   else if(advance[lane])begin
    value[16*lane+:16]<=value[16*lane+:16]+1'b1;
    full[lane]<=value[16*lane+:16]==16'hfffe;
   end
  end
 end endgenerate
endmodule

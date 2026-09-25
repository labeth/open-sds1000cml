// All five measured ADC pairs, in E1 CH1, E1 CH2, E2 CH1, ... byte order.
// This is a bit-weight map, not a claim about interleaved sample times.
`include "lanemap_seed.vh"
module adc_unpack(input wire [79:0] lanes, output wire [79:0] cores);
 genvar i;
 generate for(i=0;i<80;i=i+1) begin: g_bit
  localparam integer PAD_BIT=`LANEMAP_ENTRY(i);
  assign cores[i]=lanes[PAD_BIT];
 end endgenerate
endmodule

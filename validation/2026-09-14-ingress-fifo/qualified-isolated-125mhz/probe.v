module ingress_probe(input clk,reset,push,pop,input [31:0] data,
 output reg [31:0] q,output reg valid,output [12:0] count,output empty,full,overflow,underflow);
 reg reset_r=1,push_r=0,pop_r=0;reg [31:0] data_r;
 wire [31:0] fifo_q;wire fifo_valid;
 always @(posedge clk)begin
  reset_r<=reset;push_r<=push;pop_r<=pop;data_r<=data;
  q<=fifo_q;valid<=fifo_valid;
 end
 sram_ingress_stream fifo(clk,reset_r,push_r,data_r,pop_r,fifo_valid,fifo_q,count,overflow,underflow);
 assign empty=count==0;assign full=count>=4608;
endmodule

module host_fifo_probe(input reset,source_clk,dest_clk,input [80:0] pins,input ready_pin,
 output reg [1:0] source_status,output reg [80:0] result);
reg [80:0] i;reg ready;wire available,overflow,valid;wire [79:0] data;
always @(posedge source_clk)begin i<=pins;source_status<={overflow,available};end
always @(posedge dest_clk)begin ready<=ready_pin;result<={valid,data};end
sram_host_fifo dut(.reset(reset),.source_clk(source_clk),.dest_clk(dest_clk),
 .push(i[80]),.source_data(i[79:0]),.source_ready(available),.overflow(overflow),
 .dest_valid(valid),.dest_data(data),.dest_ready(ready));
endmodule

module host_ram_probe(input write_clk,read_clk,input [77:0] wpins,input [15:0] rpins,
 output reg wf,output reg [17:0] result);
reg [77:0] w;reg [15:0] r;wire fault,valid,error;wire [15:0] data;
always @(posedge write_clk)begin w<=wpins;wf<=fault;end
always @(posedge read_clk)begin r<=rpins;result<={error,valid,data};end
sram_host_ram dut(.write_clk(write_clk),.write_reset(w[0]),.write_enable(w[1]),
 .write_pair(w[13:2]),.write_data(w[77:14]),.write_fault(fault),.read_clk(read_clk),
 .read_reset(r[0]),.read_enable(r[1]),.read_halfword(r[15:2]),.read_valid(valid),
 .read_error(error),.read_data(data));
endmodule

module ingress_probe(input core,ram_clk,reset,source_valid,ready,input [35:0] data,
 output reg [35:0] q,output reg valid,source_ready,output reg [12:0] pending,output reg fault);
 reg sv=0,rd=0;reg [35:0] d;
 wire [35:0] qi;wire vi,sr,fi;wire [12:0] pi;
 always @(posedge core)begin
  sv<=source_valid;rd<=ready;d<=data;q<=qi;valid<=vi;source_ready<=sr;pending<=pi;fault<=fi;
 end
 sram_ingress_path path(reset,core,ram_clk,sv,d,sr,rd,vi,qi,pi,fi);
endmodule

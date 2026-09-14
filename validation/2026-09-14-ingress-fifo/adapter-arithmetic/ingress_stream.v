// Ready/valid adapter for the banked ingress FIFO. Four response slots reserve
// space before each synchronous RAM pop, including both stages of read response latency.
// This allows one output word per clock and tolerates abrupt sink stalls.
module sram_ingress_stream #(parameter WIDTH=32,BANKS=9,ROWS=512)(
 input wire clk,reset,push,input wire [WIDTH-1:0] data_in,
 input wire ready,output wire valid,output wire [WIDTH-1:0] data_out,
 output wire [$clog2(BANKS*ROWS+5)-1:0] count,
 output wire overflow,underflow
);
 wire [WIDTH-1:0] response;
 wire response_valid,empty,full;
 wire [$clog2(BANKS*ROWS+1)-1:0] ram_count;
 reg [WIDTH-1:0] slots[0:3];
 reg [1:0] head=0,tail=0;reg [2:0] held=0;reg issued=0;
 assign valid=held!=0 && !reset;
 assign data_out=slots[tail];
 wire consume=valid && ready;
 // response_valid already occupies a reserved slot although its write into
 // slots occurs at the next edge. Include it before issuing another read.
 wire fetch=!empty && !reset && ({1'b0,held}+response_valid+issued-consume<4);
 assign count=ram_count+held+response_valid+issued;
 sram_ingress_fifo #(.WIDTH(WIDTH),.BANKS(BANKS),.ROWS(ROWS)) memory(
  .clk(clk),.reset(reset),.push(push),.pop(fetch),.data_in(data_in),
  .data_out(response),.data_valid(response_valid),.empty(empty),.full(full),
  .count(ram_count),.overflow(overflow),.underflow(underflow));
 always @(posedge clk)begin
  issued<=fetch;
  if(reset)begin head<=0;tail<=0;held<=0;issued<=0;end
  else begin
   if(response_valid)begin slots[head]<=response;head<=head+1'b1;end
   if(consume)tail<=tail+1'b1;
   case({response_valid,consume})
    2'b10:held<=held+1'b1;
    2'b01:held<=held-1'b1;
   endcase
  end
 end
endmodule

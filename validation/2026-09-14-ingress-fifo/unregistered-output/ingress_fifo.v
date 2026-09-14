// Single-clock ingress storage for time-sliced SRAM acquisition.
// Explicit 512-word banks avoid rounding 4608 words up to 8192 in block RAM.
// A successful pop returns data_valid/data after that clock edge; the caller
// must reserve space for this response before popping. No empty bypass.
// Full+pop still rejects a simultaneous push, avoiding mixed-port same-address
// read/write dependence. Any rejected request sets a sticky fault until reset.
module sram_ingress_fifo #(parameter WIDTH=32,BANKS=9,ROWS=512)(
 input wire clk,reset,push,pop,
 input wire [WIDTH-1:0] data_in,
 output wire [WIDTH-1:0] data_out,
 output reg data_valid=0,
 output reg empty=1,full=0,
 output reg [$clog2(BANKS*ROWS+1)-1:0] count=0,
 output reg overflow=0,underflow=0
);
 localparam RW=$clog2(ROWS),BW=(BANKS>1 ? $clog2(BANKS) : 1);
 reg [RW-1:0] wr_row=0,rd_row=0;
 reg [BW-1:0] wr_bank=0,rd_bank=0,response_bank=0;
 wire accept_write=push && !full && !reset;
 wire accept_read=pop && !empty && !reset;
 wire [WIDTH-1:0] bank_q[0:BANKS-1];
 assign data_out=bank_q[response_bank];
 genvar b;
 generate for(b=0;b<BANKS;b=b+1)begin:storage
  (* ramstyle="M9K, no_rw_check" *) reg [WIDTH-1:0] memory[0:ROWS-1];
  reg [WIDTH-1:0] q;
  always @(posedge clk)begin
   if(accept_write && wr_bank==b)memory[wr_row]<=data_in;
   q<=memory[rd_row]; // Validity travels separately; avoid high-fanout read enables.
  end
  assign bank_q[b]=q;
 end endgenerate
 always @(posedge clk)begin
  data_valid<=accept_read;
  if(reset)begin
   wr_row<=0;rd_row<=0;wr_bank<=0;rd_bank<=0;response_bank<=0;
   count<=0;empty<=1;full<=0;overflow<=0;underflow<=0;
  end else begin
   if(push && full)overflow<=1;
   if(pop && empty)underflow<=1;
   case({accept_write,accept_read})
    2'b10:begin count<=count+1'b1;empty<=0;full<=count==BANKS*ROWS-1;end
    2'b01:begin count<=count-1'b1;full<=0;empty<=count==1;end
   endcase
   if(accept_write)begin
    if(wr_row==ROWS-1)begin wr_row<=0;wr_bank<=wr_bank==BANKS-1 ? 0 : wr_bank+1'b1;end
    else wr_row<=wr_row+1'b1;
   end
   if(accept_read)begin
    response_bank<=rd_bank;
    if(rd_row==ROWS-1)begin rd_row<=0;rd_bank<=rd_bank==BANKS-1 ? 0 : rd_bank+1'b1;end
    else rd_row<=rd_row+1'b1;
   end
  end
 end
endmodule

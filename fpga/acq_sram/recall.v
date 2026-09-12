// Plan a sequential-counter seek, without a second sample memory. Addresses
// and current_position use the same logical origin as sram_record.write_addr.
// The bus engine reports its NEXT physical transaction address, including any
// read pipeline flush clocks; do not substitute a count of delivered words.
// Zero length is explicitly empty. A full-depth request is DEPTH, never zero.
module sram_recall #(parameter AW=19)(
 input wire clk, reset, request, frozen,
 input wire [AW-1:0] record_start, current_position,
 input wire [AW:0] record_length, offset, length,
 output reg valid=0, error=0,
 output reg [AW-1:0] skip=0,
 output reg [AW:0] count=0
);
 wire [AW+1:0] end_offset={1'b0,offset}+{1'b0,length};
 localparam [AW:0] DEPTH={1'b1,{AW{1'b0}}};
 always @(posedge clk) begin
  valid<=0;
  if(reset) begin error<=0;skip<=0;count<=0;end
  else if(request) begin
   error<=!frozen || record_length>DEPTH || end_offset>{1'b0,record_length};
   if(frozen && record_length<=DEPTH && end_offset<={1'b0,record_length}) begin
    valid<=1;
    skip<=record_start+offset[AW-1:0]-current_position;
    count<=length;
   end
  end
 end
endmodule

// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Single-domain queue. A CDC transport must consume ready/valid on this domain.
// Input events cannot be backpressured. Overflow inserts an ordered loss record
// before later data; a simultaneous input during loss insertion starts a new loss.
// Epoch must remain stable until reset. Reset discards the previous epoch's queue.
// Byte zero occupies bits 7:0, matching sramcapture.DecodedEvent ABI version 1.
// TRLC-LINKS: REQ-SDS-013
module decoded_event_queue #(parameter AW=4,VALUE_BITS=32,COUNT_BITS=32)(
 input wire clk,reset,
 input wire [31:0] epoch,
 input wire in_valid,
 input wire [63:0] in_sample,
 input wire [7:0] in_kind,in_protocol,in_flags,
 input wire [31:0] in_record,in_value,in_count,
 output wire out_valid,
 input wire out_ready,
 output wire [255:0] out_data,
 output reg overflow=0
);
 localparam DEPTH=1<<AW;
 // Epoch is held for the entire queue lifetime and sequence follows dequeue
 // order. Neither needs to be duplicated in every RAM entry. Loss records
 // remain ordinary queued records and receive their own sequence number.
 // Loss records have no Value, so their full 32-bit Count shares the payload
 // with normal Value/Count. Parameter widths describe the producer contract;
 // the default retains both full-width fields. Kind 5 is reserved for loss.
 localparam PAYLOAD_BITS=VALUE_BITS+COUNT_BITS<32 ? 32 : VALUE_BITS+COUNT_BITS;
 localparam STORED_BITS=129+PAYLOAD_BITS;
 (* ramstyle="M9K" *) reg [STORED_BITS-1:0] mem[0:DEPTH-1];
 reg [STORED_BITS-1:0] ram_data=0,forward_data=0;
 reg forward_valid=0;
 reg [AW-1:0] wr=0,rd=0;
 reg [AW:0] used=0;
 reg [31:0] event_seq=0,lost=0;
 reg pending=0,loss_unknown=0;
 reg [63:0] first_lost=0;
 assign out_valid=used!=0;
 wire [STORED_BITS-1:0] head=forward_valid ? forward_data : ram_data;
 wire head_loss=head[STORED_BITS-1];
 wire [31:0] head_value=head_loss ? 32'd0 : head[128+:VALUE_BITS];
 wire [31:0] head_count=head_loss ? head[128+:32] : head[128+VALUE_BITS+:COUNT_BITS];
 assign out_data={head_count,head_value,head[127:32],event_seq,epoch,head[31:0]};
 wire pop=out_valid && out_ready;
 wire space=(used<DEPTH) || pop;
 wire push=space && (pending || in_valid);
 wire dropped=in_valid && (!space || pending);
 wire [PAYLOAD_BITS-1:0] data_payload={in_count[COUNT_BITS-1:0],in_value[VALUE_BITS-1:0]};
 wire [PAYLOAD_BITS-1:0] loss_payload=loss_unknown?32'd0:lost;
 wire [STORED_BITS-1:0] data_event={1'b0,data_payload,in_record,in_sample,in_flags,in_protocol,in_kind,8'd1};
 wire [STORED_BITS-1:0] loss_event={1'b1,loss_payload,32'd0,first_lost,8'd0,8'd0,8'd5,8'd1};
 wire [STORED_BITS-1:0] write_event=pending?loss_event:data_event;
 wire [AW-1:0] next_rd=pop ? rd+1'b1 : rd;
 // Synchronous RAM read looks ahead to the next visible head. Explicit
 // forwarding handles empty insertion / same-address read-write independently
 // of the FPGA RAM's read-during-write mode. No reset clears the RAM contents.
 always @(posedge clk)begin
  ram_data<=mem[next_rd];
  forward_valid<=!reset && push && wr==next_rd;
  if(!reset && push)begin mem[wr]<=write_event;forward_data<=write_event;end
 end
 always @(posedge clk)begin
  if(reset)begin
   wr<=0;rd<=0;used<=0;event_seq<=0;lost<=0;pending<=0;
   loss_unknown<=0;first_lost<=0;overflow<=0;
  end else begin
   if(pop)begin rd<=rd+1'b1;event_seq<=event_seq+1'b1;end
   if(push)wr<=wr+1'b1;
   case({push,pop})
    2'b10:used<=used+1'b1;
    2'b01:used<=used-1'b1;
   endcase
   if(pending && space)begin pending<=0;lost<=0;loss_unknown<=0;end
   if(dropped)begin
    overflow<=1;pending<=1;
    if(!pending || space)begin lost<=1;first_lost<=in_sample;loss_unknown<=0;end
    else if(lost==32'hffffffff)loss_unknown<=1;
    else lost<=lost+1'b1;
   end
  end
 end
endmodule

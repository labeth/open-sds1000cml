// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Host-clock FIFO of complete events. Count is a conservative DMA reservation:
// only the host removes records, so arrivals cannot invalidate a counted batch.
// Partial reads keep the current event counted; rewind only resets its cursor.
// TRLC-LINKS: REQ-SDS-013
module decoded_event_reader #(parameter AW=0,COMPACT_IDENTITY=0)(
 input wire clk,reset,
 input wire event_valid,input wire [255:0] event_data,
 output wire event_ready,
 input wire pop,rewind,clear_error,
 output wire available,
 output reg [3:0] cursor=0,
 output wire [15:0] data,
 output reg error=0,
 output wire [15:0] count
);
 localparam DEPTH=1<<AW,PW=AW==0?1:AW;
 localparam STORED_BITS=COMPACT_IDENTITY ? 192 : 256;
 (* ramstyle="M9K" *) reg [STORED_BITS-1:0] mem[0:DEPTH-1];
 reg [PW-1:0] wr=0,rd=0;
 reg [AW:0] used=0;
 reg [STORED_BITS-1:0] ram_data=0,forward_data=0;
 reg forward_valid=0;
 reg identity_seen=0,identity_fault=0;
 reg [31:0] held_epoch=0,head_sequence=0,next_sequence=0;
 wire identity_ok=!COMPACT_IDENTITY || !identity_seen ||
  (event_data[63:32]==held_epoch && event_data[95:64]==next_sequence);
 wire remove=available && pop && !rewind && cursor==15;
 wire offered=event_valid && event_ready;
 wire insert=offered && identity_ok;
 wire [PW-1:0] next_rd=AW==0 ? 0 : (remove ? rd+1'b1 : rd);
 wire [STORED_BITS-1:0] stored_head=forward_valid ? forward_data : ram_data;
 wire [STORED_BITS-1:0] stored_input=COMPACT_IDENTITY ? {event_data[255:96],event_data[31:0]} : event_data;
 wire [255:0] head=COMPACT_IDENTITY ? {stored_head[191:32],head_sequence,held_epoch,stored_head[31:0]} : stored_head;
 assign available=used!=0 && !reset;
 assign event_ready=!reset && used<DEPTH && !identity_fault;
 assign count=used;
 assign data=available ? head[16*cursor+:16] : 16'd0;
 always @(posedge clk)begin
  ram_data<=mem[next_rd];
  forward_valid<=insert && wr==next_rd;
  if(insert)begin mem[wr]<=stored_input;forward_data<=stored_input;end
 end
 always @(posedge clk)begin
  if(reset)begin wr<=0;rd<=0;used<=0;cursor<=0;error<=0;identity_seen<=0;identity_fault<=0;head_sequence<=0;next_sequence<=0;held_epoch<=0;end
  else begin
   // Epoch is constant until reset, and sequence advances per complete event,
   // including loss records. Validate arrivals before removing repeated fields
   // from RAM, so compression cannot conceal a corrupt mailbox transfer.
   if(COMPACT_IDENTITY)begin
    if(insert)begin
     if(!identity_seen)begin held_epoch<=event_data[63:32];head_sequence<=event_data[95:64];identity_seen<=1;end
     next_sequence<=event_data[95:64]+1'b1;
    end
    if(remove)head_sequence<=head_sequence+1'b1;
   end
   if(insert)wr<=AW==0 ? 0 : wr+1'b1;
   if(remove)rd<=next_rd;
   case({insert,remove})
    2'b10:used<=used+1'b1;
    2'b01:used<=used-1'b1;
   endcase
   if(clear_error && !identity_fault)error<=0;
   if(offered && !identity_ok)begin identity_fault<=1;error<=1;end
   if(pop && rewind)error<=1;
   else if(rewind)begin
    if(available)cursor<=0;else error<=1;
   end else if(pop)begin
    if(!available)error<=1;
    else cursor<=cursor+1'b1;
   end
  end
 end
endmodule

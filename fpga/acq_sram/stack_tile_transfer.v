// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Host serializer for one channel/bin. Exactly 20 little-endian halfwords
// encode sum69,sum2 106,count32,sumA69,countA32 and twelve zero padding bits.
// Upload is staged completely before any memory write. Download and completion
// hold under backpressure. Caller reserves exclusive tile ownership while busy.
// Shared reset must reset the tile/adapter and invalidate the logical stack.
// This is not a GPMC register map or a clock-domain crossing.
// TRLC-LINKS: REQ-SDS-141
module stack_tile_transfer(
 input wire clk,reset,
 input wire command_valid,output wire command_ready,input wire command_write,
 input wire [31:0] command_bin,input wire command_channel,
 input wire upload_valid,output wire upload_ready,input wire [15:0] upload_data,
 output wire download_valid,input wire download_ready,output wire [15:0] download_data,
 output wire completion_valid,input wire completion_ready,output reg completion_error=0,output wire busy,
 output wire request_valid,input wire request_ready,output wire request_write,
 output wire [31:0] request_bin,output wire request_channel,
 output wire [68:0] write_sum,write_sum_a,output wire [105:0] write_sum2,
 output wire [31:0] write_count,write_count_a,
 input wire response_valid,response_error,output wire response_ready,
 input wire [68:0] read_sum,read_sum_a,input wire [105:0] read_sum2,
 input wire [31:0] read_count,read_count_a
);
 localparam IDLE=0,UPLOAD=1,CHECK=2,REQUEST=3,WAIT=4,DOWNLOAD=5,COMPLETE=6;
 reg [2:0] state=IDLE;
 reg [319:0] payload=0;
 reg [4:0] word_index=0;
 reg write_l=0,channel_l=0;reg [31:0] bin_l=0;
 assign command_ready=state==IDLE && !reset;
 assign upload_ready=state==UPLOAD && !reset;
 assign download_valid=state==DOWNLOAD && !reset;
 assign download_data=payload[15:0];
 assign completion_valid=state==COMPLETE && !reset;
 assign busy=state!=IDLE && !reset;
 assign request_valid=state==REQUEST && !reset;
 assign request_write=write_l;assign request_bin=bin_l;assign request_channel=channel_l;
 assign {write_count_a,write_sum_a,write_count,write_sum2,write_sum}=payload[307:0];
 assign response_ready=state==WAIT && !reset;
 always @(posedge clk)begin
  if(reset)begin state<=IDLE;payload<=0;word_index<=0;completion_error<=0;end
  else case(state)
   IDLE:if(command_valid)begin
    write_l<=command_write;bin_l<=command_bin;channel_l<=command_channel;
    payload<=0;word_index<=0;completion_error<=0;state<=command_write ? UPLOAD:REQUEST;
   end
   UPLOAD:if(upload_valid)begin
    payload<={upload_data,payload[319:16]};
    if(word_index==19)state<=CHECK;else word_index<=word_index+1'b1;
   end
   CHECK:begin
    if(|payload[319:308])begin completion_error<=1;state<=COMPLETE;end
    else state<=REQUEST;
   end
   REQUEST:if(request_ready)state<=WAIT;
   WAIT:if(response_valid)begin
    completion_error<=response_error;
    if(response_error || write_l)state<=COMPLETE;
    else begin payload<={12'd0,read_count_a,read_sum_a,read_count,read_sum2,read_sum};word_index<=0;state<=DOWNLOAD;end
   end
   DOWNLOAD:if(download_ready)begin
    payload<=payload>>16;
    if(word_index==19)state<=COMPLETE;else word_index<=word_index+1'b1;
   end
   COMPLETE:if(completion_ready)state<=IDLE;
   default:begin completion_error<=1;state<=COMPLETE;end
  endcase
 end
endmodule

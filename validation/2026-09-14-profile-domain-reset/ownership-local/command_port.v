// Host-domain register window and coherent acquisition command decoder.
// Connect to gpmc_slave's committed writes; full board status/RAM readout is
// outside this module. See docs/fpga-profile-command-abi.md.
module acq_command_port #(parameter ENABLE_STREAM=0,ENABLE_SNAPSHOT=1,ENABLE_MATCH=1)(
 input wire reset,host_clk,core_clk,host_write,
 input wire [7:0] write_sel,read_sel,input wire [15:0] write_data,
 output reg read_hit,output reg [15:0] read_data,
 output wire host_busy,output reg host_rejected=0,
 output wire core_start,core_halt,core_force,core_snapshot,core_error,
 output wire [1:0] operation,
 output wire [19:0] pre_count,post_count,offset,length,
 output wire [18:0] read_bias,output wire [4:0] decim_log,
 output wire [9:0] encode_enable,output wire [1:0] trigger_mode,
 output wire trigger_channel,trigger_falling,output wire [15:0] trigger_level,
 input wire acquisition_request_error,input wire [127:0] acquisition_status
);
 (* async_reg="true" *) reg [1:0] host_reset_sync=3,core_reset_sync=3;
 always @(posedge host_clk or posedge reset)
  if(reset)host_reset_sync<=3;else host_reset_sync<={host_reset_sync[0],1'b0};
 always @(posedge core_clk or posedge reset)
  if(reset)core_reset_sync<=3;else core_reset_sync<={core_reset_sync[0],1'b0};
 reg [191:0] config_shadow=192'h03ff_0000_0000_0000_0000_0000_0000_0000_0000_0001_0000_0000;
 localparam [191:0] DEFAULT_CONFIG=192'h03ff_0000_0000_0000_0000_0000_0000_0000_0000_0001_0000_0000;
 wire send=host_write && write_sel==8'h10;
 wire bridge_rejected,valid,request_busy;
 reg pending_result=0,result_valid=0;
 reg [1:0] result_code=0;
 reg [15:0] accepted_opcode=0,sequence_number=0;
 wire accepted=send && !host_busy;
 assign host_busy=request_busy || pending_result;
 wire [207:0] payload;
 acq_command_bridge #(.WIDTH(208)) bridge(
  .reset(reset),.host_clk(host_clk),.core_clk(core_clk),
  .host_send(accepted),.host_payload({write_data,config_shadow}),
  .host_busy(request_busy),.host_rejected(bridge_rejected),
  .core_valid(valid),.core_payload(payload));
 // Acquisition request_error is registered on the edge consuming core_start.
 // Capture it on the next edge, then send one coherent result back to host.
 reg reply_pending=0,decode_error_q=0,start_q=0,reply_send=0;
 reg [1:0] reply_code=0;
 wire reply_busy,reply_rejected,returned;
 wire [129:0] returned_payload;
 wire [1:0] returned_code=returned_payload[1:0];
 always @(posedge core_clk)begin
  if(core_reset_sync[1])begin reply_pending<=0;reply_send<=0;decode_error_q<=0;start_q<=0;end
  else begin
   reply_pending<=valid;decode_error_q<=core_error;start_q<=core_start;
   reply_send<=reply_pending;
   if(reply_pending)reply_code<=decode_error_q ? 2'd1 :
    start_q && acquisition_request_error ? 2'd2 : 2'd0;
  end
 end
 acq_command_bridge #(.WIDTH(130)) response(
  .reset(reset),.host_clk(core_clk),.core_clk(host_clk),
  .host_send(reply_send),.host_payload({acquisition_status,reply_code}),.host_busy(reply_busy),
  .host_rejected(reply_rejected),.core_valid(returned),.core_payload(returned_payload));
 // synthesis translate_off
 always @(posedge core_clk)if(!reset && reply_send && reply_busy)
  $fatal(1,"command response mailbox overrun");
 // synthesis translate_on
 always @(posedge host_clk)begin
  if(host_reset_sync[1])begin
   config_shadow<=DEFAULT_CONFIG;host_rejected<=0;pending_result<=0;
   result_valid<=0;result_code<=0;accepted_opcode<=0;sequence_number<=0;
  end
  else begin
   if(accepted)begin
    pending_result<=1;result_valid<=0;accepted_opcode<=write_data;
    sequence_number<=sequence_number+1'b1;
   end
   if(returned)begin pending_result<=0;result_valid<=1;result_code<=returned_code;end
   if(host_write && write_sel>=8'h20 && write_sel<=8'h2b)
    config_shadow[16*(write_sel-8'h20)+:16]<=write_data;
   if(host_write && write_sel==8'h11 && write_data[1])host_rejected<=0;
   if(send && host_busy)host_rejected<=1;
  end
 end
 always @* begin
  read_hit=1;read_data=0;
  if(read_sel==8'h11)read_data={13'b0,result_valid,host_rejected,host_busy};
  else if(read_sel==8'h12)read_data={14'b0,result_code};
  else if(read_sel==8'h13)read_data=accepted_opcode;
  else if(read_sel==8'h14)read_data=sequence_number;
  else if(read_sel>=8'h20 && read_sel<=8'h2b)
   read_data=config_shadow[16*(read_sel-8'h20)+:16];
  else if(read_sel>=8'h40 && read_sel<=8'h47)
   read_data=result_valid ? returned_payload[2+16*read_sel[2:0]+:16] : 16'b0;
  else read_hit=0;
 end
 wire [15:0] opcode=payload[207:192];
 wire starting=opcode==1 || opcode==2 || opcode==3;
 wire supported=opcode>=1 && opcode<=7 && (ENABLE_STREAM || opcode!=3) &&
  (ENABLE_SNAPSHOT || opcode!=6);
 // Reject reserved bits before slicing operands; never silently truncate
 // malformed geometry, encode masks, or packed trigger configuration.
 wire config_legal=payload[31:20]==0 && payload[63:52]==0 &&
  payload[95:84]==0 && payload[127:116]==0 && !payload[147] &&
  payload[159:157]==0 && payload[191:186]==0 &&
  (ENABLE_MATCH || opcode!=1 || payload[154:153]!=2);
 assign core_error=valid && (!supported || (starting && !config_legal));
 assign core_start=valid && supported && starting && config_legal;
 assign core_halt=valid && opcode==4;
 assign core_force=valid && opcode==5;
 assign core_snapshot=valid && ENABLE_SNAPSHOT && opcode==6;
 assign operation=opcode==2 ? 2'd2 : opcode==3 ? 2'd1 : 2'd0;
 assign pre_count=payload[19:0];assign post_count=payload[51:32];
 assign offset=payload[83:64];assign length=payload[115:96];
 assign read_bias={payload[146:144],payload[143:128]};
 assign decim_log=payload[152:148];assign trigger_mode=payload[154:153];
 assign trigger_channel=payload[155];assign trigger_falling=payload[156];
 assign trigger_level=payload[175:160];assign encode_enable=payload[185:176];
endmodule

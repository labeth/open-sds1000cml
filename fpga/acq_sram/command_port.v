// Host-domain register window and coherent acquisition command decoder.
// Connect to gpmc_slave's committed writes; full board status/RAM readout is
// outside this module. See docs/fpga-profile-command-abi.md.
module acq_command_port #(parameter ENABLE_STREAM=0)(
 input wire reset,host_clk,core_clk,host_write,
 input wire [7:0] write_sel,read_sel,input wire [15:0] write_data,
 output reg read_hit,output reg [15:0] read_data,
 output wire host_busy,output reg host_rejected=0,
 output wire core_start,core_halt,core_force,core_snapshot,core_error,
 output wire [1:0] operation,
 output wire [19:0] pre_count,post_count,offset,length,
 output wire [18:0] read_bias,output wire [4:0] decim_log,
 output wire [9:0] encode_enable,output wire [1:0] trigger_mode,
 output wire trigger_channel,trigger_falling,output wire [15:0] trigger_level
);
 reg [191:0] config_shadow=192'h03ff_0000_0000_0000_0000_0000_0000_0000_0000_0001_0000_0000;
 localparam [191:0] DEFAULT_CONFIG=192'h03ff_0000_0000_0000_0000_0000_0000_0000_0000_0001_0000_0000;
 wire send=host_write && write_sel==8'h10;
 wire bridge_rejected,valid;
 wire [207:0] payload;
 acq_command_bridge #(.WIDTH(208)) bridge(
  .reset(reset),.host_clk(host_clk),.core_clk(core_clk),
  .host_send(send),.host_payload({write_data,config_shadow}),
  .host_busy(host_busy),.host_rejected(bridge_rejected),
  .core_valid(valid),.core_payload(payload));
 always @(posedge host_clk)begin
  if(reset)begin config_shadow<=DEFAULT_CONFIG;host_rejected<=0;end
  else begin
   if(host_write && write_sel>=8'h20 && write_sel<=8'h2b)
    config_shadow[16*(write_sel-8'h20)+:16]<=write_data;
   if(host_write && write_sel==8'h11 && write_data[1])host_rejected<=0;
   if(bridge_rejected)host_rejected<=1;
  end
 end
 always @* begin
  read_hit=1;read_data=0;
  if(read_sel==8'h11)read_data={14'b0,host_rejected,host_busy};
  else if(read_sel>=8'h20 && read_sel<=8'h2b)
   read_data=config_shadow[16*(read_sel-8'h20)+:16];
  else read_hit=0;
 end
 wire [15:0] opcode=payload[207:192];
 wire starting=opcode==1 || opcode==2 || opcode==3;
 wire supported=opcode>=1 && opcode<=6 && (ENABLE_STREAM || opcode!=3);
 // Reject reserved bits before slicing operands; never silently truncate
 // malformed geometry, encode masks, or packed trigger configuration.
 wire config_legal=payload[31:20]==0 && payload[63:52]==0 &&
  payload[95:84]==0 && payload[127:116]==0 && !payload[147] &&
  payload[159:157]==0 && payload[191:186]==0;
 assign core_error=valid && (!supported || (starting && !config_legal));
 assign core_start=valid && supported && starting && config_legal;
 assign core_halt=valid && opcode==4;
 assign core_force=valid && opcode==5;
 assign core_snapshot=valid && opcode==6;
 assign operation=opcode==2 ? 2'd2 : opcode==3 ? 2'd1 : 2'd0;
 assign pre_count=payload[19:0];assign post_count=payload[51:32];
 assign offset=payload[83:64];assign length=payload[115:96];
 assign read_bias={payload[146:144],payload[143:128]};
 assign decim_log=payload[152:148];assign trigger_mode=payload[154:153];
 assign trigger_channel=payload[155];assign trigger_falling=payload[156];
 assign trigger_level=payload[175:160];assign encode_enable=payload[185:176];
endmodule

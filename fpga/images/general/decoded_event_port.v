// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Host register interface for decoded_event_transport (all ports host clock).
// 57: enable; 58: available/error/cursor; 59: data (read commits a halfword);
// 60: rewind bit0, clear error bit1; 61/62: stream epoch; 63: ABI capability.
// 74: batch capability; write 76=1 snapshots complete-record count into 75.
// Snapshot stays stable across an asynchronous host read and later arrivals.
// Disabling clears transport state. Each enable begins a new epoch. The host
// must not attribute events from earlier epochs to a later retained capture.
// TRLC-LINKS: REQ-SDS-013
module decoded_event_port(
 input wire clk,reset,write,read_pop,
 input wire [7:0] write_sel,read_sel,input wire [15:0] write_data,
 input wire available,error,input wire [3:0] cursor,input wire [15:0] data,
 output reg enabled=0,output reg [31:0] epoch=0,
 output wire pop,rewind,clear_error,
 output reg read_hit,output reg [15:0] read_data,
 input wire [15:0] complete_count
);
 wire control=write && write_sel==60;
 wire reserve=write && write_sel==76;
 wire malformed=(control && |write_data[15:2]) || (reserve && write_data!=1);
 reg [15:0] reserved_count=0;
 assign pop=enabled && read_pop && read_sel==59;
 assign rewind=enabled && control && !malformed && write_data[0];
 assign clear_error=control && !malformed && write_data[1];
 reg command_error=0;
 always @(posedge clk)begin
  if(reset)begin enabled<=0;epoch<=0;command_error<=0;reserved_count<=0;end
  else begin
   if(clear_error)command_error<=0;
   if(malformed)command_error<=1;
   if(reserve && !malformed)reserved_count<=enabled ? complete_count : 16'd0;
   if(write && write_sel==57)begin
    if(|write_data[15:1])command_error<=1;
    else begin
     enabled<=write_data[0];
     reserved_count<=0;
     if(write_data[0] && !enabled)epoch<=epoch+1'b1;
    end
   end
  end
 end
 always @*begin
  read_hit=1;read_data=0;
  case(read_sel)
   57:read_data={15'd0,enabled};
   58:read_data={8'd0,cursor,1'b0,command_error,error,available && enabled};
   59:read_data=enabled?data:16'd0;
   60:read_data=0;
   61:read_data=epoch[15:0];
   62:read_data=epoch[31:16];
   63:read_data=16'h4501;
   74:read_data=16'h4503;
   75:read_data=reserved_count;
   76:read_data=0;
   default:read_hit=0;
  endcase
 end
endmodule

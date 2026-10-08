// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Ready/valid mailbox: source payload stays held until destination acceptance.
// A common reset must assert in both domains; release is synchronized locally.
// Bundled payload routing requires a physical max-delay constraint at integration.
// No claim of hardware CDC qualification follows from simulation alone.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-058
module decoded_event_bridge(
 input wire reset,source_clk,host_clk,
 input wire source_valid,input wire [255:0] source_data,
 output wire source_ready,
 output reg host_valid=0,output reg [255:0] host_data=0,
 input wire host_ready
);
 (* async_reg="true" *) reg [1:0] source_reset=3,host_reset=3;
 always @(posedge source_clk or posedge reset)
  if(reset)source_reset<=3;else source_reset<={source_reset[0],1'b0};
 always @(posedge host_clk or posedge reset)
  if(reset)host_reset<=3;else host_reset<={host_reset[0],1'b0};
 reg request=0,ack=0;
 reg [255:0] held_payload=0;
 (* async_reg="true" *) reg [2:0] request_sync=0,ack_sync=0;
 assign source_ready=!source_reset[1] && request==ack_sync[2];
 always @(posedge source_clk or posedge source_reset[1])begin
  if(source_reset[1])begin request<=0;ack_sync<=0;end
  else begin
   ack_sync<={ack_sync[1:0],ack};
   if(source_valid && source_ready)request<=!request;
  end
 end
 always @(posedge source_clk)
  if(source_valid && source_ready)held_payload<=source_data;
 always @(posedge host_clk or posedge host_reset[1])begin
  if(host_reset[1])begin request_sync<=0;ack<=0;host_valid<=0;end
  else begin
   request_sync<={request_sync[1:0],request};
   if(!host_valid && request_sync[2]!=ack)begin
    host_data<=held_payload;host_valid<=1;
   end
   if(host_valid && host_ready)begin
    host_valid<=0;ack<=request_sync[2];
   end
  end
 end
endmodule

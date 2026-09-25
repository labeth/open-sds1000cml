// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-COMMAND-BRIDGE
// Coherent host -> acquisition command mailbox. A shared reset aborts the
// epoch; it must assert to both domains. Payload remains held until the core
// has sampled it and its acknowledgment has crossed back to the host.
// Acknowledgment means delivered, not accepted by the acquisition engine.
// TRLC-LINKS: REQ-SDS-054, REQ-SDS-055, REQ-SDS-058, REQ-SDS-077
module acq_command_bridge #(parameter WIDTH=192)(
 input wire reset,host_clk,core_clk,
 input wire host_send,input wire [WIDTH-1:0] host_payload,
 output wire host_busy,output reg host_rejected=0,
 output reg core_valid=0,output reg [WIDTH-1:0] core_payload=0
);
 (* async_reg = "true" *) reg [1:0] host_reset=3,core_reset=3;
 always @(posedge host_clk or posedge reset)
  if(reset)host_reset<=3;else host_reset<={host_reset[0],1'b0};
 always @(posedge core_clk or posedge reset)
  if(reset)core_reset<=3;else core_reset<={core_reset[0],1'b0};
 reg request=0,acknowledged=0;
 reg [WIDTH-1:0] held_payload=0;
 (* async_reg = "true" *) reg [1:0] request_sync=0,ack_sync=0;
 assign host_busy=host_reset[1] || request!=ack_sync[1];
 always @(posedge host_clk or posedge host_reset[1])begin
  if(host_reset[1])begin request<=0;ack_sync<=0;host_rejected<=0;end
  else begin
   ack_sync<={ack_sync[0],acknowledged};
   host_rejected<=host_send && host_busy;
   if(host_send && !host_busy)request<=!request;
  end
 end
 // No reset on unpublished payload; the token/reset controls validity.
 always @(posedge host_clk)
  if(host_send && !host_busy)held_payload<=host_payload;
 always @(posedge core_clk or posedge core_reset[1])begin
  if(core_reset[1])begin request_sync<=0;acknowledged<=0;core_valid<=0;end
  else begin
   request_sync<={request_sync[0],request};
   core_valid<=request_sync[1]!=acknowledged;
   if(request_sync[1]!=acknowledged)acknowledged<=request_sync[1];
  end
 end
 always @(posedge core_clk)
  if(!core_reset[1] && request_sync[1]!=acknowledged)
   core_payload<=held_payload;
endmodule

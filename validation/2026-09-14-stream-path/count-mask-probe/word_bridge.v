// One outstanding word, acknowledged only when the destination consumes it.
// The source must hold valid/data until ready. Payload is held throughout the
// round trip; constrain its bundled path into received_data when integrating.
// reset is a COMMON epoch boundary: assert to both clocks, with local
// synchronized release. Independent reset of one side is not supported.
module sram_word_bridge #(parameter WIDTH=32)(
 input wire reset,source_clk,dest_clk,
 input wire source_valid,input wire [WIDTH-1:0] source_data,
 output wire source_ready,
 output reg dest_valid=0,output reg [WIDTH-1:0] dest_data=0,
 input wire dest_ready
);
 (* async_reg="true" *) reg [1:0] source_reset=3,dest_reset=3;
 always @(posedge source_clk or posedge reset)
  if(reset)source_reset<=3;else source_reset<={source_reset[0],1'b0};
 always @(posedge dest_clk or posedge reset)
  if(reset)dest_reset<=3;else dest_reset<={dest_reset[0],1'b0};
 reg request=0,ack=0;
 (* preserve *) reg [WIDTH-1:0] payload=0;
 (* async_reg="true" *) reg [1:0] ack_sync=0,request_sync=0;
 assign source_ready=!source_reset[1] && !reset && request==ack_sync[1];
 always @(posedge source_clk or posedge source_reset[1])begin
  if(source_reset[1])begin request<=0;ack_sync<=0;end
  else begin
   ack_sync<={ack_sync[0],ack};
   if(source_valid && source_ready)request<=!request;
  end
 end
 // Only the token publishes a payload. Capture while the mailbox is idle;
 // during reset this storage may change, but no request can be published.
 // While a request is outstanding it remains held until acknowledgement.
 always @(posedge source_clk)begin
  if(source_valid && request==ack_sync[1])payload<=source_data;
 end
 always @(posedge dest_clk or posedge dest_reset[1])begin
  if(dest_reset[1])begin ack<=0;request_sync<=0;dest_valid<=0;dest_data<=0;end
  else begin
   request_sync<={request_sync[0],request};
   if(dest_valid)begin
    if(dest_ready)begin dest_valid<=0;ack<=request_sync[1];end
   end else if(request_sync[1]!=ack)begin dest_data<=payload;dest_valid<=1;end
  end
 end
endmodule

`timescale 1ns/1ps
// Pack the reduced 32-bit word stream into the 64-bit host RAM clock domain.
// The mailbox is acknowledged: it is never overwritten while in flight.
// Source data is not backpressurable; exceeding mailbox capacity latches fault.
// Intended source is reduced precision data, not the 250 MHz raw word stream.
//
// stop includes a valid word on the same edge, then ignores later words. After
// the last complete packet is acknowledged, a final single-word or empty seal
// closes the bank. finished means this seal was acknowledged in packet_clk.
// Payload is held until acknowledgement; bundled CDC paths need physical bounds
// when integrated. Reset is an epoch boundary in both clock domains.
module stream_packetizer(
 input wire reset,word_clk,packet_clk,
 input wire word_valid,input wire [31:0] word_data,input wire stop,
 output reg fault=0,output wire finished,
 output reg packet_valid=0,output reg [63:0] packet_data=0,
 output reg packet_single=0,output reg packet_seal=0
);
 (* async_reg = "true" *) reg [1:0] word_reset=3,packet_reset=3;
 always @(posedge word_clk or posedge reset)
  if(reset)word_reset<=3;else word_reset<={word_reset[0],1'b0};
 always @(posedge packet_clk or posedge reset)
  if(reset)packet_reset<=3;else packet_reset<={packet_reset[0],1'b0};
 wire wr=word_reset[1],pr=packet_reset[1];
 reg half=0;reg [31:0] first=0;
 reg stopping=0,sealed=0,request=0,ack=0;
 reg [63:0] payload=0;reg payload_valid=0,payload_single=0,payload_seal=0;
 (* async_reg = "true" *) reg [1:0] ack_sync=0;
 (* async_reg = "true" *) reg [2:0] request_sync=0;
 wire busy=request!=ack_sync[1];
 assign finished=!wr && !fault && sealed && !busy;
 always @(posedge word_clk or posedge wr)begin
  if(wr)begin
   half<=0;first<=0;stopping<=0;sealed<=0;request<=0;ack_sync<=0;
   payload<=0;payload_valid<=0;payload_single<=0;payload_seal<=0;fault<=0;
  end else begin
   ack_sync<={ack_sync[0],ack};
   if(stop)stopping<=1;
   if(word_valid && !stopping && !fault)begin
    if(!half)begin first<=word_data;half<=1;end
    else if(busy)fault<=1;
    else begin
     payload<={word_data,first};payload_valid<=1;payload_single<=0;payload_seal<=0;
     request<=!request;half<=0;
    end
   end
   if(stopping && !sealed && !busy && !fault)begin
    payload<={32'b0,first};payload_valid<=half;payload_single<=half;payload_seal<=1;
    request<=!request;half<=0;sealed<=1;
   end
  end
 end
 always @(posedge packet_clk or posedge pr)begin
  if(pr)begin
   request_sync<=0;ack<=0;packet_data<=0;packet_valid<=0;packet_single<=0;packet_seal<=0;
  end else begin
   request_sync<={request_sync[1:0],request};
   packet_valid<=0;packet_single<=0;packet_seal<=0;
   if(request_sync[2]!=ack)begin
    packet_data<=payload;packet_valid<=payload_valid;
    packet_single<=payload_single;packet_seal<=payload_seal;
    ack<=request_sync[2];
   end
  end
 end
endmodule

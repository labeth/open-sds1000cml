// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-HOST-SINK
// Ordered 125 MHz consumer of the host FIFO's 80-bit packets.
// [79]=descriptor, [78]=bank, [77:64]=pair address or valid word count,
// [63:0]=paired samples or first-word ordinal. DATA writes RAM on the
// next edge after acceptance; a subsequent descriptor publishes after that write.
// Caller supplies common epoch reset, prevents writes to owned banks, and
// crosses publication/metadata to ARM with an ownership handshake.
// TRLC-LINKS: REQ-SDS-094
module sram_host_sink(
 input wire clk,reset,upstream_fault,
 input wire valid,input wire [79:0] data,output wire ready,
 output reg ram_write=0,output reg [11:0] ram_pair=0,
 output reg [63:0] ram_data=0,input wire ram_fault,
 output reg fault=0,output reg [1:0] publish=0,
 output reg [63:0] first0=0,first1=0,
 output reg [11:0] words0=0,words1=0
);
 wire descriptor=data[79],bank=data[78];
 wire [13:0] field=data[77:64];
 // Track accepted words independently of the advertised descriptor. This
 // rejects missing, duplicate and reordered pairs before publishing a bank.
 reg [11:0] pairs0=0,pairs1=0;
 wire [11:0] count=bank ? pairs1 : pairs0;
 wire [13:0] expected_pair=(bank ? 14'd1280 : 14'd0)+count;
 wire legal_data=count<1280 && field==expected_pair;
 wire legal_descriptor=field!=0 && field<=2560 &&
                       (({1'b0,field}+1'b1)>>1)==count;
 wire legal=descriptor ? legal_descriptor : legal_data;
 assign ready=!reset && !fault && !upstream_fault && !ram_fault;
 // Validation controls publication and accounting, not private payload capture.
 (* keep *) wire metadata0=valid && ready && legal_descriptor && descriptor && !bank;
 (* keep *) wire metadata1=valid && ready && legal_descriptor && descriptor && bank;
 (* keep *) wire advance0=valid && ready && legal_data && !descriptor && !bank;
 (* keep *) wire advance1=valid && ready && legal_data && !descriptor && bank;
 // DATA is validated at edge N and physically written at edge N+1.
 // A following META publishes after edge N+1, once that write has occurred.
 always @(posedge clk)begin
  ram_write<=!reset && valid && ready && !descriptor && legal;
  ram_pair<=field[11:0];ram_data<=data[63:0];
 end
 // Metadata is private until publish. Capture the offered descriptor without
 // the ownership/geometry fan-in; only the validated publication below makes
 // it visible. Ownership samples on the following edge and holds its own copy.
 always @(posedge clk)begin
  if(valid && descriptor && !bank)begin first0<=data[63:0];words0<=field[11:0];end
  if(valid && descriptor && bank)begin first1<=data[63:0];words1<=field[11:0];end
 end
 always @(posedge clk)begin
  publish<=0;
  if(reset)begin
   fault<=0;pairs0<=0;pairs1<=0;
  end else begin
   if(upstream_fault || ram_fault || (valid && ready && !legal))fault<=1;
   if(metadata0)begin publish[0]<=1;pairs0<=0;end
   else if(advance0)pairs0<=pairs0+1'b1;
   if(metadata1)begin publish[1]<=1;pairs1<=0;end
   else if(advance1)pairs1<=pairs1+1'b1;
  end
 end
endmodule

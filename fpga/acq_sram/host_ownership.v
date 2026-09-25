// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-HOST-OWNERSHIP
// RAM-clock publication -> ARM-clock descriptor -> core-clock release.
// A common reset abandons the epoch; independent resets are unsupported.
// One outstanding release per descriptor. One-bit tokens reject immediate
// duplicates/stale releases, not arbitrary releases retained across two uses.
// Caller reserves banks before writing and must not reuse until core_release.
// Metadata is held at producer until release, sampled through two stages,
// and exposed only after a three-stage publication token. Requires bundled
// metadata and token timing constraints before hardware use.
// TRLC-LINKS: REQ-SDS-052
module sram_host_ownership(
 input wire reset,producer_clk,host_clk,core_clk,
 input wire [1:0] publish,input wire [63:0] first0,first1,
 input wire [11:0] words0,words1,
 output reg fault=0,output wire [1:0] producer_busy,
 input wire host_release,release_bank,release_token,
 output wire [1:0] host_ready,host_token,
 output wire [63:0] host_first0,host_first1,
 output wire [11:0] host_words0,host_words1,
 output wire [1:0] core_release
);
 (* async_reg="true" *) reg [1:0] pr=3,hr=3,cr=3;
 always @(posedge producer_clk or posedge reset)if(reset)pr<=3;else pr<={pr[0],1'b0};
 always @(posedge host_clk or posedge reset)if(reset)hr<=3;else hr<={hr[0],1'b0};
 always @(posedge core_clk or posedge reset)if(reset)cr<=3;else cr<={cr[0],1'b0};
 reg [1:0] published=0,released=0;
 reg [75:0] held0=0,held1=0;
 (* async_reg="true",preserve *) reg [1:0] ack1=0,ack2=0;
 (* async_reg="true",preserve *) reg [1:0] pub1=0,pub2=0,pub3=0;
 (* async_reg="true",preserve *) reg [1:0] rel1=0,rel2=0;
 reg [1:0] rel_seen=0;
 reg [75:0] meta0=0,meta1=0,settled0=0,settled1=0;
 assign producer_busy=published^ack2;
 // Local resets assert asynchronously and release on their destination clock.
 // Do not bypass them with the source-domain reset on functional outputs.
 assign host_ready=hr[1] ? 2'b0 : pub3^released;
 assign host_token=pub3;
 assign {host_words0,host_first0}=settled0;
 assign {host_words1,host_first1}=settled1;
 assign core_release=cr[1] ? 2'b0 : rel2^rel_seen;
 always @(posedge producer_clk or posedge pr[1])begin
  if(pr[1])begin published<=0;ack1<=0;ack2<=0;held0<=0;held1<=0;fault<=0;end
  else begin
   ack1<=released;ack2<=ack1;
   if(|(publish & producer_busy))fault<=1;
   if(!fault)begin
    if(publish[0] && !producer_busy[0])begin
     held0<={words0,first0};published[0]<=!published[0];
    end
    if(publish[1] && !producer_busy[1])begin
     held1<={words1,first1};published[1]<=!published[1];
    end
   end
  end
 end
 always @(posedge host_clk or posedge hr[1])begin
  if(hr[1])begin pub1<=0;pub2<=0;pub3<=0;released<=0;meta0<=0;meta1<=0;settled0<=0;settled1<=0;end
  else begin
   pub1<=published;pub2<=pub1;pub3<=pub2;
   meta0<=held0;meta1<=held1;settled0<=meta0;settled1<=meta1;
   if(host_release && host_ready[release_bank] && release_token==pub3[release_bank])
    released[release_bank]<=release_token;
  end
 end
 always @(posedge core_clk or posedge cr[1])begin
  if(cr[1])begin rel1<=0;rel2<=0;rel_seen<=0;end
  // Forward only the RAM-domain acknowledgment: core reuse must follow
  // producer_busy clearing, even if the producer clock pauses.
  else begin rel1<=ack2;rel2<=rel1;rel_seen<=rel2;end
 end
endmodule

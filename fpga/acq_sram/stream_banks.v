`timescale 1ns/1ps
// Ownership/metadata for a two-bank 64-bit host buffer. The caller supplies
// dual-clock RAM: write at producer_clk using mem_write/address/data, read only
// host_ready banks. Default: 2 x 1024 packets = 4096 32-bit SRAM-format words.
// A packet contains two consecutive words; first_word counts words, not packets.
//
// Ready descriptors and RAM remain immutable until a matching host release.
// release_token must be the token observed with that descriptor. A duplicate or
// stale release does not reclaim a newer generation. Host must not read a bank
// after releasing it. Tokens alternate per bank; host may have one outstanding
// release per descriptor (they are not arbitrary long-lived transaction IDs).
//
// Producer input cannot be backpressured: a valid packet without space latches
// overrun and stops writes until reset. Existing published banks remain readable.
// seal publishes a partial bank, including a packet accepted on that same edge.
// packet_single marks a final low-word-only packet and also seals the bank.
// Valid words in a descriptor = 2*packets - last_single[bank].
// Reset is an epoch boundary: abandon old descriptors/releases and restart word
// numbering. Reset assertion is asynchronous, deassertion local-clock synchronized.
module stream_banks #(parameter BANK_AW=10)(
 input wire reset,producer_clk,host_clk,
 input wire packet_valid,input wire [63:0] packet_data,input wire packet_single,seal,
 output wire packet_ready,
 output wire mem_write,output wire [BANK_AW:0] mem_address,output wire [63:0] mem_data,
 output reg overrun=0,output wire host_overrun,
 input wire host_release,input wire release_bank,release_token,
 output wire [1:0] host_ready,output wire [1:0] host_token,
 output wire [63:0] first_word0,first_word1,
 output wire [BANK_AW:0] packets0,packets1,output wire [1:0] last_single
);
 (* async_reg = "true" *) reg [1:0] producer_reset=3,host_reset=3;
 always @(posedge producer_clk or posedge reset)
  if(reset)producer_reset<=3;else producer_reset<={producer_reset[0],1'b0};
 always @(posedge host_clk or posedge reset)
  if(reset)host_reset<=3;else host_reset<={host_reset[0],1'b0};
 wire pr=producer_reset[1],hr=host_reset[1];
 reg bank=0;reg [BANK_AW-1:0] offset=0;
 reg [63:0] next_word=0,base0=0,base1=0;
 reg [BANK_AW:0] count0=0,count1=0;
 reg [1:0] published=0,released=0,single_last=0,single_q=0,single_host=0;
 (* async_reg = "true" *) reg [1:0] ack1=0,ack2=0;
 (* async_reg = "true" *) reg [1:0] pub1=0,pub2=0,pub3=0;
 (* async_reg = "true" *) reg [2:0] fault_host=0;
 // Bundled metadata settles through two registers before the three-register
 // publication token can make it visible. Producer holds it until release.
 reg [63:0] base0_q=0,base1_q=0,base0_host=0,base1_host=0;
 reg [BANK_AW:0] count0_q=0,count1_q=0,count0_host=0,count1_host=0;
 assign packet_ready=!pr && !overrun && published[bank]==ack2[bank];
 assign mem_write=packet_valid && packet_ready;
 assign mem_address={bank,offset};assign mem_data=packet_data;
 assign host_ready=hr ? 2'b00 : pub3 ^ released;
 assign host_token=pub3;assign host_overrun=fault_host[2];
 assign first_word0=base0_host;assign first_word1=base1_host;
 assign packets0=count0_host;assign packets1=count1_host;assign last_single=single_host;
 wire full=&offset;
 wire publish=packet_ready && ((packet_valid && (full || seal || packet_single)) || (seal && !packet_valid && offset!=0));
 always @(posedge producer_clk or posedge pr)begin
  if(pr)begin
   bank<=0;offset<=0;next_word<=0;base0<=0;base1<=0;count0<=0;count1<=0;
   published<=0;ack1<=0;ack2<=0;overrun<=0;single_last<=0;
  end else begin
   ack1<=released;ack2<=ack1;
   if(packet_valid && !packet_ready)overrun<=1;
   if(mem_write)begin
    if(offset==0)begin if(bank)base1<=next_word;else base0<=next_word;end
    next_word<=next_word+(packet_single ? 2'd1 : 2'd2);
    offset<=offset+1'b1;
   end
   if(publish)begin
    single_last[bank]<=packet_valid && packet_single;
    if(bank)count1<={1'b0,offset}+packet_valid;else count0<={1'b0,offset}+packet_valid;
    published[bank]<=!published[bank];bank<=!bank;offset<=0;
   end
  end
 end
 always @(posedge host_clk or posedge hr)begin
  if(hr)begin
   pub1<=0;pub2<=0;pub3<=0;released<=0;fault_host<=0;single_q<=0;single_host<=0;
   base0_q<=0;base1_q<=0;base0_host<=0;base1_host<=0;
   count0_q<=0;count1_q<=0;count0_host<=0;count1_host<=0;
  end else begin
   pub1<=published;pub2<=pub1;pub3<=pub2;fault_host<={fault_host[1:0],overrun};
   single_q<=single_last;single_host<=single_q;
   base0_q<=base0;base1_q<=base1;base0_host<=base0_q;base1_host<=base1_q;
   count0_q<=count0;count1_q<=count1;count0_host<=count0_q;count1_host<=count1_q;
   if(host_release && host_ready[release_bank] && release_token==pub3[release_bank])
    released[release_bank]<=release_token;
  end
 end
endmodule

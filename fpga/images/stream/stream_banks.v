// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// ADR-STREAM-IMAGE: two ping-pong banks of 2^BANK_AW 64-bit packets in the
// host buffer RAM (bank b at entries b*2^BANK_AW...). The producer fills one
// bank while the host drains the other; a bank is published when full or on a
// seal, with the stream ordinal of its first 32-bit word, its packet count and
// whether its last packet holds a single word.
//
// Publication and release cross by toggles: host_ready[b] while the bank's
// publish toggle differs from its release toggle, host_token[b] is that
// publication's parity, and a release names the bank and the token it read,
// so a stale release (a bank republished meanwhile) is ignored. A packet that
// finds the next bank still unreleased is dropped and raises overrun, sticky
// until reset: loss is always visible to the host, never silent.
//
// The published metadata is held from publication until release, so the host
// reads it as quasi-static data once host_ready is seen.
//
// packet_ready reports that the other bank is free, so a seal may publish the
// current one. A seal that publishes while the other bank is still the host's
// leaves the producer nowhere to go: a seal landing just after a full bank's
// publication overran at once (bench 2026-10-10, 2047 words at decimation
// 2^12, where a bank fills in exactly the seal period). The packetizer seals
// only while packet_ready holds.
// TRLC-LINKS: REQ-SDS-035
module stream_banks #(parameter BANK_AW=10)(
 input reset,producer_clk,host_clk,
 input packet_valid,input [63:0] packet_data,input packet_single,input seal,
 output packet_ready,
 output cur_ready,                    // the bank to fill next is free and empty (stream_drum)
 output reg mem_write=0,output reg [BANK_AW:0] mem_address=0,output reg [63:0] mem_data=0,
 output reg overrun=0,output host_overrun,
 input host_release,input release_bank,input release_token,
 output [1:0] host_ready,output [1:0] host_token,
 output [63:0] first_word0,first_word1,
 output [BANK_AW:0] packets0,packets1,
 output [1:0] last_single
);
 // ---- producer clock ------------------------------------------------------
 (* async_reg="true" *) reg [2:0] preset_s=3'b111;
 wire preset=preset_s[2];
 reg [1:0] pub=0;
 reg [1:0] rel=0; // host side
 (* async_reg="true" *) reg [1:0] rel_s0=0,rel_s1=0,rel_s2=0;
 reg cur=0;reg [BANK_AW:0] fill=0;reg [63:0] words=0,first_cur=0;reg single_cur=0;
 reg [63:0] first_m0=0,first_m1=0;reg [BANK_AW:0] count_m0=0,count_m1=0;reg [1:0] single_m=0;
 wire cur_free=pub[cur]==rel_s2[cur];
 assign packet_ready=pub[!cur]==rel_s2[!cur];
 assign cur_ready=cur_free && fill==0;
 wire [BANK_AW:0] next_fill=fill+1'b1;
 wire full=next_fill[BANK_AW];
 task publish(input [BANK_AW:0] count,input [63:0] first,input single);
  begin
   if(cur)begin first_m1<=first;count_m1<=count;end else begin first_m0<=first;count_m0<=count;end
   single_m[cur]<=single;pub[cur]<=!pub[cur];cur<=!cur;fill<=0;
  end
 endtask
 always @(posedge producer_clk)begin
  preset_s<={preset_s[1:0],reset};
  rel_s0<=rel;rel_s1<=rel_s0;rel_s2<=rel_s1;
  mem_write<=0;
  if(preset)begin
   pub<=0;cur<=0;fill<=0;words<=0;first_cur<=0;single_cur<=0;overrun<=0;single_m<=0;
  end else if(packet_valid)begin
   if(fill==0 && !cur_free)overrun<=1; // both banks owned by the host: drop, and say so
   else begin
    mem_write<=1;mem_address<={cur,fill[BANK_AW-1:0]};mem_data<=packet_data;
    words<=words+(packet_single ? 64'd1 : 64'd2);
    if(fill==0)first_cur<=words;
    single_cur<=packet_single;
    if(full || seal)publish(next_fill,fill==0 ? words : first_cur,packet_single);
    else fill<=next_fill;
   end
  end else if(seal && fill!=0)publish(fill,first_cur,single_cur);
 end
 // ---- host clock ----------------------------------------------------------
 (* async_reg="true" *) reg [2:0] hreset_s=3'b111;
 (* async_reg="true" *) reg [1:0] pub_s0=0,pub_s1=0,pub_s2=0;
 (* async_reg="true" *) reg [2:0] overrun_s=0;
 assign host_ready=pub_s2^rel;
 assign host_token=pub_s2;
 assign host_overrun=overrun_s[2];
 always @(posedge host_clk)begin
  hreset_s<={hreset_s[1:0],reset};
  pub_s0<=pub;pub_s1<=pub_s0;pub_s2<=pub_s1;overrun_s<={overrun_s[1:0],overrun};
  if(hreset_s[2])rel<=0;
  else if(host_release && host_ready[release_bank] && release_token==host_token[release_bank])rel[release_bank]<=!rel[release_bank];
 end
 assign first_word0=first_m0,first_word1=first_m1;
 assign packets0=count_m0,packets1=count_m1;
 assign last_single=single_m;
endmodule

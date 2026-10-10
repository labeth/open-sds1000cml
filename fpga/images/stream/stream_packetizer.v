// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// ADR-STREAM-IMAGE: carry the record's write words, as written, from the
// acquisition clock to the packet clock and pair them into 64-bit packets
// (first word low) for stream_banks.
//
// Words cross through an 8-word gray-coded FIFO: the decimators can deliver
// a few words clocks apart (the precision tail drains its own FIFO in bursts),
// which a one-word handshake turned into faults (bench 2026-10-10). A word that
// finds the FIFO full raises fault (sticky until reset), never a silent loss.
//
// A partially filled bank is sealed after SEAL_CYCLES packet clocks without a
// seal, so a slow stream still publishes about 60 times a second; an odd word
// pending at a seal goes out alone (packet_single). A stop seals the last
// packet and raises finished once everything before it has been carried. A
// seal waits for seal_ok (the banks can take it without overrunning).
// TRLC-LINKS: REQ-SDS-035
module stream_packetizer #(parameter SEAL_CYCLES=2097152, parameter STOP_SETTLE=15)(
 input reset,word_clk,packet_clk,word_valid,input [31:0] word_data,input stop,
 input seal_ok, // stream_banks.packet_ready: the other bank is free (packet clock)
 output reg fault=0,output reg finished=0,
 output reg packet_valid=0,output reg [63:0] packet_data=0,output reg packet_single=0,output reg packet_seal=0
);
 // ---- word (acquisition) clock -------------------------------------------
 localparam AW=3;
 (* preserve *) reg [31:0] mem[0:(1<<AW)-1];
 reg [AW:0] wbin=0,wgray=0;
 reg stop_req=0,word_fault=0;
 reg [AW:0] rgray=0; // packet side
 (* async_reg="true" *) reg [AW:0] rgray_s0=0,rgray_s1=0;
 wire full=wgray=={~rgray_s1[AW:AW-1],rgray_s1[AW-2:0]};
 wire [AW:0] wnext=wbin+1'b1;
 always @(posedge word_clk)begin
  rgray_s0<=rgray;rgray_s1<=rgray_s0;
  // Store on every word, free of the full compare (timing at 250 MHz): a word
  // that finds the FIFO full is a fault, so what it overwrites is lost anyway.
  if(word_valid)mem[wbin[AW-1:0]]<=word_data;
  if(reset)begin wbin<=0;wgray<=0;stop_req<=0;word_fault<=0;end
  else begin
   if(word_valid)begin
    if(full)word_fault<=1; // the packet side fell behind: say so
    else begin wbin<=wnext;wgray<=wnext^(wnext>>1);end
   end
   if(stop)stop_req<=!stop_req;
  end
 end
 // ---- packet clock --------------------------------------------------------
 (* async_reg="true" *) reg [2:0] reset_s=3'b111;
 (* async_reg="true" *) reg [AW:0] wgray_s0=0,wgray_s1=0;
 (* async_reg="true" *) reg [2:0] stop_s=0,fault_s=0;
 wire preset=reset_s[2];
 reg [AW:0] rbin=0;
 wire empty=rgray==wgray_s1;
 wire [AW:0] rnext=rbin+1'b1;
 reg stop_seen=0,stopping=0;reg [4:0] settle=0;
 reg have_low=0;reg [31:0] low=0;
 reg any=0; // a packet or a pending word since the last seal
 reg [31:0] since=0;
 wire [31:0] head=mem[rbin[AW-1:0]]; // written two or more clocks before wgray showed it
 always @(posedge packet_clk)begin
  reset_s<={reset_s[1:0],reset};
  wgray_s0<=wgray;wgray_s1<=wgray_s0;stop_s<={stop_s[1:0],stop_req};fault_s<={fault_s[1:0],word_fault};
  packet_valid<=0;packet_single<=0;packet_seal<=0;
  if(preset)begin
   rbin<=0;rgray<=0;stop_seen<=0;stopping<=0;settle<=0;have_low<=0;any<=0;since<=0;fault<=0;finished<=0;
  end else begin
   if(fault_s[2])fault<=1;
   since<=since+1'b1;
   if(!empty)begin
    rbin<=rnext;rgray<=rnext^(rnext>>1);any<=1;
    if(have_low)begin packet_valid<=1;packet_data<={head,low};have_low<=0;end
    else begin low<=head;have_low<=1;end
   // !packet_valid: a packet still in flight may yet fill and publish a bank,
   // which seal_ok (sampled before it) would not know.
   end else if(!finished && seal_ok && !packet_valid && (stopping ? settle==STOP_SETTLE && empty : (any && since>=SEAL_CYCLES)))begin
    // Seal (on timeout, or the final one after a stop): flush an odd word.
    if(have_low)begin packet_valid<=1;packet_data<={32'd0,low};packet_single<=1;have_low<=0;end
    packet_seal<=1;any<=0;since<=0;
    if(stopping)finished<=1;
   end
   // A stop may arrive just ahead of the last word's toggle through the
   // other synchronizer: let both settle before the final seal.
   if(stop_s[2]!=stop_seen)begin stop_seen<=stop_s[2];stopping<=1;settle<=0;end
   else if(stopping && settle!=STOP_SETTLE)settle<=settle+1'b1;
  end
 end
endmodule

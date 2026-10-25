// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-PRECISION
`timescale 1ns/1ps
// Decimation by picking for the fast image, where even the box averages
// leave its sample-domain UART's 125 MHz loop short of timing: every
// 2^(decim_log-1)th raw word's first sample per channel, written as the
// precision filter's words are ({C2, C1}, unsigned Q8.8, the fraction 0),
// so the record spans a slow timebase's screen and the receivers, which
// read the raw samples, trigger at any timebase. Register 30 reads 0x5053:
// no filter (what lies between the picked samples aliases). decim_log is
// the core-domain copy, static while running (4..20). The words are
// counted in two 10-bit halves, the high one stepping with the word that
// wraps the low one (known a clock ahead), and a word is picked where the
// count's bit decim_log-1 turns (a one-hot mask in two halves, a stage
// each): every 2^(decim_log-1) words, the first one the start's.
// TRLC-LINKS: REQ-SDS-040
module pick_decim(input core,packclk,clk100,enable,input [4:0] decim_log,input [31:0] raw,input raw_valid,
 output reg [31:0] data=0,output reg valid=0,output fault);
 assign fault=1'b0;
 (* preserve, dont_merge *) reg run=0;
 reg [19:0] mask=0;
 integer b;
 always @(posedge core)begin
  run<=enable;
  for(b=0;b<20;b=b+1)mask[b]<=decim_log==b+1;
 end
 // stage 1: the word; stage 2: its count's bit; stage 3: whether it turned
 reg [15:0] w1=0,w2=0;reg v1=0,v2=0;
 reg [9:0] count_lo=0,count_hi=0;reg lo_top=0;reg m_lo=0,m_hi=0;reg prev=1;
 always @(posedge core)begin
  w1<={raw[15:8],raw[7:0]};v1<=run && raw_valid;
  w2<=w1;v2<=v1;
  m_lo<=|(count_lo & mask[9:0]);m_hi<=|(count_hi & mask[19:10]);
  if(!run)begin count_lo<=0;count_hi<=0;lo_top<=0;end
  else if(v1)begin
   count_lo<=count_lo+1'b1;lo_top<=count_lo==10'h3fe;
   if(lo_top)count_hi<=count_hi+1'b1;
  end
  valid<=0;
  if(!run)prev<=1;
  else if(v2)begin
   prev<=m_lo|m_hi;
   if((m_lo|m_hi)!=prev)begin valid<=1;data<={w2[15:8],8'd0,w2[7:0],8'd0};end
  end
 end
endmodule

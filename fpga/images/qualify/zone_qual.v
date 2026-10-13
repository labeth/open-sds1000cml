// ENGMODEL-OWNER-UNIT: FU-RTL-QUAL-TRIGGER
`timescale 1ns/1ps
// The zone trigger, decided in the FPGA (ADR-HARDWARE-QUALIFIERS): up to four
// rectangles, each a window of time around the trigger edge and a range of
// codes on one channel. An edge qualifies when every intersect zone was
// entered inside its window and every avoid zone was not.
//
// Two clocks: the samples and the candidate edges arrive on the 250 MHz
// acquisition clock, where each zone's comparisons run; each pair of those
// clocks is handed to the 125 MHz half clock (in phase, half the rate), where
// the history lives: block RAM cannot run at 250 MHz (its minimum period is
// 4.2 ns). Time runs in buckets of bucket_mask+1 half clocks (a power of
// two); per bucket a zone records whether a sample of its channel fell inside
// its codes, as a running count of such buckets kept for 2^AW buckets, so the
// hits in a window before or after the edge are one difference of two
// counts. The decision is taken once the latest window has ended (wait
// buckets after the edge's) and fires a fixed time after the edge, one half
// clock wide: wait buckets plus DECIDE_CLOCKS half clocks after the half
// clock that took the candidate, so the host knows where the edge lies in
// the record. Candidate edges meanwhile are ignored. Windows are offsets in
// buckets from the edge's bucket (start za, end zb, signed); the host keeps
// every window's span, and the wait, inside the history.
// TRLC-LINKS: REQ-SDS-014
module zone_qual #(parameter AW=10)(
 input clk,                         // 250 MHz: samples and candidates
 input half,                        // 125 MHz, in phase with clk
 input enable,
 input valid,                       // the word holds samples
 input [31:0] word,                 // {c2 second, c1 second, c2 first, c1 first}
 input candidate,                   // a trigger edge to qualify
 input [3:0] zen,zch,zavoid,
 input [31:0] zlo,zhi,              // per zone 8-bit codes, zone z at [8z+7:8z]
 input [43:0] za,zb,                // per zone signed 11-bit bucket offsets
 input [10:0] wait_buckets,         // buckets from the edge's to the decision (>= 1)
 input [11:0] bucket_mask,          // a bucket is bucket_mask+1 half clocks
 output reg fire=0                  // half clock domain
);
 // ---- 250 MHz: in-zone per clock, pipelined (the zone's channel, the four
 // comparisons, the two samples), then paired for the half clock ----
 reg [7:0] s0[0:3],s1[0:3];reg [3:0] v0=0,v1=0,ge0=0,le0=0,ge1=0,le1=0,in_zone=0,in_prev=0;
 reg cand_now=0,cand_prev=0;
 // local copies of the zones' codes beside their comparators (timing)
 (* preserve, dont_merge *) reg [31:0] lo_q=0,hi_q=0;
 always @(posedge clk)begin lo_q<=zlo;hi_q<=zhi;end
 integer z;
 always @(posedge clk)begin
  for(z=0;z<4;z=z+1)begin
   s0[z]<=zch[z] ? word[15:8] : word[7:0];
   s1[z]<=zch[z] ? word[31:24] : word[23:16];
   v0[z]<=valid && zen[z];
   ge0[z]<=s0[z]>=lo_q[8*z+:8];le0[z]<=s0[z]<=hi_q[8*z+:8];
   ge1[z]<=s1[z]>=lo_q[8*z+:8];le1[z]<=s1[z]<=hi_q[8*z+:8];
   v1[z]<=v0[z];
   in_zone[z]<=v1[z] && ((ge0[z] && le0[z]) || (ge1[z] && le1[z]));
  end
  in_prev<=in_zone;
  // the candidate waits as long as the comparisons take
  cand_now<=candidate;cand_prev<=cand_now;
 end
 // the half clock reads the two 250 MHz clocks since its last edge
 wire [3:0] in_pair=in_zone|in_prev;
 wire cand_pair=cand_now|cand_prev;
 // ---- 125 MHz: buckets, their running counts, the history ----
 reg [11:0] btime=0;reg bucket_end=0;reg [AW-1:0] bucket=0;reg [3:0] hit=0,in_h=0;reg cand_h=0;
 reg [15:0] count[0:3];
 reg [AW:0] warm=0;                 // buckets since enable, up to a whole history
 reg [63:0] mem[0:(1<<AW)-1];
 // a read takes two clocks: the address, then the data registered (timing)
 reg [63:0] rdata=0,rdata_q=0;reg [AW-1:0] raddr=0;
 always @(posedge half)begin rdata<=mem[raddr];rdata_q<=rdata;end
 initial begin count[0]=0;count[1]=0;count[2]=0;count[3]=0;end
 localparam IDLE=0,WAITING=1,SELECT=2,ADDR_END=3,TAKE_END=4,ADDR_START=5,TAKE_START=6,DECIDE=7;
 reg [2:0] state=IDLE;reg [AW-1:0] edge_bucket=0,decide_at=0;reg [1:0] zi=0;reg [15:0] at_end=0;reg pass=1;reg [1:0] lag=0;
 reg [AW-1:0] a_sel=0,b_sel=0;reg on_sel=0,avoid_sel=0;
 // The configuration, held still by the host, registered on this clock
 // (the 250 MHz copies reach it through one register stage).
 reg [3:0] zen_h=0,zavoid_h=0;reg [43:0] za_h=0,zb_h=0;reg [AW-1:0] wait_h=1;reg [11:0] mask_h=0;
 always @(posedge half)begin zen_h<=zen;zavoid_h<=zavoid;za_h<=za;zb_h<=zb;wait_h<=wait_buckets[AW-1:0];mask_h<=bucket_mask;end
 // The fire's delay in half clocks: the wait in buckets (a bucket is a power
 // of two, so a shift) and the longest the zones' reads take after it.
 localparam integer DECIDE_CLOCKS=48;
 reg [22:0] wait_clocks=0,due_total=0,due=0;
 integer k;
 always @(posedge half)begin
  wait_clocks<=0;
  for(k=0;k<=12;k=k+1)if(mask_h==(12'hfff>>(12-k)))wait_clocks<={13'd0,wait_h}<<k;
  due_total<=wait_clocks+DECIDE_CLOCKS-1;
 end
 always @(posedge half)begin
  fire<=0;
  in_h<=in_pair;cand_h<=cand_pair;
  btime<=enable ? btime+1'b1 : 12'd0;
  bucket_end<=enable && (btime & mask_h)==mask_h;
  if(bucket_end)begin
   bucket<=bucket+1'b1;
   if(!warm[AW])warm<=warm+1'b1;
   mem[bucket]<={count[3]+(hit[3]|in_h[3]),count[2]+(hit[2]|in_h[2]),count[1]+(hit[1]|in_h[1]),count[0]+(hit[0]|in_h[0])};
   count[0]<=count[0]+(hit[0]|in_h[0]);count[1]<=count[1]+(hit[1]|in_h[1]);
   count[2]<=count[2]+(hit[2]|in_h[2]);count[3]<=count[3]+(hit[3]|in_h[3]);
   hit<=0;
  end else hit<=hit|in_h;
  case(state)
   IDLE:if(cand_h && warm[AW])begin edge_bucket<=bucket;decide_at<=bucket+wait_h;due<=due_total;state<=WAITING;end
   WAITING:if(bucket==decide_at)begin zi<=0;pass<=1;state<=SELECT;end
   // one zone's window, registered before its addresses are added (timing)
   SELECT:begin
     a_sel<=za_h[11*zi+:AW];b_sel<=zb_h[11*zi+:AW];on_sel<=zen_h[zi];avoid_sel<=zavoid_h[zi];state<=ADDR_END;
    end
   ADDR_END:if(!on_sel)begin
     if(zi==3)state<=DECIDE;else begin zi<=zi+1'b1;state<=SELECT;end
    end else begin raddr<=edge_bucket+b_sel;lag<=2;state<=TAKE_END;end
   TAKE_END:if(lag!=0)lag<=lag-1'b1;else begin at_end<=rdata_q[16*zi+:16];state<=ADDR_START;end
   ADDR_START:begin raddr<=edge_bucket+a_sel-1'b1;lag<=2;state<=TAKE_START;end
   TAKE_START:if(lag!=0)lag<=lag-1'b1;else begin
     // the window's hits: its end count less the count before its start
     if(avoid_sel ? at_end!=rdata_q[16*zi+:16] : at_end==rdata_q[16*zi+:16])pass<=0;
     if(zi==3)state<=DECIDE;else begin zi<=zi+1'b1;state<=SELECT;end
    end
   DECIDE:if(due==0)begin fire<=pass;state<=IDLE;end
  endcase
  if(state!=IDLE && due!=0)due<=due-1'b1;
  if(!enable)begin
   state<=IDLE;warm<=0;hit<=0;
  end
 end
endmodule

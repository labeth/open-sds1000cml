// ENGMODEL-OWNER-UNIT: FU-RTL-QUAL-TRIGGER
`timescale 1ns/1ps
// Hardware trigger qualifiers (ADR-HARDWARE-QUALIFIERS): the FPGA decides
// every trigger, software only places it on the screen. Each type fires one
// clock after the edge that completes its event, as an edge trigger fires on
// its edge:
//   PULSE  the time between a pulse's edges at the trigger level (a high
//          pulse rises then falls, a low pulse falls then rises), judged at
//          the closing edge.
//   SLOPE  the time from crossing the first level to crossing the second (a
//          rising slope crosses the low level A then the high level B, a
//          falling slope B then A); crossing the first level back aborts it.
//   VIDEO  line syncs at the trigger level (a negative sync falls into the
//          sync, a positive one rises): a broad pulse (vertical sync) starts
//          a field, line syncs after it are counted, and the sync that closes
//          line N of the field fires (every line sync when N is 0).
// Times are counted in ticks of P=prescale_m1+1 clocks (a power of two):
// min_m1 is the minimum less one (min_zero for a minimum of none), max_t the
// maximum; a width is "at least min" after min ticks and "over max" after
// max+1. An interval
// whose edges are W clocks apart counts floor((W-1)/P) ticks (the clocks
// after the opening edge's, before the closing edge's). Conditions: 0 any,
// 1 under the minimum, 2 over the maximum, 3 from minimum to maximum. Every
// trigger fires two clocks after its closing edge's.
// TRLC-LINKS: REQ-SDS-011
module qual_trigger #(parameter integer CLK_MHZ=250)(
 input clk,enable,
 input rise,fall,                       // trigger-level edges, the qualifier channel
 input rise_a,fall_a,rise_b,fall_b,     // slope level edges (A low, B high)
 input [1:0] kind,                      // 0 pulse, 1 slope, 2 video
 input negative,                        // low pulse / falling slope / negative sync
 input [1:0] cond,
 input min_zero,
 input [15:0] min_m1,max_t,
 input [15:0] prescale_m1,              // a power of two less one
 input [9:0] line,
 output fire
);
 localparam KIND_PULSE=0,KIND_SLOPE=1,KIND_VIDEO=2;
 // The configuration, decoded once into registers; the edges are registered
 // once, so every event comes one clock later and every time measured is
 // unchanged (timing).
 reg is_slope=0,is_video=0,p1=0,every_line=0;reg [15:0] pre_m1=0;reg [9:0] line_m1=0;
 always @(posedge clk)begin
  is_slope<=kind==KIND_SLOPE;is_video<=kind==KIND_VIDEO;
  p1<=prescale_m1==16'd0;pre_m1<=prescale_m1-16'd1;
  every_line<=line==10'd0;line_m1<=line-10'd1;
 end
 reg start=0,close=0,abort=0;
 always @(posedge clk)begin
  start<=!is_slope ? (negative ? fall : rise) : (negative ? fall_b : rise_a);
  close<=!is_slope ? (negative ? rise : fall) : (negative ? fall_a : rise_b);
  abort<=is_slope && (negative ? rise_b : fall_a);
 end
 // ---- pulse and slope: the measured interval ----
 // The clocks since the interval opened (modulo P) and its ticks count up
 // from zero; each limit's flag is set on the tick that takes the count to
 // it and stays (equality: no carry reaches a flag or a load).
 reg measuring=0,reached=0,over=0,fire_p=0;
 reg [15:0] pre=0;reg [15:0] ticks=0;
 // a start opens a new interval (after a close in the same clock)
 wire open=start && !(!measuring && close);
 // the next clock is a tick: the last of each P clocks after the opening's
 reg tick=0;
 // the condition on a width whose flags are r (at least min) and o (over max)
 function ok(input [1:0] c,input r,o);
  case(c)
   2'd0:ok=1'b1;
   2'd1:ok=!r;
   2'd2:ok=o;
   default:ok=r && !o;
  endcase
 endfunction
 always @(posedge clk)begin
  fire_p<=0;
  tick<=p1 || (!open && (pre & prescale_m1)==pre_m1);
  pre<=open ? 16'd0 : pre+16'd1;
  if(open)ticks<=16'd0;else if(tick)ticks<=ticks+16'd1;
  if(tick && ticks==min_m1)reached<=1;
  if(tick && ticks==max_t)over<=1;
  if(!is_video)begin
   if(measuring && close)begin
    fire_p<=ok(cond,reached,over);
    measuring<=0;
   end else if(!measuring && start && close)
    fire_p<=ok(cond,min_zero,1'b0);              // both edges in one clock: a width of none
   else if(measuring && abort)measuring<=0;
   if(open)begin measuring<=1;reached<=min_zero;over<=0;end
  end
  if(!enable)begin measuring<=0;fire_p<=0;end
 end
 // ---- video: sync widths in clocks ----
 // Shorter than a line sync (equalizing pulses, noise) is ignored. A negative
 // sync falls into the sync, a positive one rises: start and close.
 localparam integer H_MIN=CLK_MHZ*7/2;           // 3.5 us: line sync from here
 localparam integer H_MAX=CLK_MHZ*7;             // 7 us: longer is no line sync
 localparam integer BROAD_MIN=CLK_MHZ*15;        // 15 us: vertical sync (broad pulse)
 reg in_sync=0,h_w=0,h_over=0,broad_w=0,field=0,lines_full=0,at_line=0,fire_v=0;
 reg [15:0] vcount=0;reg [9:0] lines=0;
 always @(posedge clk)begin
  fire_v<=0;
  // the flags change only at sync ends, microseconds apart: registered
  lines_full<=lines==10'h3ff;
  at_line<=field && lines==line_m1;
  if(is_video)begin
   // the flags are set once at their widths and stay until the next sync
   if(in_sync)vcount<=vcount+16'd1;
   if(in_sync && vcount==H_MIN-1)h_w<=1;
   if(in_sync && vcount==H_MAX-1)h_over<=1;
   if(in_sync && vcount==BROAD_MIN-1)broad_w<=1;
   if(start && !in_sync)begin in_sync<=1;vcount<=0;h_w<=0;h_over<=0;broad_w<=0;end
   if(close && in_sync)begin
    in_sync<=0;
    if(broad_w)begin field<=1;lines<=0;end
    else if(h_w && !h_over)begin
     // a line sync: line N of the field closes here
     if(every_line || at_line)fire_v<=1;
     if(!lines_full)lines<=lines+10'd1;
    end else if(h_over)begin field<=0;lines<=0;end // neither line nor vertical sync: lost the field
   end
  end
  if(!enable)begin
   fire_v<=0;in_sync<=0;field<=0;lines<=0;
  end
 end
 assign fire=fire_p | fire_v;
endmodule

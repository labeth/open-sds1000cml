// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// qual_trigger: pulse widths under, over and between the limits (both
// polarities, each limit exactly, a glitch inside one clock, a prescaled
// count), slopes with an abort and a zero-time step, video line N after the
// vertical sync; every trigger two clocks after its closing edge.
// TRLC-LINKS: REQ-SDS-011
module tb;
 reg clk=0;always #2 clk=~clk;
 reg enable=0,rise=0,fall=0,rise_a=0,fall_a=0,rise_b=0,fall_b=0,negative=0,min_zero=0;
 reg [1:0] kind=0,cond=0;reg [15:0] min_m1=0,max_t=0;reg [15:0] prescale_m1=0;reg [9:0] line=0;
 wire fire;
 qual_trigger #(.CLK_MHZ(10)) dut(.clk(clk),.enable(enable),.rise(rise),.fall(fall),.rise_a(rise_a),.fall_a(fall_a),
  .rise_b(rise_b),.fall_b(fall_b),.kind(kind),.negative(negative),.cond(cond),.min_zero(min_zero),
  .min_m1(min_m1),.max_t(max_t),.prescale_m1(prescale_m1),.line(line),.fire(fire));
 integer fires=0,last_fire=0,now=0,last_edge=0;
 always @(posedge clk)begin now<=now+1;if(fire)begin fires<=fires+1;last_fire<=now;end if(rise|fall|rise_b|fall_a)last_edge<=now;end
 task idle(input integer n);begin repeat(n)begin @(negedge clk);rise=0;fall=0;rise_a=0;fall_a=0;rise_b=0;fall_b=0;end end endtask
 // one clock with the given edges
 task edges(input r,f,ra,fa,rb,fb);begin @(negedge clk);rise=r;fall=f;rise_a=ra;fall_a=fa;rise_b=rb;fall_b=fb;@(negedge clk);rise=0;fall=0;rise_a=0;fall_a=0;rise_b=0;fall_b=0;end endtask
 // a pulse whose closing edge comes w+1 clocks after its opening edge: w
 // clocks between them, w ticks at a prescale of one
 task pulse(input neg,input integer w);begin
  if(neg)edges(0,1,0,0,0,0);else edges(1,0,0,0,0,0);
  idle(w-1);
  if(neg)edges(1,0,0,0,0,0);else edges(0,1,0,0,0,0);
  idle(20);
 end endtask
 task check(input integer n,input [255:0] what);begin
  idle(3);
  if(fires!=n)$fatal(1,"%0s: %0d fires, expected %0d",what,fires,n);
  fires=0;
 end endtask
 integer i;
 initial begin
  idle(4);enable=1;
  // ---- pulse: widths 10, 50, 100, 200 clocks; min 40, max 150 ----
  kind=0;min_m1=39;max_t=150;prescale_m1=0;
  for(i=0;i<2;i=i+1)begin
   negative=i;
   cond=0;pulse(i,10);pulse(i,50);pulse(i,100);pulse(i,200);check(4,"pulse any");
   cond=1;pulse(i,10);pulse(i,50);pulse(i,100);pulse(i,200);check(1,"pulse under the minimum");
   cond=2;pulse(i,10);pulse(i,50);pulse(i,100);pulse(i,200);check(1,"pulse over the maximum");
   cond=3;pulse(i,10);pulse(i,50);pulse(i,100);pulse(i,200);check(2,"pulse between the limits");
  end
  // the limits themselves, one pulse at a time: 39 under the minimum, 40 at it, 150 not over the maximum, 151 over
  negative=0;cond=3;
  pulse(0,39);check(0,"39 under the minimum");pulse(0,40);check(1,"40 at the minimum");
  pulse(0,150);check(1,"150 at the maximum");pulse(0,151);check(0,"151 over the maximum");
  cond=1;pulse(0,39);check(1,"39 is under 40");pulse(0,40);check(0,"40 is not under 40");
  cond=2;pulse(0,150);check(0,"150 is not over 150");pulse(0,151);check(1,"151 is over 150");
  // a minimum of none: nothing is under it, every width at least it
  min_zero=1;cond=1;pulse(0,5);check(0,"nothing under a minimum of none");
  cond=3;pulse(0,5);check(1,"5 between a minimum of none and 150");min_zero=0;
  // the flags stay: 2^16 ticks past a limit (the count wrapped) is still past it
  cond=1;pulse(0,40+65536+10);check(0,"a long pulse stays at least the minimum");
  max_t=2;cond=2;pulse(0,3+65536+10);check(1,"a long pulse stays over the maximum");max_t=150;
  // the trigger: two clocks after the closing edge
  cond=0;pulse(0,10);if(last_fire-last_edge!=2)$fatal(1,"pulse fired %0d clocks after its closing edge",last_fire-last_edge);
  check(1,"pulse latency");
  // a glitch: both edges in one clock is a width of none
  cond=1;edges(1,1,0,0,0,0);idle(20);check(1,"glitch under the minimum");
  cond=2;edges(1,1,0,0,0,0);idle(20);check(0,"glitch not over the maximum");
  // a closing edge without an opening one does nothing
  cond=0;edges(0,1,0,0,0,0);idle(20);check(0,"lone closing edge");
  // prescaled: a tick every 4 clocks, minimum 10 ticks (40 clocks)
  prescale_m1=3;min_m1=9;max_t=1000;cond=1;pulse(0,30);pulse(0,50);check(1,"prescaled minimum");
  // at every phase of the clock the interval opens on
  for(i=0;i<4;i=i+1)begin
   idle(i);pulse(0,39);check(1,"39 clocks are 9 ticks of 4");idle(i);pulse(0,40);check(0,"40 clocks are 10 ticks of 4");
  end
  prescale_m1=0;
  // ---- slope: rising crosses A then B; falling B then A ----
  kind=1;min_m1=19;max_t=60;
  negative=0;cond=3;
  edges(0,0,1,0,0,0);idle(29);edges(0,0,0,0,1,0);idle(20);check(1,"rising slope of 30 clocks inside");
  edges(0,0,1,0,0,0);idle(9);edges(0,0,0,0,1,0);idle(20);check(0,"rising slope of 10 clocks under");
  edges(0,0,1,0,0,0);idle(18);edges(0,0,0,0,1,0);idle(20);check(0,"rising slope of 19 clocks under");
  edges(0,0,1,0,0,0);idle(19);edges(0,0,0,0,1,0);idle(20);check(1,"rising slope of 20 clocks at the minimum");
  edges(0,0,1,0,0,0);idle(9);edges(0,0,0,1,0,0);idle(19);edges(0,0,0,0,1,0);idle(20);check(0,"rising slope aborted below A");
  cond=1;edges(0,0,1,0,1,0);idle(20);check(1,"a step through both levels in one clock");
  negative=1;cond=2;
  edges(0,0,0,0,0,1);idle(79);edges(0,0,0,1,0,0);idle(20);check(1,"falling slope of 80 clocks over");
  edges(0,0,0,0,0,1);idle(29);edges(0,0,0,1,0,0);idle(20);check(0,"falling slope of 30 clocks not over");
  // ---- video at 10 MHz: H sync 4.7 us (47), equalizing 2.3 us (23), broad 27 us (270) ----
  kind=2;negative=1;line=5;
  for(i=0;i<6;i=i+1)begin pulse(1,47);idle(560);end         // lines before any vertical sync: no field yet
  check(0,"no field before a vertical sync");
  for(i=0;i<6;i=i+1)begin pulse(1,270);idle(30);end         // vertical sync: broad pulses
  for(i=0;i<6;i=i+1)begin pulse(1,23);idle(290);end         // equalizing pulses are not lines
  for(i=0;i<4;i=i+1)begin pulse(1,47);idle(560);end
  check(0,"four lines after the vertical sync");
  pulse(1,47);
  if(last_fire-last_edge!=2)$fatal(1,"line 5 fired %0d clocks after its sync's end",last_fire-last_edge);
  check(1,"line 5 fires at its sync's end");
  for(i=0;i<10;i=i+1)begin pulse(1,47);idle(560);end
  check(0,"later lines of the field");
  pulse(1,100);idle(500);                                   // neither a line nor a vertical sync
  for(i=0;i<6;i=i+1)begin pulse(1,47);idle(560);end
  check(0,"the field was lost: no line 5 until the next vertical sync");
  line=0;for(i=0;i<3;i=i+1)begin pulse(1,47);idle(560);end
  check(3,"every line");
  negative=0;for(i=0;i<2;i=i+1)begin pulse(0,47);idle(560);end
  check(2,"positive sync");
  // ---- disable clears the state ----
  kind=0;negative=0;cond=0;edges(1,0,0,0,0,0);idle(5);enable=0;idle(3);enable=1;edges(0,1,0,0,0,0);idle(10);
  check(0,"an interval opened before a disable");
  $display("PASS qual_trigger: pulse widths both polarities and limits, glitch, prescale; slopes with abort and step; video line N after the vertical sync");
  $finish;
 end
endmodule

// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// zone_qual: an intersect zone before the edge entered or missed, the same
// zone as an avoid zone, a window after the edge (the decision waits for it),
// two zones that must both pass, the other channel, the warm-up and a
// two-half-clock bucket. A bucket is a half clock (two 250 MHz clocks). The
// fire comes 2*(wait*bucket+48)+5 or +6 250 MHz clocks after the edge (the
// edge's phase against the half clock), whatever the zones.
// TRLC-LINKS: REQ-SDS-014
module tb;
 // the half clock rises with every other 250 MHz clock, as from the PLL
 reg clk=0,half=0;always #2 clk=~clk;initial begin #2 half=1;forever #4 half=~half;end
 reg enable=0,valid=1,candidate=0;reg [31:0] word={8'd100,8'd100,8'd100,8'd100};
 reg [3:0] zen=0,zch=0,zavoid=0;reg [31:0] zlo=0,zhi=0;reg [43:0] za=0,zb=0;reg [10:0] wait_buckets=1;reg [11:0] bucket_mask=0;
 wire fire;
 zone_qual #(.AW(6)) dut(.clk(clk),.half(half),.enable(enable),.valid(valid),.word(word),.candidate(candidate),.zen(zen),.zch(zch),
  .zavoid(zavoid),.zlo(zlo),.zhi(zhi),.za(za),.zb(zb),.wait_buckets(wait_buckets),.bucket_mask(bucket_mask),.fire(fire));
 integer fires=0,now=0,cand_at=0,fire_at=0;
 always @(posedge clk)begin now<=now+1;if(candidate)cand_at<=now;end
 always @(posedge half)if(fire)begin fires<=fires+1;fire_at<=now;end
 task idle(input integer n);begin repeat(n)@(negedge clk);end endtask
 // a blip on channel ch (0 C1, 1 C2) for one 250 MHz clock
 task blip(input ch);begin @(negedge clk);word=ch ? {8'd200,8'd100,8'd200,8'd100} : {8'd100,8'd200,8'd100,8'd200};@(negedge clk);word={8'd100,8'd100,8'd100,8'd100};end endtask
 task edge_now;begin @(negedge clk);candidate=1;@(negedge clk);candidate=0;end endtask
 task check(input integer n,input [255:0] what);begin idle(300);if(fires!=n)$fatal(1,"%0s: %0d fires, expected %0d",what,fires,n);fires=0;end endtask
 // the last fire's delay after its edge, for d half clocks of wait and decision
 task delay(input integer d,input [255:0] what);begin
  if(fire_at-cand_at!=2*d+5 && fire_at-cand_at!=2*d+6)$fatal(1,"%0s: fired %0d clocks after the edge, expected %0d or %0d",what,fire_at-cand_at,2*d+5,2*d+6);
 end endtask
 // zone z: channel, codes, window [a, b] buckets, avoid
 task zone(input integer z,input ch,input [7:0] lo,hi,input integer a,b,input av);begin
  zen[z]=1;zch[z]=ch;zlo[8*z+:8]=lo;zhi[8*z+:8]=hi;za[11*z+:11]=a;zb[11*z+:11]=b;zavoid[z]=av;
 end endtask
 initial begin
  idle(4);enable=1;
  zone(0,0,180,220,-20,-8,0);wait_buckets=1;
  edge_now;check(0,"warming up: the history is not full yet");
  // the blip 26 clocks (13 buckets) before the edge: inside [-20, -8]
  blip(0);idle(25);edge_now;check(1,"intersect zone entered before the edge");
  delay(1+48,"wait 1 bucket 1");
  blip(0);idle(3);edge_now;check(0,"the blip 2 buckets before the edge: outside the window");
  edge_now;check(0,"no blip at all");
  zavoid[0]=1;
  blip(0);idle(25);edge_now;check(0,"avoid zone entered");
  edge_now;check(1,"avoid zone missed");
  // a window after the edge, 8..16 buckets (16..32 clocks): the decision waits for it
  zone(0,0,180,220,8,16,0);wait_buckets=17;idle(8);
  edge_now;idle(22);blip(0);check(1,"intersect zone entered after the edge");delay(17+48,"wait 17");
  idle(1);edge_now;idle(22);blip(0);check(1,"intersect zone entered after the edge, other phase");delay(17+48,"wait 17, other phase");
  edge_now;idle(60);blip(0);check(0,"the blip after the window");
  // two zones must both pass: C1 entered before, C2 avoided 4..12 buckets after
  zone(0,0,180,220,-20,-8,0);zone(1,1,180,220,4,12,1);wait_buckets=13;idle(8);
  blip(0);idle(25);edge_now;check(1,"both zones pass");delay(13+48,"two zones");
  blip(0);idle(25);edge_now;idle(12);blip(1);check(0,"the avoid zone on C2 entered");
  zen=0;zone(0,1,180,220,-20,-8,0);wait_buckets=1;
  blip(0);idle(25);edge_now;check(0,"a C1 blip in a C2 zone");
  blip(1);idle(25);edge_now;check(1,"a C2 blip in a C2 zone");
  // two half clocks a bucket: the window [-10, -4] buckets is 40..16 clocks back
  bucket_mask=1;zone(0,0,180,220,-10,-4,0);enable=0;idle(4);enable=1;idle(400);
  blip(0);idle(27);edge_now;check(1,"two-half-clock buckets: entered");
  delay(2+48,"wait 1 of two half clocks");
  blip(0);idle(3);edge_now;check(0,"two-half-clock buckets: too recent");
  $display("PASS zone_qual: intersect and avoid before and after the edge, two zones, channels, warm-up, bucket size, a fixed delay");
  $finish;
 end
endmodule

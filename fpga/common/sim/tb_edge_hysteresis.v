// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-039, REQ-SDS-042
module tb;
 reg clk=0;always #2 clk=~clk;
 reg enable=0,valid=0;reg [7:0] first=0,second=0,level=128,low_level=124,high_level=132;
 wire rise,fall;sram_edge_pair dut(.*);
 task pair(input [7:0] a,b,input r,f);
 begin
  @(negedge clk);first=a;second=b;valid=1;
  @(negedge clk);valid=0;
  @(posedge clk);#1;
  if(rise!==r || fall!==f)$fatal(1,"pair %d,%d rise/fall=%b%b expected=%b%b",a,b,rise,fall,r,f);
 end endtask
 initial begin
  repeat(2)@(negedge clk);enable=1;
  pair(120,123,0,0);pair(126,128,1,0); // genuine rising crossing
  pair(130,129,0,0);pair(127,128,0,0); // tiny downward/upward chatter
  pair(126,129,0,0);pair(127,130,0,0);
  pair(134,133,0,0);pair(129,128,0,1); // genuine falling crossing
  pair(129,127,0,0);pair(130,128,0,0); // rising-side chatter cannot re-arm falling
  pair(122,128,1,0); // arm and fire within a word
  pair(134,128,0,1);
  @(negedge clk);enable=0;repeat(2)@(negedge clk);enable=1;
  pair(127,129,0,0); // no previous armed state survives a new capture
  @(negedge clk);enable=0;low_level=114;high_level=142;
  repeat(2)@(negedge clk);enable=1;
  pair(100,130,1,0);pair(138,140,0,0);
  pair(125,130,0,0);pair(119,129,0,0); // larger converter-offset ripple
  pair(150,128,0,1);pair(139,127,0,0);pair(114,129,1,0);
  @(negedge clk);enable=0;level=0;low_level=0;high_level=14;
  repeat(2)@(negedge clk);enable=1;
  pair(0,0,0,0);pair(255,255,0,0);pair(0,0,0,1);
  @(negedge clk);enable=0;level=255;low_level=241;high_level=255;
  repeat(2)@(negedge clk);enable=1;
  pair(0,255,1,0);pair(255,0,0,0);
  $display("PASS hysteresis: both slopes, tiny reversals, within-word crossings and capture reset");$finish;
 end
endmodule

// TRLC-LINKS: REQ-SDS-039, REQ-SDS-042
module tb_edge_pipeline;
 reg clk=0;always #2 clk=~clk;
 reg enable=0,valid=0,valid_d=0;reg [7:0] first=0,second=0,first_d=0,second_d=0;
 reg [7:0] level=128,low_level=124,high_level=132;
 wire rise,fall,ref_rise,ref_fall;
 sram_edge_pair #(.PIPELINED(1)) dut(.clk(clk),.enable(enable),.valid(valid),.first(first),.second(second),.level(level),.low_level(low_level),.high_level(high_level),.rise(rise),.fall(fall));
 sram_edge_pair reference_pair(.clk(clk),.enable(enable),.valid(valid_d),.first(first_d),.second(second_d),.level(level),.low_level(low_level),.high_level(high_level),.rise(ref_rise),.fall(ref_fall));
 always @(posedge clk)begin first_d<=first;second_d<=second;valid_d<=enable && valid;end
 integer a,b,epoch,i,checked=0;reg [6:0] pieces;reg [31:0] rng=32'h12345678;
 always @(negedge clk)begin
  if(rise!==ref_rise || fall!==ref_fall)$fatal(1,"pipeline changed trigger cycle");
  checked=checked+1;
 end
 initial begin
  for(a=0;a<256;a=a+1)for(b=0;b<256;b=b+1)begin
   pieces=dut.compare_parts(a,b);
   if((pieces[6] || (pieces[5] && (pieces[4] || (pieces[3] && (pieces[2] || (pieces[1] && pieces[0]))))))!==(a>=b))$fatal(1,"threshold comparator %d %d",a,b);
  end
  for(epoch=0;epoch<32;epoch=epoch+1)begin
   @(negedge clk);enable=0;valid=0;level=epoch*8;low_level=level>4?level-4:0;high_level=level<251?level+4:255;
   repeat(4)@(negedge clk);enable=1;
   for(i=0;i<1024;i=i+1)begin
    @(negedge clk);rng=rng^(rng<<13);rng=rng^(rng>>17);rng=rng^(rng<<5);
    first=rng[7:0];second=rng[15:8];valid=i%11<8;
   end
  end
  @(negedge clk);enable=0;valid=0;repeat(4)@(negedge clk);
  $display("PASS threshold pipeline: exhaustive comparison and %0d aligned cycles with bubbles/rearm",checked);$finish;
 end
endmodule

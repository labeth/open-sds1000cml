// ENGMODEL-OWNER-UNIT: FU-RTL-PROBE
// tb_probe.v -- the census image over the GPMC: identity, a gate's ever-0 and
// ever-1 flags, a channel's edge and high counts on a ball clocked at 10.6 MHz,
// the encode legs held low until turned on, the panel scan of a modelled key
// chain, the key interrupt pulse and the other balls held at the images' levels.
`timescale 1ns/1ps
`define BALLS 140
`define PULL 1'b1

// The encode PLL, behaviourally: five 100 MHz phases, locked after a while.
// TRLC-LINKS: REQ-SDS-288
module adc_phase_pll(input refclk,output [4:0] phase,output reg locked=0);
 assign phase={5{refclk}};
 initial #500 locked=1;
endmodule

// TRLC-LINKS: REQ-SDS-288
module tb;
 reg clk=0;always #10 clk=~clk;       // 50 MHz
 reg mclk=0;always #5 mclk=~mclk;     // 100 MHz
 reg fast=0;always #47 fast=~fast;    // 10.6 MHz, 50% high
 // The balls the probe can drive (112..122) are left to it and the panel model.
 reg [`BALLS-2:0] bv=0;
 wire [`BALLS-8:0] ball;
 genvar g;
 generate for(g=0;g<`BALLS-7;g=g+1)begin:bd
  if(g<112 || g>122)begin:tb_driven assign ball[g]=bv[g];end
 end endgenerate
 // The key chain: J2 high loads it, each rising edge of F2 shifts it, J1 its bit 0.
 reg [63:0] keys=64'h0123456789abcdef,shreg=0;
 always @(posedge ball[116] or posedge ball[115])if(ball[115])shreg<=keys;else shreg<={1'b1,shreg[63:1]};
 assign ball[117]=shreg[0];
 wire nCS1,nOE,nWE;wire [6:0] sel;wire [15:0] d;wire [4:0] ep,en;
 gpmc_bfm host(.nCS1(nCS1),.nOE(nOE),.nWE(nWE),.sel(sel),.d(d));
 probe_top dut(.clk(clk),.mclk_in(mclk),.nCS1(nCS1),.nOE(nOE),.nWE(nWE),.sel(sel[6:2]),.gpmc_a2(sel[1]),.gpmc_b1(sel[0]),
  .gpmc_d(d),.ball(ball),.clkin(bv[`BALLS-2:`BALLS-7]),.enc_p(ep),.enc_n(en));
 // Ball 0 a 10.6 MHz clock, ball 1 held high, ball 2 held low, ball 3 toggled once.
 always @* bv[0]=fast;
 reg [15:0] v,lo,hi;
 localparam [7:0] NB=`BALLS;
 integer edges,highs,k;
 initial begin
  bv[1]=1;bv[2]=0;bv[3]=0;
  #200;
  host.read(7'h12,v);if(v!==16'h5052)$fatal(1,"identity %h",v);
  host.read(7'h13,v);if(v!=={NB,8'h01})$fatal(1,"balls/pull %h",v);
  #600;
  host.read(7'h14,v);if(v[1]!==1'b1)$fatal(1,"pll not locked %h",v);
  if(ep!==5'b0 || en!==5'b0)$fatal(1,"a leg runs while off");
  // A 20 us gate; channel 0 on ball 0, channel 1 on the reference (the last ball).
  host.write(7'h16,16'd1000);host.write(7'h17,16'd0);
  host.write(7'h40,16'd0);host.write(7'h41,`BALLS-1);
  host.write(7'h15,16'd1);
  #3000;bv[3]=1;
  k=0;v=1;
  while(v[0] && k<100)begin #1000;host.read(7'h14,v);k=k+1;end
  if(v[0])$fatal(1,"gate never ended");
  host.read(7'h20,lo);host.read(7'h21,hi);edges={hi[7:0],lo};
  if(edges<210 || edges>215)$fatal(1,"10.6 MHz over 20 us: %0d edges",edges);
  host.read(7'h30,lo);host.read(7'h31,hi);highs={hi[7:0],lo};
  if(highs<480 || highs>520)$fatal(1,"50%% high over 1000 clocks: %0d",highs);
  host.read(7'h22,lo);host.read(7'h23,hi);edges={hi[7:0],lo};
  if(edges<1990 || edges>2010)$fatal(1,"100 MHz reference over 20 us: %0d edges",edges);
  host.read(7'h50,v);
  if(v[3:0]!==4'b1101)$fatal(1,"ever 0 %b",v[3:0]);
  host.read(7'h60,v);
  if(v[3:0]!==4'b1011)$fatal(1,"ever 1 %b",v[3:0]);
  host.read(7'h70,v);
  if(v[3:1]!==3'b101)$fatal(1,"live %b",v[3:1]);
  // A second gate clears what the first saw: ball 3 now only ever 1.
  host.write(7'h15,16'd1);
  k=0;v=1;#200;
  while(v[0] && k<100)begin #1000;host.read(7'h14,v);k=k+1;end
  host.read(7'h50,v);
  if(v[3]!==1'b0)$fatal(1,"ever 0 not cleared");
  // Leg 4 (E3 enc_p) on: only that ball runs.
  host.write(7'h18,16'h0010);
  #100;
  k=0;repeat(20)begin #1;if(ep[2])k=k+1;end
  if(k==0)$fatal(1,"leg 4 does not run");
  if(ep[1:0]!==2'b0 || ep[4:3]!==2'b0 || en!==5'b0)$fatal(1,"another leg runs");
  // The held codes: pair 0's lanes (balls 0..15, ball 0 the 10.6 MHz clock,
  // 1 high, 2 low, 3 high) copied at one instant.
  host.write(7'h05,16'h0001);#200;
  host.read(7'h00,v);
  if(v[3:1]!==3'b101)$fatal(1,"held code %b",v[3:0]);
  // The edge capture, triggered by ball 0 (the 10.6 MHz clock): 512 words of
  // pair E1's lanes, ball 0 their bit 0, rising across the middle.
  host.write(7'h06,16'h0001);
  k=0;v=0;
  while(!v[0] && k<200)begin #1000;host.read(7'h06,v);k=k+1;end
  if(!v[0])$fatal(1,"edge capture never done");
  edges=0;
  for(k=250;k<262;k=k+1)begin host.write(7'h07,k);host.read(7'h08,v);edges=edges+v[0];end
  if(edges<2 || edges>10)$fatal(1,"edge capture around the trigger: %0d of 12 samples high",edges);
  host.write(7'h07,16'd0);host.read(7'h08,v);
  host.write(7'h07,16'd511);host.read(7'h08,lo);
  if(v[1]!==1'b1 || lo[1]!==1'b1 || v[2]!==1'b0)$fatal(1,"edge capture's held lanes %b %b",v[2:1],lo[2:1]);
  // The other balls held at the images' levels, then released.
  if(ball[119]!==1'bz || ball[122]!==1'bz)$fatal(1,"D1/A11 driven before asked");
  host.write(7'h19,16'h0001);#100;
  if(ball[119]!==1'b0 || ball[120]!==1'b0 || ball[121]!==1'b0 || ball[122]!==1'b1)$fatal(1,"D1 D2 G2 A11 %b",ball[122:119]);
  // The panel: driven, scanned once, the chain's 64 bits read back.
  host.write(7'h19,16'h0002);#100;
  if(ball[118]!==1'b1 || ball[115]!==1'b0)$fatal(1,"panel lines idle %b %b",ball[118],ball[115]);
  host.write(7'h19,16'h0006);
  k=0;v=4;
  while(v[2] && k<400)begin #1000;host.read(7'h14,v);k=k+1;end
  if(v[2])$fatal(1,"panel scan never ended");
  host.read(7'h0c,lo);if(lo!==16'hcdef)$fatal(1,"chain bits 0..15 %h",lo);
  host.read(7'h0f,lo);if(lo!==16'h0123)$fatal(1,"chain bits 48..63 %h",lo);
  // The key interrupt: F1 low 0.4 ms, then high again.
  host.write(7'h19,16'h000a);#2000;
  if(ball[118]!==1'b0)$fatal(1,"F1 not pulsed low");
  #420000;
  if(ball[118]!==1'b1)$fatal(1,"F1 stays low");
  host.write(7'h19,16'h0000);#100;
  if(ball[118]!==1'bz || ball[115]!==1'bz)$fatal(1,"panel lines not released");
  $display("PASS probe census: flags, counts, legs, held codes, edge capture, panel scan, key interrupt, held balls");$finish;
 end
endmodule

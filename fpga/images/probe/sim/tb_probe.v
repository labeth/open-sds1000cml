// ENGMODEL-OWNER-UNIT: FU-RTL-PROBE
// tb_probe.v -- the census image over the GPMC: identity, a gate's ever-0 and
// ever-1 flags, a channel's edge and high counts on a ball clocked at 10.6 MHz,
// and the encode legs held low until turned on.
`timescale 1ns/1ps
`define BALLS 20
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
 reg [`BALLS-2:0] ball=0;
 wire nCS1,nOE,nWE;wire [6:0] sel;wire [15:0] d;wire [4:0] ep,en;
 gpmc_bfm host(.nCS1(nCS1),.nOE(nOE),.nWE(nWE),.sel(sel),.d(d));
 probe_top dut(.clk(clk),.mclk_in(mclk),.nCS1(nCS1),.nOE(nOE),.nWE(nWE),.sel(sel[6:2]),.gpmc_a2(sel[1]),.gpmc_b1(sel[0]),
  .gpmc_d(d),.ball(ball),.enc_p(ep),.enc_n(en));
 // Ball 0 a 10.6 MHz clock, ball 1 held high, ball 2 held low, ball 3 toggled once.
 always @* ball[0]=fast;
 reg [15:0] v,lo,hi;
 localparam [7:0] NB=`BALLS;
 integer edges,highs,k;
 initial begin
  ball[1]=1;ball[2]=0;ball[3]=0;
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
  #3000;ball[3]=1;
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
  $display("PASS probe census: reference %0d edges in 20 us, flags, counts and legs",edges);$finish;
 end
endmodule

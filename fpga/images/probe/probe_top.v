// ENGMODEL-OWNER-UNIT: FU-RTL-PROBE
// probe_top.v -- the diagnostics stick's census image: what another population
// of the board has on each FPGA ball. It drives the ARM bus (only during a
// read, as every image) and the ten encode legs (each off until the host turns
// it on); every other ball is an input (balls.txt), with the weak pull-up on in
// the PULL build. A ball that reads the same with and without the pull-up is
// driven; one that follows it is open.
//
// A gate of gate_len clocks of clk (50 MHz) records, for every ball, whether
// it was ever 0 and ever 1 (sampled on clk), and for each of eight channels,
// pointed at any ball, the ball's rising edges (counted on the ball itself, so
// a clock of any rate counts) and the clocks it was high. With a leg on, the
// eight lanes of the converter core it clocks move; with it off they hold.
//
// Registers (CS1, 16 bits):
//   0x12 R   identity 0x5052
//   0x13 R   [15:8] balls, [0] the pull-up build
//   0x14 R   [0] a gate is running, [1] the encode PLL is locked
//   0x15 W   [0] start a gate (clears the flags and counters first)
//   0x16 RW  gate length in clk cycles, low 16 bits; 0x17 the high 8
//   0x18 RW  encode legs: bit 2p the enc_p leg of pair E(p+1), bit 2p+1 its enc_n leg
//   0x20+2c  channel c's edge count, low 16 bits; 0x21+2c the high 8
//   0x30+2c  channel c's high clocks, low 16 bits; 0x31+2c the high 8
//   0x40+c   RW channel c's ball index
//   0x50+w   the balls 16w..16w+15 ever 0; 0x60+w ever 1; 0x70+w the live level
`timescale 1ns/1ps

// TRLC-LINKS: REQ-SDS-288
module probe_top(
 input clk,mclk_in,nCS1,nOE,nWE,input [6:2] sel,input gpmc_a2,gpmc_b1,
 inout [15:0] gpmc_d,input [`BALLS-2:0] ball,output [4:0] enc_p,enc_n
);
 localparam N=`BALLS;
 localparam CH=8;
 wire [N-1:0] in={mclk_in,ball};

 wire wc,rp,drive;wire [7:0] ws,rs;wire [15:0] wd;wire [1:0] aux;reg [15:0] rd;
 gpmc_slave busif(clk,nCS1,nOE,nWE,{sel,gpmc_a2,gpmc_b1},gpmc_d,wc,ws,wd,aux,rp,rs,rd,drive);

 // The gate: eight clocks of clearing, then gate_len clocks of counting.
 reg [23:0] gate_len=24'd50000;
 reg [23:0] left=0;
 reg [3:0] clearing=0;
 reg running=0;
 // A register, not a decode of clearing: it resets the channels asynchronously.
 reg clear=0;
 reg [9:0] legs=0;
 reg [7:0] chsel [0:CH-1];
 integer i;
 initial for(i=0;i<CH;i=i+1)chsel[i]=i;
 always @(posedge clk)begin
  if(wc)case(ws)
   8'h16:gate_len[15:0]<=wd;
   8'h17:gate_len[23:16]<=wd[7:0];
   8'h18:legs<=wd[9:0];
   default:if(ws[7:3]==5'b01000)chsel[ws[2:0]]<=wd[7:0];
  endcase
  if(wc && ws==8'h15 && wd[0])begin clearing<=4'd8;clear<=1;running<=0;end
  else if(clearing!=0)begin
   clearing<=clearing-1'b1;
   if(clearing==1)begin clear<=0;running<=1;left<=gate_len;end
  end else if(running)begin
   if(left==0)running<=0;else left<=left-1'b1;
  end
 end

 // Every ball's level, ever 0 and ever 1 over the gate.
 reg [N-1:0] s1=0,s2=0,ever0=0,ever1=0;
 always @(posedge clk)begin
  s1<=in;s2<=s1;
  if(clear)begin ever0<=0;ever1<=0;end
  else if(running)begin ever0<=ever0|~s2;ever1<=ever1|s2;end
 end

 // The channels: rising edges on the ball's own clock, high time on clk.
 wire [23:0] edges [0:CH-1];
 wire [23:0] highs [0:CH-1];
 genvar c;
 generate for(c=0;c<CH;c=c+1)begin:chan
  wire m=chsel[c]<N ? in[chsel[c]] : 1'b0;
  (* async_reg="true" *) reg r1=0,r2=0;
  reg [23:0] e=0;
  always @(posedge m or posedge clear)
   if(clear)begin r1<=0;r2<=0;e<=0;end
   else begin r1<=running;r2<=r1;if(r2)e<=e+1'b1;end
  reg h1=0,h2=0;
  reg [23:0] h=0;
  always @(posedge clk)begin
   h1<=m;h2<=h1;
   if(clear)h<=0;else if(running && h2)h<=h+1'b1;
  end
  assign edges[c]=e;
  assign highs[c]=h;
 end endgenerate

 // The encode legs, as the acquisition images drive them (adc_interleave),
 // each held low until the host turns it on.
 wire [4:0] phase;wire locked;
 adc_phase_pll clocks(mclk_in,phase,locked);
 genvar p;
 generate for(p=0;p<5;p=p+1)begin:pair
  localparam POS=(p==0 || p==1 || p==3);
  ddio_out1 ep(.outclock(phase[p]),.dh(POS && locked && legs[2*p]),.dl(!POS && locked && legs[2*p]),.pad(enc_p[p]));
  ddio_out1 en(.outclock(phase[p]),.dh(!POS && locked && legs[2*p+1]),.dl(POS && locked && legs[2*p+1]),.pad(enc_n[p]));
 end endgenerate

 localparam W=(N+15)/16;
 wire [16*W-1:0] e0={{(16*W-N){1'b0}},ever0};
 wire [16*W-1:0] e1={{(16*W-N){1'b0}},ever1};
 wire [16*W-1:0] lv={{(16*W-N){1'b0}},s2};
 always @* begin
  rd=16'h0000;
  case(rs)
   8'h12:rd=16'h5052;
   8'h13:rd={N[7:0],7'b0,`PULL};
   8'h14:rd={14'b0,locked,running|clear};
   8'h16:rd=gate_len[15:0];
   8'h17:rd={8'b0,gate_len[23:16]};
   8'h18:rd={6'b0,legs};
   default:
    if(rs[7:4]==4'h2)rd=rs[0] ? {8'b0,edges[rs[3:1]][23:16]} : edges[rs[3:1]][15:0];
    else if(rs[7:4]==4'h3)rd=rs[0] ? {8'b0,highs[rs[3:1]][23:16]} : highs[rs[3:1]][15:0];
    else if(rs[7:3]==5'b01000)rd={8'b0,chsel[rs[2:0]]};
    else if(rs[7:4]==4'h5 && rs[3:0]<W)rd=e0[16*rs[3:0]+:16];
    else if(rs[7:4]==4'h6 && rs[3:0]<W)rd=e1[16*rs[3:0]+:16];
    else if(rs[7:4]==4'h7 && rs[3:0]<W)rd=lv[16*rs[3:0]+:16];
  endcase
 end
endmodule

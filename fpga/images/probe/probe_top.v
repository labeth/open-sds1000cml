// ENGMODEL-OWNER-UNIT: FU-RTL-PROBE
// probe_top.v -- the diagnostics stick's census image: what another population
// of the board has on each FPGA ball. It drives the ARM bus (only during a
// read, as every image) and the ten encode legs (each off until the host turns
// it on); every other ball is an input (balls.txt), with the weak pull-up on in
// the PULL build, unless the host asks for one of the tests below. A ball that
// reads the same with and without the pull-up is driven; one that follows it
// is open.
//
// A gate of gate_len clocks of clk (50 MHz) records, for every ball, whether
// it was ever 0 and ever 1 (sampled on clk), and for each of eight channels,
// pointed at any ball, the ball's rising edges (counted on the ball itself, so
// a clock of any rate counts) and the clocks it was high. With a leg on, the
// eight lanes of the converter core it clocks move; with it off they hold.
//
// The tests drive what the acquisition images drive (spec 12): the SRAM test
// fills every word through the address counter, a word a K2 pulse at 25 MHz
// ({~i[12:0], i[18:0]} for word i), drives the data bus with 0, which breaks
// that pattern (a bus that only holds its last level then reads wrong), and reads
// every word back, counting words that break the pattern or the count; the
// panel scan clocks the 64-bit key chain once; the key interrupt pulse holds
// F1 low 0.4 ms; and D1, D2, G2 and A11 can be held at the images' levels.
//
// The edge capture records pair E1's two cores (C1's E1.CH1 and C2's
// E1.CH2) at their 100 MS/s for 512 samples, 256 of them before the rising
// edge of a chosen ball (a channel's trigger comparator): the trigger is
// asynchronous to the sample clock, so captures of a square's edge sample it
// at random phases, and the share of samples within the edge is its rise time.
//
// A converter that its leg stops does not hold its code (its lanes read 1),
// so codes are read running: each pair's 16 lanes are registered on its
// capture clock (the inverse of its encode phase, as the acquisition images
// register them) and copied to a hold register when the host asks; the host
// reads all ten cores' codes from one instant.
//
// Registers (CS1, 16 bits):
//   0x00-0x04 R  the held codes: register k holds core 2k in its low byte and core
//            2k+1 in its high byte, in balls.txt's order (E1.CH1, E1.CH2, E2.CH1, ...)
//   0x05 W   [0] hold every core's code now
//   0x06 W   [0] arm the edge capture, [15:8] the ball that triggers it; R [0] done
//   0x07 W   the edge capture's read address (0..511, oldest first)
//   0x08 R   the edge capture's word at that address: E1.CH1 low byte, E1.CH2 high
//   0x0c-0x0f R  the panel chain's 64 bits, bit 0 first (0x0c bits 0..15)
//   0x12 R   identity 0x5052
//   0x13 R   [15:8] balls, [0] the pull-up build
//   0x14 R   [0] a gate is running, [1] the encode PLL is locked, [2] a panel scan
//            is running, [3] the SRAM test is running, [4] the SRAM test is done
//   0x15 W   [0] start a gate (clears the flags and counters first)
//   0x16 RW  gate length in clk cycles, low 16 bits; 0x17 the high 8
//   0x18 RW  encode legs: bit 2p the enc_p leg of pair E(p+1), bit 2p+1 its enc_n leg
//   0x19 RW  [0] hold D1, D2, G2 low and A11 high; [1] drive the panel's load and
//            clock low and F1 high; W [2] scan the panel chain once, [3] pulse F1 low
//   0x1a W   [0] run the SRAM test
//   0x1b,0x1c R the first word the SRAM test checked, low and high 16 bits
//   0x20+2c  channel c's edge count, low 16 bits; 0x21+2c the high 8
//   0x30+2c  channel c's high clocks, low 16 bits; 0x31+2c the high 8
//   0x40+c   RW channel c's ball index
//   0x48,0x49 R SRAM words whose high 13 bits are not their low 13 inverted (low 16, high 8)
//   0x4a,0x4b R SRAM words that are not the word before plus one (low 16, high 8)
//   0x4c,0x4d R SRAM data bits ever wrong (low 16, high 16)
//   0x4e,0x4f R SRAM words checked (low 16, high 8)
//   0x50+w   the balls 16w..16w+15 ever 0; 0x60+w ever 1; 0x70+w the live level
`timescale 1ns/1ps
// build.ts defines both from balls.txt and the build; these are balls.txt's
// count and the build without the pull-ups.
`ifndef BALLS
`define BALLS 140
`endif
`ifndef PULL
`define PULL 1'b0
`endif

// TRLC-LINKS: REQ-SDS-288
module probe_top(
 input clk,mclk_in,nCS1,nOE,nWE,input [6:2] sel,input gpmc_a2,gpmc_b1,
 inout [15:0] gpmc_d,inout [`BALLS-8:0] ball,input [5:0] clkin,output [4:0] enc_p,enc_n
);
 localparam N=`BALLS;
 localparam CH=8;
 localparam PULLED=`PULL;
 // The dedicated clock inputs (balls.txt group clkin, before M2) take no
 // output: they are a port of their own.
 wire [N-1:0] in={mclk_in,clkin,ball};
 // The balls the tests drive, by their index in balls.txt (build.ts checks
 // the names at these indexes).
 localparam DQ0=80,G1=112,K1=113,K2=114,J2=115,F2=116,J1=117,F1=118,D1=119,D2=120,G2=121,A11=122;
 reg [N-8:0] oe=0,out=0;
 genvar b;
 generate for(b=0;b<N-7;b=b+1)begin:drv
  assign ball[b]=oe[b] ? out[b] : 1'bz;
 end endgenerate

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
 reg [1:0] ctl=0;
 reg [7:0] chsel [0:CH-1];
 integer i;
 initial for(i=0;i<CH;i=i+1)chsel[i]=i;
 always @(posedge clk)begin
  if(wc)case(ws)
   8'h16:gate_len[15:0]<=wd;
   8'h17:gate_len[23:16]<=wd[7:0];
   8'h18:legs<=wd[9:0];
   8'h19:ctl<=wd[1:0];
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

 // The tests' drivers: the panel and the other balls as the host asks, the
 // SRAM's while its test runs.
 wire sram_on;wire [31:0] sram_dq;wire sram_dq_oe,sram_g1,sram_k1,sram_k2;
 wire panel_j2,panel_f2;reg f1_low=0;
 always @* begin
  oe=0;out=0;
  if(ctl[0])begin
   oe[D1]=1;oe[D2]=1;oe[G2]=1;oe[A11]=1;out[A11]=1;
  end
  if(ctl[1] || f1_low)begin
   oe[J2]=1;oe[F2]=1;oe[F1]=1;out[J2]=panel_j2;out[F2]=panel_f2;out[F1]=!f1_low;
  end
  if(sram_on)begin
   oe[G1]=1;oe[K1]=1;oe[K2]=1;out[G1]=sram_g1;out[K1]=sram_k1;out[K2]=sram_k2;
   oe[DQ0+:32]={32{sram_dq_oe}};out[DQ0+:32]=sram_dq;
  end
 end

 // The key interrupt pulse: F1 low for 0.4 ms (20000 clocks).
 reg [14:0] f1_left=0;
 always @(posedge clk)begin
  if(wc && ws==8'h19 && wd[3])begin f1_low<=1;f1_left<=15'd20000;end
  else if(f1_low)begin if(f1_left==0)f1_low<=0;else f1_left<=f1_left-1'b1;end
 end

 // The panel scan (spec 12 §7): load high two half periods, then 64 bits,
 // J1 sampled before each rising edge of the shift clock (half period 83 clocks).
 reg scanning=0,sj2=0,sf2=0;reg [6:0] hp=0;reg [7:0] half=0;reg [63:0] chain=0;reg [1:0] j1s=0;
 assign panel_j2=sj2;assign panel_f2=sf2;
 always @(posedge clk)begin
  j1s<={j1s[0],in[J1]};
  if(!scanning)begin
   if(wc && ws==8'h19 && wd[2])begin scanning<=1;sj2<=1;sf2<=0;hp<=0;half<=0;end
  end else if(hp!=82)hp<=hp+1'b1;
  else begin
   hp<=0;half<=half+1'b1;
   if(half==1)sj2<=0;
   if(half>=2)begin
    if(!sf2)chain<={j1s[1],chain[63:1]};
    sf2<=!sf2;
    if(half==2+127)begin scanning<=0;sf2<=0;end
   end
  end
 end

 wire [23:0] rel_err,seq_err,checked;wire [31:0] bad_bits,first_word;wire sram_done;
 sram_test sram(.clk(clk),.start(wc && ws==8'h1a && wd[0]),.dq_in(in[DQ0+:32]),.on(sram_on),.dq(sram_dq),.dq_oe(sram_dq_oe),
  .g1(sram_g1),.k1(sram_k1),.k2(sram_k2),.done(sram_done),.rel_err(rel_err),.seq_err(seq_err),.bad_bits(bad_bits),.checked(checked),.first(first_word));

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

 // The held codes: a toggle from the host, synchronised into each pair's
 // capture clock, copies the pair's registered lanes.
 reg snap_tog=0;
 always @(posedge clk)if(wc && ws==8'h05 && wd[0])snap_tog<=!snap_tog;
 wire [79:0] held;
 generate for(p=0;p<5;p=p+1)begin:hold
  wire cap=~phase[p];
  reg [15:0] q=0,h=0;
  (* async_reg="true" *) reg [2:0] t=0;
  always @(posedge cap)begin
   q<=in[16*p+:16];t<={t[1:0],snap_tog};
   if(t[2]!=t[1])h<=q;
  end
  assign held[16*p+:16]=h;
 end endgenerate

 // The edge capture, on pair E1's capture clock.
 reg cap_tog=0;reg [7:0] cap_ball=0;reg [8:0] cap_raddr=0;
 always @(posedge clk)begin
  if(wc && ws==8'h06 && wd[0])begin cap_tog<=!cap_tog;cap_ball<=wd[15:8];end
  if(wc && ws==8'h07)cap_raddr<=wd[8:0];
 end
 wire cap0=~phase[0];
 wire trig_in=cap_ball<N ? in[cap_ball] : 1'b0;
 (* async_reg="true" *) reg [2:0] ct=0;
 (* async_reg="true" *) reg [2:0] tr=0;
 reg [15:0] cmem [0:511];
 reg [8:0] wa=0,left_n=0;reg [8:0] fill=0;reg arming=0,triggered=0,cdone=0;
 reg [15:0] q0=0;
 always @(posedge cap0)begin
  q0<=in[15:0];
  ct<={ct[1:0],cap_tog};tr<={tr[1:0],trig_in};
  if(ct[2]!=ct[1])begin arming<=1;triggered<=0;cdone<=0;fill<=0;end
  else if(arming)begin
   cmem[wa]<=q0;wa<=wa+1'b1;
   if(fill!=9'd256)fill<=fill+1'b1;
   if(!triggered)begin
    if(fill==9'd256 && tr[1] && !tr[2])begin triggered<=1;left_n<=9'd254;end
   end else if(left_n==0)begin arming<=0;cdone<=1;end
   else left_n<=left_n-1'b1;
  end
 end
 (* async_reg="true" *) reg [1:0] cd=0;
 reg [15:0] cword=0;
 always @(posedge clk)begin
  cd<={cd[0],cdone};
  cword<=cmem[cap_raddr+wa];
 end

 localparam W=(N+15)/16;
 wire [16*W-1:0] e0={{(16*W-N){1'b0}},ever0};
 wire [16*W-1:0] e1={{(16*W-N){1'b0}},ever1};
 wire [16*W-1:0] lv={{(16*W-N){1'b0}},s2};
 always @* begin
  rd=16'h0000;
  case(rs)
   8'h12:rd=16'h5052;
   8'h13:rd={N[7:0],7'b0,PULLED};
   8'h00:rd=held[15:0];
   8'h01:rd=held[31:16];
   8'h02:rd=held[47:32];
   8'h03:rd=held[63:48];
   8'h04:rd=held[79:64];
   8'h06:rd={15'b0,cd[1]};
   8'h07:rd={7'b0,cap_raddr};
   8'h08:rd=cword;
   8'h0c:rd=chain[15:0];
   8'h0d:rd=chain[31:16];
   8'h0e:rd=chain[47:32];
   8'h0f:rd=chain[63:48];
   8'h14:rd={11'b0,sram_done,sram_on,scanning,locked,running|clear};
   8'h19:rd={14'b0,ctl};
   8'h1b:rd=first_word[15:0];
   8'h1c:rd=first_word[31:16];
   8'h48:rd=rel_err[15:0];
   8'h49:rd={8'b0,rel_err[23:16]};
   8'h4a:rd=seq_err[15:0];
   8'h4b:rd={8'b0,seq_err[23:16]};
   8'h4c:rd=bad_bits[15:0];
   8'h4d:rd=bad_bits[31:16];
   8'h4e:rd=checked[15:0];
   8'h4f:rd={8'b0,checked[23:16]};
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

// The SRAM test: fill every word, then read every word back (spec 12 §5.2,
// at a pulse every two clocks). The address counter counts from 0 when G1
// rises; written, word i lands at i+2 or i+3; read, a word arrives two pulses
// late and the first 16 of a burst are unreliable, so the test checks the
// words read after the first 20 against the pattern and against the word
// before them, which needs no address bookkeeping.
// TRLC-LINKS: REQ-SDS-288
module sram_test #(parameter AW=19)(
 input clk,start,input [31:0] dq_in,
 output reg on=0,output reg [31:0] dq=0,output reg dq_oe=0,output reg g1=0,output reg k1=0,output reg k2=0,
 output reg done=0,output reg [23:0] rel_err=0,seq_err=0,checked=0,output reg [31:0] bad_bits=0,first=0
);
 localparam [19:0] WORDS=20'd1<<AW,SKIP=20'd20,EXTRA=20'd16;
 localparam [18:0] MASK=(19'd1<<AW)-19'd1;
 localparam IDLE=0,RST1=1,WSET=2,WRITE=3,WREL=4,KEEP=5,RREL=6,RST2=7,RSET=8,READ=9,FIN=10;
 reg [3:0] st=IDLE;reg [7:0] wait_n=0;reg [19:0] n=0;reg ph=0;
 reg [31:0] cap=0,prev=0;reg capv=0,have=0;
 wire [18:0] word=n[18:0];
 wire [18:0] next=(prev[18:0]+19'd1)&MASK;
 always @(posedge clk)begin
  capv<=0;
  case(st)
   IDLE:if(start)begin on<=1;done<=0;g1<=0;k1<=0;k2<=0;dq_oe<=0;wait_n<=64;st<=RST1;
    rel_err<=0;seq_err<=0;checked<=0;bad_bits<=0;have<=0;end
   RST1:if(wait_n!=0)wait_n<=wait_n-1'b1;else begin g1<=1;k1<=0;wait_n<=32;st<=WSET;end
   WSET:if(wait_n!=0)wait_n<=wait_n-1'b1;else begin n<=0;ph<=0;dq_oe<=1;st<=WRITE;end
   WRITE:begin
    ph<=!ph;
    if(!ph)begin k2<=0;dq<={~word[12:0],word};end
    else begin k2<=1;n<=n+1'b1;if(n==WORDS-1)begin wait_n<=8;st<=WREL;end end
   end
   WREL:begin k2<=0;if(wait_n!=0)wait_n<=wait_n-1'b1;else begin dq<=0;wait_n<=4;st<=KEEP;end end
   KEEP:if(wait_n!=0)wait_n<=wait_n-1'b1;else begin dq_oe<=0;wait_n<=32;st<=RREL;end
   RREL:if(wait_n!=0)wait_n<=wait_n-1'b1;else begin k1<=1;g1<=0;wait_n<=64;st<=RST2;end
   RST2:if(wait_n!=0)wait_n<=wait_n-1'b1;else begin g1<=1;wait_n<=32;st<=RSET;end
   RSET:if(wait_n!=0)wait_n<=wait_n-1'b1;else begin n<=0;ph<=0;st<=READ;end
   READ:begin
    ph<=!ph;
    if(!ph)begin k2<=0;if(n>SKIP)begin cap<=dq_in;capv<=1;end end
    else begin k2<=1;n<=n+1'b1;if(n==WORDS+EXTRA-1)st<=FIN;end
   end
   FIN:begin k2<=0;k1<=0;g1<=0;on<=0;done<=1;st<=IDLE;end
   default:st<=IDLE;
  endcase
  if(capv)begin
   checked<=checked+1'b1;
   if(!have)first<=cap;
   if(cap[31:19]!=~cap[12:0])rel_err<=rel_err+1'b1;
   if(have && cap[18:0]!=next)seq_err<=seq_err+1'b1;
   if(have)bad_bits<=bad_bits|(cap^{~next[12:0],next});
   prev<=cap;have<=1;
  end
 end
endmodule

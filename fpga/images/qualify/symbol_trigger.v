// ENGMODEL-OWNER-UNIT: FU-RTL-QUAL-TRIGGER
`timescale 1ns/1ps
// ADR-SYMBOL-TRIGGER: one programmable receiver for the serial buses that are
// timed pulses on one or two lines (pulse-width codes, Manchester and
// biphase-mark line codes, UART characters after a break or an idle gap,
// clocked two-wire buses). Everything runs on the 125 MHz receiver clock from
// two sliced lines; the host sets it up while acquisition is idle and holds it
// while armed.
//
// Runs: the run line (line A, or A and B for Wiegand's two idle-high lines)
// is cut into runs at its edges. A run of W clocks counts floor((W-1)/2^p)
// prescaled ticks (saturating at 2^21-1); with its level it addresses a
// host-written table on a log scale: index {level, e, m}, e=0 and m the
// count under 64, else e the count's top bit less five and m the six bits
// below it (a step of 1/64 or finer). Each entry holds two codes:
//   leader code [7:4]: 0 none, 1 separator, 2+s step s of the leader (0..3)
//   data code   [3:0]: 0 end, 1 separator, 2 bit 0, 3 bit 1, 4 restart
// Leader: the frame opens once lead_len runs have matched steps 0..lead_len-1
// in order (separators may come between; a step-0 run restarts it; any other
// run abandons it). With lead_len 0 the receiver is always in a frame. In a
// frame each run's data code acts: end closes the frame (with lead_len 0 it
// restarts the count), restart is a step-0 run, and in CLASS mode bit 0/1
// are the frame's bits.
//
// Bits (mode): CLASS the runs' data codes; LEVEL / EDGE a unit sampler,
// re-timed by every edge to half a unit (unit_q8, 1/256 clocks) and then
// sampling every unit, that takes the run line's level or whether an edge
// came since the last sample; with frame_units F it stops after F samples
// until the next edge (UART characters), and a frame's opening restarts it;
// CLOCKED line B (data) at the run line's rising (or falling) edges.
// Sampler and clocked bits wait for the table's verdict on an edge of the
// same clock (five clocks), so that a bit taken on or after the edge that
// opened the frame counts in it.
//
// Match: the last 64 bits under mask equal pattern, tested at bit k after
// the frame opened (period 0) or at bit k and every period bits after it
// (phase0 = -k mod period); a frame start fires itself when k is 0.
// match is one clock, eight clocks after the edge or sample that
// completed it.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
module symbol_trigger(
 input clk,reset,
 input a,b,                     // sliced lines (A the run line / clock, B data)
 input inv_a,inv_b,and_ab,clk_fall,
 input [1:0] mode,              // 0 CLASS, 1 LEVEL, 2 EDGE, 3 CLOCKED
 input [2:0] lead_len,          // 0..4
 input [2:0] prescale,          // a tick is 2^prescale clocks
 input [27:0] unit_q8,          // at least 2048 (eight clocks)
 input [5:0] frame_units,period,phase0,
 input [9:0] k,
 input [63:0] pattern,mask,
 input wr_clk,wr_en,input [10:0] wr_addr,input [7:0] wr_data,
 output reg match=0
);
 localparam CLASS=0,LEVEL=1,EDGE=2,CLOCKED=3;
 // ---- table (block RAM, host-written) ----
 (* ramstyle="M9K" *) reg [7:0] table_mem[0:2047];
 integer ti;
 initial for(ti=0;ti<2048;ti=ti+1)table_mem[ti]=0;
 always @(posedge wr_clk)if(wr_en)table_mem[wr_addr]<=wr_data;
 // ---- configuration, decoded once (held while armed) ----
 reg [6:0] pmask=0;reg [27:0] half=0,unit_m256=0;reg [2:0] ll=0;reg [1:0] md=0;
 reg [5:0] fr=0,per=0,ph0=0;reg [9:0] kk=0;
 always @(posedge clk)begin
  pmask<=(7'd1<<prescale)-7'd1;half<={1'b0,unit_q8[27:1]};unit_m256<=unit_q8-28'd256;ll<=lead_len;md<=mode;
  fr<=frame_units;per<=period;ph0<=phase0;kk<=k;
 end
 // ---- lines ----
 reg r0=1,d0=1,rp=1,primed=0;
 wire la=a^inv_a,lb=b^inv_b;
 always @(posedge clk)begin r0<=and_ab ? (la && lb) : la;d0<=lb;end
 wire edge_now=primed && r0!=rp;
 // ---- run lengths ----
 reg [6:0] pc=0;reg [20:0] cnt=0;
 always @(posedge clk)begin
  if(reset)begin primed<=0;pc<=0;cnt<=0;rp<=r0;end
  else begin
   primed<=1;rp<=r0;
   if(edge_now)begin pc<=0;cnt<=0;end
   else begin
    pc<=pc+1'b1;
    if((pc&pmask)==pmask && cnt!=21'h1fffff)cnt<=cnt+1'b1;
   end
  end
 end
 // stage 1: the run that just ended; 2: its top bit; 3: the table index; 4: its codes
 reg v1=0,v2=0,v3=0,v4=0;reg l1=0,l2=0;reg [20:0] n1=0,n2=0;reg [3:0] e2=0,s2=0;reg [10:0] addr3=0;reg [7:0] q4=0;
 integer bi;reg [4:0] top;wire [20:0] mant=n2>>s2;
 always @*begin
  top=0;
  for(bi=6;bi<21;bi=bi+1)if(n1[bi])top=bi;
 end
 always @(posedge clk)begin
  v1<=!reset && edge_now;n1<=cnt;l1<=rp;
  v2<=!reset && v1;n2<=n1;l2<=l1;e2<=n1[20:6]==0 ? 4'd0 : top-5'd5;s2<=n1[20:6]==0 ? 4'd0 : top-5'd6;
  v3<=!reset && v2;addr3<={l2,e2,mant[5:0]};
  v4<=!reset && v3;q4<=table_mem[addr3];
 end
 // ---- unit sampler and clocked bits ----
 reg [27:0] rr=0;reg act=0,ef=0;reg [5:0] fu=0;
 reg sb_v=0,sb=0;           // a bit taken this clock
 wire restart_units;        // a frame opened (below): the sampler starts a character
 wire rr_due=rr[27:8]==0;
 always @(posedge clk)begin
  sb_v<=0;
  if(reset)begin rr<=0;act<=0;ef<=0;fu<=0;end
  else if(md==CLOCKED)begin
   sb_v<=edge_now && (clk_fall ? !r0 : r0);sb<=d0;
  end else begin
   if(edge_now)begin
    rr<=half;ef<=1;
    if(!act)begin act<=1;fu<=0;end
   end else if(act)begin
    if(rr_due)begin
     sb_v<=md!=CLASS;sb<=md==EDGE ? ef : r0;ef<=0;
     rr<=rr+unit_m256;fu<=fu+1'b1;
     if(fr!=0 && fu+6'd1==fr)act<=0;
    end else rr<=rr-28'd256;
   end
   if(restart_units)begin fu<=0;act<=1;end
  end
 end
 // the sampled bits wait for the table's verdict on the same clock's edge
 localparam CLASS_LATENCY=4;
 reg [CLASS_LATENCY-1:0] dv=0,dbit=0;
 always @(posedge clk)begin
  dv<={dv[CLASS_LATENCY-2:0],sb_v && !reset};dbit<={dbit[CLASS_LATENCY-2:0],sb};
 end
 wire bv=dv[CLASS_LATENCY-1],bbit=dbit[CLASS_LATENCY-1];
 // stage 5: the codes decoded
 reg v5=0,d_end=0,d_bit=0,d_val=0,d_restart=0,l_sep=0;reg [3:0] l_step=0;
 always @(posedge clk)begin
  v5<=!reset && v4;
  d_end<=q4[3:0]==4'd0;d_bit<=q4[3:0]==4'd2 || q4[3:0]==4'd3;d_val<=q4[0];d_restart<=q4[3:0]==4'd4;
  l_sep<=q4[7:4]==4'd1;
  l_step<={q4[7:4]==4'd5,q4[7:4]==4'd4,q4[7:4]==4'd3,q4[7:4]==4'd2};
 end
 // ---- frame state ----
 reg in_frame=0;reg [2:0] got=0;reg [9:0] n=0;reg [5:0] ph=0;reg [63:0] sh=0;
 // the run's verdict first, then this clock's bit
 reg f_in,f_open,f_bit_v,f_bit;reg [2:0] f_got;
 always @*begin
  f_in=in_frame;f_got=got;f_open=0;f_bit_v=0;f_bit=0;
  if(v5)begin
   if(in_frame)begin
    if(d_end)begin
     if(ll==0)f_open=1;else begin f_in=0;f_got=0;end
    end else if(d_bit)begin
     if(md==CLASS)begin f_bit_v=1;f_bit=d_val;end
    end else if(d_restart)begin
     if(ll<=1)f_open=1;else begin f_in=0;f_got=1;end
    end
   end else begin
    if(got<ll && l_step[got[1:0]])begin
     f_got=got+1'b1;if(got+1'b1==ll)f_open=1;
    end else if(l_step[0])begin
     f_got=1;if(ll==1)f_open=1;
    end else if(!l_sep)f_got=0;
   end
  end
  if(f_open)begin f_in=1;f_got=0;end
  if(md!=CLASS && bv)begin f_bit_v=f_in;f_bit=bbit;end
 end
 assign restart_units=v5 && f_open;
 wire [9:0] n_base=f_open ? 10'd0 : n;
 wire [5:0] ph_base=f_open ? ph0 : ph;
 wire [9:0] n_next=n_base==10'h3ff ? n_base : n_base+1'b1;
 wire [5:0] ph_next=ph_base+6'd1==per ? 6'd0 : ph_base+6'd1;
 reg hit_a=0,open_a=0;
 always @(posedge clk)begin
  hit_a<=0;open_a<=0;
  if(reset)begin in_frame<=ll==0;got<=0;n<=0;ph<=ph0;sh<=0;end
  else begin
   in_frame<=f_in;got<=f_got;
   if(f_open)begin n<=0;ph<=ph0;end
   open_a<=f_open && kk==0;
   if(f_bit_v)begin
    sh<={sh[62:0],f_bit};n<=n_next;ph<=per==0 ? 6'd0 : ph_next;hit_a<=1;
   end
  end
 end
 // ---- the count's test and the window's compare, in two clocks ----
 reg [15:0] part=0;reg hit_b=0,open_b=0;
 integer pi;
 always @(posedge clk)begin
  for(pi=0;pi<16;pi=pi+1)part[pi]<=((sh[4*pi+:4]^pattern[4*pi+:4])&mask[4*pi+:4])==4'd0;
  hit_b<=hit_a && !reset && (per==0 ? n==kk : (n>=kk && ph==0));open_b<=open_a && !reset;
  match<=!reset && ((hit_b && &part) || open_b);
 end
endmodule

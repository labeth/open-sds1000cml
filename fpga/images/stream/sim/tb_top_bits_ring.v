// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// Self-checking: the bits image's top level in ring mode (ADR-STREAM-RING),
// driven over GPMC as sramcapture does it (start, two stops, the record read
// back span by span as recallFrozen reads it), with modelled SRAM. The app's
// Go test (engine, TestStreamRingTopRTL) gives the image's defines (the
// decimation RING_LOG_P, the stream's STREAM_LOG_P) and checks its own
// mapping of each edge against the one printed here. CH1 is a
// square wave the digital stream slices; CH2 a slow ramp, so a ring word's
// CH2 value tells the time it was made. Checks: every word of the frozen
// ring the host reads (its origin, start and length) holds the ramp at the
// ring's pace - no stale words; and each CH1 edge the digital stream shows,
// mapped through the stamp as the app maps it (soft_ring.go), lands on the
// ring word made at that time, within two ring words (the decimator's delay
// is modelled as its CIC3 stages' alone).
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
module bench_pll(input refclk,output reg c0=0,output c1,output locked,output reg halfclk=0);
 always #2 c0=~c0;initial begin #2;forever #4 halfclk=~halfclk;end assign #1 c1=c0;assign locked=1;
endmodule
// Raw words: {CH2,CH1,CH2,CH1} codes, the earlier pair lowest; word n holds
// samples 2n and 2n+1. CH1: a square wave of HALF samples a level; CH2:
// the ramp sample>>RAMP_SHIFT.
// TRLC-LINKS: REQ-SDS-035
module adc_interleave(input refclk,memclk,packclk,enable,input [79:0] lane,input [9:0] encode_enable,
 input snapshot_request,output snapshot_ack,input consume,output [31:0] word_data,output valid,fault,output [79:0] snapshot,output [4:0] enc_p,enc_n,output locked);
 parameter HALF=`ifdef HALF_P `HALF_P `else 3000 `endif ,RAMP_SHIFT=6;
 reg [31:0] n=0;
 wire [31:0] s0=2*n,s1=2*n+1;
 wire [7:0] a0=(s0/HALF)%2 ? 8'd200 : 8'd40,a1=(s1/HALF)%2 ? 8'd200 : 8'd40;
 wire [7:0] b0=s0>>RAMP_SHIFT,b1=s1>>RAMP_SHIFT;
 assign word_data={b1,a1,b0,a0};
 assign valid=enable;
 always @(posedge memclk)if(enable && consume)n<=n+1;
 assign snapshot_ack=snapshot_request;assign fault=0;assign snapshot=0;assign enc_p=0;assign enc_n=0;assign locked=1;
endmodule
// TRLC-LINKS: REQ-SDS-035, REQ-SDS-240
module tb_top_bits_ring;
 localparam RING_LOG=`ifdef RING_LOG_P `RING_LOG_P `else 8 `endif ,STREAM_LOG=`ifdef STREAM_LOG_P `STREAM_LOG_P `else 9 `endif ,HALF=`ifdef HALF_P `HALF_P `else 3000 `endif ,RAMP_SHIFT=6;
 localparam RUN=`ifdef RUN_P `RUN_P `else 300000 `endif ;
 reg clk=0,mclk_in=0;always #6.25 clk=~clk;always #5 mclk_in=~mclk_in;
 reg ncs=1,noe=1,nwe=1;reg [6:0] selector=0;reg drive=0;reg [15:0] bus_value=0;
 wire [15:0] gpmc_d;assign gpmc_d=drive ? bus_value : 16'hzzzz;
 wire [31:0] dq;wire k1,k2,g1;
 acq_sram_top dut(.clk(clk),.mclk_in(mclk_in),.nCS1(ncs),.nOE(noe),.nWE(nwe),.sel(selector[6:2]),
  .gpmc_a2(selector[1]),.gpmc_b1(selector[0]),.gpmc_d(gpmc_d),.dq(dq),.lane(80'b0),.k1(k1),.k2(k2),.g1(g1));
 reg [31:0] mem[0:524287];reg [18:0] address=0;reg [31:0] s1=0,s2=0;
 assign dq=k1&&g1?s2:32'bz;
 always @(posedge k2) if(g1)begin
  if(!k1)mem[address+2'd2]<=dq;else begin s1<=mem[address];s2<=#4.5 s1;end
  address<=address+1'b1;
 end
 task write_bus(input [6:0] a,input [15:0] v);
 begin
  @(negedge clk);selector=a;bus_value=v;drive=1;ncs=0;nwe=0;
  repeat(8)@(negedge clk);nwe=1;repeat(5)@(negedge clk);ncs=1;drive=0;repeat(6)@(negedge clk);
 end endtask
 reg [15:0] value;
 task read_bus(input [6:0] a);
 begin
  @(negedge clk);selector=a;ncs=0;noe=0;repeat(8)@(negedge clk);value=gpmc_d;
  noe=1;ncs=1;repeat(6)@(negedge clk);
 end endtask
 task host_command(input [3:0] op);
 integer cycles;
 begin
  write_bus(1,op);cycles=0;read_bus(1);
  while(value[9]!=dut.request)begin read_bus(1);cycles=cycles+1;if(cycles>1000)$fatal(1,"host ack timeout");end
  // A halt or a read waits for ready as sramcapture does; an arm does not
  // (the ring's writes hold the transport).
  cycles=0;read_bus(1);
  if(op!=1)while(!value[2])begin read_bus(1);cycles=cycles+1;if(cycles>100000)$fatal(1,"not ready %h",value);end
 end endtask
 task read32(input [6:0] a,output [31:0] v);
 reg [15:0] lo;
 begin read_bus(a);lo=value;read_bus(a+1);v={value,lo};end
 endtask
 // The digital stream as the host reads it: its words in order from the
 // stream's start; each CH1 level change's sample ordinal (2 ns).
 integer words=0,nedges=0;reg [31:0] edges[0:255];reg last_bit=0,have_bit=0;
 always @(posedge dut.core)begin
  if(!dut.drum_active)begin words=0;have_bit=0;end
  else if(dut.drum_in_valid)begin:w
   integer b;
   for(b=0;b<16;b=b+1)begin
    if(have_bit && dut.drum_in_data[b]!=last_bit && nedges<256)begin edges[nedges]=(16*words+b)*(1<<(STREAM_LOG-4));nedges=nedges+1;end
    last_bit=dut.drum_in_data[b];have_bit=1;
   end
   words=words+1;
  end
 end
 // The record as sramcapture.recallFrozen reads it: spans of 4080 words,
 // each a discard to 16 words before it, a read of n+16, the buffer popped
 // from word 16.
 reg [31:0] got[0:524287];
 task recall(input [31:0] origin,start,length);
 reg [31:0] off,n,target,pos,skip,lo;integer j;
 begin
  off=0;
  while(off<length)begin
   n=length-off;if(n>4080)n=4080;
   target=(origin+start+off-16)&19'h7ffff;
   read32(8,pos);
   skip=(target-pos)&19'h7ffff;
   if(skip!=0)begin write_bus(8,skip[15:0]);write_bus(9,skip[31:16]);host_command(3);end
   write_bus(8,(n+16)&16'hffff);write_bus(9,(n+16)>>16);host_command(2);
   read_bus(12);if(value!=n+16)$fatal(1,"short read %0d",value);
   write_bus(16,16);read_bus(17);write_bus(17,32);
   for(j=0;j<n;j=j+1)begin read_bus(25);lo=value;read_bus(25);got[off+j]={value,lo[15:0]};end
   off=off+n;
  end
 end endtask
 integer i,k,errors=0,ch,bad=0;
 // The ring's writes hold the transport: it never goes idle while the ring
 // runs (a word offered then would be lost).
 integer idle_clocks=0,first_idle=0;
 always @(posedge dut.core)if(dut.running && dut.ready && !dut.priming && !dut.arm)begin
  if(idle_clocks==0)first_idle=dut.ring_time.lo+256*dut.ring_time.hi;idle_clocks=idle_clocks+1;end
 reg [31:0] length,start,tindex,origin,last,phys,word,want_t;
 // A ring word whose neighbours step by the ramp's pace: its value tells time.
 function on_ramp(input integer i);
  integer j;reg [15:0] d;
  begin
   on_ramp=i>=2 && i+2<length;
   for(j=i-1;j<=i+2 && on_ramp;j=j+1)begin
    d=got[j][31:16]-got[j-1][31:16];
    if(d<((1<<RING_LOG)>>RAMP_SHIFT)*256-64 || d>((1<<RING_LOG)>>RAMP_SHIFT)*256+64)on_ramp=0;
   end
  end
 endfunction
 real centre,kk,ring_t,err_t;
 initial begin
  for(i=0;i<524288;i=i+1)mem[i]=32'hdeadbeef;
  repeat(20)@(negedge clk);
  read_bus(61);if(value!==16'h5247)$fatal(1,"no ring capability: %h",value);
  // sramcapture.StartStreamRing: stream decimation, mode, then Arm.
  write_bus(21,STREAM_LOG);write_bus(19,1|2|4|32);
  write_bus(18,RING_LOG);
  write_bus(2,16'hffef);write_bus(3,16'h7);write_bus(4,17);write_bus(5,0);
  write_bus(6,16'h0001);write_bus(7,16'h0480);write_bus(59,16'h0080);
  host_command(1);
  // let the ring run, then the host's stop
  repeat(RUN)@(posedge dut.core);
  host_command(5);
  read32(2,length);read32(4,start);read32(6,tindex);read32(10,origin);read32(62,last);
  host_command(5);write_bus(19,0);
  $display("ring: length %0d start %0d trigger %0d origin %0d stamp %0d; %0d edges",length,start,tindex,origin,last,nedges);
  if(dut.drum_active)begin $display("FAIL: the second stop left the stream running");errors=errors+1;end
  if(idle_clocks)begin $display("FAIL: the transport was idle %0d clocks while the ring ran, first at raw word %0d",idle_clocks,first_idle);errors=errors+1;end
  recall(origin,start,length);
  // Every word read holds the ramp at the ring's pace (2^RING_LOG samples a
  // word), but across the ramp's wraps, which the filter spreads.
  for(i=1;i<length;i=i+1)begin:chk
   reg [15:0] a,b;integer d;
   a=got[i-1][31:16];b=got[i][31:16];
   d=(b-a)&16'hffff; // Q8.8 ramp step: 2^RING_LOG samples >> RAMP_SHIFT, x256
   if(!(d>=((1<<RING_LOG)>>RAMP_SHIFT)*256-64 && d<=((1<<RING_LOG)>>RAMP_SHIFT)*256+64) && !(a>16'hc000 || b<16'h4000 || a<16'h1000))begin
    if(bad<8)$display("FAIL: ring word %0d: CH2 %h after %h",i,b,a);bad=bad+1;
   end
  end
  if(bad)begin $display("FAIL: %0d of %0d ring words off the ramp",bad,length);errors=errors+1;end
  // The app's mapping (soft_ring.go): newest word's centre 2*stamp - 1.5(2^n-1).
  for(k=0;k<nedges;k=k+1)begin
   centre=2.0*last-1.5*((1<<RING_LOG)-1);
   kk=(centre-edges[k])/(1<<RING_LOG);
   kk=$rtoi(kk+0.5);
   if(kk<0)kk=0;
   if(kk<length)begin
    i=length-1-kk;
    word=got[i];
    // the word's CH2 Q8.8 value is the ramp's mean over its span, the
    // filter's delay ago: its time
    ring_t=word[31:16]*(1<<RAMP_SHIFT)/256.0+1.5*((1<<RING_LOG)-1);
    want_t=edges[k]&((256<<RAMP_SHIFT)-1);
    err_t=ring_t-want_t;
    if(err_t>(128<<RAMP_SHIFT))err_t=err_t-(256<<RAMP_SHIFT);
    if(err_t<-(128<<RAMP_SHIFT))err_t=err_t+(256<<RAMP_SHIFT);
    $display("edge at ordinal %0d -> ring word %0d, made at %0.0f (mod), off by %0.0f samples",edges[k],i,ring_t,err_t);
    if(!on_ramp(i))err_t=0; // the filter's span crosses the ramp's wrap: no time
    if(err_t>(2<<RING_LOG) || err_t<-(2<<RING_LOG))begin $display("FAIL: edge %0d mapped %0.0f samples off",k,err_t);errors=errors+1;end
   end
  end
  if(errors==0)$display("PASS tb_top_bits_ring");else $display("FAIL tb_top_bits_ring: %0d errors",errors);
  $finish;
 end
endmodule

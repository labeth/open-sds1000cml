// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// Self-checking: ring mode (ADR-STREAM-RING).
// A. stream_drum on chip with stream_banks: every word arrives in order with
//    no transport command, a host stall within the FIFO and banks loses
//    nothing, a longer one raises fault.
// B. sram_record as a ring with ring_stamp: the ring's words hold the raw-word
//    time they were made at; after the host's stop or an FPGA trigger, the
//    stamp, the record's start/length (and trigger index) and the decimation
//    place every word, and any time in the history maps to the word holding
//    it, with one fixed pipeline constant across freezes.
// C. stream_bits beside the ring (the bits image): an edge on CH1 reaches the
//    digital stream at the sample ordinal its period gives, and the ordinal,
//    turned into a ring word through the stamp as the host does (the word
//    whose span centres nearest it), names the ring word whose span holds
//    the edge: the stream and the stamp count from the same start.
// A's drum also sees the record's transport traffic (ready and done
// pulsing): on chip that is not the drum's.
// TRLC-LINKS: REQ-SDS-035
module tb_stream_ring;
 integer errors=0;
 reg core=0,hclk=0,host=0;
 always #2 core=!core;      // 250 MHz
 always #4 hclk=!hclk;      // 125 MHz
 always #6.25 host=!host;   // 80 MHz
 // ------------------------------------------------------------------ A
 localparam BANK_AW=4,BANK=1<<BANK_AW;
 reg active=0,in_valid=0;reg [31:0] in_data=0;
 wire cmd,cmd_read,cmd_discard,cmd_continue,wvalid,wstop,fault;wire [12:0] cmd_count;wire [31:0] wdata;
 wire pvalid,psingle,pseal,bank_ready,next_free;
 reg t_ready=1,t_done=0;integer tq=0;
 always @(posedge core)begin tq=tq+1;t_ready<=tq%37<20;t_done<=tq%37==20;end
 wire [63:0] pdata;
 stream_drum #(.AW(12),.WF_AW(8),.DESC_AW(7),.BANK_AW(BANK_AW),.WRITE_AT(48),.WRITE_URGENT(160),.AGE_CYCLES(3000)) dut(
  .clk(core),.active(active),.onchip(1'b1),.in_valid(in_valid),.in_data(in_data),
  .cmd(cmd),.cmd_read(cmd_read),.cmd_discard(cmd_discard),.cmd_continue(cmd_continue),.cmd_count(cmd_count),
  // the record owns the transport: its ready and done come and go
  .t_ready(t_ready),.t_write_ready(1'b0),.t_done(t_done),.wdata(wdata),.wvalid(wvalid),.wstop(wstop),
  .rdata(32'd0),.rvalid(1'b0),.position(12'd0),.fault(fault),
  .hclk(hclk),.bank_ready(bank_ready),.bank_next_free(next_free),.packet_valid(pvalid),.packet_data(pdata),.packet_single(psingle),.packet_seal(pseal));
 wire mw,overrun,host_overrun;wire [BANK_AW:0] ma;wire [63:0] md;
 reg rel=0,rel_bank=0,rel_token=0;
 wire [1:0] ready,token,single;wire [63:0] first0,first1;wire [BANK_AW:0] count0,count1;
 stream_banks #(.BANK_AW(BANK_AW)) bk(.reset(!active),.producer_clk(hclk),.host_clk(host),.packet_valid(pvalid),.packet_data(pdata),
  .packet_single(psingle),.seal(pseal),.packet_ready(next_free),.cur_ready(bank_ready),.mem_write(mw),.mem_address(ma),.mem_data(md),.overrun(overrun),
  .host_overrun(host_overrun),.host_release(rel),.release_bank(rel_bank),.release_token(rel_token),.host_ready(ready),
  .host_token(token),.first_word0(first0),.first_word1(first1),.packets0(count0),.packets1(count1),.last_single(single));
 reg [63:0] bmem[0:2*BANK-1];
 always @(posedge hclk)if(mw)bmem[ma]<=md;
 reg [63:0] expected=0;reg draining=1;integer blocks=0,commands=0,sent=0;
 always @(posedge core)if(cmd || wvalid)commands=commands+1;
 task drain_one;
  integer b,i,n;reg [63:0] first;reg [BANK_AW:0] cnt;reg sg,tk;reg [31:0] w;reg got;
  begin
   got=0;
   // both banks published and neither the next words: a gap
   if(ready==2'b11 && first0!=expected && first1!=expected && errors<10)begin
    $display("FAIL: banks start at %0d and %0d, expected %0d",first0,first1,expected);errors=errors+1;end
   for(b=0;b<2 && !got;b=b+1)if(ready[b] && (b ? first1 : first0)==expected)begin
    first=b ? first1 : first0;
    cnt=b ? count1 : count0;sg=single[b];tk=token[b];n=cnt*2-sg;
    for(i=0;i<n;i=i+1)begin
     w=i[0] ? bmem[b*BANK+i/2][63:32] : bmem[b*BANK+i/2][31:0];
     if(w!==first[31:0]+i && errors<10)begin $display("FAIL: word %0d is %h",first+i,w);errors=errors+1;end
    end
    expected=first+n;blocks=blocks+1;
    @(posedge host);rel<=1;rel_bank<=b;rel_token<=tk;@(posedge host);rel<=0;repeat(4)@(posedge host);
    got=1;
   end
  end
 endtask
 always begin @(posedge host);if(draining && active)drain_one;end
 task send(input integer n,input integer gap);
  integer k;
  begin
   for(k=0;k<n;k=k+1)begin
    @(posedge core);in_valid<=1;in_data<=sent;sent=sent+1;@(posedge core);in_valid<=0;
    repeat(gap)@(posedge core);
   end
  end
 endtask
 // ------------------------------------------------------------------ B
 localparam AW=10,DEPTH=1<<AW,P=4; // ring of 1024 words, a ring word per 4 raw words
 reg r_active=0,raw_valid=0,arm=0,halt=0,trig=0;reg [AW:0] pre=0,post=0;
 reg [31:0] raw_time=0;integer phase=0;reg rstep=0;reg [31:0] rdata=0;
 wire running,rdone,triggered,cfg_err,hready;wire [AW-1:0] waddr,rstart;wire [AW:0] rlen,tindex,filled;
 sram_record #(.AW(AW)) rec(.clk(core),.reset(1'b0),.arm(arm),.halt(halt),.pre_count(pre),.post_count(post),
  .step(rstep),.trigger(trig),.running(running),.done(rdone),.triggered(triggered),.config_error(cfg_err),.history_ready(hready),
  .write_addr(waddr),.record_start(rstart),.record_length(rlen),.trigger_index(tindex),.filled(filled),
  .pre_start_config({AW{1'b0}}),.pre_plus_one_config({(AW+1){1'b0}}),.post_minus_one_config({(AW+1){1'b0}}));
 wire [31:0] last;
 ring_stamp stamp(.clk(core),.active(r_active),.raw_valid(raw_valid),.write(rstep && running && !halt),.last(last));
 reg [31:0] ring[0:DEPTH-1];
 always @(posedge core)if(rstep && running && !halt)ring[waddr]<=rdata;
 // Raw words with irregular gaps (the stamp counts words, not clocks); a
 // ring word (its data the raw-word time it completes at) every P raw words,
 // a few clocks later, as a decimator's output.
 reg [2:0] dv=0;reg [31:0] dd0=0,dd1=0,dd2=0;reg [31:0] seed=1;reg gaps=0;reg [31:0] trig_at=32'hffffffff;
 always @(posedge core)begin
  seed=seed*1103515245+12345;
  raw_valid<=r_active && (!gaps || seed[20:18]!=0);
  rstep<=0;trig<=0;
  dv<={dv[1:0],raw_valid && phase==P-1};dd0<=raw_time;dd1<=dd0;dd2<=dd1;
  if(raw_valid)begin
   raw_time<=raw_time+1;
   phase<=phase==P-1 ? 0 : phase+1;
  end
  if(dv[2])begin rstep<=1;rdata<=dd2;trig<=dd2>=trig_at && trig_at!=32'hffffffff;end
  if(!r_active)begin raw_time<=0;phase<=0;dv<=0;end
 end
 integer c0;reg c0_set=0;
 // Check a frozen record: every word against the newest (exactly P raw words
 // apart, whatever the gaps), the newest against the stamp, and the host's
 // lookup of history times. The stamp's constant is exact with raw words on
 // every clock; with gaps, a raw word that falls in the decimator's latency
 // counts or not, so it may read up to the latency (dly+2 clocks) less, and a
 // lookup within that of a word boundary may name the neighbouring word.
 task check_record(input [8*12-1:0] what,input exact);
  integer i,c,k,e,far;reg [AW-1:0] a;reg [31:0] t,newest;
  begin
   a=rstart+rlen-1;newest=ring[a];
   c=last-newest;
   if(exact && !c0_set)begin c0=c;c0_set=1;end
   if(exact ? c!=c0 : (c>c0 || c<c0-5))begin $display("FAIL: %0s: stamp constant %0d, reference %0d",what,c,c0);errors=errors+1;end
   for(i=0;i<rlen;i=i+1)begin
    a=rstart+rlen-1-i;
    if(ring[a]!==newest-i*P && errors<10)begin $display("FAIL: %0s: word %0d from the end is %0d, not %0d",what,i,ring[a],newest-i*P);errors=errors+1;end
   end
   // the host's lookup with the reference constant: time t -> the word that
   // completes at or after it
   far=0;
   for(k=0;k<400;k=k+1)begin
    seed=seed*1103515245+12345;
    t=newest-(seed[30:8]%((rlen-1)*P));
    i=(last-c0-t)/P;
    if(i<0)i=0;
    a=rstart+rlen-1-i;
    e=(ring[a]>=t && ring[a]-t<P) ? 0 : 1;
    if(e)far=far+1;
    if(e && (exact || !(ring[a]+P>=t && ring[a]<t+2*P)) && errors<10)begin $display("FAIL: %0s: time %0d maps to word %0d",what,t,ring[a]);errors=errors+1;end
   end
   $display("%0s: %0d words, newest at raw word %0d, stamp constant %0d (reference %0d), %0d/400 lookups a word off",what,rlen,newest,c,c0,far);
  end
 endtask
 initial begin
  // ---------------- A
  repeat(20)@(posedge core);active<=1;
  repeat(5000)@(posedge core);
  send(3000,40);
  // A stall: 1500 clocks at a word every 42 clocks is ~36 words... make it
  // ~200 words, inside the FIFO (252 words) and the banks.
  draining=0;send(200,40);draining=1;
  send(1500,40);
  repeat(60)begin send(6,0);repeat(300)@(posedge core);end
  repeat(20000)@(posedge core);
  if(expected!==sent)begin $display("FAIL: drained %0d of %0d",expected,sent);errors=errors+1;end
  if(fault || overrun)begin $display("FAIL: fault %b overrun %b in a stall the FIFO holds",fault,overrun);errors=errors+1;end
  if(commands!=0)begin $display("FAIL: on-chip drum touched the transport %0d times",commands);errors=errors+1;end
  $display("A: %0d words in %0d banks on chip, no transport traffic",expected,blocks);
  // a stall beyond FIFO + banks: fault, never a silent gap
  draining=0;send(600,10);repeat(2000)@(posedge core);
  if(!fault)begin $display("FAIL: overflowing the on-chip FIFO did not raise fault");errors=errors+1;end
  draining=1;active<=0;
  // ---------------- B0: FPGA trigger with pre/post counts, raw words every clock
  gaps<=0;@(posedge core);r_active<=1;repeat(10)@(posedge core);
  pre<=300;post<=200;trig_at<=20000;@(posedge core);arm<=1;@(posedge core);arm<=0;
  wait(rdone);repeat(10)@(posedge core);
  if(rlen!=500 || tindex!=300)begin $display("FAIL: trigger: length %0d index %0d",rlen,tindex);errors=errors+1;end
  if(!(ring[rstart+tindex]>=20000 && ring[rstart+tindex]-20000<P))begin $display("FAIL: trigger word holds %0d",ring[rstart+tindex]);errors=errors+1;end
  check_record("FPGA trigger",1);
  trig_at<=32'hffffffff;
  // ---------------- B1: host stop of a ring that has lapped several times
  r_active<=0;repeat(5)@(posedge core);r_active<=1;repeat(10)@(posedge core);
  pre<=DEPTH-1;post<=1;@(posedge core);arm<=1;@(posedge core);arm<=0;
  repeat(70777)@(posedge core); // past a carry of the stamp's low half
  @(posedge core);halt<=1;@(posedge core);halt<=0;repeat(10)@(posedge core);
  if(!rdone || rlen!=DEPTH)begin $display("FAIL: stop: done %b length %0d",rdone,rlen);errors=errors+1;end
  check_record("host stop",1);
  // the stamp holds while frozen (the stream runs on)
  begin:hold reg [31:0] l;l=last;repeat(500)@(posedge core);if(last!==l)begin $display("FAIL: stamp moved while frozen");errors=errors+1;end end
  // ---------------- B2: raw words with gaps, a stop before the ring has filled
  r_active<=0;gaps<=1;repeat(5)@(posedge core);r_active<=1;repeat(10)@(posedge core);
  @(posedge core);arm<=1;@(posedge core);arm<=0;
  repeat(DEPTH*P/2)@(posedge core);
  @(posedge core);halt<=1;@(posedge core);halt<=0;repeat(10)@(posedge core);
  if(!rdone || rlen>=DEPTH || rstart!=0)begin $display("FAIL: short stop: length %0d start %0d",rlen,rstart);errors=errors+1;end
  check_record("short stop",0);
  // ---------------- B3: gaps, a ring lapped many times, stop
  r_active<=0;repeat(5)@(posedge core);r_active<=1;repeat(10)@(posedge core);
  pre<=DEPTH-1;post<=1;@(posedge core);arm<=1;@(posedge core);arm<=0;
  repeat(140333)@(posedge core); // past two carries of the stamp's low half
  @(posedge core);halt<=1;@(posedge core);halt<=0;repeat(10)@(posedge core);
  if(!rdone || rlen!=DEPTH)begin $display("FAIL: stop with gaps: length %0d",rlen);errors=errors+1;end
  check_record("gaps, stop",0);
  // ---------------- C
  part_c(1234);part_c(5000);part_c(7777);
  if(errors==0)$display("PASS tb_stream_ring");else $display("FAIL tb_stream_ring: %0d errors",errors);
  $finish;
 end
 initial begin #40000000 $display("FAIL: timeout");$finish;end
 // ------------------------------------------------------------------ C
 localparam CN=4,CBITS=3;            // ring words of 2^CN samples, bit periods of 2^CBITS raw words
 reg c_act=0,c_on=0;reg [31:0] c_t=0,c_edge=0;reg [31:0] c_word=0;
 wire c_bv;wire [31:0] c_bw;
 stream_bits cbits(.clk(core),.enable(c_on),.log_words(5'd3),.low1(8'd124),.high1(8'd132),.low2(8'd124),.high2(8'd132),
  .in_valid(c_act),.in_word(c_word),.out_valid(c_bv),.out_word(c_bw));
 reg c_write=0;reg [31:0] c_ring[0:4095];integer c_n=0;
 wire [31:0] c_last;
 ring_stamp cstamp(.clk(core),.active(c_act),.raw_valid(c_act),.write(c_write),.last(c_last));
 integer c_k=0,c_first=-1,c_frozen=0;
 always @(posedge core)begin
  c_on<=c_act;                       // the producer's enable is registered from the stream's start
  // raw word t: samples 2t and 2t+1 of each line; CH1 steps up at sample c_edge
  c_word<={8'd0,(2*c_t+1>=c_edge)?8'd255:8'd0,8'd0,(2*c_t>=c_edge)?8'd255:8'd0};
  c_write<=0;
  if(c_act)begin
   c_t<=c_t+1;
   // a ring word completes on its last raw word; written a few clocks on
   if(c_t%(1<<(CN-1))==(1<<(CN-1))-1 && !c_frozen)begin c_ring[c_n]<=c_t;c_n<=c_n+1;c_write<=1;end
  end else begin c_t<=0;c_n<=0;end
  if(c_bv)begin
   if(c_first<0)begin:find integer b;for(b=15;b>=0;b=b-1)if(c_bw[b])c_first=16*c_k+b;end
   c_k<=c_k+1;
  end
  if(!c_on)c_k<=0;
 end
 task part_c(input integer at);
  integer o,k,idx,lo,hi;
  begin
   c_act<=0;c_frozen=0;c_first=-1;c_edge<=at;repeat(20)@(posedge core);
   c_act<=1;
   repeat(at/2+3000)@(posedge core);
   c_frozen=1;repeat(10)@(posedge core);
   if(c_first<0)begin $display("FAIL: C: the at at %0d never reached the digital stream",at);errors=errors+1;end
   else begin
    o=c_first*(1<<(CBITS+1));         // the period's first sample ordinal
    if(at<o || at>=o+(1<<(CBITS+1))+4)begin $display("FAIL: C: at %0d in the period from %0d",at,o);errors=errors+1;end
    // host: ring word from the end whose span (centre 2*time - 2^(n-1)) is nearest o
    k=(2*c_last-(1<<(CN-1))-o+(1<<(CN-1)))/(1<<CN);
    idx=c_n-1-k;
    lo=2*c_ring[idx]-(1<<CN)+2;hi=2*c_ring[idx]+1;
    if(at<lo-(1<<CN) || at>hi+(1<<CN))begin $display("FAIL: C: at %0d maps to ring word %0d spanning %0d..%0d",at,idx,lo,hi);errors=errors+1;end
    else $display("C: edge at sample %0d, stream ordinal %0d, ring word %0d spans %0d..%0d",at,o,idx,lo,hi);
   end
   c_act<=0;repeat(5)@(posedge core);
  end
 endtask
endmodule

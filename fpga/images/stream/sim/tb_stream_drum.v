// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// Self-checking: stream_drum + sram_transport + stream_banks against a model
// of the sequential SRAM (spec 12 §5: one shared forward-only counter, a
// two-stage read pipeline, unreliable first 16 words of a fresh read or a
// write burst). A small ring keeps laps short. The host drains by ordinal as
// sramcapture.DrainStream does and stalls for several laps: the ring must
// carry every word. A stall beyond the ring must raise fault.
//
// Rates are scaled by the lap: AW=12 makes a lap 4096 clocks, and with the
// 256-word FIFO one word per 31 clocks brings half the FIFO a lap, as 978k
// words/s does on the board (2^19 pulses, 4096 words); one per 24 clocks is
// the GPMC ceiling (1.27M words/s). The host drains at drain_ns a word. With
// FAST=1 two banks are the FIFO and seeks are a quarter lap, so command
// overheads weigh about as on the board; it runs only the fast phases.
// REAL=1 runs them with the board's sizes and rates.
// TRLC-LINKS: REQ-SDS-035
module tb_stream_drum #(parameter REAL=0,FAST=0,AW=REAL ? 19 : 12,BANK_AW=REAL ? 10 : FAST ? 6 : 4,WF_AW=REAL ? 12 : 8,DESC_AW=REAL ? 9 : 7);
 localparam N=1<<AW,BANK=1<<BANK_AW;
 reg core=0,hclk=0,host=0;
 always #2 core=!core;      // 250 MHz
 always #4 hclk=!hclk;      // 125 MHz, same phase as the PLL derives it
 always #6.25 host=!host;   // 80 MHz
 wire sample_clk;assign #1 sample_clk=core;
 reg active=0,in_valid=0;reg [31:0] in_data=0;
 wire cmd,cmd_read,cmd_discard,cmd_continue,t_ready,t_write_ready,t_done,wvalid,wstop,rvalid,fault;
 wire [AW:0] cmd_count;wire [31:0] wdata,rdata;wire [AW-1:0] position;
 wire k1,k2,g1;wire [31:0] dq;
 sram_transport #(.AW(AW),.CONTINUOUS_ONLY(1),.READ_DELAY(1)) tr(.clk(core),.sample_clk(sample_clk),.reset(1'b0),.locked(1'b1),
  .command(cmd),.command_read(cmd_read),.command_discard(cmd_discard),.command_continue(cmd_continue),.command_count(cmd_count),
  .ready(t_ready),.write_data(wdata),.write_valid(wvalid),.write_stop(wstop),.write_ready(t_write_ready),
  .read_data(rdata),.read_valid(rvalid),.done(t_done),.position(position),.dq(dq),.k1(k1),.k2(k2),.g1(g1));
 // SRAM model with the spec's unreliable burst starts.
 reg [31:0] mem[0:N-1];reg [AW-1:0] address=0;reg [31:0] stage1=0,stage2=0;
 reg last_dir=1;integer since_turn=0;
 // The board's read data reaches the FPGA a core clock later than the SRAM
 // drives it (top.v: SRAM_READ_DELAY=1 with the interleaved front end).
 reg [31:0] dq_late=0;
 always @(posedge core)dq_late<=stage2;
 assign dq=k1 && g1 ? dq_late : 32'bz;
 always @(posedge k2)if(g1)begin
  if(k1!=last_dir)begin last_dir<=k1;since_turn=0;end
  // Late write, as the board measures it (spec 12 §5.2): with back-to-back
  // pulses the word offered at count P lands at P+2.
  if(!k1)mem[address+2'd2]<=since_turn<16 ? 32'hdead0000|since_turn : dq;
  else begin stage1<=since_turn<16 ? 32'hbad00000|since_turn : mem[address];stage2<=stage1;end
  since_turn=since_turn+1;
  address<=address+1'b1;
 end
 wire pvalid,psingle,pseal,bank_ready,bank_next_free;wire [63:0] pdata;
 stream_drum #(.AW(AW),.WF_AW(WF_AW),.DESC_AW(DESC_AW),.BANK_AW(BANK_AW),
  .WRITE_AT(REAL ? 512 : 48),.WRITE_URGENT(REAL ? 3072 : 160),.TRIP_AT(REAL ? 3584 : 224),.LAP_SHIFT(REAL ? 7 : 4),
  .AGE_CYCLES(REAL ? 2097152 : 3000),.SEG(REAL ? 8192 : FAST ? 1024 : 256)) dut(
  .clk(core),.active(active),.in_valid(in_valid),.in_data(in_data),
  .cmd(cmd),.cmd_read(cmd_read),.cmd_discard(cmd_discard),.cmd_continue(cmd_continue),.cmd_count(cmd_count),
  .t_ready(t_ready),.t_write_ready(t_write_ready),.t_done(t_done),.wdata(wdata),.wvalid(wvalid),.wstop(wstop),
  .rdata(rdata),.rvalid(rvalid),.position(position),.fault(fault),
  .hclk(hclk),.bank_ready(bank_ready),.bank_next_free(bank_next_free),.packet_valid(pvalid),.packet_data(pdata),.packet_single(psingle),.packet_seal(pseal));
 wire mw,overrun,host_overrun;wire [BANK_AW:0] ma;wire [63:0] md;
 reg rel=0,rel_bank=0,rel_token=0;
 wire [1:0] ready,token,single;wire [63:0] first0,first1;wire [BANK_AW:0] count0,count1;
 stream_banks #(.BANK_AW(BANK_AW)) bk(.reset(!active),.producer_clk(hclk),.host_clk(host),.packet_valid(pvalid),.packet_data(pdata),
  .packet_single(psingle),.seal(pseal),.packet_ready(bank_next_free),.cur_ready(bank_ready),.mem_write(mw),.mem_address(ma),.mem_data(md),.overrun(overrun),
  .host_overrun(host_overrun),.host_release(rel),.release_bank(rel_bank),.release_token(rel_token),.host_ready(ready),
  .host_token(token),.first_word0(first0),.first_word1(first1),.packets0(count0),.packets1(count1),.last_single(single));
 reg [63:0] bmem[0:2*BANK-1];
 always @(posedge hclk)if(mw)bmem[ma]<=md;
 integer errors=0;reg [63:0] expected=0;reg draining=1;integer blocks=0;real drain_ns=0;
 task drain_one;
  integer b,i,n;reg [63:0] first;reg [BANK_AW:0] cnt;reg sg,tk;reg [31:0] w;reg got;
  begin
   got=0;
   for(b=0;b<2 && !got;b=b+1)if(ready[b])begin
    first=b ? first1 : first0;
    if(first==expected)begin
     cnt=b ? count1 : count0;sg=single[b];tk=token[b];n=cnt*2-sg;
     for(i=0;i<n;i=i+1)begin
      w=i[0] ? bmem[b*BANK+i/2][63:32] : bmem[b*BANK+i/2][31:0];
      if(w!==expected[31:0]+i && errors<10)begin $display("FAIL: word %0d is %h",expected+i,w);errors=errors+1;end
     end
     expected=expected+n;blocks=blocks+1;
     if(drain_ns>0)#(n*drain_ns);
     @(posedge host);rel<=1;rel_bank<=b;rel_token<=tk;@(posedge host);rel<=0;repeat(4)@(posedge host);
     got=1;
    end
   end
  end
 endtask
 always begin @(posedge host);if(draining && active)drain_one;end
 integer sent=0,commands=0,writes=0,reads=0;
 always @(posedge core)if(cmd)begin commands=commands+1;if(!cmd_read)writes=writes+1;else if(!cmd_discard)reads=reads+1;end
 // A fresh session: counters and the drum restart.
 task session;
  begin
   active<=0;repeat(50)@(posedge core);expected=0;sent=0;blocks=0;writes=0;reads=0;active<=1;
  end
 endtask
 // The backlog: its peak (ring words) and when the ring last emptied.
 integer peak=0;realtime spill_t=0,clear_t=0;reg had=0;
 always @(posedge hclk)begin
  if(dut.used>peak)peak=dut.used;
  if(dut.used!=0 && !had)spill_t=$realtime;
  if(dut.used==0 && had)begin clear_t=$realtime;if(REAL)$display("  backlog %0.3f-%0.3f ms, ring peak %0d words",spill_t/1e6,clear_t/1e6,peak);end
  had<=dut.used!=0;
 end
 task settle_check(input integer clocks,input [8*24-1:0] name);
  begin
   repeat(clocks)@(posedge core);
   if(expected!==sent || fault)begin $display("FAIL: %0s drained %0d of %0d, fault %b",name,expected,sent,fault);errors=errors+1;end
   $display("%0s: %0d words in %0d banks, %0d ring writes, %0d ring reads, ring peak %0d words, last backlog %0.3f-%0.3f ms, fault %b",
    name,expected,blocks,writes,reads,peak,spill_t/1e6,clear_t/1e6,fault);
   peak=0;
  end
 endtask
 // n words, one per period clocks, with the host stalled for the middle
 // stall of them.
 task stream(input integer n,input integer period,input integer stall);
  integer k;
  begin
   for(k=0;k<n;k=k+1)begin
    if(k==(n-stall)/2)draining=0;
    if(k==(n+stall)/2)draining=1;
    send(1,period-2);
   end
   draining=1;
  end
 endtask
 task send(input integer n,input integer gap);
  integer k;
  begin
   for(k=0;k<n;k=k+1)begin
    @(posedge core);in_valid<=1;in_data<=sent;sent=sent+1;@(posedge core);in_valid<=0;
    repeat(gap)@(posedge core);
   end
  end
 endtask
 initial begin
  repeat(20)@(posedge core);active<=1;
  if(REAL)begin
   // The board's sizes: the GPMC ceiling (one word per 197 clocks) with the
   // host draining at 1.6M words/s and a 5 ms stall, then 1.6M words/s
   // (above the GPMC; a lap brings 3.3k words) with the host at 1.9M words/s
   // and a 2 ms stall. The settle outlasts the age rule (16.8 ms).
   drain_ns=625;stream(30000,197,6350);settle_check(5000000,"real 1.27M");
   session;drain_ns=526;stream(30000,156,3200);settle_check(5000000,"real 1.6M");
   if(errors==0)$display("PASS tb_stream_drum REAL");else $display("FAIL tb_stream_drum REAL: %0d errors",errors);
   $finish;
  end
  if(FAST)begin
   // 5. Above the old lap limit (one word per 31 clocks): with no backlog the
   //    words go straight to the host's banks; host stalls spill into the ring
   //    and the backlog drains while the stream keeps coming.
   session;drain_ns=90;stream(6000,24,0);settle_check(20000,"phase 5a GPMC, steady");
   session;drain_ns=70;stream(8000,24,1500);stream(8000,24,1500);settle_check(60000,"phase 5b GPMC, stalls");
   session;drain_ns=60;stream(8000,22,1500);stream(8000,22,1500);settle_check(60000,"phase 5c 1.4M, stalls");
   session;drain_ns=55;stream(8000,21,1200);stream(8000,21,1200);settle_check(60000,"phase 5d 1.5M, stalls");
   if(errors==0)$display("PASS tb_stream_drum FAST");else $display("FAIL tb_stream_drum FAST: %0d errors",errors);
   $finish;
  end
  // 0. Active with no input: the drum must stay idle (no empty bursts).
  repeat(30000)@(posedge core);
  if(commands!=0)begin $display("FAIL: idle drum issued %0d transport commands",commands);errors=errors+1;end
  // 1. Steady stream, about one word per 40 clocks.
  send(3000,40);
  // 2. The host stalls for ~6 laps (a lap is 4096 clocks) while words keep
  //    coming: the ring holds them, nothing is lost.
  draining=0;send(500,40);draining=1;
  send(2000,40);
  // 2b. Bursts of words on consecutive clocks (the precision tail drains its
  //     FIFO so): the clk-side input FIFO carries them across.
  repeat(60)begin send(6,0);repeat(400)@(posedge core);end
  // Let everything drain (writes are age-triggered, reads follow).
  repeat(80000)@(posedge core);
  if(expected!==sent)begin $display("FAIL: drained %0d of %0d",expected,sent);errors=errors+1;end
  if(fault)begin $display("FAIL: fault in a stream the ring could hold");errors=errors+1;end
  $display("phase 1-2: %0d words in %0d banks, fault %b",expected,blocks,fault);
  // 4. A new session at the fastest rate the drum sustains: a lap (4096
  //    clocks, the cost of reading back what was just written) brings half
  //    the FIFO. A host stall of several laps, and recovery.
  active<=0;repeat(50)@(posedge core);expected=0;sent=0;blocks=0;active<=1;
  repeat(2000)send(1,30);
  draining=0;repeat(600)send(1,30);draining=1;
  repeat(3000)send(1,30);
  repeat(80000)@(posedge core);
  if(expected!==sent || fault)begin $display("FAIL: fast session drained %0d of %0d, fault %b",expected,sent,fault);errors=errors+1;end
  $display("phase 4: %0d words in %0d banks, fault %b",expected,blocks,fault);
  // 3. A stall longer than the ring: fault, not a silent loss.
  draining=0;send(6000,20);
  repeat(5000)@(posedge core);
  if(!fault)begin $display("FAIL: overflowing the ring did not raise fault");errors=errors+1;end
  $display("phase 3: fault %b after %0d words with the host stalled",fault,sent);
  if(errors==0)$display("PASS tb_stream_drum");else $display("FAIL tb_stream_drum: %0d errors",errors);
  $finish;
 end
 initial begin #(REAL ? 300000000 : 50000000) $display("FAIL: timeout (drained %0d of %0d)",expected,sent);$finish;end
endmodule

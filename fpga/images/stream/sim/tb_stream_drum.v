// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// Self-checking: stream_drum + sram_transport + stream_banks against a model
// of the sequential SRAM (spec 12 §5: one shared forward-only counter, a
// two-stage read pipeline, unreliable first 16 words of a fresh read or a
// write burst). A small ring keeps laps short. The host drains by ordinal as
// sramcapture.DrainStream does and stalls for several laps: the ring must
// carry every word. A stall beyond the ring must raise fault.
// TRLC-LINKS: REQ-SDS-035
module tb_stream_drum;
 localparam AW=12,N=1<<AW,BANK_AW=4,BANK=1<<BANK_AW;
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
 wire pvalid,psingle,pseal,bank_ready;wire [63:0] pdata;
 stream_drum #(.AW(AW),.WF_AW(8),.DESC_AW(7),.BANK_AW(BANK_AW),.WRITE_AT(48),.WRITE_URGENT(160),.AGE_CYCLES(3000),.SEG(256)) dut(
  .clk(core),.active(active),.in_valid(in_valid),.in_data(in_data),
  .cmd(cmd),.cmd_read(cmd_read),.cmd_discard(cmd_discard),.cmd_continue(cmd_continue),.cmd_count(cmd_count),
  .t_ready(t_ready),.t_write_ready(t_write_ready),.t_done(t_done),.wdata(wdata),.wvalid(wvalid),.wstop(wstop),
  .rdata(rdata),.rvalid(rvalid),.position(position),.fault(fault),
  .hclk(hclk),.bank_ready(bank_ready),.packet_valid(pvalid),.packet_data(pdata),.packet_single(psingle),.packet_seal(pseal));
 wire mw,overrun,host_overrun;wire [BANK_AW:0] ma;wire [63:0] md;
 reg rel=0,rel_bank=0,rel_token=0;
 wire [1:0] ready,token,single;wire [63:0] first0,first1;wire [BANK_AW:0] count0,count1;
 stream_banks #(.BANK_AW(BANK_AW)) bk(.reset(!active),.producer_clk(hclk),.host_clk(host),.packet_valid(pvalid),.packet_data(pdata),
  .packet_single(psingle),.seal(pseal),.packet_ready(),.cur_ready(bank_ready),.mem_write(mw),.mem_address(ma),.mem_data(md),.overrun(overrun),
  .host_overrun(host_overrun),.host_release(rel),.release_bank(rel_bank),.release_token(rel_token),.host_ready(ready),
  .host_token(token),.first_word0(first0),.first_word1(first1),.packets0(count0),.packets1(count1),.last_single(single));
 reg [63:0] bmem[0:2*BANK-1];
 always @(posedge hclk)if(mw)bmem[ma]<=md;
 integer errors=0;reg [63:0] expected=0;reg draining=1;integer blocks=0;
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
     @(posedge host);rel<=1;rel_bank<=b;rel_token<=tk;@(posedge host);rel<=0;repeat(4)@(posedge host);
     got=1;
    end
   end
  end
 endtask
 always begin @(posedge host);if(draining && active)drain_one;end
 integer sent=0,commands=0;
 always @(posedge core)if(cmd)commands=commands+1;
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
  if(errors==0)$display("PASS tb_stream_drum");else $display("FAIL tb_stream_drum: %0d errors",errors);
  $finish;
 end
 initial begin #50000000 $display("FAIL: timeout (drained %0d of %0d)",expected,sent);$finish;end
endmodule

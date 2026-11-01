// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// Self-checking: the stream image's top level streaming the counter ramp
// through the drum, paced at RAMP_CLOCKS_P core clocks a word (register 46),
// driven over GPMC as sramcapture does it (pace, mode, arm; then the banks
// drained by ordinal as DrainStream does, each released after its words),
// with modelled SRAM. Every word drained must be the ramp's next value, and
// the stream must not fault while the host keeps up. MODE_P 35 is ring mode
// (the bits image): the drum on chip while the record keeps the SRAM.
// TRLC-LINKS: REQ-SDS-035
module bench_pll(input refclk,output reg c0=0,output c1,output locked,output reg halfclk=0);
 always #2 c0=~c0;initial begin #2;forever #4 halfclk=~halfclk;end assign #1 c1=c0;assign locked=1;
endmodule
// TRLC-LINKS: REQ-SDS-035
module adc_interleave(input refclk,memclk,packclk,enable,input [79:0] lane,input [9:0] encode_enable,
 input snapshot_request,output snapshot_ack,input consume,output [31:0] word_data,output valid,fault,output [79:0] snapshot,output [4:0] enc_p,enc_n,output locked);
 reg [31:0] n=0;
 assign word_data=n;assign valid=enable;
 always @(posedge memclk)if(enable && consume)n<=n+1;
 assign snapshot_ack=snapshot_request;assign fault=0;assign snapshot=0;assign enc_p=0;assign enc_n=0;assign locked=1;
endmodule
// TRLC-LINKS: REQ-SDS-035
module tb_top_stream_drum;
 localparam STOP_ST=`ifdef STOP_ST_P `STOP_ST_P `else 0 `endif ,MODE=`ifdef MODE_P `MODE_P `else 3 `endif ,RING_LOG=`ifdef RING_LOG_P `RING_LOG_P `else 8 `endif ,RAMP_CLOCKS=`ifdef RAMP_CLOCKS_P `RAMP_CLOCKS_P `else 125 `endif ,LOG=`ifdef LOG_P `LOG_P `else 9 `endif ,WORDS=`ifdef WORDS_P `WORDS_P `else 30000 `endif ;
 reg clk=0,mclk_in=0;always #6.25 clk=~clk;always #5 mclk_in=~mclk_in;
 reg ncs=1,noe=1,nwe=1;reg [6:0] selector=0;reg drive=0;reg [15:0] bus_value=0;
 wire [15:0] gpmc_d;assign gpmc_d=drive ? bus_value : 16'hzzzz;
 wire [31:0] dq;wire k1,k2,g1;
 acq_sram_top dut(.clk(clk),.mclk_in(mclk_in),.nCS1(ncs),.nOE(noe),.nWE(nwe),.sel(selector[6:2]),
  .gpmc_a2(selector[1]),.gpmc_b1(selector[0]),.gpmc_d(gpmc_d),.dq(dq),.lane(80'b0),.k1(k1),.k2(k2),.g1(g1));
 // The SRAM: a word offered at count P lands at P+2; reads are two-stage
 // with the board's late data.
 reg [31:0] mem[0:524287];reg [18:0] address=0;reg [31:0] s1=0,s2=0;
 assign dq=k1&&g1?s2:32'bz;
 always @(negedge g1)address<=0; // the enable low clears the counter
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
 end endtask
 integer errors=0,banks=0;reg [63:0] expected=0;reg [15:0] status,bufwords;
 // DrainStream, the words read straight from the buffer (the GPMC pops are
 // the slow part of the model, not the drum's concern).
 task drain;
  integer b,i,n;reg [63:0] first;reg [15:0] cnt;reg [31:0] w;reg got;
  begin
   read_bus(32);status=value;
   if(status[6])begin $display("FAIL: fault, status %h (bank overrun %b input %b read %b FIFO full %b backlog %b) after %0d words",
     status,status[9],status[10],status[11],status[12],status[13],expected);errors=errors+1;$finish;end
   got=0;
   for(b=0;b<2 && !got;b=b+1)if(status[b])begin
    first=b ? dut.stream_first1 : dut.stream_first0;
    if(first==expected)begin
     cnt=b ? dut.stream_count1 : dut.stream_count0;n=cnt*2-status[4+b];
     for(i=0;i<n;i=i+1)begin
      w=i[0] ? dut.buffer_mem64[b*(bufwords/4)+i/2][63:32] : dut.buffer_mem64[b*(bufwords/4)+i/2][31:0];
      if(w!==expected[31:0]+i && errors<10)begin $display("FAIL: word %0d is %h",expected+i,w);errors=errors+1;end
     end
     repeat(n*2)@(negedge clk); // ~1 word / 25 ns: the bus's 2.8M words/s while draining
     expected=expected+n;banks=banks+1;
     if(banks%4==1)$display("%0.3f ms: %0d words, %0d banks, status %h",$realtime/1e6,expected,banks,status);
     write_bus(20,b|status[2+b]<<1);
     got=1;
    end
   end
  end
 endtask
 // sramcapture.StartStream: pace, mode (stream, continuous), then Arm:
 // decimation, pre/post, config (counter source), level.
 task start;
 begin
  write_bus(29,0);write_bus(30,0);write_bus(46,RAMP_CLOCKS);
  // Ring mode (bit 5, the bits image): the stream's decimation in register
  // 21, the ring's in 18, as sramcapture.StartStreamRing does.
  write_bus(21,LOG);write_bus(19,MODE);
  write_bus(18,MODE&32 ? RING_LOG : LOG);
  write_bus(2,16'hffef);write_bus(3,16'h7);write_bus(4,17);write_bus(5,0);
  write_bus(6,16'h0000);write_bus(7,16'h0480);
  host_command(1);
 end endtask
 // sramcapture.StopStream: halt, wait for the transport, mode 0.
 task stop;
 integer cycles;
 begin
  host_command(5);cycles=0;read_bus(1);
  while(!value[2])begin read_bus(1);cycles=cycles+1;if(cycles>2000)begin $display("FAIL: the halt never saw the transport ready");errors=errors+1;$finish;end end
  write_bus(19,0);
 end endtask
 initial begin
  repeat(20)@(negedge clk);
  read_bus(26);bufwords=value;
  start;
  if(STOP_ST!=0)begin
   // A first run the host stalls into a backlog, stopped while the drum's
   // state is STOP_ST (1 seek, 4 write, 6 read): the next run starts clean.
   while(expected<WORDS/4 && errors==0)drain;
   wait(dut.drum.backlog);
   begin:hunt
    integer k;
    forever begin
     for(k=0;k<20000;k=k+1)begin @(posedge dut.halfclk);if(dut.drum.st==STOP_ST)disable hunt;end
     drain;
    end
   end
   stop;
   $display("first run stopped in state %0d after %0d words",STOP_ST,expected);
   repeat(20000)@(negedge clk);
   expected=0;banks=0;start;
  end
  while(expected<WORDS && errors==0)drain;
  $display("drained %0d words in %0d banks (buffer %0d words), status %h",expected,banks,bufwords,status);
  if(errors==0)$display("PASS tb_top_stream_drum");else $display("FAIL tb_top_stream_drum: %0d errors",errors);
  $finish;
 end
 initial begin #(64'd400000000) $display("FAIL: timeout after %0d words, status %h",expected,status);$finish;end
endmodule

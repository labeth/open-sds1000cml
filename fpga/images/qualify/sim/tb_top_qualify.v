// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// Qualify image top level with the symbol trigger (ADR-SYMBOL-TRIGGER): two
// channels of synthetic acquisition words through the interleave stub, the
// symbol block configured over GPMC (its table written entry by entry), a
// normal-trigger record frozen in modelled SRAM. Checks the image's
// capability registers, prints each match of the block ("M <word>"), the
// acquisition word at the trigger ("T <word>") and the host's count of the
// matches until the record froze ("C <count>"); the app's Go test (engine,
// TestSymbolTopRTL) compares them with the planner's settings run through
// the Go model.
// TRLC-LINKS: REQ-SDS-013
module bench_pll(input refclk,output reg c0=0,output c1,output locked,output reg halfclk=0);
 always #2 c0=~c0;initial begin #2;forever #4 halfclk=~halfclk;end assign #1 c1=c0;assign locked=1;
endmodule
// The acquisition words, one a core clock: {C2,C1,C2,C1} codes, the earlier
// pair lowest; the last word repeats.
// TRLC-LINKS: REQ-SDS-013
module adc_interleave(input refclk,memclk,packclk,enable,input [79:0] lane,input [9:0] encode_enable,
 input snapshot_request,output snapshot_ack,input consume,output [31:0] word_data,output valid,fault,output [79:0] snapshot,output [4:0] enc_p,enc_n,output locked);
 integer n=0,length=1;
 reg [31:0] words[0:4194303];reg [4095:0] input_file;
 initial begin
  if(!$value$plusargs("input=%s",input_file) || !$value$plusargs("length=%d",length))$fatal(1,"missing words");
  $readmemh(input_file,words,0,length-1);
 end
 assign word_data=n<length ? words[n] : words[length-1];
 assign valid=enable;
 always @(posedge memclk)if(enable && consume)n<=n+1;
 assign snapshot_ack=snapshot_request;assign fault=0;assign snapshot=0;assign enc_p=0;assign enc_n=0;assign locked=1;
endmodule
// TRLC-LINKS: REQ-SDS-013
module tb_top_qualify;
 reg clk=0,mclk_in=0;always #6.25 clk=~clk;always #5 mclk_in=~mclk_in;
 reg ncs=1,noe=1,nwe=1;reg [6:0] selector=0;reg drive=0;reg [15:0] bus_value=0;
 wire [15:0] gpmc_d;assign gpmc_d=drive ? bus_value : 16'hzzzz;
 wire [31:0] dq;wire k1,k2,g1;
 acq_sram_top dut(.clk(clk),.mclk_in(mclk_in),.nCS1(ncs),.nOE(noe),.nWE(nwe),.sel(selector[6:2]),
  .gpmc_a2(selector[1]),.gpmc_b1(selector[0]),.gpmc_d(gpmc_d),.dq(dq),.lane(80'b0),.k1(k1),.k2(k2),.g1(g1));
 reg [31:0] mem[0:524287];reg [18:0] address=0;reg [31:0] s1=0,s2=0;
 assign dq=k1&&g1?s2:32'bz;
 always @(posedge k2) if(g1)begin
  if(!k1)mem[address]<=dq;else begin s1<=mem[address];s2<=#4.5 s1;end
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
 task check_reg(input [6:0] a,input [15:0] v);
 begin read_bus(a);if(value!==v)$fatal(1,"register %0d = %h, want %h",a,value,v);end
 endtask
 task host_command(input [3:0] op);
 integer cycles;
 begin
  write_bus(1,op);cycles=0;read_bus(1);
  while(value[9]!=dut.request)begin read_bus(1);cycles=cycles+1;if(cycles>1000)$fatal(1,"host ack timeout");end
 end endtask
 always @(posedge dut.halfclk)if(dut.sym_match)$display("M %0d",dut.frontend.n);
 reg fired=0;
 always @(posedge dut.core)if(dut.sym_fire && !fired)begin fired<=1;$display("T %0d",dut.frontend.n);end
 // the record's trigger: the fire taken with a word written (one clock in
 // many on a decimated record)
 reg taken=0;
 always @(posedge dut.core)if(dut.il_fire_stage && dut.step && dut.history_ready && !taken)begin taken<=1;$display("R %0d",dut.frontend.n);end
 integer cycles,nregs,pre,post;
 reg [31:0] regs[0:8191];reg [4095:0] regs_file;
 initial begin
  for(cycles=0;cycles<524288;cycles=cycles+1)mem[cycles]=32'hdeadbeef;
  repeat(20)@(negedge clk);
  check_reg(0,16'h5a52);check_reg(74,16'h5154);check_reg(76,16'h5359);check_reg(98,0);
  check_reg(48,0);check_reg(118,0);check_reg(93,0);check_reg(97,0);check_reg(102,0);
  // +regs lists the "register value" writes: the levels, the table, the block.
  if(!$value$plusargs("regs=%s",regs_file) || !$value$plusargs("nregs=%d",nregs))$fatal(1,"missing registers");
  $readmemh(regs_file,regs,0,2*nregs-1);
  for(cycles=0;cycles<nregs;cycles=cycles+1)write_bus(regs[2*cycles][6:0],regs[2*cycles+1][15:0]);
  check_reg(98,1);
  if(!$value$plusargs("pre=%d",pre))pre=64;
  if(!$value$plusargs("post=%d",post))post=2000;
  write_bus(2,pre);write_bus(3,0);write_bus(4,post);write_bus(5,0);
  write_bus(6,16'h0011);host_command(1);
  cycles=0;read_bus(1);
  while(!(value[4] && value[2]))begin read_bus(1);cycles=cycles+1;if(cycles>4000000 || (!value[5] && dut.frontend.n>dut.frontend.length+post+1000))$fatal(1,"no trigger %h",value);end
  if(!value[5])$fatal(1,"record frozen without a trigger");
  // the block's count of its matches until the record froze
  read_bus(78);$display("C %0d",value);
  $display("PASS qualify top");$finish;
 end
endmodule

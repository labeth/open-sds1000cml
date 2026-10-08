// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// Packet image top level (ADR-PROTOCOL-PACKET-IMAGE): a protocol waveform on
// CH1 through the interleave stub, the packet trigger block configured over
// GPMC, a normal-trigger record frozen in modelled SRAM. Prints the selected
// decoder's events, the ADC sample index at the trigger and the frozen record.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018, REQ-SDS-039
module bench_pll(input refclk,output reg c0=0,output c1,output locked,output reg halfclk=0);
 always #2 c0=~c0;initial begin #2;forever #4 halfclk=~halfclk;end assign #1 c1=c0;assign locked=1;
endmodule
// CH1 plays the fixture: each entry lasts `unit` samples. CH2 idles mid-scale.
// TRLC-LINKS: REQ-SDS-013
module adc_interleave(input refclk,memclk,packclk,enable,input [79:0] lane,input [9:0] encode_enable,
 input snapshot_request,output snapshot_ack,input consume,output [31:0] word_data,output valid,fault,output [79:0] snapshot,output [4:0] enc_p,enc_n,output locked);
 integer n=0,unit=1,length=1;
 reg [7:0] entries[0:1048575];reg [4095:0] input_file;
 initial begin
  if(!$value$plusargs("input=%s",input_file) || !$value$plusargs("length=%d",length) ||
     !$value$plusargs("unit=%d",unit))$fatal(1,"missing waveform");
  $readmemh(input_file,entries,0,length-1);
 end
 function [7:0] code(input integer i);
  code=i/unit<length ? entries[i/unit] : entries[length-1];
 endfunction
 assign word_data={8'd128,code(n+1),8'd128,code(n)};
 assign valid=enable;
 always @(posedge memclk)if(enable && consume)n<=n+2;
 assign snapshot_ack=snapshot_request;assign fault=0;assign snapshot=0;assign enc_p=0;assign enc_n=0;assign locked=1;
endmodule
// TRLC-LINKS: REQ-SDS-013
module tb_top_packet;
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
 // The selected decoder's events as the transport would receive them.
 always @(posedge dut.halfclk)if(dut.protocol_valid_sync[1] && dut.selected_event_valid)
  $display("E %0d %0d %08x %0d",dut.selected_event_protocol,dut.selected_event_kind,dut.selected_event_data,dut.frontend.n);
 reg fired=0;
 always @(posedge dut.core)if(dut.uart_fired && !fired)begin fired<=1;$display("T %0d",dut.frontend.n);end
 integer control,ticks,aux,len,cycles,nregs;reg [63:0] pattern;
 reg [31:0] regs[0:511];reg [4095:0] regs_file;
 integer env_k;reg [18:0] env_pos,env_target,env_start,env_skip;reg [31:0] env_word;
 initial begin
  for(cycles=0;cycles<524288;cycles=cycles+1)mem[cycles]=32'hdeadbeef;
  if(!$value$plusargs("control=%h",control) || !$value$plusargs("ticks=%d",ticks))$fatal(1,"missing configuration");
  aux=0;cycles=$value$plusargs("aux=%h",aux);pattern=0;cycles=$value$plusargs("pattern=%h",pattern);
  repeat(20)@(negedge clk);
  check_reg(0,16'h5a52);check_reg(13,16'h000a);check_reg(30,16'h4e44);check_reg(28,0);check_reg(29,8);
  // +manchester selects the Manchester image: no packet block, Manchester only.
  // +manchester (the line image: Manchester, USB) and +serial (the general
  // image's UART, I2C, SPI) select those images; the default is the packet
  // image (ARINC 429, CAN, FlexRay, MIL-1553, SENT).
  if($test$plusargs("manchester"))begin
   check_reg(109,0);check_reg(110,0);check_reg(102,16'h4d01);check_reg(97,16'h5501);check_reg(93,0);check_reg(77,0);
  end else if($test$plusargs("serial"))begin
   check_reg(109,0);check_reg(102,0);check_reg(48,16'h5502);check_reg(53,16'h4901);check_reg(64,16'h5301);check_reg(93,0);check_reg(97,0);
  end else begin
   check_reg(109,16'h5001);check_reg(110,16'h0540);check_reg(102,0);check_reg(93,16'h4d01);check_reg(77,16'h5e01);check_reg(97,0);
  end
  if(!$test$plusargs("serial"))begin check_reg(48,0);check_reg(53,0);check_reg(64,0);end
  check_reg(118,16'h5351);check_reg(119,32);
  check_reg(63,16'h4501);check_reg(73,16'h5201);
  // Mid-scale slicing with the default hysteresis; 2000 words each side.
  write_bus(7,16'h0480);
  if($test$plusargs("manchester"))begin
   // Manchester keeps its own block: control 102, ticks 103/104, pattern 105..108.
   write_bus(103,ticks[15:0]);write_bus(104,ticks[23:16]);
   write_bus(105,pattern[15:0]);write_bus(106,pattern[31:16]);write_bus(107,pattern[47:32]);write_bus(108,pattern[63:48]);
   write_bus(102,control[15:0]);
  end else begin
   write_bus(110,ticks[15:0]);write_bus(111,ticks[23:16]);write_bus(112,aux[15:0]);write_bus(113,aux[31:16]);
   write_bus(114,pattern[15:0]);write_bus(115,pattern[31:16]);write_bus(116,pattern[47:32]);write_bus(117,pattern[63:48]);
   write_bus(109,control[15:0]);
  end
  // +regs lists further "register value" writes (other decoder blocks, the
  // sequence trigger), applied in order before arming.
  if($value$plusargs("regs=%s",regs_file) && $value$plusargs("nregs=%d",nregs))begin
   $readmemh(regs_file,regs,0,2*nregs-1);
   for(cycles=0;cycles<nregs;cycles=cycles+1)write_bus(regs[2*cycles][6:0],regs[2*cycles+1][15:0]);
  end
  write_bus(2,2000);write_bus(3,0);write_bus(4,2000);write_bus(5,0);
  write_bus(6,16'h0011);host_command(1);
  cycles=0;read_bus(1);
  while(!(value[4] && value[2]))begin read_bus(1);cycles=cycles+1;if(cycles>400000)$fatal(1,"no trigger %h",value);end
  if(!value[5])$fatal(1,"record frozen without a trigger");
  read_bus(6);$display("R %0d",value);
  read_bus(120);$display("O %0d",value);
  // +envelope=K: recall 32 buckets of K words from the transport position as an
  // envelope; print the recalled SRAM words and the buffer words.
  if($value$plusargs("envelope=%d",env_k))begin
   check_reg(43,16'h454e);
   // Seek to the record start less the 16 warm-up words, as the driver does.
   read_bus(10);env_target[15:0]=value;read_bus(11);env_target[18:16]=value[2:0];
   read_bus(4);env_start[15:0]=value;read_bus(5);env_start[18:16]=value[2:0];
   env_target=env_target+env_start-19'd16;
   read_bus(8);env_pos[15:0]=value;read_bus(9);env_pos[18:16]=value[2:0];
   if(env_target!=env_pos)begin
    env_skip=env_target-env_pos;write_bus(8,env_skip[15:0]);write_bus(9,env_skip[18:16]);host_command(3);
    cycles=0;read_bus(1);while(!value[2])begin read_bus(1);cycles=cycles+1;if(cycles>100000)$fatal(1,"seek timeout");end
   end
   env_pos=env_target;
   write_bus(43,env_k[15:0]);write_bus(44,16'h8000|env_k[19:16]);write_bus(45,16);
   write_bus(8,(32*env_k+16)&16'hffff);write_bus(9,(32*env_k+16)>>16);host_command(2);
   cycles=0;read_bus(1);while(!value[2])begin read_bus(1);cycles=cycles+1;if(cycles>100000)$fatal(1,"envelope recall timeout");end
   read_bus(46);$display("N %0d",value);
   for(cycles=0;cycles<32*env_k+16;cycles=cycles+1)$display("S %08x",mem[(env_pos+cycles)%524288]);
   for(cycles=0;cycles<32;cycles=cycles+1)begin
    write_bus(16,cycles);read_bus(17);env_word[15:0]=value;read_bus(18);env_word[31:16]=value;$display("B %08x",env_word);
   end
   write_bus(44,0);
  end
  $display("PASS packet top");$finish;
 end
 initial begin #400000000;$fatal(1,"timeout");end
endmodule

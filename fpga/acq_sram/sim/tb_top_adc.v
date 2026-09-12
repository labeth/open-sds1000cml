`timescale 1ns/1ps
module bench_pll(input refclk,output reg c0=0,output c1,output locked,output reg halfclk=0);
 always #2 c0=~c0;initial begin #2;forever #4 halfclk=~halfclk;end assign #1 c1=c0;assign locked=1;
endmodule
module adc_phase_pll(input refclk,output [4:0] phase,output locked);
 assign #3 phase[0]=refclk;assign #4 phase[1]=refclk;assign #2 phase[2]=refclk;assign phase[3]=refclk;assign #1 phase[4]=refclk;assign locked=1;
endmodule
`include "lanemap_seed.vh"
module tb;
 reg clk=0,mclk_in=0;always #6.25 clk=~clk;always #5 mclk_in=~mclk_in;
 wire [79:0] lane;reg [79:0] adc=0;wire [4:0] ep,en;
 wire [15:0] gpmc_d;wire [31:0] dq;wire k1,k2,g1;
 acq_sram_top dut(.clk(clk),.mclk_in(mclk_in),.nCS1(1'b1),.nOE(1'b1),.nWE(1'b1),.sel(5'b0),
  .gpmc_a2(1'b0),.gpmc_b1(1'b0),.gpmc_d(gpmc_d),.dq(dq),.lane(lane),.enc_p(ep),.enc_n(en),.k1(k1),.k2(k2),.g1(g1));
 reg [31:0] mem[0:524287];reg [18:0] address=0;reg [31:0] s1=0,s2=0;
 assign dq=k1&&g1?s2:32'bz;
 always @(posedge k2) if(g1)begin
  if(!k1)mem[address]<=dq;else begin s1<=mem[address];s2<=#4.5 s1; /* Modeled board read-response latency exceeds one 4 ns period. */end
  address<=address+1'b1;
 end
 genvar p,b;
 generate for(p=0;p<5;p=p+1)begin:converter
  localparam CH1_P=(p==1 || p==3 || p==4);
  wire c1=CH1_P?ep[p]:en[p];wire c2=CH1_P?en[p]:ep[p];
  always @(posedge c1)adc[16*p+:8]<=#(4.5+0.3*p) $time;
  always @(posedge c2)adc[16*p+8+:8]<=#(6.0-0.3*p) $time;
  for(b=0;b<16;b=b+1)begin:bit_map
   assign lane[`LANEMAP_ENTRY(16*p+b)]=adc[16*p+b];
  end
 end endgenerate
 integer i,j,last=-1,start_addr,trigger_addr;
 task command(input integer code);
 begin
  @(negedge clk);dut.opcode=code;dut.request=~dut.request;
  wait(dut.ack==dut.request);#100;
  if(dut.command_error)$fatal(1,"command rejected %0d",code);
 end endtask
 initial begin
  #100;dut.config_word=$test$plusargs("normal")?17:1;dut.pre_cfg=2048;dut.post_cfg=2048;#100;command(1);
  wait(dut.record_done && dut.ready);#20;
  if(dut.record_length!=4096 || dut.il_failed)$fatal(1,"ADC metadata/fault");
  start_addr=(dut.origin+dut.record_start)%524288;
  trigger_addr=(start_addr+dut.trigger_index)%524288;
  if($test$plusargs("normal"))begin
   if(!((mem[trigger_addr][7:0]>=128 && mem[(trigger_addr+524287)%524288][23:16]<128) || (mem[trigger_addr][23:16]>=128 && mem[trigger_addr][7:0]<128)))$fatal(1,"trigger word lacks rising edge");
  end
  for(i=0;i<4096;i=i+1)for(j=0;j<4;j=j+1)begin
   if(^mem[(i+start_addr)%524288][8*j+:8]===1'bx)$fatal(1,"undefined ADC byte");
   if(last>=0 && mem[(i+start_addr)%524288][8*j+:8]!==((last+1)%256))$fatal(1,"ADC memory chronology word %0d byte %0d",i,j);
   last=mem[(i+start_addr)%524288][8*j+:8];
  end
  if(!$test$plusargs("recall"))begin $display("PASS integrated ADC capture: chronological bytes, exact trigger length, no FIFO fault");$finish;end
  dut.count_cfg=(start_addr+524288-dut.position)%524288;command(3);wait(dut.ready);#40;
  dut.count_cfg=512;command(2);wait(dut.ready);#40;
  for(i=0;i<512;i=i+1)if((i%2 ? dut.buffer_mem64[i/2][63:32] : dut.buffer_mem64[i/2][31:0])!==mem[(i+start_addr)%524288])$fatal(1,"ADC recall %0d",i);
  $display("PASS integrated ADC to SRAM: chronological bytes, trigger length, no FIFO fault, and host recall");$finish;
 end
 initial begin #30000000;$fatal(1,"timeout");end
endmodule

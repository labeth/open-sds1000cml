// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-041, REQ-SDS-043, REQ-SDS-053, REQ-SDS-054, REQ-SDS-056, REQ-SDS-075, REQ-SDS-076, REQ-SDS-185, REQ-SDS-188
module tb_profile_core;
 reg reset=1,locked=1,core_clk=0,ram_clk=0,host_clk=0,clk100=0;
 always #2 core_clk=~core_clk;always #4 ram_clk=~ram_clk;
 always #5 host_clk=~host_clk;always #5 clk100=~clk100;
 wire sample_clk;assign #0.4 sample_clk=core_clk;
 reg nCS1=1,nOE=1,nWE=1,drive=0;reg [6:0] sel=0;
 reg [15:0] bus_value=0;wire [15:0] gpmc_d;
 assign gpmc_d=drive ? bus_value : 16'hzzzz;
 reg [79:0] lane=80'h12345678;
 wire [31:0] dq;wire k1,k2,g1;wire [4:0] enc_p,enc_n;
 acq_profile_core #(.BUILD_ID(32'h1234abcd)) dut(.*);
 reg [31:0] memory[0:524287],s1=0,s2=0;reg [18:0] address=0;
 assign dq=k1 && g1 ? s2 : 32'bz;
 always @(posedge k2)if(g1)begin
  if(!k1)memory[address]<=dq;else begin s1<=memory[address];s2<=s1;end
  address<=address+1'b1;
 end
 task write_bus(input [6:0] address,input [15:0] value);
  begin
   @(negedge host_clk);sel=address;bus_value=value;drive=1;nCS1=0;nWE=0;
   repeat(8)@(negedge host_clk);nWE=1;repeat(5)@(negedge host_clk);nCS1=1;drive=0;
   repeat(6)@(negedge host_clk);
  end
 endtask
 reg [15:0] value;
 task read_bus(input [6:0] address);
  begin
   @(negedge host_clk);sel=address;nCS1=0;nOE=0;
   repeat(7)@(negedge host_clk);value=gpmc_d;nOE=1;nCS1=1;
   repeat(8)@(negedge host_clk);
  end
 endtask
 task expect_reg(input [6:0] address,input [15:0] expected);
  begin read_bus(address);if(value!==expected)$fatal(1,"reg %h got=%h expected=%h",address,value,expected);end
 endtask
 task command(input [15:0] opcode,input [15:0] result);
  begin
   write_bus(7'h10,opcode);read_bus(7'h11);
   while(value[0])read_bus(7'h11);
   if(!value[2])$fatal(1,"missing command result");
   expect_reg(7'h12,result);
  end
 endtask
 integer i;reg token;
 initial begin
  repeat(10)@(negedge host_clk);reset=0;repeat(20)@(negedge host_clk);
  expect_reg(0,16'hacd2);expect_reg(1,1);expect_reg(2,1);expect_reg(3,11);
  expect_reg(4,5120);expect_reg(5,2560);expect_reg(7,8);expect_reg(10,0);
  expect_reg(11,16'habcd);expect_reg(12,16'h1234);
  command(6,1);command(3,1); // unavailable services explicitly reject
  write_bus(7'h29,16'h400);command(1,1);write_bus(7'h29,0);
  write_bus(7'h22,17);command(1,0);
  command(7,0);read_bus(7'h40);
  while(!value[4] || value[1])begin command(7,0);read_bus(7'h40);end
  if(value[3])$fatal(1,"capture fault");
  expect_reg(7'h42,17);expect_reg(7'h43,0);expect_reg(7'h44,0);
  write_bus(7'h26,17);command(2,0);
  read_bus(7'h34);while(!value[0])read_bus(7'h34);
  if(value[4])$fatal(1,"host fault");token=value[2];
  expect_reg(7'h35,17);expect_reg(7'h38,0);expect_reg(7'h39,0);
  write_bus(7'h30,{1'b0,token,1'b0,13'b0});expect_reg(7'h32,9);
  for(i=0;i<34;i=i+1)expect_reg(7'h31,i%2 ? 16'h1234 : 16'h5678);
  expect_reg(7'h32,10);write_bus(7'h33,1);
  command(7,0);read_bus(7'h40);
  if(!value[2] || value[1] || value[3])$fatal(1,"recall completion status");
  command(2,0);read_bus(7'h34);while(!value[0])read_bus(7'h34);
  token=value[2];write_bus(7'h30,{1'b0,token,1'b0,13'b0});expect_reg(7'h32,9);
  force dut.fault=1'b1;repeat(6)@(negedge host_clk);
  expect_reg(7'h32,4);expect_reg(7'h35,0);expect_reg(7'h31,0);
  command(7,0);read_bus(7'h40);if(!value[3])$fatal(1,"fault snapshot missing");
  release dut.fault;
  $display("PASS profile core: real GPMC commands/results/status, finite capture, physical-width SRAM recall, host readout/release; ADC mocked");
  $finish;
 end
 initial begin #10000000;$fatal(1,"profile integration timeout");end
endmodule

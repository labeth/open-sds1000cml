`timescale 1ns/1ps
module tb_host_read_port;
 reg clk=0,ram_clk=0,reset=1;
 always #5 clk=~clk;always #4 ram_clk=~ram_clk;
 reg ncs=1,noe=1,nwe=1,drive=0;reg [6:0] selector=0;
 reg [15:0] bus_value=0;wire [15:0] bus_data;
 assign bus_data=drive ? bus_value : 16'hzzzz;
 wire host_write,host_pop,read_hit;wire [7:0] write_sel,read_sel;
 wire [15:0] write_data,read_data;
 gpmc_slave #(.QUALIFIED_READ(1)) slave(.clk(clk),.nCS1(ncs),.nOE(noe),.nWE(nwe),
  .sel(selector),.gpmc_d(bus_data),.we_commit(host_write),.wr_sel(write_sel),.wr_data(write_data),
  .wr_aux(),.rd_pop(host_pop),.rd_sel(read_sel),.rdata(read_hit ? read_data : 16'hffff),.drive_active());
 reg [1:0] host_ready=0,host_token=2;
 reg host_fault=0;reg [11:0] host_words0=3,host_words1=2560;
 reg [63:0] host_first0=64'h1122334455667788,host_first1=64'hffeeddccbbaa0099;
 wire host_release,release_bank,release_token,ram_read_enable,ram_read_valid,ram_read_error;
 wire [13:0] ram_read_halfword;wire [15:0] ram_read_data;
 acq_host_read_port dut(.*);
 reg wr=0;reg [11:0] pair=0;reg [63:0] pair_data=0;wire wrfault;
 sram_host_ram ram(ram_clk,reset,wr,pair,pair_data,wrfault,
  clk,reset,ram_read_enable,ram_read_halfword,ram_read_valid,ram_read_error,ram_read_data);
 integer releases=0;
 always @(posedge clk)if(host_release)begin
  if(!host_ready[release_bank] || release_token!=host_token[release_bank])$fatal(1,"release token");
  host_ready[release_bank]<=0;releases=releases+1;
 end
 function [15:0] pattern(input integer address);pattern=16'hac31 ^ (address*16'h631d);endfunction
 task write_bus(input [6:0] address,input [15:0] value);
  begin
   @(negedge clk);selector=address;bus_value=value;drive=1;ncs=0;nwe=0;
   repeat(8)@(negedge clk);nwe=1;repeat(5)@(negedge clk);ncs=1;drive=0;
   repeat(6)@(negedge clk);
  end
 endtask
 task read_bus(input [6:0] address,input [15:0] value);
  begin
   @(negedge clk);selector=address;ncs=0;noe=0;
   repeat(7)begin @(negedge clk);if(bus_data!==value)$fatal(1,"read %h got=%h expected=%h",address,bus_data,value);end
   noe=1;ncs=1;#0.1;if(bus_data!==16'hzzzz)$fatal(1,"bus release");
   repeat(8)@(negedge clk);
  end
 endtask
 integer i;
 initial begin
  repeat(8)@(negedge clk);reset=0;
  for(i=0;i<2560;i=i+1)begin
   @(negedge ram_clk);wr=1;pair=i;
   pair_data={pattern(4*i+3),pattern(4*i+2),pattern(4*i+1),pattern(4*i)};
  end
  @(negedge ram_clk);wr=0;host_ready=3;
  read_bus(7'h34,11);read_bus(7'h35,3);read_bus(7'h36,2560);
  read_bus(7'h38,16'h7788);read_bus(7'h39,16'h5566);
  read_bus(7'h3a,16'h3344);read_bus(7'h3b,16'h1122);
  read_bus(7'h3c,16'h0099);read_bus(7'h3f,16'hffee);
  write_bus(7'h30,0);read_bus(7'h32,9);read_bus(7'h30,0);
  for(i=0;i<6;i=i+1)read_bus(7'h31,pattern(i));
  read_bus(7'h32,10);read_bus(7'h30,6);
  write_bus(7'h33,1);read_bus(7'h35,0);read_bus(7'h38,0);
  if(releases!=1)$fatal(1,"release count");
  write_bus(7'h30,16'h6000+5119);read_bus(7'h31,pattern(10239));
  read_bus(7'h32,10);read_bus(7'h30,5120);
  // Reserved control bits must not release or silently truncate selection.
  write_bus(7'h33,5);read_bus(7'h32,12);
  if(releases!=1)$fatal(1,"malformed release accepted");
  write_bus(7'h33,2);write_bus(7'h30,16'h8000);read_bus(7'h32,12);
  read_bus(7'h30,5120);write_bus(7'h33,3);
  if(releases!=2)$fatal(1,"clear and release");
  read_bus(7'h36,0);read_bus(7'h70,16'hffff);
  if(wrfault)$fatal(1,"RAM write fault");
  $display("PASS host read port: GPMC selection/read/pop/release, descriptors, final address, malformed writes, unclaimed registers");
  $finish;
 end
 initial begin #1000000;$fatal(1,"host read port timeout");end
endmodule

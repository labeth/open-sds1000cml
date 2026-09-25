// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-094
module tb_host_sink;
 reg clk=0,rclk=0,reset=1,valid=0,upstream_fault=0;
 always #4 clk=~clk;
 always #5 rclk=~rclk;
 reg [79:0] data=0;
 wire ready,wr,rf,fault;wire [11:0] addr,w0,w1;
 wire [63:0] wd,f0,f1;wire [1:0] pub;
 reg ren=0;reg [13:0] ra=0;wire rv,re;wire [15:0] rd;
 sram_host_sink dut(clk,reset,upstream_fault,valid,data,ready,wr,addr,wd,rf,fault,pub,f0,f1,w0,w1);
 sram_host_ram ram(clk,reset,wr,addr,wd,rf,rclk,reset,ren,ra,rv,re,rd);
 integer published=0,writes=0,i;
 always @(posedge clk)if(wr)writes=writes+1;
 always @(negedge clk)if(pub!=0)published=published+1;
 function [63:0] payload(input integer index);
  payload=64'hcdef89ab45670123 ^ (64'h0001000100010001*index);
 endfunction
 task send(input bit meta,input bit bank,input integer field,input [63:0] value);
  begin
   @(negedge clk);valid=1;data={meta,bank,14'(field),value};
   @(posedge clk);#1;
   @(negedge clk);valid=0;
  end
 endtask
 task clear_epoch;
  begin @(negedge clk);reset=1;valid=0;upstream_fault=0;
   repeat(3)@(negedge clk);reset=0;repeat(2)@(negedge clk);
   if(fault || pub)$fatal(1,"reset failed");
  end
 endtask
 task read_check(input integer address,input [15:0] expected);
  begin
   @(negedge rclk);ra=address;ren=1;
   @(negedge rclk);ren=0;
   @(posedge rclk);#1;
   if(!rv || re || rd!==expected)$fatal(1,"RAM mismatch addr=%0d got=%h expected=%h",address,rd,expected);
  end
 endtask
 reg [63:0] value;integer before_pub;
 initial begin
  clear_epoch;
  for(i=0;i<1280;i=i+1)begin
   send(0,0,i,payload(i));
   if(pub || published)$fatal(1,"early publication");
  end
  send(1,0,2560,64'h123456789abcdef0);
  if(pub!==1 || w0!=2560 || f0!=64'h123456789abcdef0 || writes!=1280)$fatal(1,"full descriptor");
  for(i=0;i<1280;i=i+1)begin
   value=payload(i);
   read_check(i*4,value[15:0]);read_check(i*4+1,value[31:16]);
   read_check(i*4+2,value[47:32]);read_check(i*4+3,value[63:48]);
  end
  send(0,1,1280,64'h00000000deadbeef);
  send(1,1,1,64'd2560);
  if(pub!==2 || w1!=1 || f1!=2560)$fatal(1,"odd descriptor");
  read_check(5120,16'hbeef);read_check(5121,16'hdead);
  read_check(5122,0);read_check(5123,0);
  // Next epoch: malformed length must never expose partial data.
  clear_epoch;send(0,0,0,payload(0));send(1,0,3,0);
  if(!fault || pub || ready)$fatal(1,"bad length accepted");
  clear_epoch;send(0,1,0,0);
  if(!fault || wr || pub)$fatal(1,"cross-bank address accepted");
  clear_epoch;send(0,0,0,0);send(0,0,0,0);
  if(!fault || pub)$fatal(1,"duplicate accepted");
  clear_epoch;send(1,0,0,0);
  if(!fault || pub)$fatal(1,"empty descriptor accepted");
  clear_epoch;upstream_fault=1;repeat(2)@(negedge clk);
  if(!fault || ready)$fatal(1,"upstream fault ignored");
  $display("PASS host sink: full bank RAM readback, odd tail, ordering, malformed descriptors, fault/reset");$finish;
 end
 initial begin #2000000;$fatal(1,"timeout");end
endmodule

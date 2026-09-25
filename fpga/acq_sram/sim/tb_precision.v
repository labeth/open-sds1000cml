// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-040
module tb;
 reg core=0,packclk=0,clk100=0,enable=0;initial begin #2;forever #4 packclk=~packclk;end
 always #2 core=~core;always #5 clk100=~clk100;
 reg [4:0] log=4;reg [31:0] raw={8'd101,8'd40,8'd100,8'd40};
 wire [31:0] data;wire valid,fault;integer count=0;integer l;
 adc_precision dut(core,packclk,clk100,enable,log,raw,enable,data,valid,fault);
 always @(posedge core)if(valid)begin
  count=count+1;
  // Preserve a half-code mean on CH2; CH1 is an independent exact DC.
  if(data[15:0]!==16'd10240 || data[31:16]!==16'd25728)$fatal(1,"DC/fraction log=%d count=%d data=%h",log,count,data);
 end
 initial begin
  for(l=4;l<=12;l=l+1)begin
   enable=0;#200;log=l;count=0;enable=1;
   wait(count>=20);if(fault)$fatal(1,"FIFO fault");
   $display("PASS precision /%0d: DC unity, half-code retained, rearm, both channels",1<<l);
  end
  enable=0;#100;$finish;
 end
 initial begin #20000000;$fatal(1,"timeout");end
endmodule

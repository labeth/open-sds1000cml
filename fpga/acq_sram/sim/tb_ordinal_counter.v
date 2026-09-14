`timescale 1ns/1ps
module tb;
 reg clk=0;always #2 clk=~clk;
 reg clear=1,step=0;wire [63:0] value;
 reg [63:0] expected=0;
 integer i,k;
 sram_ordinal_counter dut(.*);
 always @(posedge clk)begin
  if(clear)expected=0;else if(step)expected=expected+1'b1;
  #0.5;if(value!==expected)$fatal(1,"ordinal mismatch %h expected %h",value,expected);
 end
 task seed(input [63:0] base);
 begin
  @(negedge clk);clear=0;step=0;expected=base;
  // Seed below a carry boundary, then exercise actual carry prediction and
  // increment logic across it, including stalled clocks.
  dut.value=base;
  for(k=0;k<4;k=k+1)dut.full[k]=base[16*k+:16]==16'hffff;
  for(i=0;i<24;i=i+1)begin @(negedge clk);step=i%3!=0;end
  @(negedge clk);step=0;clear=1;@(negedge clk);
 end endtask
 initial begin
  seed(64'hfd);seed(64'hfffd);seed(64'hfffffd);seed(64'hfffffffd);
  seed(64'hfffffffffd);seed(64'hfffffffffffd);seed(64'hfffffffffffffd);seed(64'hfffffffffffffffd);
  $display("PASS 64-bit ordinals across all carry boundaries and stalled steps");$finish;
 end
endmodule

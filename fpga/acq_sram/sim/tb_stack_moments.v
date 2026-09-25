// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-141
module tb_stack_moments #(parameter COUNT_BITS=32);
 reg clk=0;always #4 clk=~clk;
 reg reset=1,start=0,valid=0,last=0;
 reg [7:0] x=0,y=0;
 wire ready,busy,done,overflow;
 wire [COUNT_BITS-1:0] count;
 wire [COUNT_BITS+7:0] sum_x,sum_y;
 wire [COUNT_BITS+15:0] sum_xx,sum_yy,sum_xy;
 stack_moments #(.COUNT_BITS(COUNT_BITS)) dut(.*);
 reg [19:0] commands[0:262143];reg [4095:0] input_file;
 reg previous_overflow=0;
 integer length,i;
 always @(posedge clk)begin
  #1;
  if(done)begin
   if(busy || overflow)$fatal(1,"invalid result qualification");
   $display("D %0d %0d %0d %0d %0d %0d",count,sum_x,sum_y,sum_xx,sum_yy,sum_xy);
  end
  if(overflow && !previous_overflow)$display("O");
  previous_overflow=overflow;
 end
 initial begin
  if(!$value$plusargs("input=%s",input_file) || !$value$plusargs("length=%d",length))$fatal(1,"missing fixture");
  $readmemh(input_file,commands,0,length-1);
  repeat(4)@(negedge clk);reset=0;
  for(i=0;i<length;i=i+1)begin
   {reset,start,last,valid,y,x}=commands[i];@(negedge clk);
  end
  reset=0;start=0;valid=0;last=0;repeat(5)@(negedge clk);$finish(0);
 end
 initial begin #4000000;$fatal(1,"timeout");end
endmodule

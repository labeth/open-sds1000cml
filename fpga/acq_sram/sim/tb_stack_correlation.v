// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-141
module tb_stack_correlation #(parameter FROM_SAMPLES=0,COUNT_BITS=32);
 reg clk=0;always #4 clk=~clk;
 reg reset=1,start=0,valid=0,last=0;reg [7:0] x=0,y=0;
 reg [31:0] count=0;reg [39:0] sum_x=0,sum_y=0;
 reg [47:0] sum_xx=0,sum_yy=0,sum_xy=0;
 wire busy,done,defined,invalid;wire signed [49:0] score;
 wire ready,overflow;
 generate if(FROM_SAMPLES)begin
  stack_match_score dut(.clk(clk),.reset(reset),.start(start),.valid(valid),.last(last),.x(x),.y(y),
   .ready(ready),.defined(defined),.invalid(invalid),.overflow(overflow),.busy(busy),.done(done),.score(score));
 end else begin
  stack_correlation #(.COUNT_BITS(COUNT_BITS)) dut(.clk(clk),.reset(reset),.start(start),.count(count),.sum_x(sum_x),.sum_y(sum_y),
   .sum_xx(sum_xx),.sum_yy(sum_yy),.sum_xy(sum_xy),.busy(busy),.done(done),.defined(defined),.invalid(invalid),.score(score));
  assign ready=0;assign overflow=0;
 end endgenerate
 reg [255:0] moments[0:1023];reg [19:0] commands[0:524287];
 reg [4095:0] input_file;integer length,i,elapsed=0,cycles=0;
 always @(posedge clk)begin
  if(reset || start)cycles=0;else cycles=cycles+1;
  #1;
  if(done)begin
   if(busy)$fatal(1,"busy result");
   $display("C %0d %0d %0d %0d",defined,invalid,score,cycles);
  end
 end
 initial begin
  if(!$value$plusargs("input=%s",input_file) || !$value$plusargs("length=%d",length))$fatal(1,"missing fixture");
  if(FROM_SAMPLES)$readmemh(input_file,commands,0,length-1);
  else $readmemh(input_file,moments,0,length-1);
  repeat(4)@(negedge clk);reset=0;
  for(i=0;i<length;i=i+1)begin
   if(FROM_SAMPLES)begin
    {reset,start,last,valid,y,x}=commands[i];@(negedge clk);
   end else begin
    {count,sum_x,sum_y,sum_xx,sum_yy,sum_xy}=moments[i];start=1;
    @(negedge clk);start=0;elapsed=0;
    while(!done)begin @(negedge clk);elapsed=elapsed+1;if(elapsed>3000)$fatal(1,"score latency exceeded");end
   end
  end
  start=0;reset=0;valid=0;repeat(5)@(negedge clk);$finish(0);
 end
 initial begin #5000000;$fatal(1,"timeout");end
endmodule

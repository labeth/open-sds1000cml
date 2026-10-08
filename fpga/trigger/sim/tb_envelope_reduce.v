// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// Streams "word gap" lines into envelope_reduce two words per packet and
// prints each buffer entry written as "E address high low" and the word count.
// TRLC-LINKS: REQ-SDS-010
module tb_envelope_reduce;
 reg clk=0;always #4 clk=~clk;
 reg start=0,q8=0,in_valid=0;reg [18:0] bucket=1;reg [3:0] skip=0;reg [63:0] in_data=0;
 wire out_write;wire [10:0] out_addr;wire [63:0] out_data;wire [11:0] words;
 envelope_reduce dut(.*);
 reg [31:0] lines[0:2*262143];reg [4095:0] file;
 integer n,i,ignored,b=1,s=0,q=0;
 always @(posedge clk)begin
  #1;
  if(out_write)$display("E %0d %08x %08x",out_addr,out_data[63:32],out_data[31:0]);
 end
 initial begin
  if(!$value$plusargs("input=%s",file) || !$value$plusargs("n=%d",n))$fatal(1,"missing input");
  ignored=$value$plusargs("bucket=%d",b);ignored=$value$plusargs("skip=%d",s);ignored=$value$plusargs("q8=%d",q);
  bucket=b;skip=s;q8=q!=0;
  $readmemh(file,lines,0,2*n-1);
  @(negedge clk);start=1;@(negedge clk);start=0;
  for(i=0;i+1<n;i=i+2)begin
   in_valid=1;in_data={lines[2*i+2],lines[2*i]};@(negedge clk);in_valid=0;
   repeat(lines[2*i+1])@(negedge clk);
  end
  repeat(8)@(negedge clk);$display("N %0d",words);$finish(0);
 end
endmodule

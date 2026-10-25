// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// box_decim: each channel's 2^log samples (two a raw word) averaged into
// one unsigned Q8.8 word {C2, C1}, at the shortest sum (/16, eight words)
// and longer ones to the longest (/2^20), with the raw words' valid gapped;
// no fault.
// TRLC-LINKS: REQ-SDS-040
module tb_box_decim;
 reg core=0;always #2 core=~core;
 reg enable=0,raw_valid=0;reg [4:0] log=4;reg [31:0] raw=0;
 wire [31:0] data;wire valid,fault;
 box_decim dut(.core(core),.packclk(1'b0),.clk100(1'b0),.enable(enable),.decim_log(log),.raw(raw),.raw_valid(raw_valid),
  .data(data),.valid(valid),.fault(fault));
 integer n=0,got=0,errors=0,k,sample=0;
 reg [63:0] sum1=0,sum2=0; // (sum << 8) of 2^20 samples overflows 32 bits
 reg [31:0] want[0:63];
 // C1 sample i: (i*7) mod 256, C2: 255-(i*3 mod 256)
 function [7:0] c1(input integer i);c1=(i*7)%256;endfunction
 function [7:0] c2(input integer i);c2=255-((i*3)%256);endfunction
 always @(posedge core)if(valid)begin
  if(data!==want[got])begin errors=errors+1;$display("FAIL out %0d: %h want %h",got,data,want[got]);end
  got=got+1;
 end
 always @(posedge core)if(fault)begin errors=errors+1;$display("FAIL fault");end
 task run(input [4:0] l,input integer outs,input gapped);
  integer w,s,o;
  begin
   @(negedge core);enable=0;log=l;repeat(4)@(negedge core);
   got=0;sample=0;
   for(o=0;o<outs;o=o+1)begin
    sum1=0;sum2=0;
    for(s=0;s<(1<<l);s=s+1)begin sum1=sum1+c1(sample+s);sum2=sum2+c2(sample+s);end
    sample=sample+(1<<l);
    want[o][15:0]=(sum1<<8)>>l;want[o][31:16]=(sum2<<8)>>l;
   end
   enable=1;repeat(3)@(negedge core);
   sample=0;
   for(w=0;w<outs*(1<<(l-1));w=w+1)begin
    if(gapped && w%3==1)begin raw_valid=0;@(negedge core);end
    raw={c2(sample+1),c1(sample+1),c2(sample),c1(sample)};raw_valid=1;sample=sample+2;@(negedge core);
   end
   // the word after a sum's last closes it (the stream is continuous)
   raw={c2(sample+1),c1(sample+1),c2(sample),c1(sample)};raw_valid=1;@(negedge core);
   raw_valid=0;repeat(40)@(negedge core);
   if(got!=outs)begin errors=errors+1;$display("FAIL log %0d: %0d outputs, want %0d",l,got,outs);end
  end
 endtask
 initial begin
  run(4,8,0);run(4,6,1);run(5,4,0);run(8,3,1);run(11,2,0);run(14,2,1);run(20,1,0);
  if(errors==0)$display("PASS box_decim");else $display("FAIL box_decim %0d",errors);
  $finish;
 end
endmodule

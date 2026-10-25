// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// pick_decim: every 2^(log-1)th raw word's first sample per channel, from
// the first word, as unsigned Q8.8 {C2, C1} with the fraction 0, at /16
// and longer decimations past the count's low half, the raw words' valid
// gapped.
// TRLC-LINKS: REQ-SDS-040
module tb_pick_decim;
 reg core=0;always #2 core=~core;
 reg enable=0,raw_valid=0;reg [4:0] log=4;reg [31:0] raw=0;
 wire [31:0] data;wire valid,fault;
 pick_decim dut(.core(core),.packclk(1'b0),.clk100(1'b0),.enable(enable),.decim_log(log),.raw(raw),.raw_valid(raw_valid),
  .data(data),.valid(valid),.fault(fault));
 integer got=0,errors=0,want_n=0,n;
 reg [31:0] want[0:63];
 // word w: C1 (w*7) mod 256, C2 255-(w*3 mod 256) in its first sample
 function [7:0] c1(input integer w);c1=(w*7)%256;endfunction
 function [7:0] c2(input integer w);c2=255-((w*3)%256);endfunction
 always @(posedge core)if(valid)begin
  if(got>=want_n || data!==want[got])begin errors=errors+1;$display("FAIL out %0d: %h want %h",got,data,want[got]);end
  got=got+1;
 end
 task run(input [4:0] l,input integer outs,input gapped);
  integer w,o;
  begin
   @(negedge core);enable=0;log=l;repeat(4)@(negedge core);
   got=0;want_n=outs;
   for(o=0;o<outs;o=o+1)want[o]={c2(o<<(l-1)),8'd0,c1(o<<(l-1)),8'd0};
   enable=1;repeat(3)@(negedge core);
   for(w=0;w<((outs-1)<<(l-1))+1;w=w+1)begin
    if(gapped && w%3==1)begin raw_valid=0;@(negedge core);end
    raw={8'h55,8'haa,c2(w),c1(w)};raw_valid=1;@(negedge core);
   end
   raw_valid=0;repeat(10)@(negedge core);
   if(got!=outs)begin errors=errors+1;$display("FAIL log %0d: %0d outputs, want %0d",l,got,outs);end
  end
 endtask
 initial begin
  run(4,9,0);run(4,6,1);run(5,4,1);run(12,3,0);
  if(errors==0)$display("PASS pick_decim");else $display("FAIL pick_decim %0d",errors);
  $finish;
 end
endmodule

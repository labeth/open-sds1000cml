// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-188
module tb;
 reg clk=0; always #6.25 clk=~clk;
 reg cs=1,oe=1; reg [6:0] sel=25;
 wire [15:0] d,old_d; wire pop,old_pop; wire [7:0] rs;
 integer count=0,old_count=0,i,j,expected=0;
 gpmc_slave #(.QUALIFIED_READ(1)) dut(.clk(clk),.nCS1(cs),.nOE(oe),.nWE(1'b1),.sel(sel),.gpmc_d(d),.rd_pop(pop),.rd_sel(rs),.rdata(16'hcafe));
 gpmc_slave legacy(.clk(clk),.nCS1(cs),.nOE(oe),.nWE(1'b1),.sel(sel),.gpmc_d(old_d),.rd_pop(old_pop),.rdata(16'hcafe));
 always @(posedge clk)begin
  if(pop)begin count=count+1;if(rs!=25)$fatal(1,"lost read selector");end
  if(old_pop)old_count=old_count+1;
 end
 initial begin
  #100;
  // Sweep relative clock phase and independently releasing CS/OE edges.
  for(i=0;i<25;i=i+1)for(j=-10;j<=10;j=j+1)begin
   #(0.5*i);cs=0;#40;oe=0;#60;
   if(d!==16'hcafe)$fatal(1,"read data");
   if(j<0)begin cs=1;#(-j);oe=1;end
   else begin oe=1;#(j);cs=1;end
   expected=expected+1;#100;
   if(count!=expected)$fatal(1,"missing/duplicate pop phase=%0d skew=%0d count=%0d want=%0d",i,j,count,expected);
  end
  // OE activity on another chip select must never consume our FIFO.
  repeat(20)begin oe=0;#80;oe=1;#100;end
  if(count!=expected)$fatal(1,"unselected pop");
  if(old_count>=expected)$fatal(1,"test did not exercise legacy defect");
  $display("PASS qualified reads %0d/%0d; legacy counted %0d",count,expected,old_count);$finish;
 end
endmodule

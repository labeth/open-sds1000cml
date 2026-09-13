`timescale 1ns/1ps
// Independent direct FIR oracle for the first CIC: convolve three length-16
// boxcars, then decimate. This checks varying data, overflow wrap and order;
// it does not reproduce the RTL integrator/comb recurrence.
module tb;
 reg clk=0,enable=0;always #2 clk=~clk;
 reg [7:0] a=0,b=0;wire valid;wire [15:0] q;
 cic16_pair dut(clk,enable,enable,a,b,valid,q);
 integer coeff[0:45],hist[0:45],expect_q[0:4095];
 integer i,j,k,n=0,writes=0,reads=8,checked=0;reg [31:0] rng=32'h73ad182f;
 reg signed [63:0] sum;
 task push(input integer x);
 begin
  for(j=45;j>0;j=j-1)hist[j]=hist[j-1];hist[0]=x;
  n=n+1;
  if(n%16==0)begin
   sum=0;for(j=0;j<46;j=j+1)sum=sum+coeff[j]*hist[j];
   expect_q[writes]=(sum*256)/4096;writes=writes+1;
  end
 end endtask
 always @(negedge clk)begin
  rng={rng[30:0],rng[31]^rng[21]^rng[1]^rng[0]};a=rng[7:0];b=rng[23:16];
 end
 always @(posedge clk)begin
  if(dut.i1.local_enable && enable)begin push(a);push(b);end
  #0.1;
  if(valid)begin
   if(q!==expect_q[reads])$fatal(1,"FIR mismatch output %0d: got %0d expected %0d",reads,q,expect_q[reads]);
   reads=reads+1;checked=checked+1;
   if(checked==500)begin $display("PASS 500 independent FIR outputs: random full-range input, CIC3/16 Q8");$finish;end
  end
 end
 initial begin
  for(i=0;i<46;i=i+1)begin coeff[i]=0;hist[i]=0;end
  for(i=0;i<16;i=i+1)for(j=0;j<16;j=j+1)for(k=0;k<16;k=k+1)coeff[i+j+k]=coeff[i+j+k]+1;
  #9;enable=1;
  #100000;$fatal(1,"timeout");
 end
endmodule

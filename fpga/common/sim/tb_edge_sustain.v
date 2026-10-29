// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// sram_edge_pair with sustain against a sample-by-sample model: a flat line
// whose interleaved converters sit a few codes either side of the level never
// fires; a real crossing fires on its fifth sample at or past the level; and
// random walks with chatter and invalid words fire on exactly the model's
// cycles.
// TRLC-LINKS: REQ-SDS-039, REQ-SDS-042
module tb_edge_sustain;
 reg clk=0;always #2 clk=~clk;
 reg enable=0,valid=0;reg [7:0] first=0,second=0,level=128,low_level=124,high_level=132;
 wire rise,fall;
 sram_edge_pair #(.PIPELINED(1)) dut(.clk(clk),.enable(enable),.valid(valid),.first(first),.second(second),
  .level(level),.low_level(low_level),.high_level(high_level),.sustain(1'b1),.rise(rise),.fall(fall));
 // the model: one sample at a time
 integer up_armed=0,up_run=0,dn_armed=0,dn_run=0;
 // TRLC-LINKS: REQ-SDS-039, REQ-SDS-042
 task model_sample(input [7:0] s,output r,output f);
 begin
  r=0;f=0;
  if(up_run!=0)begin
   if(s<level)begin up_run=0;up_armed=1;end
   else if(up_run+1>=5)begin r=1;up_run=0;up_armed=0;end
   else up_run=up_run+1;
  end else if(s<level)begin if(s<=low_level)up_armed=1;end
  else if(up_armed)begin up_run=1;up_armed=0;end
  if(dn_run!=0)begin
   if(s>level)begin dn_run=0;dn_armed=1;end
   else if(dn_run+1>=5)begin f=1;dn_run=0;dn_armed=0;end
   else dn_run=dn_run+1;
  end else if(s>level)begin if(s>=high_level)dn_armed=1;end
  else if(dn_armed)begin dn_run=1;dn_armed=0;end
 end endtask
 // expected outputs, on the third clock after the word (two compare stages, the decision)
 reg exp_r[0:3],exp_f[0:3];integer k;
 initial for(k=0;k<4;k=k+1)begin exp_r[k]=0;exp_f[k]=0;end
 integer fires=0,checked=0;
 // TRLC-LINKS: REQ-SDS-039, REQ-SDS-042
 task word(input [7:0] a,b,input v);
 reg r1,f1,r2,f2;
 begin
  @(negedge clk);first=a;second=b;valid=v;
  r1=0;f1=0;r2=0;f2=0;
  if(v && enable)begin model_sample(a,r1,f1);model_sample(b,r2,f2);end
  exp_r[2]=r1||r2;exp_f[2]=f1||f2;
 end endtask
 always @(posedge clk)begin
  #1;
  if(enable)begin
   if(rise!==exp_r[0] || fall!==exp_f[0])$fatal(1,"sustain rise/fall=%b%b expected=%b%b at check %0d",rise,fall,exp_r[0],exp_f[0],checked);
   fires=fires+rise+fall;checked=checked+1;
  end
  for(k=0;k<3;k=k+1)begin exp_r[k]=exp_r[k+1];exp_f[k]=exp_f[k+1];end
  exp_r[3]=0;exp_f[3]=0;
 end
 integer i,epoch,base,spread;reg [31:0] rng=32'h9e3779b9;reg [7:0] s0,s1;integer flat_fires;
 // TRLC-LINKS: REQ-SDS-039, REQ-SDS-042
 task restart(input [7:0] l,lo,hi);
 begin
  @(negedge clk);enable=0;valid=0;level=l;low_level=lo;high_level=hi;
  repeat(4)@(negedge clk);
  for(k=0;k<4;k=k+1)begin exp_r[k]=0;exp_f[k]=0;end
  up_armed=0;up_run=0;dn_armed=0;dn_run=0;
  enable=1;
 end endtask
 initial begin
  // a flat top 4 codes under the level, five converters spread over 11 codes
  restart(140,126,154);flat_fires=fires;
  word(100,100,1);word(100,100,1); // armed on the bottom
  for(i=0;i<4000;i=i+1)word(136+((2*i)%5)*2-3,136+((2*i+1)%5)*2-3,1); // 133..141
  repeat(4)word(136,136,0);
  if(fires!=flat_fires)$fatal(1,"converter spread past the level fired %0d",fires-flat_fires);
  // a real crossing fires on the fifth sample past the level
  restart(128,124,132);
  word(100,100,1);word(130,130,1);word(130,140,1);word(140,140,1);
  repeat(4)word(0,0,0);
  if(fires!=1)$fatal(1,"one rising crossing fired %0d",fires);
  // random walks with converter offsets, chatter, invalid words
  for(epoch=0;epoch<64;epoch=epoch+1)begin
   restart(16+epoch*3,16+epoch*3-(epoch%9),16+epoch*3+(epoch%9));
   base=16+epoch*3;spread=epoch%13;
   for(i=0;i<3000;i=i+1)begin
    rng=rng^(rng<<13);rng=rng^(rng>>17);rng=rng^(rng<<5);
    if(rng[31:28]==0)base=rng[7:0];
    else if(rng[27])base=base+rng[2:0];else base=base-rng[2:0];
    if(base<0)base=0;if(base>255-spread)base=255-spread;
    s0=base+((2*i)%5)*spread/4;s1=base+((2*i+1)%5)*spread/4;
    word(s0,s1,rng[20:18]!=0);
   end
  end
  @(negedge clk);valid=0;repeat(4)@(negedge clk);
  $display("PASS edge sustain: flat converter spread 0 fires, %0d fires on the model's cycles over %0d checks",fires,checked);$finish;
 end
endmodule

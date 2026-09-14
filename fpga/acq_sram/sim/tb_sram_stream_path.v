`timescale 1ns/1ps
module tb_stream_path;
 parameter AW=13,TARGET=10003;
 localparam N=1<<AW;
 reg c=0,m=0,h=0;always #2 c=~c;always #4 m=~m;always #5 h=~h;
 wire sample;assign #0.4 sample=c;
 reg reset=1,start=0,stop=0,finished=0,valid=0;
 reg [35:0] data=0;
 wire source_ready,start_ready,enable,active,done,captured,fault,hfault;
 wire [3:0] error;wire [63:0] committed,recalled;wire [AW:0] unread;
 wire [1:0] busy,ready,token;wire [63:0] first0,first1;wire [11:0] count0,count1;
 reg release_en=0,release_bank=0,release_token=0,ren=0;
 reg [13:0] raddr=0;wire rv,re;wire [15:0] rdata;
 wire [31:0] dq;wire k1,k2,g1;
 sram_stream_path #(.AW(AW)) dut(reset,1'b1,c,sample,m,h,start,stop,finished,{AW{1'b0}},
  valid,1'b0,data,source_ready,start_ready,enable,active,done,captured,fault,error,committed,recalled,unread,busy,
  release_en,release_bank,release_token,hfault,ready,token,first0,first1,count0,count1,ren,raddr,rv,re,rdata,dq,k1,k2,g1);
 reg [31:0] memory[0:N-1],stage1=0,stage2=0;
 reg [AW-1:0] address=0;
 integer produced=0,written=0,received=0,blocks=0,cycles=0;
 assign dq=k1 && g1 ? stage2 : 32'bz;
 always @(posedge k2)if(g1)begin
  if(!k1)begin
   if(address!=written%N || dq!==written)$fatal(1,"SRAM write order/address");
   if(written-recalled>=N)$fatal(1,"unread overwrite");
   memory[address]<=dq;written=written+1;
  end else begin stage1<=memory[address];stage2<=stage1;end
  address<=address+1'b1;
 end
 always @(negedge c)begin
  valid=!reset && enable && produced<TARGET && cycles%128==0;
  data={4'(produced),32'(produced)};
  stop=produced>=TARGET;
  finished=stop && !valid;
 end
 always @(posedge c)begin
  cycles<=cycles+1;
  if(valid)begin if(!source_ready)$fatal(1,"source overflow");produced=produced+1;end
  if(!reset && (fault || hfault))$fatal(1,"stream fault %0d",error);
 end
 task halfword(input integer addr,output reg [15:0] value);
  begin @(negedge h);raddr=addr;ren=1;
   @(negedge h);ren=0;@(posedge h);#0.1;
   if(!rv || re)$fatal(1,"host read response");value=rdata;
  end
 endtask
 integer bank,count,i;reg [15:0] lo,hi;
 initial begin
  wait(!reset);
  forever begin
   @(negedge h);
   if((ready[0] && first0==received) || (ready[1] && first1==received))begin
    bank=ready[0] && first0==received ? 0 : 1;
    count=bank ? count1 : count0;
    if(count<1 || count>2560)$fatal(1,"descriptor count");
    for(i=0;i<count;i=i+1)begin
     if(!ready[bank])$fatal(1,"lost ownership");
     halfword(bank*5120+i*2,lo);halfword(bank*5120+i*2+1,hi);
     if({hi,lo}!==received)$fatal(1,"sample order");received=received+1;
     repeat(36)@(negedge h);
    end
    @(negedge h);release_en=1;release_bank=bank;release_token=token[bank];
    @(negedge h);release_en=0;blocks=blocks+1;
   end
  end
 end
 initial begin
  repeat(10)@(negedge c);reset=0;
  wait(start_ready);@(negedge c);start=1;@(negedge c);start=0;
  wait(done && received==TARGET && busy==0);repeat(4)@(negedge c);
  if(produced!=TARGET || written!=TARGET || committed!=TARGET || recalled!=TARGET || unread!=0)$fatal(1,"final accounting");
  $display("PASS stream wrapper: %0d words, %0d banks, AW=%0d, actual SRAM pins and host reads",received,blocks,AW);$finish;
 end
 initial begin #100000000;$fatal(1,"timeout");end
endmodule

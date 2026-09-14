`timescale 1ns/1ps
module tb_host_path;
 parameter PHASE=0;
 reg clk=0,mclk=0,rclk=0,reset=1;
 always #2 clk=~clk;
 initial begin #(PHASE);forever #4 mclk=~mclk;end
 always #5 rclk=~rclk;
 reg valid=0,bank=0;reg [31:0] data=0;reg [11:0] index=0;
 reg [1:0] done=0;reg [63:0] first0=0,first1=2560;
 reg [19:0] words0=2560,words1=2560;
 wire push,pfault,fr,overflow,fv,ready;wire [79:0] packet,fd;
 wire wr,rf,sfault;wire [11:0] addr,w0,w1;wire [63:0] wd,f0,f1;wire [1:0] pub;
 reg ren=0;reg [13:0] ra=0;wire rv,re;wire [15:0] rd;
 reg release_en=0,release_bank=0,release_token=0;
 wire ofault;wire [1:0] busy,host_ready,host_token,core_release;
 wire [63:0] hf0,hf1;wire [11:0] hw0,hw1;
 wire core_fault,host_fault;
 sram_host_path path(reset,clk,mclk,rclk,valid,bank,data,index,done,first0,first1,words0,words1,
  core_fault,host_fault,core_release,release_en,release_bank,release_token,
  host_ready,host_token,hf0,hf1,hw0,hw1,ren,ra,rv,re,rd);
 assign wr=path.ram_write;assign addr=path.ram_pair;
 assign pub=path.publish;assign w0=path.count0;assign w1=path.count1;
 assign f0=path.base0;assign f1=path.base1;assign busy=path.busy;
 assign pfault=path.pack_fault;assign overflow=path.overflow;
 assign sfault=path.sink_fault;assign rf=path.ram_fault;assign ofault=path.ownership_fault;
 integer releases=0;
 always @(posedge clk)if(!reset)begin
  if(|(core_release & busy))$fatal(1,"release before RAM acknowledgment");
  releases=releases+core_release[0]+core_release[1];
 end
 always @(posedge mclk)if(!reset && wr && busy[addr>=1280])$fatal(1,"write to owned bank");
 always @(posedge rclk)if(!reset && ren && !host_ready[ra>=5120])$fatal(1,"read unowned bank");
 integer pubs=0,writes=0,i,b,n;reg [31:0] expected;
 always @(posedge mclk)if(wr)writes=writes+1;
 always @(negedge mclk)begin
  if(pub[0])begin
   if(w0!=words0 || f0!=first0)$fatal(1,"metadata0");pubs=pubs+1;
  end
  if(pub[1])begin
   if(w1!=words1 || f1!=first1)$fatal(1,"metadata1");pubs=pubs+1;
  end
  if(!reset && (pfault || overflow || sfault || rf || ofault))$fatal(1,"path fault");
 end
 function [31:0] sample(input integer bank_id,input integer offset);
  sample=32'had000000 ^ (bank_id<<20) ^ (offset*65537);
 endfunction
 task check_half(input integer address,input [15:0] value);
  begin
   @(negedge rclk);ren=1;ra=address;
   @(negedge rclk);ren=0;
   @(posedge rclk);#1;if(!rv || re || rd!==value)$fatal(1,"read %0d %h != %h",address,rd,value);
  end
 endtask
 task run_capture(input integer length0,input integer length1,input bit new_epoch);
  begin
   if(new_epoch)begin
    @(negedge clk);reset=1;valid=0;done=0;
    repeat(6)@(negedge clk);reset=0;
    repeat(10)@(negedge clk);
   end
   if(busy || host_ready)$fatal(1,"reuse while owned");
   releases=0;
   pubs=0;writes=0;words0=length0;words1=length1;
   for(b=0;b<2;b=b+1)begin
    n=b ? length1 : length0;
    for(i=0;i<n;i=i+1)begin
     @(negedge clk);valid=1;bank=b;index=i;data=sample(b,i);done=0;
     // Complete bank 0 while the first pair of bank 1 is being emitted.
     if(b==1 && i==(length1==1 ? 0 : 1) && length0%2==0)done=1;
    end
    if(b==0 && length0%2)begin
     // A partial bank ends a controller excursion; allow padding to drain.
     @(negedge clk);valid=0;done=1;
     @(negedge clk);done=0;repeat(20)@(negedge clk);
    end
   end
   @(negedge clk);valid=0;done=2;
   @(negedge clk);done=0;
   repeat(40)@(negedge clk);
   if(pubs!=2 || writes!=(length0+1)/2+(length1+1)/2)$fatal(1,"completion pubs=%0d writes=%0d",pubs,writes);
   if(host_ready!=3 || hw0!=length0 || hw1!=length1 || hf0!=first0 || hf1!=first1)$fatal(1,"ARM descriptor mismatch");
   for(b=0;b<2;b=b+1)begin
    n=b ? length1 : length0;
    for(i=0;i<n;i=i+1)begin
     expected=sample(b,i);
     check_half(b*5120+i*2,expected[15:0]);check_half(b*5120+i*2+1,expected[31:16]);
    end
    if(n%2)begin check_half(b*5120+n*2,0);check_half(b*5120+n*2+1,0);end
    @(negedge rclk);release_en=1;release_bank=b;release_token=host_token[b];
    @(negedge rclk);release_en=0;
   end
   repeat(12)@(negedge rclk);
   if(releases!=2 || busy || host_ready)$fatal(1,"release accounting");
  end
 endtask
 initial begin
  run_capture(2560,2560,1);
  run_capture(2560,1,0);
  run_capture(2560,2559,0);
  $display("PASS host path phase=%0d full banks, odd tails, exact RAM, metadata, ownership and reuse",PHASE);$finish;
 end
 initial begin #1000000;$fatal(1,"timeout");end
endmodule

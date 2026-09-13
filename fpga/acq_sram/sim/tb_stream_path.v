`timescale 1ns/1ps
module tb #(parameter BANK_AW=3);
 localparam BANK_PACKETS=1<<BANK_AW;
 reg reset=1,wc=0,pc=0,hc=0,valid=0,stop=0;
 always #2 wc=~wc;always #4 pc=~pc;always #6.25 hc=~hc;
 reg [31:0] data=0;wire fault,finished,pv,ps,seal;wire [63:0] pd;
 stream_packetizer pack(reset,wc,pc,valid,data,stop,fault,finished,pv,pd,ps,seal);
 reg release_req=0,release_bank=0,release_token=0;
 wire ready,we,overrun,host_overrun;wire [BANK_AW:0] address,count0,count1;wire [63:0] wd,first0,first1;
 wire [1:0] available,token,last_single;
 stream_banks #(.BANK_AW(BANK_AW)) banks(reset,pc,hc,pv,pd,ps,seal,ready,we,address,wd,overrun,host_overrun,
  release_req,release_bank,release_token,available,token,first0,first1,count0,count1,last_single);
 reg [63:0] mem[0:2*BANK_PACKETS-1];integer writes=0;
 always @(posedge pc)if(we)begin mem[address]<=wd;writes=writes+1;end
 integer expected=0,blocks=0,j,n,b;reg consume=0,t;
 initial forever begin
  @(negedge hc);
  if(consume && available!=0)begin
   b=available[0] ? 0 : 1;if(available==3)b=first0<first1 ? 0 : 1;
   t=token[b];n=b ? count1 : count0;
   if((b ? first1 : first0)!==expected)$fatal(1,"descriptor %d",expected);
   for(j=0;j<n;j=j+1)begin
    @(negedge hc);
    if(mem[b*BANK_PACKETS+j][31:0]!==32'(expected))$fatal(1,"low word %d",expected);
    expected=expected+1;
    if(j!=n-1 || !last_single[b])begin
     if(mem[b*BANK_PACKETS+j][63:32]!==32'(expected))$fatal(1,"high word %d",expected);
     expected=expected+1;
    end
   end
   release_bank=b;release_token=t;release_req=1;
   @(negedge hc);release_req=0;blocks=blocks+1;
  end
 end
 task restart;
 begin
  consume=0;valid=0;stop=0;release_req=0;reset=1;
  #100;reset=0;#100;expected=0;blocks=0;writes=0;
 end endtask
 integer i,k,total;
 task run(input integer words,input integer simultaneous_stop);
 begin
  restart();consume=1;
  for(i=0;i<words;i=i+1)begin
   @(negedge wc);valid=1;data=i;stop=simultaneous_stop && i==words-1;
   @(negedge wc);valid=0;stop=0;
   repeat(16+i%11)@(negedge wc);
  end
  if(!simultaneous_stop || words==0)begin @(negedge wc);stop=1;@(negedge wc);stop=0;end
  wait(finished);wait(expected==words);#150;
  if(fault || overrun || host_overrun || available!=0 || writes!=(words+1)/2)$fatal(1,"end state %d",words);
 end endtask
 initial begin
  run(0,0);run(1,0);run(1,1);run(2,0);run(2,1);run(3,1);
  run(2*BANK_PACKETS,1);run(2*BANK_PACKETS+1,1);
  run(20*BANK_PACKETS+3,0);run(20*BANK_PACKETS+3,1);
  // A producer faster than the handshake capacity must fault, not replace the
  // in-flight first pair with later words. Host consumption is held for review.
  restart();
  @(negedge wc);valid=1;
  for(i=0;i<12;i=i+1)begin data=i;@(negedge wc);end
  valid=0;stop=1;#200;
  if(!fault || writes!=1 || mem[0]!=={32'd1,32'd0})$fatal(1,"mailbox overwrite or missing fault");
  run(7,1);
  $display("PASS packetizer + banks: bank=%d, odd/even/empty stop, live word+stop, sequence and overload reset",BANK_PACKETS);$finish;
 end
 initial begin #10000000;$fatal(1,"timeout expected=%d",expected);end
endmodule

// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-047, REQ-SDS-049, REQ-SDS-052, REQ-SDS-058
module tb #(parameter BANK_AW=3);
 localparam BANK_PACKETS=1<<BANK_AW;
 localparam TOTAL_PACKETS=BANK_AW==3 ? 1003 : 10*BANK_PACKETS+3;
 reg reset=1,pc=0,hc=0,valid=0,single=0,seal=0,release_req=0,release_bank=0,release_token=0;
 always #5 pc=~pc;always #7 hc=~hc;
 reg [63:0] data=0;wire ready,we,fault,host_fault;wire [BANK_AW:0] address;wire [63:0] wd;
 wire [1:0] available,token,last_single;wire [63:0] first0,first1;wire [BANK_AW:0] count0,count1;
 stream_banks #(.BANK_AW(BANK_AW)) dut(reset,pc,hc,valid,data,single,seal,ready,we,address,wd,fault,host_fault,
  release_req,release_bank,release_token,available,token,first0,first1,count0,count1,last_single);
 reg [63:0] mem[0:2*BANK_PACKETS-1];integer writes=0;
 always @(posedge pc)if(we)begin mem[address]<=wd;writes=writes+1;end
 integer expected=0,blocks=0,i,j,n,b;reg t;reg consume=0;
 // Sequential, asynchronous consumer. The ready flag is permission to read;
 // release follows the final read, with deliberate variable host delays.
 initial forever begin
  @(negedge hc);
  if(consume && available!=0)begin
   b=available[0] ? 0 : 1;
   if(available==3)b=first0<first1 ? 0 : 1;
   t=token[b];n=b ? count1 : count0;
   if((b ? first1 : first0)!==expected)$fatal(1,"descriptor sequence %d",expected);
   if(n<1 || n>BANK_PACKETS)$fatal(1,"bad count %d",n);
   repeat(blocks%4)@(negedge hc);
   for(j=0;j<n;j=j+1)begin
    @(negedge hc);
    if(j==n-1 && last_single[b])begin
     if(mem[b*BANK_PACKETS+j][31:0]!==32'(expected))$fatal(1,"odd final word");
     expected=expected+1;
    end else begin
     if(mem[b*BANK_PACKETS+j]!=={32'(expected+1),32'(expected)})$fatal(1,"payload %d got %h",expected,mem[b*BANK_PACKETS+j]);
     expected=expected+2;
    end
   end
   release_bank=b;release_token=t;release_req=1;
   @(negedge hc);release_req=0;blocks=blocks+1;
  end
 end
 task send(input integer word_index);
 begin
  @(negedge pc);valid=1;data={32'(word_index+1),32'(word_index)};
  @(negedge pc);valid=0;
 end endtask
 task restart;
 begin
  consume=0;valid=0;single=0;seal=0;release_req=0;reset=1;
  #100;reset=0;#100;expected=0;blocks=0;writes=0;
 end endtask
 initial begin
  restart();consume=1;
  for(i=0;i<TOTAL_PACKETS;i=i+1)begin send(i*2);repeat(4+i%5)@(negedge pc);end
  @(negedge pc);seal=1;@(negedge pc);seal=0;
  wait(expected==2*TOTAL_PACKETS);#100;
  if(fault || writes!=TOTAL_PACKETS || blocks!=(TOTAL_PACKETS+BANK_PACKETS-1)/BANK_PACKETS)$fatal(1,"stream totals %d %d",writes,blocks);
  // An empty seal must not publish a phantom block.
  seal=1;#50;seal=0;#100;if(available!=0)$fatal(1,"empty block");
  restart();
  // Deliberately stall the host: exactly two banks survive, then fail closed.
  for(i=0;i<2*BANK_PACKETS+4;i=i+1)send(i*2);
  #100;if(!fault || !host_fault || writes!=2*BANK_PACKETS || available!=3)$fatal(1,"overrun not contained");
  for(i=0;i<2*BANK_PACKETS;i=i+1)if(mem[i]!=={32'(i*2+1),32'(i*2)})$fatal(1,"overwrote owned bank");
  // Reject a release with the previous token; descriptors must remain owned.
  @(negedge hc);release_bank=0;release_token=!token[0];release_req=1;
  @(negedge hc);release_req=0;repeat(5)@(negedge hc);
  if(available!=3 || first0!=0 || first1!=2*BANK_PACKETS)$fatal(1,"stale release reclaimed data");
  consume=1;wait(expected==4*BANK_PACKETS);#100;
  send(100);if(writes!=2*BANK_PACKETS)$fatal(1,"fault did not latch");
  restart();
  for(i=0;i<BANK_PACKETS;i=i+1)send(i*2);
  #100;if(available!=1)$fatal(1,"pre-reset bank missing");
  restart();#100;if(available!=0 || host_fault)$fatal(1,"reset leaked an old epoch");
  consume=1;
  // Partial seal with the final valid packet on the same producer edge.
  send(0);@(negedge pc);valid=1;seal=1;data={32'd3,32'd2};
  @(negedge pc);valid=0;seal=0;wait(expected==4);#100;
  if(fault || writes!=2)$fatal(1,"partial restart failed");
  restart();consume=1;send(0);
  @(negedge pc);valid=1;single=1;data={32'hdeadbeef,32'd2};
  @(negedge pc);valid=0;single=0;wait(expected==3);#100;
  if(fault || writes!=2)$fatal(1,"odd final bank");
  $display("PASS stream banks: bank=%0d packets, %0d input packets, asynchronous releases, partial/empty seal, stale release, overrun retention and reset",BANK_PACKETS,TOTAL_PACKETS);$finish;
 end
 initial begin #2000000;$fatal(1,"timeout expected=%d",expected);end
endmodule

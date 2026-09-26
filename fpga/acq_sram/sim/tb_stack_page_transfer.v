// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-141, REQ-SDS-050, REQ-SDS-078
module tb_stack_page_transfer;
 parameter PHASE=1;
 reg core_clk=0,memory_clk=0,reset=1,memory_running=1;
 always #2 core_clk=~core_clk;
 initial begin #PHASE;forever begin #4;if(memory_running)memory_clk=~memory_clk;end end
 reg bad_ordinal=0;
 reg word_valid=0,word_bank=0;reg [31:0] word_data=0;reg [11:0] word_index=0;
 reg [1:0] bank_done=0,page_release=0;
 reg [63:0] first0=0,first1=0;reg [19:0] words0=0,words1=0;
 wire core_fault,page_fault,memory_write;wire [1:0] core_release,page_ready;
 wire [11:0] memory_pair,page_words0,page_words1;
 wire [63:0] memory_data,page_first0,page_first1;
 reg memory_fault=0;
 stack_page_transfer dut(.*);
 reg [63:0] ram[0:2559];integer writes[0:1];integer releases[0:1];
 integer i,j,b,n,cases=0,timeout=0,before_release;
 function [31:0] value(input integer bank,input integer index);
 value=32'hace17359 ^ (32'(index)*32'h9e3779b9) ^ (bank ? 32'hf012a357:0);
 endfunction
 always @(posedge memory_clk)begin
  if(reset)begin writes[0]=0;writes[1]=0;end
  else if(memory_write)begin
   if(memory_pair>=2560)$fatal(1,"invalid RAM address");
   ram[memory_pair]=memory_data;
   if(memory_pair<1280)writes[0]=writes[0]+1;else writes[1]=writes[1]+1;
  end
 end
 always @(posedge core_clk)begin
  if(reset)begin releases[0]=0;releases[1]=0;end
  else begin if(core_release[0])releases[0]=releases[0]+1;if(core_release[1])releases[1]=releases[1]+1;end
 end
 task clear_epoch;
 begin
  reset=1;word_valid=0;bank_done=0;page_release=0;memory_fault=0;
  repeat(4)@(negedge core_clk);reset=0;repeat(12)@(negedge core_clk);
  if(core_fault || page_fault || page_ready || core_release)$fatal(1,"reset retained state");
 end
 endtask
 task send(input integer bank,input integer length);
 begin
  @(negedge core_clk);
  if(bank)begin words1=length;first1=(bad_ordinal ? 64'h80000:64'h40000)+length;end
  else begin words0=length;first0=(bad_ordinal ? 64'h8000000000000000:64'h10000)+length;end
  for(i=0;i<length;i=i+1)begin
   word_valid=1;word_bank=bank;word_index=i;word_data=value(bank,i);@(negedge core_clk);
  end
  word_valid=0;bank_done=1<<bank;@(negedge core_clk);bank_done=0;
 end
 endtask
 task check_page(input integer bank,input integer length);
 reg [63:0] expected;
 begin
  timeout=0;while(!page_ready[bank])begin
   @(negedge memory_clk);timeout=timeout+1;
   if(page_fault || core_fault || timeout>100)$fatal(1,"page did not publish bank=%0d length=%0d",bank,length);
  end
  if((bank ? page_words1:page_words0)!=length ||
     (bank ? page_first1:page_first0)!=(bank ? first1:first0))$fatal(1,"descriptor mismatch");
  if(writes[bank]!=(length+1)/2)$fatal(1,"publication preceded final write");
  for(j=0;j<(length+1)/2;j=j+1)begin
   expected={2*j+1<length ? value(bank,2*j+1):32'd0,value(bank,2*j)};
   if(ram[bank*1280+j]!==expected)$fatal(1,"word order/tail mismatch bank=%0d pair=%0d",bank,j);
  end
  repeat(7)begin @(negedge memory_clk);if(!page_ready[bank] || page_fault)$fatal(1,"page not held");end
  cases=cases+1;
 end
 endtask
 task release_page(input integer bank);
 begin
  @(negedge memory_clk);before_release=releases[bank];page_release=1<<bank;@(negedge memory_clk);page_release=0;
  timeout=0;while(releases[bank]==before_release)begin @(negedge core_clk);timeout=timeout+1;if(timeout>30)$fatal(1,"release did not cross");end
  if(page_ready[bank])$fatal(1,"release retained page");writes[bank]=0;
 end
 endtask
 task expect_fault;
 begin
  repeat(35)@(negedge core_clk);
  if(!core_fault || !page_fault || page_ready)$fatal(1,"fault did not poison both domains case=%0d",cases);cases=cases+1;
 end
 endtask
 initial begin
  clear_epoch();
  for(n=1;n<=256;n=n+1)begin
   b=n%2;send(b,n);check_page(b,n);release_page(b);
  end
  // Both banks can be retained independently, then reused after acknowledgement.
  send(0,255);send(1,256);check_page(0,255);check_page(1,256);release_page(1);release_page(0);
  send(0,256);check_page(0,256);
  send(0,2);expect_fault(); // no overwrite of a published bank
  clear_epoch();word_valid=1;word_index=1;word_bank=0;@(negedge core_clk);word_valid=0;expect_fault();
  clear_epoch();send(0,5);check_page(0,5);memory_fault=1;expect_fault();
  clear_epoch();@(negedge memory_clk);page_release=1;@(negedge memory_clk);page_release=0;expect_fault();
  clear_epoch();word_valid=1;word_index=0;@(negedge core_clk);word_valid=0;words0=2;bank_done=1;@(negedge core_clk);bank_done=0;expect_fault();
  clear_epoch();bad_ordinal=1;send(0,7);expect_fault();
  clear_epoch();send(1,8);expect_fault();bad_ordinal=0;
  // A stopped memory consumer cannot silently lose a fixed-rate SRAM burst.
  clear_epoch();@(negedge memory_clk);memory_running=0;send(0,256);
  if(!core_fault)$fatal(1,"FIFO overflow not reported in transport domain");
  memory_running=1;expect_fault();
  // Discard queued payload and descriptors with the common epoch reset.
  clear_epoch();send(1,11);clear_epoch();repeat(30)@(negedge memory_clk);
  if(page_ready || memory_write || core_release)$fatal(1,"old epoch escaped reset");
  send(0,13);check_page(0,13);release_page(0);
  $display("PASS page transfer phase=%0d cases=%0d",PHASE,cases);$finish;
 end
 initial begin repeat(200000)@(negedge core_clk);$fatal(1,"timeout");end
endmodule

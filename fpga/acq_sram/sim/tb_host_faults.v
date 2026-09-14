`timescale 1ns/1ps
module tb_host_faults;
 parameter PHASE=0;
 reg c=0,m=0,h=0,reset=1;
 always #2 c=~c;initial begin #(PHASE);forever #4 m=~m;end
 always #5 h=~h;
 reg valid=0;reg [31:0] data=0;reg [11:0] index=0;reg [1:0] done=0;
 wire cf,hf;wire [1:0] rel,ready,token;wire [63:0] f0,f1;wire [11:0] w0,w1;
 reg ren=0;reg [13:0] ra=0;wire rv,re;wire [15:0] rd;
 integer writes=0,before_writes;
 sram_host_path dut(reset,c,m,h,valid,1'b0,data,index,done,64'd123,64'd0,20'd2,20'd0,
  cf,hf,rel,1'b0,1'b0,1'b0,ready,token,f0,f1,w0,w1,ren,ra,rv,re,rd);
 always @(posedge m)if(dut.ram_write)writes=writes+1;
 task clear_epoch;
  begin @(negedge c);reset=1;valid=0;done=0;ren=0;
   repeat(10)@(negedge h);reset=0;repeat(10)@(negedge h);
   if(cf || hf || ready || rel)$fatal(1,"reset failed");writes=0;
  end
 endtask
 task word(input integer offset,input [31:0] value);
  begin @(negedge c);valid=1;index=offset;data=value;
   @(negedge c);valid=0;
  end
 endtask
 task expect_fault;
  begin repeat(16)@(negedge h);
   if(!cf || !hf)$fatal(1,"fault propagation");
   before_writes=writes;repeat(16)@(negedge h);
   if(!cf || !hf || writes!=before_writes)$fatal(1,"fault not sticky/quiescent");
  end
 endtask
 task read_check(input integer address,input [15:0] value);
  begin @(negedge h);ren=1;ra=address;
   @(negedge h);ren=0;@(posedge h);#1;
   if(!rv || re || rd!==value)$fatal(1,"owned memory corrupted");
  end
 endtask
 initial begin
  clear_epoch;
  word(1,32'hbad);expect_fault;
  if(writes || ready)$fatal(1,"malformed first word published");
  clear_epoch;
  word(0,32'h12345678);word(1,32'habcd9876);
  @(negedge c);done=1;@(negedge c);done=0;
  repeat(20)@(negedge h);
  if(ready!=1 || f0!=123 || w0!=2 || writes!=1)$fatal(1,"setup capture");
  // Packer accepts a new sequence; the ownership guard must reject its RAM write.
  word(0,32'hdeadbeef);word(1,32'hfeedface);expect_fault;
  if(writes!=1 || ready!=1 || f0!=123 || w0!=2)$fatal(1,"owned descriptor/data changed");
  read_check(0,16'h5678);read_check(1,16'h1234);
  read_check(2,16'h9876);read_check(3,16'habcd);
  clear_epoch;
  word(0,32'h1234);
  // Reset discards the unpaired word and restores index zero.
  clear_epoch;
  word(0,32'hcafebabe);word(1,32'h87654321);
  @(negedge c);done=1;@(negedge c);done=0;repeat(20)@(negedge h);
  if(cf || hf || ready!=1 || writes!=1)$fatal(1,"post-reset capture");
  read_check(0,16'hbabe);read_check(1,16'hcafe);
  $display("PASS host faults phase=%0d malformed input, owned-bank protection, fault CDC and reset recovery",PHASE);$finish;
 end
 initial begin #100000;$fatal(1,"timeout");end
endmodule

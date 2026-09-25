// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-039, REQ-SDS-041
module tb_sample_timeline;
 reg core=0,receiver_clk=0;always #2 core=~core;initial begin #2;forever #4 receiver_clk=~receiver_clk;end
 reg core_enabled=0,receiver_enabled=0,raw_valid=0,receiver_valid=0;
 wire [7:0] raw_sample;reg [7:0] receiver_sample=0;
 wire [63:0] recent_sample,delayed_sample,retained_last_word;wire retained_valid;
 wire [31:0] retained_id,retained_epoch;
 reg arm=0,write_step=0,raw_mode=1,frozen=1;reg [7:0] write_sample=0;
 reg [31:0] record_id=0,record_epoch=7;
 sample_timeline #(.LOW_WIDTH(8)) dut(.*);
 integer n;reg [63:0] saved;
 task rx(input integer ordinal);begin @(negedge receiver_clk);receiver_sample=ordinal;receiver_valid=1;@(posedge receiver_clk);#1;if(recent_sample!==ordinal)$fatal(1,"timeline got %d expected %d",recent_sample,ordinal);end endtask
 task start;begin @(negedge core);arm=1;record_id=record_id+1;frozen=0;@(negedge core);arm=0;end endtask
 task put(input integer ordinal);begin @(negedge core);write_sample=ordinal;write_step=1;@(negedge core);write_step=0;end endtask
 initial begin
  #20;core_enabled=1;receiver_enabled=1;raw_valid=1;
  for(n=1;n<=253;n=n+4)rx(n);
  start;put(248);put(250);put(252);put(254);frozen=1;
  rx(257);rx(261);repeat(6)@(negedge receiver_clk);
  if(!retained_valid || retained_last_word!=254 || retained_id!=1 || retained_epoch!=7)$fatal(1,"freeze across wrap");
  saved=retained_last_word;
  for(n=265;n<=1021;n=n+4)rx(n);
  if(retained_last_word!=saved || !retained_valid)$fatal(1,"frozen tag changed while stream wrapped");
  // A missing word makes sample-index arithmetic invalid.
  start;put(240);put(244);frozen=1;repeat(8)@(negedge receiver_clk);
  if(retained_valid)$fatal(1,"discontinuous record advertised a linear mapping");
  // A one-word capture must invalidate and replace the preceding metadata.
  start;put(250);frozen=1;repeat(8)@(negedge receiver_clk);
  if(!retained_valid || retained_id!=3 || retained_last_word!=1018)$fatal(1,"short capture metadata");
  receiver_enabled=0;core_enabled=0;repeat(5)@(negedge receiver_clk);
  if(!retained_valid || retained_last_word!=1018)$fatal(1,"stream disable changed retained mapping");
  $display("PASS sample timeline wraps, frozen retention, discontinuity rejection and short captures");$finish;
 end
 initial begin #20000;$fatal(1,"timeout");end
endmodule

// TRLC-LINKS: REQ-SDS-039, REQ-SDS-041
module tb_sample_timeline_carry;
 reg core=0;always #2 core=~core;
 reg [31:0] sample=0;
 reg raw_valid=1;
 sample_timeline dut(.core(core),.receiver_clk(core),.core_enabled(1'b1),.receiver_enabled(1'b0),
  .raw_valid(raw_valid),.receiver_valid(1'b0),.receiver_sample(32'd0),.arm(1'b0),.write_step(1'b1),
  .raw_mode(1'b1),.write_sample(sample),.frozen(1'b0),.record_id(32'd0),.record_epoch(32'd0));
 // TRLC-LINKS: REQ-SDS-039, REQ-SDS-041
 task check(input [31:0] value);reg [31:0] expected;begin
  @(negedge core);sample=value;expected=value+2;
  repeat(2)@(posedge core);#1;
  if(dut.expected_candidate!==expected)$fatal(1,"byte carry %h + 2 became %h",value,dut.expected_candidate);
 end endtask
 initial begin
  check(0);check(1);check(32'hfe);check(32'hff);check(32'hfffe);check(32'hffff);
  check(32'hfffffe);check(32'hffffff);check(32'hfffffffe);check(32'hffffffff);
  @(negedge core);dut.raw_sample=32'hfffffffa;dut.raw_carry=0;
  @(posedge core);#1;if(dut.raw_sample!==32'hfffffffc)$fatal(1,"raw counter before carry");
  @(posedge core);#1;if(dut.raw_sample!==32'hfffffffe)$fatal(1,"raw counter carry preparation");
  @(negedge core);raw_valid=0;
  repeat(3)@(posedge core);#1;if(dut.raw_sample!==32'hfffffffe)$fatal(1,"raw counter advanced without samples");
  @(negedge core);raw_valid=1;
  @(posedge core);#1;if(dut.raw_sample!==0)$fatal(1,"raw counter wrap after pause");
  $display("PASS full-width timeline byte carries and low-word wrap");$finish;
 end
endmodule

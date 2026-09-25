// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-040
module tb_precision_tail_queue;
 parameter MAX_LOG=12;
 reg clk=0;always #5 clk=~clk;
 reg enable=0,valid=0;reg [3:0] remaining_log=0;reg [31:0] data=0;
 wire ready,out_valid,fault;wire [31:0] q;
 cic_precision_tail dut(.*);
 wire [31:0] rd[0:3];wire [3:0] rv;
 assign rd[0]=data;assign rv[0]=valid;
 genvar s;generate for(s=0;s<3;s=s+1)begin:reference
  wire [3:0] rem=remaining_log>4*s ? remaining_log-4*s : 0;
  wire [3:0] factor_log=rem>4 ? 4 : rem;
  wire second_valid;
  cic_stage ch1(clk,enable,rv[s],factor_log,rd[s][15:0],rv[s+1],rd[s+1][15:0]);
  cic_stage ch2(clk,enable,rv[s],factor_log,rd[s][31:16],second_valid,rd[s+1][31:16]);
 end endgenerate
 reg [31:0] reference_words[0:1023],actual_words[0:1023];
 integer rn=0,an=0,checked=0,epochs=0,max_queued=0;
 reg checking=0,expect_fault=0;
 always @(posedge clk)begin
  #0.1;
  if(checking)begin
   if(fault && !expect_fault)$fatal(1,"unexpected overflow log=%0d",remaining_log);
   if(dut.queued>max_queued)max_queued=dut.queued;
   if(rv[3])begin reference_words[rn]=rd[3];rn=rn+1;end
   if(out_valid)begin actual_words[an]=q;an=an+1;end
   if(rn>1024 || an>1024)$fatal(1,"scoreboard size");
   while(checked<rn && checked<an)begin
    if(reference_words[checked]!==actual_words[checked])
     $fatal(1,"log=%0d output=%0d expected=%h actual=%h",remaining_log,checked,reference_words[checked],actual_words[checked]);
    checked=checked+1;
   end
  end
 end
 task tick;begin @(negedge clk);#0.1;end endtask
 reg [31:0] random_word=32'h739ac15d;
 task next_data(input integer i);
 begin
  random_word=random_word^(random_word<<13);random_word=random_word^(random_word>>17);random_word=random_word^(random_word<<5);
  case(i%9)
   0:data=0;
   1:data=32'hffffffff;
   2:data=32'h80008000;
   3:data=32'hffff0000;
   default:data=random_word;
  endcase
 end endtask
 task epoch(input integer log,input integer count,input bit burst);
 integer i,k,spacing;
 begin
  checking=0;enable=0;valid=0;repeat(5)tick;
  remaining_log=log;rn=0;an=0;checked=0;max_queued=0;expect_fault=0;enable=1;checking=1;
  // Start immediately: the queue must retain inputs during RAM initialization.
  for(i=0;i<count;i=i+1)begin
   next_data(i);valid=1;tick;valid=0;
   spacing=log==0 ? 1 : burst ? (i%4==3 ? 205 : 1) : 51+i%3;
   for(k=1;k<spacing;k=k+1)begin data=~data;tick;end
  end
  repeat(600)tick;
  if(fault || rn!=an || checked!=rn || rn==0)$fatal(1,"tail count mismatch log=%0d ref=%0d got=%0d",log,rn,an);
  epochs=epochs+1;$display("PASS precision tail log=%0d burst=%0d inputs=%0d outputs=%0d max_queue=%0d",log,burst,count,rn,max_queued);$fflush();
 end endtask

 integer k;
 initial begin
  repeat(5)tick;remaining_log=1;enable=1;checking=1;
  // Fill while the state RAM is being initialized.
  for(k=0;k<4;k=k+1)begin next_data(k);valid=1;tick;end
  valid=0;
  wait(dut.queued==4 && dut.state==1 && dut.read_available);#0.1;
  // This is before the next rising edge: read the oldest pair while replacing
  // its physical RAM slot with a new pair. The scoreboard retains all five.
  if(!ready)$fatal(1,"full simultaneous pop/push not ready");
  next_data(4);valid=1;tick;valid=0;
  if(dut.queued!=4)$fatal(1,"full simultaneous pop/push changed count");
  repeat(240)tick;
  for(k=5;k<96;k=k+1)begin next_data(k);valid=1;tick;valid=0;repeat(51)tick;end
  repeat(600)tick;
  if(rn!=an || checked!=rn || rn==0 || fault)$fatal(1,"shared RAM queue ordering");
  $display("PASS RAM queue: full simultaneous push/pop, every output matches parallel reference");
  // Interrupt an operation with queued input, then verify the next epoch.
  checking=0;enable=0;tick;enable=1;valid=1;repeat(4)tick;valid=0;
  wait(dut.state==4 && dut.queued!=0);epoch(1,96,0);
  $display("PASS RAM queue: reset during filter update with pending input");$finish;
 end
 initial begin #10000000;$fatal(1,"timeout");end
endmodule

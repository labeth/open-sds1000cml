`timescale 1ns/1ps
module tb;
 localparam AW=19,N=1<<AW;
 reg clk=0;always #5 clk=~clk;
 reg reset=1,arm=0,halt=0,step=0,trigger=0;
 reg [AW:0] pre_count=0,post_count=1;
 wire running,done,triggered,config_error;
 wire [AW-1:0] write_addr,record_start;
 wire [AW:0] record_length,trigger_index,filled;
 sram_record #(.AW(AW)) dut(.*);
 reg request=0,frozen=0;
 reg [AW-1:0] current_position=0;
 reg [AW:0] offset=0,length=0;
 wire valid,error;wire [AW-1:0] skip;wire [AW:0] count;
 sram_recall #(.AW(AW)) reader(.*);
 integer memory[0:N-1];integer sequence_number=0,i,j,first,last,physical;
 // RAM in the testbench only: accepted writes carry a monotonic timestamp.
 always @(posedge clk) if(running && step && !halt && !arm && !reset) begin
  memory[write_addr]=sequence_number;sequence_number=sequence_number+1;
 end
 task tick;begin @(posedge clk);#1;@(negedge clk);end endtask
 task start(input integer pre,input integer post);
 begin pre_count=pre;post_count=post;arm=1;tick;arm=0;end endtask
 task put(input integer number,input integer fire);
 begin step=1;trigger=fire;repeat(number)tick;step=0;trigger=0;end endtask
 task window(input integer off,input integer len,input integer expected_first);
 begin
  frozen=done;offset=off;length=len;request=1;tick;request=0;
  if(!valid || error || count!=len)$fatal(1,"valid window rejected");
  physical=(current_position+skip)%N;
  for(j=0;j<len;j=j+1) begin
   if(memory[physical]!=expected_first+j)
    $fatal(1,"recall mismatch j=%0d address=%0d got=%0d expected=%0d",j,physical,memory[physical],expected_first+j);
   physical=(physical+1)%N;
  end
  // Emulate two extra SRAM read-pipeline clocks. Next seek must include them.
  current_position=(physical+2)%N;
 end endtask
 initial begin
  tick;reset=0;
  start(N-17,17);
  put(N+233,0);first=sequence_number-(N-17);
  put(1,1);put(16,0);
  if(!done || running || !triggered || record_length!=N || trigger_index!=N-17)
   $fatal(1,"full-depth trigger metadata");
  current_position=write_addr;
  window(0,N,first);window(0,N,first); // repeat the complete wrapped record
  window(N-31,31,first+N-31);window(0,32,first);window(N,0,first+N);
  offset=N;length=1;request=1;tick;request=0;
  if(!error || valid)$fatal(1,"out-of-record recall accepted");
  // Reject invalid rearm while preserving a readable frozen record.
  start(N,1);
  if(!config_error || !done || record_length!=N)$fatal(1,"invalid arm destroyed record");
  window(100,100,first+100);
  // Early trigger ignored until prehistory exists. Triggering word is post #1.
  start(3,1);first=sequence_number;put(3,1);
  if(triggered || done)$fatal(1,"early trigger accepted");
  put(1,1);
  if(!done || record_length!=4 || trigger_index!=3)$fatal(1,"one-post trigger");
  current_position=write_addr;window(0,4,first);
  // Halt with no trigger exposes only initialized data, even across a wrap.
  start(0,N);put(N+7,0);first=sequence_number-N;halt=1;tick;halt=0;
  if(triggered || record_length!=N || trigger_index!=N)$fatal(1,"untriggered halt");
  current_position=write_addr;window(0,N,first);
  // Halt during posthistory preserves exactly the words already accepted.
  start(2,100);put(7,0);first=sequence_number-2;put(1,1);put(4,0);
  halt=1;tick;halt=0;
  if(!triggered || record_length!=7 || trigger_index!=2)$fatal(1,"partial post halt");
  current_position=write_addr;window(0,7,first);
  // A stalled writer must not advance metadata or latch a trigger.
  start(0,1);trigger=1;repeat(10)tick;
  if(filled!=0 || triggered || write_addr!=0)$fatal(1,"advanced without accepted write");
  put(1,1);
  if(record_length!=1 || !done)$fatal(1,"zero-pre trigger");
  // Invalid rearm during the final post word must preserve the pending stop.
  start(0,2);put(1,1);start(N,1);put(1,0);
  if(!done || running || record_length!=2 || trigger_index!=0)$fatal(1,"invalid active rearm damaged countdown");
  $display("PASS full 524288-word wrapped trigger, repeated/partial recall, pipeline seek, bounds, halt and backpressure");
  $finish;
 end
 initial begin #30000000;$fatal(1,"timeout");end
endmodule

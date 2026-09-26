// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-141, REQ-SDS-043, REQ-SDS-044
module tb_stack_record_cache #(parameter AW=8,CACHE_AW=3,FULL_ONLY=0,PHASE=1);
 localparam N=1<<AW,PAGE=1<<CACHE_AW;
 reg clk=0;always #2 clk=~clk;wire sample_clk;assign #0.4 sample_clk=clk;
 reg memory_clk=0,memory_running=1;initial begin #PHASE;forever begin #4;if(memory_running)memory_clk=~memory_clk;end end
 reg reset=1,owned=1,transport_owned=1,frozen=1,raw8=1;
 reg [31:0] epoch=7,record_id=9;reg [AW-1:0] record_start=N-11,read_bias=0;
 reg [AW:0] record_words=N;
 reg request_valid=0,adjacent=0,response_ready=0;reg [31:0] sample_index=0;
 wire request_ready,response_valid,response_error,busy,fault;
 wire [7:0] ch0_left,ch0_right,ch1_left,ch1_right;
 wire transport_ready,transport_done,transport_read_valid,command,command_read,command_discard,command_continue;
 wire [31:0] transport_read_data;wire [AW-1:0] position;wire [AW:0] command_count;
 reg early_done=0;
 stack_record_cache #(.AW(AW),.CACHE_AW(CACHE_AW)) dut(
 .clk(memory_clk),.transport_clk(clk),.transport_owned(transport_owned),.reset(reset),.owned(owned),.frozen(frozen),.raw8(raw8),.epoch(epoch),.record_id(record_id),
 .record_start(record_start),.read_bias(read_bias),.record_words(record_words),
 .request_valid(request_valid),.request_ready(request_ready),.sample_index(sample_index),.adjacent(adjacent),
 .response_valid(response_valid),.response_ready(response_ready),.response_error(response_error),
 .ch0_left(ch0_left),.ch0_right(ch0_right),.ch1_left(ch1_left),.ch1_right(ch1_right),.busy(busy),.fault(fault),
 .transport_ready(transport_ready),.transport_done(transport_done || early_done),.transport_read_valid(transport_read_valid),
 .transport_read_data(transport_read_data),.position(position),.command(command),.command_read(command_read),
 .command_discard(command_discard),.command_continue(command_continue),.command_count(command_count));
 wire [31:0] dq;wire k1,k2,g1;
 sram_transport #(.AW(AW),.CONTINUOUS_ONLY(1)) transport(.clk(clk),.sample_clk(sample_clk),.reset(reset),.locked(1'b1),
 .command(command),.command_read(command_read),.command_discard(command_discard),.command_continue(command_continue),.command_count(command_count),
 .ready(transport_ready),.write_data(32'd0),.write_valid(1'b0),.write_stop(1'b1),.read_data(transport_read_data),.read_valid(transport_read_valid),
 .done(transport_done),.position(position),.dq(dq),.k1(k1),.k2(k2),.g1(g1));
 reg [31:0] memory[0:N-1],stage1=0,stage2=0;reg [AW-1:0] address=0;
 integer pulses=0,commands=0,seeks=0,physical_origin=0,cases=0,cycles=0,i,j,before_pulses,before_seeks;
 function [31:0] pattern(input integer a);pattern=32'h615eb27d ^ (32'(a)*32'h9e3779b9);endfunction
 function [7:0] expected(input integer idx,input integer channel);
 reg [31:0] w;
 begin w=pattern((physical_origin+record_start+read_bias+idx/2)%N);expected=w>>(8*(channel+2*(idx%2)));end
 endfunction
 assign dq=k1 && g1 ? stage2:32'bz;
 always @(posedge k2)if(g1)begin
  if(!k1)$fatal(1,"stack cache wrote SRAM");
  stage1<=memory[address];stage2<=stage1;address<=address+1'b1;pulses=pulses+1;
 end
 always @(posedge clk)if(!reset && command)begin
  if(!command_read || command_count==0 || command_count>N)$fatal(1,"illegal cache transport command");
  commands=commands+1;if(command_discard)seeks=seeks+1;
 end
 task launch(input integer idx,input integer pair_request);
 begin
  cycles=0;while(!request_ready)begin @(negedge memory_clk);cycles=cycles+1;if(cycles>100)$fatal(1,"request ready timeout");end
  sample_index=idx;adjacent=pair_request;request_valid=1;
  @(negedge memory_clk);request_valid=0;sample_index=32'hffffffff;adjacent=0;
 end
 endtask
 task finish(input integer idx,input integer pair_request,input integer bad);
 reg [32:0] held;
 begin
  cycles=0;while(!response_valid)begin @(negedge memory_clk);cycles=cycles+1;if(cycles>2*N+2*PAGE+2000)$fatal(1,"response timeout state=%0d recall=%0d",dut.state,dut.recall.state);end
  if(response_error!==bad[0])$fatal(1,"error idx=%0d got=%b expected=%0d",idx,response_error,bad);
  if(!bad)begin
   if(ch0_left!==expected(idx,0) || ch1_left!==expected(idx,1))$fatal(1,"left byte order idx=%0d got=%h %h expected=%h %h",idx,ch0_left,ch1_left,expected(idx,0),expected(idx,1));
   if(pair_request && (ch0_right!==expected(idx+1,0) || ch1_right!==expected(idx+1,1)))$fatal(1,"right byte order idx=%0d",idx);
   if(!pair_request && (ch0_right!=0 || ch1_right!=0))$fatal(1,"unrequested right sample");
  end
  held={response_error,ch0_left,ch1_left,ch0_right,ch1_right};
  repeat(11)begin @(negedge memory_clk);if(!response_valid || request_ready || held!=={response_error,ch0_left,ch1_left,ch0_right,ch1_right})$fatal(1,"unstable held response");end
  response_ready=1;@(negedge memory_clk);response_ready=0;
  if(response_valid || busy)$fatal(1,"response not consumed");
  if(transport_ready && position!==AW'(address-physical_origin))$fatal(1,"transport cursor lost flush pulses");
  cases=cases+1;
 end
 endtask
 task run(input integer idx,input integer pair_request,input integer bad);
 begin launch(idx,pair_request);finish(idx,pair_request,bad);end
 endtask
 task new_epoch;
 begin
  // Shared reset abandons the old epoch. Rebase a synthetic new record's
  // logical origin to the actual counter; reset does not reset physical SRAM.
  reset=1;request_valid=0;response_ready=0;owned=1;transport_owned=1;frozen=1;raw8=1;early_done=0;
  repeat(3)@(negedge memory_clk);physical_origin=address;epoch=epoch+1;record_id=record_id+1;
  record_start=N-11;read_bias=0;record_words=N;reset=0;repeat(12)@(negedge memory_clk);
 end
 endtask
 initial begin
  for(i=0;i<N;i=i+1)memory[i]=pattern(i);
  repeat(5)@(negedge memory_clk);reset=0;repeat(12)@(negedge memory_clk);
  run(0,1,0);run(2*N-1,0,0);before_pulses=pulses;run(1,1,0);
  if(pulses!=before_pulses)$fatal(1,"cached pair touched SRAM");
  if(!FULL_ONLY)begin
   for(j=0;j<2*N-1;j=j+1)run(j,1,0);
   // Cross-page pair and both direct-mapped banks survive repeated hits.
   run(2*PAGE-1,1,0);before_pulses=pulses;
   run(2*PAGE-1,1,0);run(0,1,0);run(2*PAGE+1,1,0);
   if(pulses!=before_pulses)$fatal(1,"adjacent cache pages not reused");
   run(4*PAGE,1,0);before_pulses=pulses;run(2*PAGE+1,1,0);
   if(pulses!=before_pulses)$fatal(1,"collision evicted unrelated bank");
   // Geometry and unsupported sample format reject without a bus command.
   before_pulses=pulses;run(2*N-1,1,1);run(2*N,0,1);run(-1,0,1);
   raw8=0;run(0,0,1);raw8=1;frozen=0;run(0,0,1);frozen=1;
   record_words=N+1;run(0,0,1);record_words=0;run(0,0,1);record_words=N;
   if(pulses!=before_pulses || fault)$fatal(1,"invalid request accessed SRAM or poisoned reader");
   record_words=PAGE+3;run(2*(PAGE+3)-2,1,0);before_pulses=pulses;run(2*(PAGE+3)-1,1,1);
   if(pulses!=before_pulses)$fatal(1,"partial-page overrun accessed SRAM");record_words=N;
   run(0,1,0);before_pulses=pulses;record_id=record_id+1;run(0,1,0);
   if(pulses==before_pulses)$fatal(1,"record identity reused stale cache");
   read_bias=7;run(0,1,0);read_bias=0;record_start=position+16;before_seeks=seeks;run(0,1,0);
   if(seeks!=before_seeks)$fatal(1,"zero-distance refill sought unnecessarily");
   // Identity change while a response is held must be an error on the same
   // consume edge, even before the controller's registered fault is updated.
   launch(1,1);while(!response_valid)@(negedge memory_clk);record_id=record_id+1;#1;
   if(!response_error)$fatal(1,"held response ignored identity change");
   response_ready=1;@(negedge memory_clk);response_ready=0;
   if(!fault || request_ready || response_valid)$fatal(1,"identity fault did not poison reader");
   new_epoch();
   // Revoke ownership with an actual transport read active, then drain it.
   launch(0,1);while(!dut.recall_active || transport_ready)@(negedge memory_clk);owned=0;
   finish(0,1,1);if(!fault || request_ready || !transport_ready)$fatal(1,"ownership loss did not drain/poison");
   new_epoch();
   // The fast arbiter can revoke independently of the numerical controller.
   launch(0,1);while(!dut.word_valid)@(negedge clk);transport_owned=0;#0.1;
   if(command)$fatal(1,"fast grant loss did not suppress commands");
   finish(0,1,1);
   if(!fault || request_ready || !transport_ready || dut.cache_valid!=0)$fatal(1,"fast grant loss did not drain/poison");
   new_epoch();
   // A fault on the same edge as recall completion must win. The bridge
   // must not publish a successful completion before entering its drain state.
   launch(0,1);while(!dut.recall_done)@(negedge clk);transport_owned=0;
   @(negedge clk);
   if(dut.completion_toggle==dut.request_seen)$fatal(1,"fault published simultaneous completion");
   @(negedge clk);
   if(dut.completion_toggle==dut.request_seen || dut.core_state!=3 || !dut.core_failed)
    $fatal(1,"fault lost priority over recall completion");
   finish(0,1,1);if(!fault || dut.cache_valid!=0)$fatal(1,"completion race exposed cache");
   new_epoch();
   // A newly revoked grant in the final verification cycle returns an error
   // payload even before the registered failure summary sees it.
   launch(0,1);while(dut.core_state!=4)@(negedge clk);
   if(!transport_ready)$fatal(1,"verification before transport drain");
   transport_owned=0;@(negedge clk);
   if(!dut.core_failed || dut.completion_toggle!=dut.request_seen)$fatal(1,"late grant loss escaped completion payload");
   finish(0,1,1);if(!fault || dut.cache_valid!=0)$fatal(1,"late grant loss exposed cache");
   new_epoch();
   // Drop each identity field while a page is receiving words. The RAM may
   // finish an unpublished write, but no tag or response may remain valid.
   for(j=0;j<5;j=j+1)begin
    launch(0,1);while(!dut.word_valid)@(negedge clk);
    case(j)
     0:epoch=epoch+1;
     1:record_id=record_id+1;
     2:record_start=record_start+1'b1;
     3:read_bias=read_bias+1'b1;
     4:record_words=record_words-1'b1;
    endcase
    finish(0,1,1);
    if(!fault || request_ready || dut.cache_valid!=0 || !transport_ready)$fatal(1,"refill identity change escaped poisoning field=%0d",j);
    new_epoch();run(0,1,0);new_epoch();
   end
   // Unexpected early completion must never publish a partial cache page.
   launch(0,1);while(dut.recall.state!=10)@(negedge clk);early_done=1;
   @(negedge clk);early_done=0;finish(0,1,1);if(!fault)$fatal(1,"partial refill not poisoned");
   new_epoch();
   // No cache tag or successful response can precede slow-memory publication.
   new_epoch();launch(0,1);while(!dut.word_valid)@(negedge clk);
   memory_running=0;repeat(100)@(negedge clk);
   if((response_valid && !response_error) || dut.cache_valid!=0 || dut.recall_done)$fatal(1,"cache published before RAM acknowledgement");
   memory_running=1;
   if(PAGE>16)begin
    finish(0,1,1);if(!fault)$fatal(1,"cache ignored transfer overflow");
    repeat(20)begin @(negedge memory_clk);if(response_valid || request_ready)$fatal(1,"poisoned cache repeated a response");end
    new_epoch();run(0,1,0);
   end else finish(0,1,0);
   // Reset also cancels a cached read in the memory clock domain.
   launch(1,1);while(dut.storage.read_state==0)@(negedge memory_clk);new_epoch();
   repeat(20)@(negedge memory_clk);if(response_valid || fault)$fatal(1,"old memory read escaped reset");run(0,1,0);
   // A coordinated reset during a burst clears tags and in-flight response.
   new_epoch();launch(0,1);while(dut.recall.state!=10)@(negedge memory_clk);new_epoch();
   if(response_valid || fault)$fatal(1,"reset exposed old response");run(0,1,0);
  end
  for(i=0;i<N;i=i+1)if(memory[i]!==pattern(i))$fatal(1,"retained SRAM was modified");
  $display("PASS frozen SRAM cache AW=%0d cases=%0d commands=%0d seeks=%0d K2_pulses=%0d",AW,cases,commands,seeks,pulses);$finish;
 end
 initial begin repeat(4000000)@(negedge clk);$fatal(1,"global timeout");end
endmodule

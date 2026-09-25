// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-043, REQ-SDS-052
module tb_finite_recall;
 parameter AW=13,CONTINUE_READS=1,FULL_ONLY=0;
 localparam N=1<<AW;
 reg c=0,m=0,h=0;always #2 c=~c;always #4 m=~m;always #5 h=~h;
 wire sample;assign #0.4 sample=c;
 reg reset=1,start=0,frozen=1;reg [AW-1:0] record_start=N-73,read_bias=0;
 reg [AW:0] record_words=N,offset=0,length=0;
 wire start_ready,active,done,request_error,fault,host_fault,core_fault;
 wire [3:0] error_code;wire [63:0] recalled;
 wire [1:0] bank_busy,bank_done,bank_release,host_ready,host_token;
 wire word_valid,word_bank;wire [31:0] word_data;wire [11:0] word_index;
 wire [63:0] first0,first1,host_first0,host_first1;
 wire [AW:0] words0,words1;wire [19:0] hw0=words0,hw1=words1;
 wire [11:0] host_words0,host_words1;
 wire tr,td,tv,command,command_read,command_discard,command_continue;
 wire [31:0] tdata;wire [AW-1:0] position;wire [AW:0] command_count;
 reg release_en=0,release_bank=0,release_token=0,ren=0;
 reg [13:0] raddr=0;wire rv,re;wire [15:0] rdata;
 sram_finite_recall #(.AW(AW),.CONTINUE_READS(CONTINUE_READS)) reader(
  .clk(c),.reset(reset),.start(start),.frozen(frozen),.record_start(record_start),.read_bias(read_bias),
  .record_words(record_words),.offset(offset),.length(length),.start_ready(start_ready),.active(active),.done(done),
  .request_error(request_error),.fault(fault),.error_code(error_code),.recalled(recalled),
  .host_core_fault(core_fault),.bank_release(bank_release),.bank_busy(bank_busy),.bank_done(bank_done),
  .word_valid(word_valid),.word_bank(word_bank),.word_data(word_data),.word_index(word_index),
  .first0(first0),.first1(first1),.words0(words0),.words1(words1),
  .transport_ready(tr),.transport_done(td),.transport_read_valid(tv),.transport_read_data(tdata),.position(position),
  .command(command),.command_discard(command_discard),.command_continue(command_continue),
  .command_read(command_read),.command_count(command_count));
 sram_host_path host(reset,c,m,h,word_valid,word_bank,word_data,word_index,
  bank_done,first0,first1,hw0,hw1,core_fault,host_fault,bank_release,
  release_en,release_bank,release_token,host_ready,host_token,host_first0,host_first1,host_words0,host_words1,
  ren,raddr,rv,re,rdata);
 wire [31:0] dq;wire k1,k2,g1;
 sram_transport #(.AW(AW),.CONTINUOUS_ONLY(1)) transport(c,sample,reset,1'b1,
  command,command_read,command_discard,command_continue,command_count,tr,
  32'b0,1'b0,1'b1,,tdata,tv,td,position,dq,k1,k2,g1);
 reg [31:0] memory[0:N-1],stage1=0,stage2=0;reg [AW-1:0] address=0;
 integer pulses=0,received=0,blocks=0,total_words=0,cases=0,hold_cycles=0;
 integer seeks=0,before_seeks;
 integer bank,count,i,block_first,expected_address;reg [31:0] expected;
 function [31:0] pattern(input integer a);pattern=32'h615eb27d ^ (32'(a)*32'h9e3779b9);endfunction
 assign dq=k1 && g1 ? stage2 : 32'bz;
 always @(posedge k2)if(g1)begin
  if(!k1)$fatal(1,"recall wrote SRAM");
  stage1<=memory[address];stage2<=stage1;address<=address+1'b1;pulses=pulses+1;
 end
 always @(posedge c)if(!reset)begin
  if(command)begin
   if(command_count==0 || command_count>N)$fatal(1,"invalid recall command length");
   if(command_discard)seeks=seeks+1;
  end
  if(fault || host_fault || core_fault)$fatal(1,"recall/host fault %0d",error_code);
  if(done && bank_busy!=0)$fatal(1,"done before final bank release");
 end
 initial begin
  wait(!reset);
  forever begin
   @(negedge h);
   if((host_ready[0] && host_first0==received) || (host_ready[1] && host_first1==received))begin
    bank=host_ready[0] && host_first0==received ? 0 : 1;
    count=bank ? host_words1 : host_words0;block_first=received;
    if(count<1 || count>2560)$fatal(1,"bad descriptor length");
    if(blocks==0)repeat(hold_cycles)@(negedge h);
    // One halfword request per host clock, checking the documented latency.
    for(i=0;i<count*2+2;i=i+1)begin
     @(negedge h);
     if(!host_ready[bank])$fatal(1,"bank ownership lost");
     if(i>=2)begin
      expected_address=(record_start+read_bias+offset+block_first+(i-2)/2)%N;
      expected=pattern(expected_address);
      if(!rv || re || rdata!==((i-2)%2 ? expected[31:16] : expected[15:0]))
       $fatal(1,"readback word=%0d half=%0d got=%h expected=%h",block_first+(i-2)/2,(i-2)%2,rdata,expected);
      if((i-2)%2)received=received+1;
     end
     ren=i<count*2;raddr=bank*5120+i;
    end
    @(negedge h);release_en=1;release_bank=bank;release_token=host_token[bank];
    @(negedge h);release_en=0;blocks=blocks+1;
   end
  end
 end
 task launch;begin
  wait(start_ready);@(negedge c);start=1;@(negedge c);start=0;
 end endtask
 task run_case(input integer off,input integer n,input integer pause);
 begin
  wait(start_ready);@(negedge c);offset=off;length=n;received=0;blocks=0;hold_cycles=pause;
  launch;wait(done);repeat(4)@(negedge c);
  if(received!=n || recalled!=n || bank_busy!=0 || position!==address)$fatal(1,"final count/position");
  total_words=total_words+n;cases=cases+1;
  $display("PASS finite case: AW=%0d continuation=%0d offset=%0d words=%0d banks=%0d",AW,CONTINUE_READS,off,n,blocks);
 end endtask
 integer j,before_pulses;
 initial begin
  for(j=0;j<N;j=j+1)memory[j]=pattern(j);
  repeat(10)@(negedge c);reset=0;wait(start_ready);
  if(!FULL_ONLY)begin
   // Invalid ranges and non-frozen requests must not issue SRAM commands.
   before_pulses=pulses;offset=N;length=1;launch;wait(request_error);wait(start_ready);
   if(pulses!=before_pulses || fault || bank_busy!=0)$fatal(1,"invalid range damaged state");
   frozen=0;offset=0;length=1;launch;wait(request_error);wait(start_ready);frozen=1;
   if(pulses!=before_pulses || fault || bank_busy!=0)$fatal(1,"unfrozen read accepted");
   run_case(N,0,0);run_case(19,N-19,2000);
   read_bias=7;run_case(0,1,0);read_bias=0;
   // Target already equals the transport cursor: skip seek, but still read
   // the warmup plus the requested data using the correct command operand.
   record_start=position+16;before_seeks=seeks;
   run_case(0,17,0);
   if(seeks!=before_seeks)$fatal(1,"zero-distance recall issued a seek");
  end
  run_case(0,N,2000);
  for(j=0;j<N;j=j+1)if(memory[j]!==pattern(j))$fatal(1,"frozen memory changed");
  $display("PASS finite recall suite: cases=%0d total_words=%0d SRAM_pulses=%0d",cases,total_words,pulses);$finish;
 end
 initial begin #100000000;$fatal(1,"timeout");end
endmodule

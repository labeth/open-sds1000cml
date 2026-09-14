`timescale 1ns/1ps
module tb_board_capture;
 parameter WRAPPED=1;
 localparam AW=13,N=1<<AW;
 reg core_clk=0,ram_clk=0,host_clk=0;
 always #2 core_clk=~core_clk;always #4 ram_clk=~ram_clk;always #5 host_clk=~host_clk;
 wire sample_clk;assign #0.4 sample_clk=core_clk;
 reg reset=1,start=0,finite_mode=0,stop=0,source_finished=0,frozen=1;
 reg [AW-1:0] record_start=N-73,read_bias=0;reg [AW:0] record_words=N,offset=19,length=5121;
 reg source_valid=0;reg [35:0] source_data=0;
 wire source_ready,start_ready,source_enable,active,done,capture_done,fault,request_error,start_rejected,selected_finite;
 wire [3:0] error_code;wire [63:0] committed,read_ordinal;wire [AW:0] unread;
 wire [1:0] bank_busy,host_ready,host_token;
 reg host_release=0,release_bank=0,release_token=0,read_enable=0;
 reg [13:0] read_halfword=0;wire read_valid,read_error;wire [15:0] read_data;
 wire host_fault;wire [63:0] host_first0,host_first1;wire [11:0] host_words0,host_words1;
 wire transport_ready,transport_write_ready,transport_done,transport_read_valid;
 wire [31:0] transport_read_data;wire [AW-1:0] position;
 wire command,command_read,command_discard,command_continue,write_valid,write_stop;
 wire [AW:0] command_count;wire [31:0] write_data;
 wire [31:0] dq;wire k1,k2,g1;
 reg writer_request=0,writer_command=0,writer_valid=0,writer_stop=1;
 reg [31:0] writer_data=0;
 wire writer_granted,writer_ready,writer_write_ready,writer_done;
 sram_board_capture_path #(.AW(AW)) dut(.locked(1'b1),.sample_clk(sample_clk),.*);
 reg [31:0] memory[0:N-1],stage1=0,stage2=0;reg [AW-1:0] address=0;
 integer cycles=0,produced=0,written=0,stream_target=0,sample_base=0,write_origin=0;
 integer received=0,expected_count=0,expected_base=0,blocks=0,cases=0,rejections=0;
 reg expected_selection=0,expected_wrapped=0,allow_final_release=0;
 function [31:0] pattern(input integer a);pattern=32'h615eb27d ^ (32'(a)*32'h9e3779b9);endfunction
 assign dq=k1 && g1 ? stage2 : 32'bz;
 always @(posedge k2)if(g1)begin
  if(!k1)begin
   if(!writer_granted && (expected_selection || selected_finite))$fatal(1,"finite producer wrote SRAM");
   if(written==0)write_origin=address;
   if(address!=(write_origin+written)%N || dq!==pattern(sample_base+written))$fatal(1,"SRAM write sequence");
   memory[address]<=dq;written=written+1;
  end else begin stage1<=memory[address];stage2<=stage1;end
  address<=address+1'b1;
 end
 always @(negedge core_clk)begin
  source_valid=!reset && source_enable && produced<stream_target && cycles%128==0;
  source_data={4'b0,pattern(sample_base+produced)};
  stop=stream_target!=0 && produced>=stream_target;source_finished=stop && !source_valid;
  if(!reset && (active || bank_busy!=0) && selected_finite!==expected_selection)$fatal(1,"mode changed while owned");
 end
 always @(posedge core_clk)begin
  cycles<=cycles+1;
  if(source_valid)begin if(!source_ready)$fatal(1,"ingress overflow");produced=produced+1;end
  if(!reset && (fault || host_fault || request_error))$fatal(1,"shared engine fault %0d",error_code);
  if(!reset && done && bank_busy!=0)$fatal(1,"done before release");
 end
 integer bank,count,block_first,i,a;reg [31:0] expected;
 initial begin
  wait(!reset);
  forever begin
   @(negedge host_clk);
   if((host_ready[0] && host_first0==received) || (host_ready[1] && host_first1==received))begin
    bank=host_ready[0] && host_first0==received ? 0 : 1;
    count=bank ? host_words1 : host_words0;block_first=received;
    if(count<1 || count>2560)$fatal(1,"bad bank descriptor");
    if(blocks==0)begin
     // A wrong token cannot free the current bank or affect the other mode.
     host_release=1;release_bank=bank;release_token=!host_token[bank];
     @(negedge host_clk);host_release=0;repeat(5)@(negedge host_clk);
     if(!host_ready[bank])$fatal(1,"wrong token released bank");
     repeat(1000)@(negedge host_clk);
    end
    for(i=0;i<count*2+2;i=i+1)begin
     @(negedge host_clk);
     if(!host_ready[bank])$fatal(1,"ownership lost during read");
     if(i>=2)begin
      a=expected_base+block_first+(i-2)/2;if(expected_wrapped)a=a%N;expected=pattern(a);
      if(!read_valid || read_error || read_data!==((i-2)%2 ? expected[31:16] : expected[15:0]))
       $fatal(1,"case=%0d word=%0d half=%0d got=%h expected=%h",cases,block_first+(i-2)/2,(i-2)%2,read_data,expected);
      if((i-2)%2)received=received+1;
     end
     read_enable=i<count*2;read_halfword=bank*5120+i;
    end
    if(received==expected_count)wait(allow_final_release);
    @(negedge host_clk);host_release=1;release_bank=bank;release_token=host_token[bank];
    @(negedge host_clk);host_release=0;blocks=blocks+1;
   end
  end
 end
 task tick;begin @(negedge core_clk);#0.1;end endtask
 task reject_busy;
 begin
  tick;writer_request=1;finite_mode=!expected_selection;start=1;tick;
  if(writer_granted)$fatal(1,"writer stole owned backend");writer_request=0;
  if(!start_rejected || selected_finite!==expected_selection)$fatal(1,"busy start accepted");
  start=0;rejections=rejections+1;tick;
 end endtask
 task run_case(input bit fin,input integer n,input integer base,input bit wrapped);
 begin
  wait(start_ready);tick;
  expected_selection=fin;expected_count=n;expected_base=base;expected_wrapped=wrapped;
  received=0;blocks=0;allow_final_release=0;finite_mode=fin;start=1;
  tick;start=0;
  if(selected_finite!==fin || start_rejected)$fatal(1,"idle selection rejected");
  wait(bank_busy!=0);reject_busy;
  wait(received==n);
  if(!fin)wait(!active);else repeat(4)tick;
  if(done || bank_busy==0 || start_ready)$fatal(1,"final bank was not retained");
  reject_busy;allow_final_release=1;wait(done);repeat(4)tick;
  if(bank_busy!=0 || read_ordinal!=n || (!WRAPPED && position!==address))$fatal(1,"final shared accounting");
  if(!fin && (produced!=n || written!=n || committed!=n || unread!=0))$fatal(1,"stream accounting");
  cases=cases+1;
  $display("PASS shared case %0d: finite=%0d words=%0d banks=%0d",cases,fin,n,blocks);$fflush();
 end endtask
 integer j;
 initial begin
  for(j=0;j<N;j=j+1)memory[j]=pattern(j);
  repeat(10)tick;reset=0;repeat(4)tick;
  run_case(1,5121,N-54,1);
  // Hand the physical bus to the finite writer, then recall its exact data.
  frozen=0;writer_request=1;wait(writer_ready);tick;
  start=1;tick;if(!start_rejected)$fatal(1,"backend started during writer grant");start=0;
  sample_base=196608;written=0;writer_stop=0;writer_command=1;tick;writer_command=0;
  wait(writer_write_ready);
  for(j=0;j<97;j=j+1)begin
   tick;writer_valid=1;writer_data=pattern(sample_base+j);
  end
  tick;writer_valid=0;writer_stop=1;writer_request=0;
  // Releasing the reservation early cannot cut off the in-flight write drain.
  if(!writer_granted)$fatal(1,"writer released before drain");
  wait(!writer_granted);tick;
  if(written!=97 || position!==address)$fatal(1,"writer drain/origin mismatch");
  frozen=1;record_start=write_origin;record_words=97;offset=0;length=97;
  run_case(1,97,sample_base,0);
  frozen=0;stream_target=10003;produced=0;written=0;sample_base=65536;
  run_case(0,10003,sample_base,0);
  frozen=1;record_start=(write_origin+10003-N)%N;record_words=N;offset=0;length=N;
  run_case(1,N,sample_base+10003-N,0);
  frozen=0;stream_target=257;produced=0;written=0;sample_base=131072;
  run_case(0,257,sample_base,0);
  frozen=1;record_start=write_origin;record_words=257;offset=256;length=1;
  run_case(1,1,sample_base+256,0);
  if(rejections!=12)$fatal(1,"missing busy rejection coverage");
  $display("PASS board capture: wrapped=%0d, six backend operations plus external writer without reset, ownership and physical origin verified",WRAPPED);$finish;
 end
 initial begin #100000000;$fatal(1,"timeout");end
endmodule

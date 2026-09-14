`timescale 1ns/1ps
module tb_command_port;
 parameter ENABLE_STREAM=0,PHASE=0;
 reg reset=1,host_clk=0,core_clk=0,host_write=0;
 reg [7:0] write_sel=0,read_sel=0;reg [15:0] write_data=0;
 wire read_hit;wire [15:0] read_data;
 reg acquisition_request_error=0,reject_next=0;
 always @(posedge core_clk)acquisition_request_error<=!reset && core_start && reject_next;
 wire host_busy,host_rejected,core_start,core_halt,core_force,core_snapshot,core_error;
 wire [1:0] operation,trigger_mode;
 wire [19:0] pre_count,post_count,offset,length;
 wire [18:0] read_bias;wire [4:0] decim_log;wire [9:0] encode_enable;
 wire trigger_channel,trigger_falling;wire [15:0] trigger_level;
 always #5 host_clk=~host_clk;
 initial begin #(PHASE*0.3);forever #2 core_clk=~core_clk;end
 acq_command_port #(.ENABLE_STREAM(ENABLE_STREAM)) dut(.*);
 integer delivered=0,errors=0,starts=0,halts=0,forces=0,snapshots=0;
 reg [19:0] last_pre,last_post;reg [1:0] last_operation;
 always @(posedge core_clk)if(!reset)begin
  if(core_start || core_halt || core_force || core_snapshot || core_error)delivered=delivered+1;
  if(core_start)begin
   starts=starts+1;last_pre=pre_count;last_post=post_count;last_operation=operation;
  end
  if(core_error)errors=errors+1;
  if(core_halt)halts=halts+1;
  if(core_force)forces=forces+1;
  if(core_snapshot)snapshots=snapshots+1;
 end
 task write_reg(input [7:0] address,input [15:0] value);
  begin @(negedge host_clk);host_write=1;write_sel=address;write_data=value;
   @(negedge host_clk);host_write=0;
  end
 endtask
 task settle;
  begin wait(!host_busy);repeat(4)@(negedge host_clk);end
 endtask
 task command(input [15:0] code);
  begin
   settle;write_reg(8'h10,code);settle;
   check_read(8'h11,{13'b0,1'b1,host_rejected,1'b0});
   check_read(8'h13,code);
  end
 endtask
 task check_read(input [7:0] address,input [15:0] value);
  begin read_sel=address;#0.1;if(!read_hit || read_data!==value)$fatal(1,"readback %h",address);end
 endtask
 integer previous,abort_phase;
 initial begin
  repeat(8)@(negedge host_clk);reset=0;settle;
  check_read(8'h22,1);check_read(8'h2b,16'h3ff);
  command(1);
  check_read(8'h12,0);check_read(8'h14,1);
  if(starts!=1 || last_pre!=0 || last_post!=1 || last_operation!=0)$fatal(1,"default capture");
  write_reg(8'h20,16'h5678);write_reg(8'h21,4);
  write_reg(8'h22,16'h1234);write_reg(8'h23,2);
  settle;write_reg(8'h10,1);
  // A second command must reject while the first is in flight.
  if(!host_busy)$fatal(1,"missing busy");
  write_reg(8'h10,2);
  write_reg(8'h20,16'hffff);settle;
  if(starts!=2 || last_pre!=20'h45678 || last_post!=20'h21234 || !host_rejected)
   $fatal(1,"torn snapshot or busy command accepted");
  check_read(8'h14,2);check_read(8'h13,1);check_read(8'h12,0);
  check_read(8'h20,16'hffff);write_reg(8'h11,2);settle;
  if(host_rejected)$fatal(1,"sticky rejection clear");
  command(2);if(last_operation!=2 || last_pre!=20'h4ffff)$fatal(1,"recall decode");
  previous=errors;write_reg(8'h21,16'h10);command(1);
  if(errors!=previous+1)$fatal(1,"geometry truncation");
  check_read(8'h12,1);
  // Control actions do not consume malformed shadow geometry.
  command(4);command(5);command(6);
  if(halts!=1 || forces!=1 || snapshots!=1)$fatal(1,"control decode");
  check_read(8'h12,0);
  write_reg(8'h21,0);write_reg(8'h2b,16'h400);previous=errors;command(2);
  if(errors!=previous+1)$fatal(1,"encode truncation");
  write_reg(8'h2b,16'h3ff);write_reg(8'h29,16'h8);previous=errors;command(1);
  if(errors!=previous+1)$fatal(1,"packed reserved bit");
  write_reg(8'h29,0);previous=errors;command(3);
  if(ENABLE_STREAM)begin if(last_operation!=1 || errors!=previous)$fatal(1,"stream support");end
  else if(errors!=previous+1)$fatal(1,"unsupported stream accepted");
  previous=errors;command(16'h101);command(0);command(7);
  if(errors!=previous+3)$fatal(1,"unknown opcode accepted");
  check_read(8'h12,1);
  reject_next=1;command(1);check_read(8'h12,2);reject_next=0;
  command(1);check_read(8'h12,0);
  read_sel=8'h70;#0.1;if(read_hit)$fatal(1,"claimed unrelated register");
  @(negedge host_clk);reset=1;repeat(8)@(negedge host_clk);reset=0;settle;
  check_read(8'h20,0);check_read(8'h22,1);check_read(8'h2b,16'h3ff);
  check_read(8'h11,0);check_read(8'h14,0);
  for(abort_phase=0;abort_phase<4;abort_phase=abort_phase+1)begin
   write_reg(8'h10,1);#(0.2+abort_phase*10);
   reset=1;repeat(8)@(negedge host_clk);reset=0;settle;
   repeat(8)@(negedge host_clk);
   check_read(8'h11,0);check_read(8'h14,0);
  end
  $display("PASS command port stream=%0d phase=%0d snapshot, rejection, geometry, controls, defaults",ENABLE_STREAM,PHASE);
  $finish;
 end
 initial begin #100000;$fatal(1,"command port timeout");end
endmodule

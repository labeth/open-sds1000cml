`timescale 1ns/1ps
module tb_command_gpmc;
 reg host_clk=0,core_clk=0,reset=1;
 always #5 host_clk=~host_clk;always #2 core_clk=~core_clk;
 reg ncs=1,noe=1,nwe=1;reg [6:0] selector=0;
 reg drive=0;reg [15:0] bus_value=0;wire [15:0] bus_data;
 assign bus_data=drive ? bus_value : 16'hzzzz;
 wire host_write,rd_pop,drive_active,read_hit,host_busy,host_rejected;
 wire [7:0] write_sel,read_sel;wire [15:0] write_data,port_data;
 wire [1:0] wr_aux;
 gpmc_slave #(.QUALIFIED_READ(1)) bus_slave(
  .clk(host_clk),.nCS1(ncs),.nOE(noe),.nWE(nwe),.sel(selector),.gpmc_d(bus_data),
  .we_commit(host_write),.wr_sel(write_sel),.wr_data(write_data),.wr_aux(wr_aux),
  .rd_pop(rd_pop),.rd_sel(read_sel),.rdata(read_hit ? port_data : 16'hffff),.drive_active(drive_active));
 wire core_start,core_halt,core_force,core_snapshot,core_error;
 wire [1:0] operation,trigger_mode;wire [19:0] pre_count,post_count,offset,length;
 wire [18:0] read_bias;wire [4:0] decim_log;wire [9:0] encode_enable;
 wire trigger_channel,trigger_falling;wire [15:0] trigger_level;
 acq_command_port dut(.read_data(port_data),.*);
 integer starts=0,errors=0,writes=0;
 always @(posedge host_clk)if(host_write)writes=writes+1;
 always @(posedge core_clk)begin
  if(core_start)begin
   starts=starts+1;
   if(pre_count!=20'h45678 || post_count!=1 || operation!=0)$fatal(1,"GPMC command geometry");
  end
  if(core_error)errors=errors+1;
 end
 task write_bus(input [6:0] address,input [15:0] value);
  begin
   @(negedge host_clk);selector=address;bus_value=value;drive=1;ncs=0;nwe=0;
   repeat(8)@(negedge host_clk);nwe=1;
   repeat(5)@(negedge host_clk);ncs=1;drive=0;
   repeat(6)@(negedge host_clk);
  end
 endtask
 task read_bus(input [6:0] address,input [15:0] value);
  begin
   @(negedge host_clk);selector=address;ncs=0;noe=0;
   repeat(8)@(negedge host_clk);
   if(bus_data!==value || !drive_active)$fatal(1,"GPMC readback selector=%h",address);
   noe=1;ncs=1;#0.1;
   if(bus_data!==16'hzzzz || drive_active)$fatal(1,"GPMC bus not released");
   repeat(6)@(negedge host_clk);
  end
 endtask
 initial begin
  repeat(10)@(negedge host_clk);reset=0;repeat(5)@(negedge host_clk);
  read_bus(7'h22,1);write_bus(7'h20,16'h5678);write_bus(7'h21,4);
  read_bus(7'h20,16'h5678);read_bus(7'h21,4);write_bus(7'h10,1);
  if(starts!=1 || errors!=0)$fatal(1,"GPMC command delivery");
  write_bus(7'h21,16'h10);write_bus(7'h10,1);
  if(starts!=1 || errors!=1 || writes!=5)$fatal(1,"GPMC invalid request");
  read_bus(7'h11,0);read_bus(7'h70,16'hffff);
  $display("PASS real GPMC slave -> command port: coherent write commits, readback, core decode, shared-bus release");
  $finish;
 end
 initial begin #100000;$fatal(1,"GPMC command timeout");end
endmodule

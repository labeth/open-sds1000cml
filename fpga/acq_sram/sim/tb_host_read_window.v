`timescale 1ns/1ps
module tb_host_read_window;
 reg clk=0,ram_clk=0,reset=1;
 always #5 clk=~clk;always #4 ram_clk=~ram_clk;
 reg select=0,select_bank=0,select_token=0,release_request=0,clear_error=0;
 reg [12:0] select_offset=0;
 reg [1:0] host_ready=0,host_token=0;
 reg host_fault=0;
 reg [11:0] host_words0=2560,host_words1=3;
 wire data_ready,exhausted,selected,fault;wire [15:0] data;
 wire [12:0] cursor;wire host_release,release_bank,release_token;
 wire ram_read_enable;wire [13:0] ram_read_halfword;
 wire ram_read_valid,ram_read_error;wire [15:0] ram_read_data;
 reg write_enable=0;reg [11:0] write_pair=0;reg [63:0] write_data=0;wire write_fault;
 sram_host_ram ram(ram_clk,reset,write_enable,write_pair,write_data,write_fault,
  clk,reset,ram_read_enable,ram_read_halfword,ram_read_valid,ram_read_error,ram_read_data);
 reg ncs=1,noe=1;reg [6:0] selector=0;wire [15:0] bus_data;
 wire rd_pop,drive_active;wire [7:0] rd_sel;wire pop=rd_pop && rd_sel==8'h31;
 gpmc_slave #(.QUALIFIED_READ(1)) slave(.clk(clk),.nCS1(ncs),.nOE(noe),.nWE(1'b1),
  .sel(selector),.gpmc_d(bus_data),.we_commit(),.wr_sel(),.wr_data(),.wr_aux(),
  .rd_pop(rd_pop),.rd_sel(rd_sel),.rdata(rd_sel==8'h31 ? data : {13'b0,fault,exhausted,data_ready}),
  .drive_active(drive_active));
 acq_host_read_window dut(.*);
 function [15:0] pattern(input integer index);
  pattern=16'hac31 ^ (index*16'h631d);
 endfunction
 task choose(input bit bank,input bit token,input integer offset);
  begin
   @(negedge clk);select=1;select_bank=bank;select_token=token;select_offset=offset;
   @(negedge clk);select=0;
  end
 endtask
 task clear_fault;
  begin @(negedge clk);clear_error=1;@(negedge clk);clear_error=0;end
 endtask
 task read_word(input integer address);
  begin
   if(!data_ready || fault)$fatal(1,"prefetch not ready at address %0d",address);
   @(negedge clk);selector=7'h31;ncs=0;noe=0;
   repeat(7)begin
    @(negedge clk);
    if(bus_data!==pattern(address))$fatal(1,"GPMC data changed/wrong address=%0d got=%h",address,bus_data);
   end
   noe=1;ncs=1;#0.1;if(bus_data!==16'hzzzz)$fatal(1,"bus not released");
   repeat(8)@(negedge clk);
  end
 endtask
 task release_owned;
  begin
   @(negedge clk);release_request=1;@(negedge clk);
   if(!host_release || release_bank!==select_bank || release_token!==select_token)
    $fatal(1,"release identity");
   release_request=0;host_ready[release_bank]=0;
   @(negedge clk);if(selected || data_ready)$fatal(1,"release retained stale data");
  end
 endtask
 integer i;
 initial begin
  repeat(8)@(negedge clk);reset=0;
  for(i=0;i<2560;i=i+1)begin
   @(negedge ram_clk);write_enable=1;write_pair=i;
   write_data={pattern(4*i+3),pattern(4*i+2),pattern(4*i+1),pattern(4*i)};
  end
  @(negedge ram_clk);write_enable=0;host_ready=3;
  choose(0,0,0);wait(data_ready);
  for(i=0;i<5120;i=i+1)read_word(i);
  if(!exhausted || cursor!=5120 || data_ready || fault)$fatal(1,"full bank boundary");
  release_owned;
  choose(1,0,0);wait(data_ready);
  for(i=0;i<6;i=i+1)read_word(5120+i);
  if(!exhausted || cursor!=6 || fault)$fatal(1,"short descriptor boundary");
  release_owned;
  host_words1=2560;host_token[1]=1;host_ready[1]=1;
  choose(1,1,5117);wait(data_ready);
  for(i=10237;i<10240;i=i+1)read_word(i);
  if(!exhausted || fault)$fatal(1,"physical RAM end");
  // An extra bus read must fault without advancing or auto-releasing.
  @(negedge clk);selector=7'h31;ncs=0;noe=0;
  repeat(7)@(negedge clk);noe=1;ncs=1;repeat(8)@(negedge clk);
  if(!fault || cursor!=5120 || host_release)$fatal(1,"overread not rejected");
  clear_fault;release_owned;
  // Stale token and out-of-range selections cannot expose data.
  host_ready[1]=1;choose(1,0,0);if(!fault || data_ready)$fatal(1,"stale token accepted");
  clear_fault;choose(1,1,5120);if(!fault || data_ready)$fatal(1,"out-of-range selection");
  clear_fault;choose(1,1,0);wait(ram_read_enable);@(posedge clk);#0.1;
  host_token[1]=0;repeat(8)@(negedge clk);
  if(!fault || selected || data_ready)$fatal(1,"in-flight reuse exposed stale data");
  clear_fault;choose(1,0,0);wait(data_ready);read_word(5120);release_owned;
  host_ready[1]=1;choose(1,0,0);wait(data_ready);
  @(negedge clk);host_fault=1;#0.1;
  if(data_ready || data!=0)$fatal(1,"upstream fault exposed buffered data");
  repeat(4)@(negedge clk);
  if(!fault || selected)$fatal(1,"upstream fault retained selection");
  if(write_fault)$fatal(1,"RAM initialization fault");
  $display("PASS host read window: real RAM/GPMC, 5120-halfword bank, odd record, final address, overread, token reuse, release");
  $finish;
 end
 initial begin #5000000;$fatal(1,"read window timeout");end
endmodule

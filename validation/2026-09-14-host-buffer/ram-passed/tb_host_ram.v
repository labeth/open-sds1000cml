`timescale 1ns/1ps
module tb;
 reg write_clk=0,read_clk=0;always #4 write_clk=~write_clk;always #5 read_clk=~read_clk;
 reg write_reset=1,write_enable=0,read_reset=1,read_enable=0;
 reg [11:0] write_pair=0;reg [63:0] write_data=0;
 reg [13:0] read_halfword=0;
 wire write_fault,read_valid,read_error;wire [15:0] read_data;
 sram_host_ram dut(.*);
 reg [63:0] expected[0:2559];
 integer checked=0;
 function [63:0] pattern(input integer address);
  pattern={16'(address^16'hcafe),16'((address*17)^16'hbeef),16'(~address),16'(address*29)};
 endfunction
 reg queued=0,queued_error=0;reg [15:0] queued_data=0;
 reg want_valid,want_error;reg [15:0] want_data;
 always @(posedge read_clk)begin
  want_valid=queued;want_error=queued_error;want_data=queued_data;
  if(read_reset)begin queued=0;want_valid=0;end
  else begin
   queued=read_enable;queued_error=read_halfword>=10240;
   if(read_halfword<10240)queued_data=expected[read_halfword>>2][16*read_halfword[1:0]+:16];
   else queued_data=0;
  end
  #0.1;
  if(read_valid!==want_valid)$fatal(1,"response latency/valid");
  if(want_valid)begin
   if(read_error!==want_error || read_data!==want_data)$fatal(1,"RAM data/error got %h expected %h",read_data,want_data);
   checked=checked+1;
  end
 end
 task write_range(input integer first,input integer last,input integer invert);
  integer a;
  begin
   for(a=first;a<=last;a=a+1)begin
    @(negedge write_clk);write_enable=1;write_pair=a;write_data=invert ? ~pattern(a) : pattern(a);
    expected[a]=write_data;
   end
   @(negedge write_clk);write_enable=0;
  end
 endtask
 task read_range(input integer first,input integer last);
  integer a;
  begin
   for(a=first;a<=last;a=a+1)begin @(negedge read_clk);read_enable=1;read_halfword=a;end
   @(negedge read_clk);read_enable=0;repeat(3)@(negedge read_clk);
  end
 endtask
 initial begin
  repeat(4)@(negedge write_clk);write_reset=0;
  @(negedge read_clk);read_reset=0;
  write_range(0,2559,0);read_range(0,10239);
  fork
   write_range(1280,2559,1);
   // Section 2 contains the end of bank 0 and the beginning of bank 1.
   read_range(4096,5119);
  join
  read_range(0,4095);read_range(5120,10239);
  // Both out-of-range writes and writes during reset must not alias valid RAM.
  @(negedge write_clk);write_enable=1;write_pair=2560;write_data=0;
  @(negedge write_clk);write_enable=0;
  if(!write_fault)$fatal(1,"invalid write not reported");
  @(negedge write_clk);write_reset=1;write_enable=1;write_pair=7;
  @(negedge write_clk);write_reset=0;write_enable=0;
  if(write_fault)$fatal(1,"write fault reset");
  read_range(0,31);read_range(10240,10243);read_range(16383,16383);
  // Abort a pending read and verify the preserved contents after reset.
  @(negedge read_clk);read_enable=1;read_halfword=17;
  @(negedge read_clk);read_reset=1;
  @(negedge read_clk);read_enable=0;read_reset=0;
  read_range(0,10239);
  if(checked<30720)$fatal(1,"insufficient address coverage");
  $display("PASS host RAM: %0d halfword responses, full range, disjoint concurrency, invalid addresses, resets",checked);
  $finish;
 end
 initial begin #10000000;$fatal(1,"timeout");end
endmodule

// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// Exercise both complete address spaces, inactive-client write attempts and
// reset-time handoff. Clients must reset their pointers, not clear these RAMs.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018, REQ-SDS-039
module tb_protocol_scratch;
 reg clk=0;always #4 clk=~clk;
 reg reset=1,manchester=1,usb_write=1;
 reg [3:0] usb_wr=0,usb_rd=0;reg [63:0] usb_data=64'hdeadbeef01234567;
 wire [63:0] usb_q;
 reg [1:0] man_write=0;reg [7:0] man_wr_a=0,man_wr_b=0,man_rd=0;
 reg [32:0] man_data_a=0,man_data_b=0;wire [32:0] man_q_a,man_q_b;
 protocol_scratch dut(.*);
 integer i,j;
 initial begin
  repeat(3)@(negedge clk);reset=0;
  for(i=0;i<256;i=i+1)begin
   man_write=3;man_wr_a=i;man_wr_b=i;man_data_a=33'h100000000+i;man_data_b=33'h055aa0000+i;
   usb_wr=i;@(negedge clk);
  end
  man_write=0;
  for(i=0;i<256;i=i+1)begin
   j=(i*73)%256;man_rd=j;usb_wr=i;@(negedge clk);
   if(man_q_a!==33'h100000000+j || man_q_b!==33'h055aa0000+j)$fatal(1,"Manchester capacity or inactive USB corruption at %0d",j);
  end
  // Assert reset before mode switch while both writers request the same slot.
  reset=1;manchester=0;usb_wr=7;usb_data=0;man_write=3;man_wr_a=7;man_wr_b=7;man_data_a=0;man_data_b=0;
  repeat(3)@(negedge clk);manchester=1;man_rd=7;@(negedge clk);
  if(man_q_a!==33'h100000007 || man_q_b!==33'h055aa0007)$fatal(1,"handoff reset failed to inhibit writes");
  manchester=0;reset=0;
  for(i=0;i<16;i=i+1)begin
   usb_write=1;usb_wr=i;usb_data=64'hfffffff000000000+i;man_wr_a=i;man_wr_b=i;
   @(negedge clk);
  end
  usb_write=0;
  for(i=0;i<16;i=i+1)begin
   usb_rd=i;man_wr_a=i;man_wr_b=i;@(negedge clk);
   if(usb_q!==64'hfffffff000000000+i)$fatal(1,"USB timestamp width or inactive Manchester corruption at %0d",i);
  end
  reset=1;manchester=1;man_write=0;repeat(3)@(negedge clk);reset=0;
  // Reuse only the new frame's written entries. Unwritten upper banks remain
  // unchanged, proving handoff does not accidentally clear a retained bank.
  man_write=1;man_wr_a=200;man_data_a=33'h112345678;@(negedge clk);man_write=0;
  man_rd=200;@(negedge clk);
  if(man_q_a!==33'h112345678 || man_q_b!==33'h055aa00c8)$fatal(1,"independent phase-bank write or mode reuse");
  $display("PASS shared protocol scratch: full capacity, 64-bit USB ordinals, exclusive writes, reset handoff and reuse");$finish(0);
 end
 initial begin #100000;$fatal(1,"timeout");end
endmodule

// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-013
module tb_i2c_trigger;
 reg clk=0;always #5 clk=~clk;
 reg reset=1,scl=1,sda=1;
 reg [6:0] address=7'h24;reg [1:0] direction=0;
 reg [31:0] pattern=32'hdead;reg [2:0] pattern_len=2;
 wire hit;integer hits=0;
 wire ev;wire [7:0] kind,data;wire [1:0] meta;
 integer starts=0,ends=0,bytes=0,addresses=0;
 i2c_trigger dut(clk,reset,1'b1,1'b1,scl,sda,address,1'b0,direction,pattern,pattern_len,hit,ev,kind,data,meta);
 always @(negedge clk)if(ev)begin
  case(kind)
   1:starts=starts+1;
   2:begin
    bytes=bytes+1;if(meta[0])addresses=addresses+1;
    if(meta[1])$fatal(1,"unexpected NACK");
    if(bytes==1 && (data!=8'h48 || !meta[0]))$fatal(1,"address event");
    if(bytes==2 && (data!=8'hde || meta[0]))$fatal(1,"payload event");
   end
   3:ends=ends+1;
   default:$fatal(1,"bad event kind");
  endcase
 end
 always @(negedge clk)if(hit)hits=hits+1;
// TRLC-LINKS: REQ-SDS-013
 task lines(input c,input d);begin @(negedge clk);scl=c;sda=d;repeat(5)@(posedge clk);end endtask
// TRLC-LINKS: REQ-SDS-013
 task start_bus;begin lines(0,1);lines(1,1);lines(1,0);end endtask
// TRLC-LINKS: REQ-SDS-013
 task stop_bus;begin lines(0,0);lines(1,0);lines(1,1);end endtask
// TRLC-LINKS: REQ-SDS-013
 task send(input [7:0] v);integer i;begin
  for(i=7;i>=0;i=i-1)begin lines(0,v[i]);lines(1,v[i]);end
  lines(0,0);lines(1,0);lines(0,0);
 end endtask
 initial begin
  repeat(4)@(posedge clk);@(negedge clk);reset=0;
  start_bus;send(8'h48);send(8'hde);send(8'had);stop_bus;
  if(hits!=1)$fatal(1,"write pattern did not match");
  start_bus;send(8'h49);send(8'hde);send(8'had);stop_bus;
  if(hits!=1)$fatal(1,"wrong direction matched");
  start_bus;send(8'h48);send(8'hde);start_bus;send(8'h48);send(8'had);stop_bus;
  if(hits!=1)$fatal(1,"pattern crossed repeated START");
  start_bus;send(8'h50);send(8'hde);send(8'had);stop_bus;
  if(hits!=1)$fatal(1,"wrong address matched");
  pattern_len=0;direction=2;
  start_bus;send(8'h49);stop_bus;
  if(hits!=2)$fatal(1,"address-only read did not match");
  // Arming in a high-clock/low-data portion is not an observed START.
  @(negedge clk);reset=1;scl=1;sda=0;
  repeat(4)@(posedge clk);@(negedge clk);reset=0;
  repeat(4)@(posedge clk);send(8'h48);stop_bus;
  if(hits!=2)$fatal(1,"arming level fabricated a START");
  if(starts!=6 || ends!=5 || bytes!=14 || addresses!=6)
   $fatal(1,"event accounting starts=%0d ends=%0d bytes=%0d addresses=%0d",starts,ends,bytes,addresses);
  $display("PASS I2C address, direction, data, repeated START isolation");$finish;
 end
 initial begin #100000;$fatal(1,"timeout");end
endmodule

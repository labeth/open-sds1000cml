`timescale 1ns/1ps
// Ideal FIFO interface model: this bench checks the complete precision
// arithmetic/selection path, not vendor FIFO CDC or memory timing.
module dcfifo #(parameter lpm_width=32,lpm_numwords=32,lpm_widthu=5,
 lpm_showahead="ON",add_ram_output_register="ON",rdsync_delaypipe=4,wrsync_delaypipe=4,
 read_aclr_synch="ON",write_aclr_synch="ON",use_eab="ON",intended_device_family="Cyclone IV E")(
 input aclr,wrclk,input [lpm_width-1:0] data,input wrreq,output wrfull,
 input rdclk,rdreq,output [lpm_width-1:0] q,output rdempty);
 reg [lpm_width-1:0] mem[0:lpm_numwords-1];integer wp=0,rp=0;
 assign wrfull=wp-rp>=lpm_numwords;assign rdempty=wp==rp;assign q=mem[rp%lpm_numwords];
 always @(posedge wrclk or posedge aclr)if(aclr)wp<=0;else if(wrreq && !wrfull)begin mem[wp%lpm_numwords]<=data;wp<=wp+1;end
 always @(posedge rdclk or posedge aclr)if(aclr)rp<=0;else if(rdreq && !rdempty)rp<=rp+1;
endmodule
module tb_precision_config;
 reg core=0,packclk=0,clk100=0;always #2 core=~core;
 initial begin #2;forever #4 packclk=~packclk;end
 always #5 clk100=~clk100;
 reg enable=0,ref_enable=0,raw_valid=0;reg [4:0] decim_log=4;reg [31:0] raw=0;
 wire [31:0] data,ref_data;wire valid,fault,ref_valid,ref_fault;
 adc_precision #(.SHARED_TAIL(1)) dut(.*);
 reference_adc_precision reference(.core(core),.packclk(packclk),.clk100(clk100),.enable(ref_enable),
  .decim_log(decim_log),.raw(raw),.raw_valid(raw_valid),.data(ref_data),.valid(ref_valid),.fault(ref_fault));
 reg [31:0] a[0:1023],b[0:1023];integer na=0,nb=0,checked=0;reg checking=0;
 always @(negedge core)if(checking)begin
  if(fault || ref_fault)$fatal(1,"precision integration fault log=%0d",decim_log);
  if(valid)begin a[na]=data;na=na+1;end
  if(ref_valid)begin b[nb]=ref_data;nb=nb+1;end
  while(checked<na && checked<nb)begin
   if(a[checked]!==b[checked])$fatal(1,"precision log=%0d output=%0d expected=%h got=%h",decim_log,checked,b[checked],a[checked]);
   checked=checked+1;
  end
 end
 task tick;begin @(negedge core);#0.1;end endtask
 integer log,i,epoch;reg [31:0] rng=32'ha3856c19;
 initial begin
  for(epoch=0;epoch<4;epoch=epoch+1)begin
   case(epoch)0:log=8;1:log=9;2:log=4;3:log=12;endcase
   checking=0;ref_enable=0;raw_valid=0;repeat(100)tick;
   // The reference gets a long reset. The DUT gets precisely one core cycle,
   // entirely between consecutive 100 MHz AND 125 MHz rising edges.
   @(posedge clk100);@(posedge core);#0.1;enable=0;decim_log=log;
   @(posedge core);#0.1;enable=1;ref_enable=1;
   na=0;nb=0;checked=0;checking=1;
   for(i=0;i<28*(1<<log);i=i+1)begin
    rng=rng^(rng<<13);rng=rng^(rng>>17);rng=rng^(rng<<5);
    raw=rng;raw_valid=1;tick;
   end
   raw_valid=0;repeat(2000)tick;
   if(na==0 || na!=nb || checked!=na)$fatal(1,"short epoch count log=%0d new=%0d old=%0d",log,na,nb);
   $display("PASS short precision epoch /%0d: %0d words match clean reference",1<<log,na);$fflush();
  end
  $display("PASS precision settings/rearm after one-core-cycle disable");$finish;
 end
 initial begin #100000000;$fatal(1,"timeout");end
endmodule

`timescale 1ns/1ps
// Reduced-rate, loss-detecting ARM stream. The caller must enforce log2>=13:
// at most 61,035 sample pairs/s gives this 4096-word FIFO about 67 ms of cover.
// GPMC consumes two little-endian halfwords per word from a single pop port.
module adc_stream(input core,packclk,cpu,enable,input [31:0] data,input valid,
 input cpu_pop,output [15:0] cpu_data,output [12:0] available,
 output [15:0] flags,output reg [31:0] consumed=0,output fault);
 reg [31:0] mailbox=0;reg toggle=0;
 always @(posedge core)if(enable && valid)begin mailbox<=data;toggle<=!toggle;end
 reg toggle_s=0,seen=0;reg [2:0] pack_enable=0,cpu_enable=0;
 always @(posedge packclk)begin toggle_s<=toggle;seen<=toggle_s;pack_enable<={pack_enable[1:0],enable};end
 always @(posedge cpu)cpu_enable<={cpu_enable[1:0],enable};
 wire push=toggle_s!=seen && pack_enable[2];
 wire full,empty,read_full;wire [31:0] q;wire [11:0] used;
 reg half=0,underflow=0,overflow=0;
 wire pop=cpu_pop && half && !empty && cpu_enable[2];
 dcfifo #(.lpm_width(32),.lpm_numwords(4096),.lpm_widthu(12),.lpm_showahead("ON"),.add_ram_output_register("ON"),
 .rdsync_delaypipe(4),.wrsync_delaypipe(4),.read_aclr_synch("ON"),.write_aclr_synch("ON"),.use_eab("ON"),.intended_device_family("Cyclone IV E")) queue(
 .aclr(!enable),.wrclk(packclk),.data(mailbox),.wrreq(push && !full),.wrfull(full),
 .rdclk(cpu),.rdreq(pop),.q(q),.rdempty(empty),.rdfull(read_full),.rdusedw(used));
 always @(posedge packclk)if(!pack_enable[2])overflow<=0;else if(push && full)overflow<=1;
 always @(posedge cpu)begin
  if(!cpu_enable[2])begin half<=0;consumed<=0;underflow<=0;end
  else if(cpu_pop)begin
   if(empty)underflow<=1;
   else begin half<=!half;if(half)consumed<=consumed+1'b1;end
  end
 end
 reg [2:0] overflow_s=0,underflow_s=0;
 always @(posedge core)begin overflow_s<={overflow_s[1:0],overflow};underflow_s<={underflow_s[1:0],underflow};end
 assign fault=overflow_s[2] || underflow_s[2];
 assign cpu_data=half ? q[31:16] : q[15:0];
 assign available=read_full ? 13'd4096 : {1'b0,used};
 assign flags={12'b0,empty,half,cpu_enable[2],underflow};
endmodule

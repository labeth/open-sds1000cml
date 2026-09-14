`timescale 1ns/1ps
// Mocks isolate epoch/configuration/word alignment. ADC pin ordering and CIC
// arithmetic are covered by their own benches and still need board validation.
module adc_interleave #(parameter SYNC_ENCODE=0)(input refclk,memclk,packclk,enable,input [79:0] lane,input [9:0] encode_enable,
 input snapshot_request,output snapshot_ack,input consume,output [31:0] word_data,output valid,fault,
 output [79:0] snapshot,output [4:0] enc_p,enc_n,output locked);
 assign word_data=lane[31:0];assign valid=enable && lane[32];assign fault=enable && lane[33];
 assign snapshot=lane;assign snapshot_ack=snapshot_request;
 assign enc_p=encode_enable[4:0];assign enc_n=encode_enable[9:5];assign locked=!lane[34];
 always @(posedge memclk)if(enable && !consume)$fatal(1,"ADC consumption paused");
endmodule
module adc_precision #(parameter SHARED_TAIL=0)(input core,packclk,clk100,enable,input [4:0] decim_log,input [31:0] raw,input raw_valid,
 output [31:0] data,output valid,fault);
 assign data=raw^32'h965a87c3;assign valid=enable && raw_valid && raw[0];
 assign fault=enable && raw[31];
endmodule
module tb_acquisition_source;
 reg core=0,packclk=0,clk100=0;
 always #2 core=~core;always #4 packclk=~packclk;always #5 clk100=~clk100;
 reg enable=0;reg [79:0] lane=0;reg [4:0] decim_log=0;
 reg [9:0] encode_enable=10'h3ff;reg snapshot_request=0;
 wire snapshot_ack;wire [79:0] snapshot;wire [4:0] enc_p,enc_n;
 wire adc_locked,valid,fault,precision_mode;wire [31:0] data;
 adc_acquisition_source dut(.*);
 task tick;begin @(posedge core);#0.1;@(negedge core);end endtask
 task off;begin enable=0;tick;if(valid || fault)$fatal(1,"epoch reset");end endtask
 task begin_epoch(input integer log);
 begin decim_log=log;enable=1;tick;tick;end endtask
 integer i,j;
 initial begin
  tick;begin_epoch(0);
  // Changes during an epoch cannot change its format or encode mask.
  decim_log=8;encode_enable=0;
  for(i=0;i<40;i=i+1)begin
   lane=i;lane[32]=i%3!=0;tick;
   if(precision_mode || valid!==(i%3!=0) || data!==i || enc_p!=31 || enc_n!=31)
    $fatal(1,"raw timeline/configuration alignment");
  end
  lane[33]=1;tick;if(!fault || valid)$fatal(1,"raw fault not propagated");
  lane[33]=0;tick;if(!fault || valid)$fatal(1,"fault not sticky");off;
  encode_enable=10'h3ff;
  for(j=4;j<=20;j=j+1)begin
   begin_epoch(j);decim_log=0;
   for(i=0;i<8;i=i+1)begin
    lane=i;lane[32]=1;tick;
    if(!precision_mode || valid!==i[0] || data!==(32'(i)^32'h965a87c3))
     $fatal(1,"precision word/valid/configuration alignment");
   end
   off;
  end
  begin_epoch(8);lane=0;lane[31]=1;lane[32]=1;tick;
  if(!fault || valid)$fatal(1,"precision fault not propagated");off;
  lane=0;lane[32]=1;lane[34]=1;begin_epoch(0);
  if(fault || valid)$fatal(1,"unlocked startup published data or faulted prematurely");
  lane[34]=0;tick;if(!valid || fault)$fatal(1,"initial lock not accepted");
  lane[34]=1;tick;if(!fault || valid)$fatal(1,"lost ADC clock lock not detected");off;
  lane[34]=0;
  for(j=1;j<32;j=j+1)if(j<4 || j>20)begin
   begin_epoch(j);if(!fault || valid || dut.run)$fatal(1,"invalid decimation accepted");off;
  end
  $display("PASS acquisition source: uninterrupted consumption, latched raw/all precision formats, word alignment, sticky faults, ADC lock loss and invalid modes");$finish;
 end
 initial begin #100000;$fatal(1,"timeout");end
endmodule

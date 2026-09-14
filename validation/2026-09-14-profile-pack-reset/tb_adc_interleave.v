`timescale 1ns/1ps
`include "lanemap_seed.vh"
module adc_phase_pll(input refclk,output [4:0] phase,output locked);
 assign #3 phase[0]=refclk;assign #4 phase[1]=refclk;assign #2 phase[2]=refclk;assign phase[3]=refclk;assign #1 phase[4]=refclk;assign locked=1;
endmodule
module tb;
 reg refclk=0,memclk=0,packclk=0,enable=0;always #4 packclk=~packclk;always #5 refclk=~refclk;always #2 memclk=~memclk;
 wire [79:0] lane;reg [79:0] adc=0;wire [4:0] ep,en;wire [31:0] data;wire valid,fault,locked,ack;wire [79:0] snap;
 adc_interleave dut(refclk,memclk,packclk,enable,lane,10'h3ff,1'b0,ack,enable,data,valid,fault,snap,ep,en,locked);
 // Each modeled converter presents the value of a 1 ns ramp at its encode
 // edge after an output propagation delay spanning the datasheet interval.
 genvar p,b;
 generate for(p=0;p<5;p=p+1)begin:converter
  localparam CH1_P=(p==1 || p==3 || p==4);
  wire c1=CH1_P?ep[p]:en[p];wire c2=CH1_P?en[p]:ep[p];
  always @(posedge c1)adc[16*p+:8]<=#(4.5+0.3*p) $time;
  always @(posedge c2)adc[16*p+8+:8]<=#(6.0-0.3*p) $time;
  for(b=0;b<16;b=b+1)begin:bit_map
   assign lane[`LANEMAP_ENTRY(16*p+b)]=adc[16*p+b];
  end
 end endgenerate
 integer count=0,last=-1,i;
 always @(posedge memclk)if(enable && valid)begin
  for(i=0;i<4;i=i+1)begin
   if(^data[i*8+:8]===1'bx)$fatal(1,"undefined ADC byte");
   if(last>=0 && data[i*8+:8]!==((last+1)%256))$fatal(1,"ADC chronology word %d byte %d got %d after %d",count,i,data[i*8+:8],last);
   last=data[i*8+:8];
  end
  count=count+1;
 end
 initial begin
  #200;enable=1;
  #40000;if(fault || count<9900)$fatal(1,"sustained packing fault=%b count=%d",fault,count);
  $display("PASS factory-phase ADC model: continuous chronological 1 GB/s over propagation delay 4.5..6 ns");$finish;
 end
endmodule

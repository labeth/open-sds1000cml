// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-040, REQ-SDS-041, REQ-SDS-044
module bench_pll(input refclk,output reg c0=0,output c1,output locked,output reg halfclk=0);
 always #2 c0=~c0;initial begin #2;forever #4 halfclk=~halfclk;end assign #1 c1=c0;assign locked=1;
endmodule
// TRLC-LINKS: REQ-SDS-040, REQ-SDS-041, REQ-SDS-044
module adc_interleave(input refclk,memclk,packclk,enable,input [79:0] lane,input [9:0] encode_enable,
 input snapshot_request,output snapshot_ack,input consume,output [31:0] word_data,output valid,fault,output [79:0] snapshot,output [4:0] enc_p,enc_n,output locked);
 assign snapshot_ack=snapshot_request;assign word_data=32'h65646464;assign valid=enable;assign fault=0;assign snapshot=0;assign enc_p=0;assign enc_n=0;assign locked=1;
endmodule
// TRLC-LINKS: REQ-SDS-040, REQ-SDS-041, REQ-SDS-044
module tb;
 reg clk=0,mclk_in=0;always #6.25 clk=~clk;always #5 mclk_in=~mclk_in;
 wire [15:0] gpmc_d;wire [31:0] dq;wire k1,k2,g1;
 acq_sram_top dut(.clk(clk),.mclk_in(mclk_in),.nCS1(1'b1),.nOE(1'b1),.nWE(1'b1),.sel(5'b0),
  .gpmc_a2(1'b0),.gpmc_b1(1'b0),.gpmc_d(gpmc_d),.dq(dq),.lane(80'b0),.k1(k1),.k2(k2),.g1(g1));
 reg [31:0] mem[0:524287];reg [18:0] address=0;reg [31:0] s1=0,s2=0;
 assign dq=k1&&g1?s2:32'bz;
 always @(posedge k2) if(g1)begin
  if(!k1)mem[address]<=dq;else begin s1<=mem[address];s2<=#4.5 s1; /* Modeled board read-response latency exceeds one 4 ns period. */end
  address<=address+1'b1;
 end
 integer i;
 task command(input integer code);
 begin
  @(negedge clk);dut.opcode=code;dut.request=~dut.request;
  wait(dut.ack==dut.request);#100;
  if(dut.command_error)$fatal(1,"command rejected %0d",code);
 end endtask
 integer log;
 reg [18:0] frozen_position;
 reg [31:0] frozen_id;
`ifdef UART_TRIGGER
 reg [31:0] tagged_adc=0;
 wire [31:0] tagged_payload={16'd0,tagged_adc[15:0]};
 reg check_tags=0;
 reg [31:0] last_tagged_write=0;
 always @(posedge dut.core)begin
  if(!dut.timeline.count_enabled)tagged_adc<=0;
  else if(dut.frontend_run && dut.il_raw_valid)tagged_adc<=tagged_adc+2;
  if(check_tags && dut.step && dut.running && !dut.halt && !dut.arm)begin
   if(dut.capture_word!==dut.write_ordinal)$fatal(1,"raw payload/tag pipeline misaligned: %h != %h",dut.capture_word,dut.write_ordinal);
   last_tagged_write<={(tagged_adc[31:16]-(dut.capture_word[15:0]>tagged_adc[15:0])),dut.capture_word[15:0]};
  end
 end
`endif
 initial begin
  #100;
`ifdef UART_TRIGGER
  // TRLC-LINKS: REQ-SDS-013, REQ-SDS-039
  // Select every decoder in priority order, then prove a running receiver
  // cannot observe host edits to a different protocol's staging registers.
  force dut.config_idle=1;
  force dut.uart_run_sync=0;force dut.event_run_sync=0;
  dut.uart_pattern=32'h11223344;dut.uart_ticks=101;
  dut.i2c_pattern=32'h22334455;
  dut.spi_pattern=32'h33445566;dut.spi_gap=202;
  dut.sent_pattern=32'h44556677;dut.sent_ticks=303;
  dut.mil_pattern=16'h8899;dut.mil_ticks=404;dut.usb_pattern=32'hff55aa00;dut.usb_ticks=21333;
  dut.uart_cfg=1;dut.i2c_cfg=1;dut.spi_cfg=1;dut.sent_cfg=1;dut.mil_cfg=1;dut.usb_cfg=1;
  dut.man_cfg=16'h0919;dut.man_ticks=125;dut.man_pattern=64'h1111222233334444;
  #100;
  if(dut.protocol_pattern_rx!==64'h1111222233334444 || dut.protocol_ticks_rx!==125 ||
     dut.protocol_match_enable!==7'b1000000 || dut.usb.enable || dut.mil.enable || dut.uart.enable)
   $fatal(1,"Manchester wide shared config or exclusive selection");
  force dut.rs=7'd102;#1;if(dut.rd!==16'h4d01)$fatal(1,"Manchester capability read");release dut.rs;
  force dut.man_event_valid=1;force dut.man_event_kind=2;force dut.man_event_data=16'hcafe;
  force dut.man_event_sample=64'h100000025;
  #1;if(!dut.selected_event_valid || dut.selected_event_protocol!=4 || dut.selected_event_data!=32'hcafe ||
        dut.event_sample!=64'h100000025 || dut.selected_event_flags!=5)$fatal(1,"Manchester event routing");
  force dut.man_event_kind=5;
  #1;if(dut.selected_event_flags[2])$fatal(1,"Manchester loss marked valid");
  force dut.man_event_kind=3;force dut.man_event_data=1;
  #1;if(dut.selected_event_flags[2])$fatal(1,"Manchester bad frame END marked valid");
  force dut.man_match_sync=7;
  #1;if(!dut.protocol_match_selected)$fatal(1,"Manchester trigger routing");release dut.man_match_sync;
  release dut.man_event_valid;release dut.man_event_kind;release dut.man_event_data;release dut.man_event_sample;
  dut.man_cfg=0;
  #100;if(dut.protocol_pattern_rx!==32'hff55aa00 || dut.protocol_ticks_rx!==21333)$fatal(1,"USB shared config priority");
  dut.usb_cfg=0;
  #100;if(dut.protocol_pattern_rx!==32'h8899 || dut.protocol_ticks_rx!==404)$fatal(1,"MIL shared config priority");
  dut.mil_cfg=0;
  #100;if(dut.protocol_pattern_rx!==32'h44556677 || dut.protocol_ticks_rx!==303)$fatal(1,"SENT shared config priority");
  dut.sent_cfg=0;
  #100;if(dut.protocol_pattern_rx!==32'h33445566 || dut.protocol_ticks_rx!==202)$fatal(1,"SPI shared config priority");
  dut.spi_cfg=0;
  #100;if(dut.protocol_pattern_rx!==32'h22334455)$fatal(1,"I2C shared config priority");
  dut.i2c_cfg=0;
  #100;if(dut.protocol_pattern_rx!==32'h11223344 || dut.protocol_ticks_rx!==101)$fatal(1,"UART shared config priority");
  force dut.config_idle=0;force dut.uart_run_sync=3;
  dut.mil_cfg=1;dut.uart_pattern=32'hdeadbeef;dut.uart_ticks=505;
  #100;if(dut.protocol_pattern_rx!==32'h11223344 || dut.protocol_ticks_rx!==101 || dut.mil_cfg_rx!==0)$fatal(1,"active shared config changed");
  // Streaming alone must also hold the receiver bank, even when core is idle.
  force dut.config_idle=1;force dut.uart_run_sync=0;force dut.event_run_sync=7;
  #100;if(dut.protocol_pattern_rx!==32'h11223344 || dut.protocol_ticks_rx!==101 || dut.mil_cfg_rx!==0)$fatal(1,"streaming shared config changed");
  force dut.event_run_sync=0;
  #100;if(dut.protocol_pattern_rx!==32'h8899 || dut.protocol_ticks_rx!==404 || dut.mil_cfg_rx!==1)$fatal(1,"idle shared config did not refresh");
  dut.uart_cfg=0;dut.mil_cfg=0;
  dut.uart_ticks=2170;dut.spi_gap=938;dut.sent_ticks=375;dut.mil_ticks=125;
  dut.usb_pattern=0;dut.usb_ticks=21333;dut.uart_pattern=0;dut.i2c_pattern=0;dut.spi_pattern=0;dut.sent_pattern=0;dut.mil_pattern=0;
  dut.man_pattern=0;dut.man_ticks=125;
  #100;release dut.config_idle;release dut.uart_run_sync;release dut.event_run_sync;
  $display("PASS shared protocol configuration selection and active/streaming hold");
`endif
  force dut.cores=80'h123456789abcdef01234;
  command(6);
  if(dut.snap_busy || dut.snapshot!==80'h123456789abcdef01234)$fatal(1,"snapshot acknowledgement/data mismatch");
  force dut.cores=80'hfedcba9876543210fedc;
  command(6);
  if(dut.snap_busy || dut.snapshot!==80'hfedcba9876543210fedc)$fatal(1,"repeated snapshot returned old data");
  release dut.cores;
  $display("PASS staged snapshot acknowledgement and repeated data capture");
  for(log=4;log<=8;log=log+1)begin
   dut.decim_request=log;dut.pre_cfg=239;dut.post_cfg=17;
   #100;command(1);wait(dut.record_done && dut.ready);#20;
   if(dut.il_failed)$fatal(1,"precision FIFO failed");
   // This ideal SRAM has no MAX V address propagation; the extra sparse
   // origin offset is separately checked by physical device counter tests.
   for(i=0;i<256;i=i+1)if(mem[(dut.origin-1+i)%524288]!==i)
     $fatal(1,"sparse write log=%0d index=%0d got=%h origin=%0d position=%0d",log,i,mem[(dut.origin-1+i)%524288],dut.origin,dut.position);
   $display("PASS sparse /%0d: every accepted SRAM word and origin, rearm",1<<log);
  end
`ifdef UART_TRIGGER
  // Streaming may continue consuming ADC words while the retained record is
  // frozen. It must neither restart the SRAM writer nor move its position.
  frozen_id=dut.retained_id;
  @(negedge clk);dut.event_port.epoch=9;dut.event_port.enabled=1;
  #100;command(1);wait(dut.record_done && dut.ready);#20;
  if(dut.retained_id!=frozen_id+1 || dut.retained_epoch!=9)
   $fatal(1,"retained capture identity not bound to stream epoch");
  frozen_id=dut.retained_id;frozen_position=dut.position;
  #1000;
  if(!dut.frontend_run || !dut.frontend_consume || !dut.record_done || dut.running)
   $fatal(1,"event streaming did not preserve frozen acquisition");
  if(dut.position!=frozen_position || dut.il_failed)$fatal(1,"stream changed retained SRAM or failed");
  if(dut.retained_id!=frozen_id || dut.retained_epoch!=9)$fatal(1,"frozen identity changed");
  if(dut.timeline_valid)$fatal(1,"precision record advertised raw sample mapping");
  @(negedge clk);dut.event_port.enabled=0;
  #1000;
  if(dut.frontend_run)$fatal(1,"frontend did not stop after stream disable");
  if(dut.retained_id!=frozen_id || dut.retained_epoch!=9)$fatal(1,"disable invalidated retained identity");
  $display("PASS event streaming keeps ADC consuming while retained SRAM stays frozen");
  // The frontend resumes after event enable. Stale-to-live sliced levels
  // during that startup must not fabricate an I2C START before valid samples.
  force dut.uart_valid=0;force dut.uart_line=1;force dut.i2c_line=1;
  force dut.i2c_cfg_rx=16'h0001;
  @(negedge clk);dut.event_port.enabled=1;
  #64;force dut.i2c_line=0;#64;
  if(dut.i2c.active)$fatal(1,"invalid frontend levels fabricated START");
  force dut.uart_valid=1;#64;
  if(dut.i2c.active)$fatal(1,"first valid low SDA fabricated START");
  force dut.i2c_line=1;#64;force dut.i2c_line=0;#64;
  if(!dut.i2c.active)$fatal(1,"valid START was not accepted");
  release dut.uart_valid;release dut.uart_line;release dut.i2c_line;release dut.i2c_cfg_rx;
  @(negedge clk);dut.event_port.enabled=0;#1000;
  $display("PASS protocol startup waits for valid synchronized ADC levels");
  dut.decim_request=0;dut.config_word=16'h11;dut.uart_cfg=1;dut.pre_cfg=256;dut.post_cfg=16;
  #100;command(1);wait(dut.running);
  if(dut.history_ready)$fatal(1,"test failed to inject early match");
  force dut.uart_match_sync=3'b111;#12;release dut.uart_match_sync;
  wait(dut.history_ready);#80;
  if(dut.triggered || dut.uart_fired)$fatal(1,"prehistory match was reused after buffer fill");
  force dut.uart_match_sync=3'b111;#12;release dut.uart_match_sync;
  wait(dut.record_done && dut.ready);
  if(!dut.triggered)$fatal(1,"eligible protocol match did not trigger");
  $display("PASS protocol trigger ignores early matches and accepts a fresh match after prehistory");
  // Carry independently generated sample numbers through the actual raw
  // data pipeline and SRAM transport, then check the frozen timeline tag.
  force dut.il_raw_word=tagged_payload;
  @(negedge clk);dut.event_port.enabled=1;dut.uart_cfg=0;
  dut.config_word=1;dut.pre_cfg=256;dut.post_cfg=16;
  #130000;check_tags=1;command(1);wait(dut.record_done && dut.ready);#100;
  if(!dut.timeline_valid || dut.timeline_last_word!=={32'b0,last_tagged_write})
   $fatal(1,"frozen raw timeline mismatch last=%h metadata=%h valid=%b",last_tagged_write,dut.timeline_last_word,dut.timeline_valid);
  if(last_tagged_write<65536 || last_tagged_write-542>=65536)$fatal(1,"test did not retain across tag wrap");
  for(i=0;i<272;i=i+1)if(mem[(dut.origin+i)%524288]!==((last_tagged_write-2*(271-i))&32'hffff))
   $fatal(1,"raw retained sample mismatch index=%0d word=%h",i,mem[(dut.origin+i)%524288]);
  $display("PASS raw ADC ordinal follows accepted payload through SRAM and frozen metadata");
  #140000;
  if(!dut.timeline_valid || dut.timeline_last_word!=={32'b0,last_tagged_write})$fatal(1,"frozen timeline moved on live tag wrap");
  $display("PASS retained raw timeline survives later live tag wrap");
  release dut.il_raw_word;
`endif
  $finish;
 end
 initial begin #1000000;$fatal(1,"timeout");end
endmodule

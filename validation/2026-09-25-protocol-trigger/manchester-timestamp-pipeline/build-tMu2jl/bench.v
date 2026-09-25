`define UART_TRIGGER
`define INTERLEAVE
`define BURST_RECALL
`define PRECISION
`define HOST_READ_FIX
`define BENCH_MHZ 250
// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
`include "lanemap_seed.vh"
// Integration probe: full external record, 512-word host recall buffer only.
// Default mode selects one ADC pair. INTERLEAVE reproduces the factory
// five-phase schedule at 500 MS/s/channel. See README for qualification and ABI.
// TRLC-LINKS: REQ-SDS-039, REQ-SDS-040, REQ-SDS-041, REQ-SDS-042, REQ-SDS-043, REQ-SDS-044, REQ-SDS-049, REQ-SDS-052, REQ-SDS-082, REQ-SDS-083, REQ-SDS-084, REQ-SDS-186
module acq_sram_top(
 input clk,mclk_in,nCS1,nOE,nWE,input [6:2] sel,input gpmc_a2,gpmc_b1,
 inout [15:0] gpmc_d,inout [31:0] dq,input [79:0] lane,
 output k1,k2,g1,g2,d1,d2,f1,f2,j2,a11,output [4:0] enc_p,enc_n
);
 reg [7:0] level_l=128,level_low_l=124,level_high_l=132;
`ifdef STREAM_BUFFER_AW
 localparam BUF_AW=`STREAM_BUFFER_AW;
`else
`ifdef BURST_RECALL
 localparam BUF_AW=12;
`else
 localparam BUF_AW=9;
`endif
`endif
 localparam BUF_WORDS=(1<<BUF_AW);
 wire core,sample_clk,locked;
`ifdef STREAM_CAPTURE
 reg [1:0] stream_cfg=0,stream_mode=0;
 reg [1:0] stream_rise_enable=0,stream_fall_enable=0;reg stream_auto=0;
 reg streaming_fault=0;
`endif
`ifdef INTERLEAVE
 wire halfclk;bench_pll pll(mclk_in,core,sample_clk,locked,halfclk);
`else
 bench_pll pll(mclk_in,core,sample_clk,locked);
`endif
 assign g2=0;assign d1=0;assign d2=0;assign f1=1;assign f2=1;assign j2=1;assign a11=1;
 wire [79:0] cores;
`ifdef INTERLEAVE
 wire adc_locked,il_raw_valid,il_fault;reg il_failed=0,frontend_run=0;wire [31:0] il_raw_word;
 reg frontend_finished=0;
 (* preserve, dont_merge *) reg frontend_count=0;
 // A new write command cancels a stale frozen-record stop before priming.
 always @(posedge core)frontend_finished<=record_done && !priming && !arm && !(command && !command_read);
`ifdef PRECISION
 reg [4:0] decim_request=0;
 (* preserve, dont_merge *) reg [4:0] decim_log=0;
 (* preserve, dont_merge *) reg decim_active=0;
 (* preserve, dont_merge *) reg decim_data_active=0,decim_trigger_active=0;
 always @(posedge core)if(config_idle)decim_active<=decim_request!=0;
 always @(posedge core)if(config_idle)begin
  decim_data_active<=decim_request!=0;decim_trigger_active<=decim_request!=0;
 end
 // Host configuration settles before arm. Keep configuration out of the
 // running origin adder and source-selection logic across the CPU/core CDC.
 always @(posedge core)if(config_idle)decim_log<=decim_request;
 wire [31:0] precision_word;wire precision_valid,precision_fault;
 (* preserve, dont_merge *) reg precision_enable=0;
 always @(posedge core)precision_enable<=frontend_run && decim_active;
 // Precision captures the held host request in its own configuration domain.
 adc_precision #(.SHARED_TAIL(1)) precision(core,halfclk,mclk_in,precision_enable,decim_request,il_raw_word,il_raw_valid,precision_word,precision_valid,precision_fault);
 wire [31:0] selected_word=!decim_data_active ? il_raw_word : precision_word;
 wire selected_valid=!decim_active ? il_raw_valid : precision_valid;
 wire [31:0] selected_trigger=!decim_trigger_active ? il_raw_word : {precision_word[31:24],precision_word[15:8],precision_word[31:24],precision_word[15:8]};
 // Faults invalidate the whole record. One extra detection clock does not
 // turn an overflow into valid data and isolates the shutdown fanout.
 reg stream_fault=0;
 always @(posedge core)if(!frontend_run)stream_fault<=0;else stream_fault<=il_fault || precision_fault
`ifdef STREAM_CAPTURE
  || streaming_fault
`endif
  ;
`else
 wire [31:0] selected_word=il_raw_word,selected_trigger=il_raw_word;
 wire selected_valid=il_raw_valid,stream_fault=il_fault;
`endif
 // Payload selection and partial threshold comparison advance together.
 // The comparator's first stage replaces the former trigger-word register.
 reg [31:0] source_word=0;reg source_word_valid=0;
 always @(posedge core)begin
  source_word<=selected_word;
  source_word_valid<=frontend_run && selected_valid;
 end
 reg [31:0] il_word1=0,il_word=0,il_word_stage=0;reg write_offer=0,il_fire_stage=0;reg il_valid1=0,il_valid=0,source_valid=0;
 wire [1:0] rise_ch,fall_ch;
`ifdef UART_TRIGGER
 // Config is held stable by the host until command ACK, as for geometry.
 // UART samples the raw stream even when the stored record is decimated.
 reg [15:0] uart_cfg=0,uart_cfg_l=0;
 reg [23:0] uart_ticks=2170;
 reg [31:0] uart_pattern=0;
 reg [15:0] i2c_cfg=0,i2c_cfg_l=0,i2c_cfg_rx=0;
 reg [6:0] i2c_address=0,i2c_address_l=0,i2c_address_rx=0;
 reg [31:0] i2c_pattern=0;
 // SPI control: enable, clock channel, inversion, CPOL, CPHA, MSB, length[8:6].
 reg [15:0] spi_cfg=0,spi_cfg_l=0,spi_cfg_rx=0;
 reg [23:0] spi_gap=24'd938;
 reg [31:0] spi_pattern=0;
 reg [15:0] sent_cfg=0,sent_cfg_l=0,sent_cfg_rx=0;
 reg [23:0] sent_ticks=375;
 reg [6:0] sent_nibbles=8,sent_nibbles_l=8,sent_nibbles_rx=8;
 reg [31:0] sent_pattern=0;
 reg [15:0] mil_cfg=0,mil_cfg_l=0,mil_cfg_rx=0;
 reg [23:0] mil_ticks=125;
 reg [15:0] mil_pattern=0;
 reg [15:0] usb_cfg=0,usb_cfg_l=0,usb_cfg_rx=0;
 reg [23:0] usb_ticks=21333;reg [31:0] usb_pattern=0;
 // Manchester control: enable, channel, inversion, IEEE, MSB, bits[9:5], length[12:10].
 reg [15:0] man_cfg=0,man_cfg_l=0,man_cfg_rx=0;
 reg [23:0] man_ticks=125;reg [63:0] man_pattern=0;
 wire man_match,man_event_valid,man_overflow;wire [7:0] man_event_kind;
 wire [15:0] man_event_data;wire [63:0] man_event_sample;
 reg [2:0] man_match_sync=0;
 always @(posedge core)man_match_sync<={man_match_sync[1:0],man_match};
 wire usb_match,usb_event_valid;wire [7:0] usb_event_kind,usb_event_data;
 wire [15:0] usb_event_count;wire [63:0] usb_event_sample;
 wire usb_scratch_write;wire [3:0] usb_scratch_wr,usb_scratch_rd;
 wire [63:0] usb_scratch_data,usb_scratch_q;
 wire [1:0] man_scratch_write;wire [7:0] man_scratch_wr_a,man_scratch_wr_b,man_scratch_rd;
 wire [32:0] man_scratch_data_a,man_scratch_data_b,man_scratch_q_a,man_scratch_q_b;
 reg [2:0] usb_match_sync=0;
 always @(posedge core)usb_match_sync<={usb_match_sync[1:0],usb_match};
 wire mil_match,mil_event_valid,mil_event_command;wire [7:0] mil_event_kind;wire [15:0] mil_event_data;
 reg [2:0] mil_match_sync=0;
 always @(posedge core)mil_match_sync<={mil_match_sync[1:0],mil_match};
 wire sent_match,sent_event_valid;wire [7:0] sent_event_kind,sent_event_data;wire [6:0] sent_event_index;
 reg [2:0] sent_match_sync=0;
 always @(posedge core)sent_match_sync<={sent_match_sync[1:0],sent_match};
 wire spi_match,spi_event_valid;wire [7:0] spi_event_kind,spi_event_data;
 reg [2:0] spi_match_sync=0;
 always @(posedge core)spi_match_sync<={spi_match_sync[1:0],spi_match};
 reg uart_line=1,uart_valid=0,uart_fired=0;
 reg [6:0] uart_high_parts=0,uart_low_parts=0,i2c_high_parts=0,i2c_low_parts=0;
 reg uart_sample_valid=0,uart_compare_valid=0,uart_high=0,uart_low=0;
 reg i2c_high=0,i2c_low=0,i2c_line=1;
 wire uart_match;
 wire uart_byte_valid,uart_frame_error;wire [7:0] uart_byte;
 wire event_enabled,event_available,event_error,event_pop,event_rewind,event_clear,event_read_hit;
 wire [31:0] event_epoch;
 wire [3:0] event_cursor;wire [15:0] event_data,event_read_data;
 wire event_overflow;
 (* async_reg="true" *) reg [2:0] event_run_sync=0;
 (* async_reg="true" *) reg [2:0] event_core_sync=0;
 always @(posedge core)event_core_sync<={event_core_sync[1:0],event_enabled};
 // Only pipeline-local tags cross the 250 MHz stages. Sixteen bits span
 // 131 us at 500 MS/s, well beyond the bounded pipeline/snapshot latency.
 // The receiver extends every wrap and publishes full 64-bit ordinals.
 wire [15:0] raw_sample_ordinal;
 reg [15:0] source_ordinal=0,ordinal1=0,ordinal2=0,write_ordinal=0;
 reg [15:0] uart_sample_ordinal=0,uart_compare_ordinal=0,uart_line_ordinal=0;
 reg [15:0] receiver_ordinal0=0,receiver_ordinal1=0;
 wire [63:0] recent_sample,delayed_sample,timeline_last_word;
 wire timeline_valid;wire [31:0] timeline_id,timeline_epoch;
 wire [63:0] event_sample=man_cfg_rx[0] ? man_event_sample : usb_cfg_rx[0] ? usb_event_sample : mil_cfg_rx[0] ? recent_sample : (sent_cfg_rx[0] || !(spi_cfg_rx[0] || i2c_cfg_rx[0])) ? delayed_sample : recent_sample;
 always @(posedge core)begin
  source_ordinal<=raw_sample_ordinal;ordinal1<=source_ordinal;ordinal2<=ordinal1;write_ordinal<=ordinal2;
  uart_sample_ordinal<=raw_sample_ordinal|16'd1;uart_compare_ordinal<=uart_sample_ordinal;
  if(uart_compare_valid)uart_line_ordinal<=uart_compare_ordinal;
 end
 always @(posedge halfclk)begin receiver_ordinal0<=uart_line_ordinal;receiver_ordinal1<=receiver_ordinal0;end
 wire timeline_raw_mode=cfg[0]
`ifdef PRECISION
  && !decim_active
`endif
  ;
 sample_timeline #(.LOW_WIDTH(16)) timeline(.core(core),.receiver_clk(halfclk),.core_enabled(event_core_sync[2]),.receiver_enabled(event_run_sync[2]),
  .raw_valid(frontend_count && il_raw_valid),.raw_sample(raw_sample_ordinal),
  .receiver_valid(protocol_valid_sync[1]),.receiver_sample(receiver_ordinal1),.recent_sample(recent_sample),.delayed_sample(delayed_sample),
  .arm(arm),.write_step(step && running && !halt),.raw_mode(timeline_raw_mode),.write_sample(write_ordinal),
  .frozen(record_done),.record_id(retained_id),.record_epoch(retained_epoch),
  .retained_valid(timeline_valid),.retained_last_word(timeline_last_word),.retained_id(timeline_id),.retained_epoch(timeline_epoch));
 reg [31:0] retained_id=0,retained_epoch=0;
 reg [2:0] retained_carry=0;
 (* preserve, dont_merge *) reg retained_arm=0;
 always @(posedge core)retained_arm<=locked && arm;
 // Arm is already validated by the acquisition owner. Identity remains
 // unchanged throughout trigger, freeze and repeated host recall.
 always @(posedge core)begin
  if(!locked)begin retained_id<=0;retained_epoch<=0;retained_carry<=0;end
  else if(retained_arm)begin
   retained_id[7:0]<=retained_id[7:0]+1'b1;
   retained_id[15:8]<=retained_id[15:8]+retained_carry[0];
   retained_id[23:16]<=retained_id[23:16]+retained_carry[1];
   retained_id[31:24]<=retained_id[31:24]+retained_carry[2];
   retained_carry<={retained_id[23:0]==24'hfffffe,retained_id[15:0]==16'hfffe,retained_id[7:0]==8'hfe};
   retained_epoch<=event_core_sync[2] ? event_epoch : 32'd0;
  end
 end
 always @(posedge halfclk)begin
  event_run_sync<={event_run_sync[1:0],event_enabled};
 end
 wire i2c_match;
 wire i2c_event_valid;wire [7:0] i2c_event_kind,i2c_event_data;
 wire [1:0] i2c_event_meta;
 reg [1:0] uart_line_sync=2'b11,uart_run_sync=0;
 reg [1:0] protocol_valid_sync=0;
 wire protocol_receiver_reset=!(uart_run_sync[1] || event_run_sync[2]) || !protocol_valid_sync[1];
 reg [2:0] uart_match_sync=0;
 reg [1:0] i2c_line_sync=2'b11;
 reg [2:0] i2c_match_sync=0;
 reg [15:0] uart_cfg_rx=0;
 // TRLC-LINKS: REQ-SDS-013, REQ-SDS-039
 // Only the selected decoder runs. Share its held timing/pattern banks while
 // preserving independent host staging registers and the selection priority.
 reg [23:0] protocol_ticks_l=2170,protocol_ticks_rx=2170;
 reg [63:0] protocol_pattern_l=0,protocol_pattern_rx=0;
 reg [23:0] protocol_ticks_host=2170;reg [63:0] protocol_pattern_host=0;
 // Resolve host selection before the held configuration crosses into core.
 always @(posedge clk)begin
  protocol_ticks_host<=man_cfg[0] ? man_ticks : usb_cfg[0] ? usb_ticks : mil_cfg[0] ? mil_ticks : sent_cfg[0] ? sent_ticks : spi_cfg[0] ? spi_gap : uart_ticks;
  protocol_pattern_host<=man_cfg[0] ? man_pattern : {32'd0,(usb_cfg[0] ? usb_pattern : mil_cfg[0] ? {16'd0,mil_pattern} : sent_cfg[0] ? sent_pattern : spi_cfg[0] ? spi_pattern : i2c_cfg[0] ? i2c_pattern : uart_pattern)};
 end
 always @(posedge halfclk)begin
  uart_line_sync<={uart_line_sync[0],uart_line};
  i2c_line_sync<={i2c_line_sync[0],i2c_line};
  protocol_valid_sync<={protocol_valid_sync[0],uart_valid};
  uart_run_sync<={uart_run_sync[0],running && !arm};
  if(!uart_run_sync[1] && !event_run_sync[2])begin
   man_cfg_rx<=man_cfg_l;usb_cfg_rx<=usb_cfg_l;mil_cfg_rx<=mil_cfg_l;
   protocol_ticks_rx<=protocol_ticks_l;protocol_pattern_rx<=protocol_pattern_l;
   sent_cfg_rx<=sent_cfg_l;sent_nibbles_rx<=sent_nibbles_l;
   spi_cfg_rx<=spi_cfg_l;
   uart_cfg_rx<=uart_cfg_l;
   i2c_cfg_rx<=i2c_cfg_l;i2c_address_rx<=i2c_address_l;
  end
 end
 always @(posedge core)uart_match_sync<={uart_match_sync[1:0],uart_match};
 always @(posedge core)i2c_match_sync<={i2c_match_sync[1:0],i2c_match};
 reg protocol_channel=0;
 (* preserve, dont_merge *) reg [6:0] protocol_match_enable=7'b0000001;
 wire protocol_match_selected=(protocol_match_enable[6] && man_match_sync[2]) || (protocol_match_enable[5] && usb_match_sync[2]) || (protocol_match_enable[4] && mil_match_sync[2]) || (protocol_match_enable[3] && sent_match_sync[2]) ||
  (protocol_match_enable[2] && spi_match_sync[2]) || (protocol_match_enable[1] && i2c_match_sync[2]) ||
  (protocol_match_enable[0] && uart_match_sync[2]);
 reg protocol_match_level=0,protocol_match_previous=0,protocol_match_ready=0;
 // Separate decoder selection from trigger eligibility and edge detection.
 always @(posedge core)begin
  protocol_match_level<=protocol_match_selected;protocol_match_ready<=running && !arm && history_ready;
 end
 wire history_ready;
 always @(posedge core)if(config_idle)begin
  protocol_channel<=man_cfg[0] ? man_cfg[1] : usb_cfg[0] ? usb_cfg[1] : mil_cfg[0] ? mil_cfg[1] : sent_cfg[0] ? sent_cfg[1] : spi_cfg[0] ? spi_cfg[1] : i2c_cfg[0] ? i2c_cfg[1] : uart_cfg[1];
  protocol_match_enable<=man_cfg[0] ? 7'b1000000 : usb_cfg[0] ? 7'b0100000 : mil_cfg[0] ? 7'b0010000 : sent_cfg[0] ? 7'b0001000 : spi_cfg[0] ? 7'b0000100 : i2c_cfg[0] ? 7'b0000010 : 7'b0000001;
 end
 wire [7:0] uart_code=protocol_channel ? il_raw_word[31:24] : il_raw_word[23:16];
 wire [7:0] i2c_code=protocol_channel ? il_raw_word[23:16] : il_raw_word[31:24];
 (* preserve, dont_merge *) reg [7:0] protocol_low=0,protocol_high=0;
 // Partial comparison replaces the old sample register, retaining latency.
 // TRLC-LINKS: REQ-SDS-013, REQ-SDS-039
 function [6:0] compare_parts(input [7:0] a,b);begin
  compare_parts={a[7:6]>b[7:6],a[7:6]==b[7:6],a[5:4]>b[5:4],a[5:4]==b[5:4],a[3:2]>b[3:2],a[3:2]==b[3:2],a[1:0]>=b[1:0]};
 end endfunction
 always @(posedge core)begin protocol_low<=level_low_l;protocol_high<=level_high_l;end
 always @(posedge core)begin
  uart_sample_valid<=il_raw_valid;
  uart_high_parts<=compare_parts(uart_code,protocol_high);uart_low_parts<=compare_parts(protocol_low,uart_code);
  i2c_high_parts<=compare_parts(i2c_code,protocol_high);i2c_low_parts<=compare_parts(protocol_low,i2c_code);
  i2c_high<=i2c_high_parts[6] || (i2c_high_parts[5] && (i2c_high_parts[4] || (i2c_high_parts[3] && (i2c_high_parts[2] || (i2c_high_parts[1] && i2c_high_parts[0])))));
  i2c_low<=i2c_low_parts[6] || (i2c_low_parts[5] && (i2c_low_parts[4] || (i2c_low_parts[3] && (i2c_low_parts[2] || (i2c_low_parts[1] && i2c_low_parts[0])))));
  uart_high<=uart_high_parts[6] || (uart_high_parts[5] && (uart_high_parts[4] || (uart_high_parts[3] && (uart_high_parts[2] || (uart_high_parts[1] && uart_high_parts[0])))));
  uart_low<=uart_low_parts[6] || (uart_low_parts[5] && (uart_low_parts[4] || (uart_low_parts[3] && (uart_low_parts[2] || (uart_low_parts[1] && uart_low_parts[0])))));
  uart_compare_valid<=uart_sample_valid;uart_valid<=uart_compare_valid;
  if(uart_compare_valid)begin
   if(uart_high)uart_line<=1;
   else if(uart_low)uart_line<=0;
   if(i2c_high)i2c_line<=1;
   else if(i2c_low)i2c_line<=0;
  end
  protocol_match_previous<=protocol_match_level;
  if(!running || arm || !history_ready)uart_fired<=0;
  else if(protocol_match_level && !protocol_match_previous && protocol_match_ready)uart_fired<=1;
  if(config_idle)begin
   man_cfg_l<=man_cfg;usb_cfg_l<=usb_cfg;mil_cfg_l<=mil_cfg;
   protocol_ticks_l<=protocol_ticks_host;
   protocol_pattern_l<=protocol_pattern_host;
   sent_cfg_l<=sent_cfg;sent_nibbles_l<=sent_nibbles;
   spi_cfg_l<=spi_cfg;
   uart_cfg_l<=uart_cfg;
   i2c_cfg_l<=i2c_cfg;i2c_address_l<=i2c_address;
  end
 end
 uart_trigger uart(.clk(halfclk),.reset(protocol_receiver_reset),.enable(!man_cfg_rx[0] && !usb_cfg_rx[0] && uart_cfg_rx[0] && !i2c_cfg_rx[0] && !spi_cfg_rx[0] && !sent_cfg_rx[0] && !mil_cfg_rx[0]),
  .tick(1'b1),.line(uart_line_sync[1]),.inverted(uart_cfg_rx[2]),
  .bit_ticks(protocol_ticks_rx),.pattern(protocol_pattern_rx[31:0]),.pattern_len(uart_cfg_rx[5:3]),
  .match(uart_match),.byte_valid(uart_byte_valid),.framing_error(uart_frame_error),.byte_data(uart_byte));
 spi_trigger spi(.clk(halfclk),.reset(protocol_receiver_reset),
  .enable(!man_cfg_rx[0] && !usb_cfg_rx[0] && spi_cfg_rx[0] && !sent_cfg_rx[0] && !mil_cfg_rx[0]),.tick(1'b1),.sck(uart_line_sync[1]^spi_cfg_rx[2]),.data(i2c_line_sync[1]^spi_cfg_rx[2]),
  .cpol(spi_cfg_rx[3]),.cpha(spi_cfg_rx[4]),.msb(spi_cfg_rx[5]),.gap_ticks(protocol_ticks_rx),
  .pattern(protocol_pattern_rx[31:0]),.pattern_len(spi_cfg_rx[8:6]),.match(spi_match),
  .event_valid(spi_event_valid),.event_kind(spi_event_kind),.event_data(spi_event_data));
 sent_trigger sent(.clk(halfclk),.reset(protocol_receiver_reset),.enable(!man_cfg_rx[0] && !usb_cfg_rx[0] && sent_cfg_rx[0] && !mil_cfg_rx[0]),.tick(1'b1),
  .line(uart_line_sync[1]),.inverted(sent_cfg_rx[2]),.pause_pulse(sent_cfg_rx[3]),
  .tick_ticks(protocol_ticks_rx),.nibbles(sent_nibbles_rx),.pattern(protocol_pattern_rx[31:0]),.pattern_len(sent_cfg_rx[6:4]),
  .match(sent_match),.event_valid(sent_event_valid),.event_kind(sent_event_kind),.event_data(sent_event_data),.event_index(sent_event_index));
 mil1553_trigger mil(.clk(halfclk),.reset(protocol_receiver_reset),.enable(!man_cfg_rx[0] && !usb_cfg_rx[0] && mil_cfg_rx[0]),.tick(1'b1),
  .line(uart_line_sync[1]),.inverted(mil_cfg_rx[2]),.bit_ticks(protocol_ticks_rx),.pattern(protocol_pattern_rx[15:0]),.match_any(mil_cfg_rx[3]),
  .match(mil_match),.event_valid(mil_event_valid),.event_kind(mil_event_kind),.event_data(mil_event_data),.event_command(mil_event_command));
 usbls_trigger #(.EXTERNAL_RAM(1)) usb(.clk(halfclk),.reset(protocol_receiver_reset),.enable(!man_cfg_rx[0] && usb_cfg_rx[0]),.tick(1'b1),
  .line(uart_line_sync[1]),.inverted(usb_cfg_rx[2]),.bit_ticks_q8(protocol_ticks_rx),
  .pattern(protocol_pattern_rx[31:0]),.pattern_len(usb_cfg_rx[5:3]),.sample(recent_sample),
  .match(usb_match),.event_valid(usb_event_valid),.event_kind(usb_event_kind),.event_data(usb_event_data),
  .event_count(usb_event_count),.event_sample(usb_event_sample),
  .scratch_write(usb_scratch_write),.scratch_wr(usb_scratch_wr),.scratch_rd(usb_scratch_rd),
  .scratch_data(usb_scratch_data),.scratch_q(usb_scratch_q));
 manchester_trigger #(.EXTERNAL_RAM(1)) man(.clk(halfclk),.reset(protocol_receiver_reset),.enable(man_cfg_rx[0]),.tick(1'b1),
  .line(uart_line_sync[1]),.inverted(man_cfg_rx[2]),.ieee(man_cfg_rx[3]),.msb(man_cfg_rx[4]),
  .word_bits(man_cfg_rx[9:5]),.bit_ticks(protocol_ticks_rx),.pattern(protocol_pattern_rx),.pattern_len(man_cfg_rx[12:10]),
  .sample(recent_sample),.match(man_match),.event_valid(man_event_valid),.event_kind(man_event_kind),
  .event_data(man_event_data),.event_sample(man_event_sample),.overflow(man_overflow),
  .scratch_write(man_scratch_write),.scratch_wr_a(man_scratch_wr_a),.scratch_wr_b(man_scratch_wr_b),.scratch_rd(man_scratch_rd),
  .scratch_data_a(man_scratch_data_a),.scratch_data_b(man_scratch_data_b),.scratch_q_a(man_scratch_q_a),.scratch_q_b(man_scratch_q_b));
 protocol_scratch scratch(.clk(halfclk),.reset(protocol_receiver_reset),.manchester(man_cfg_rx[0]),
  .usb_write(usb_scratch_write),.usb_wr(usb_scratch_wr),.usb_rd(usb_scratch_rd),.usb_data(usb_scratch_data),.usb_q(usb_scratch_q),
  .man_write(man_scratch_write),.man_wr_a(man_scratch_wr_a),.man_wr_b(man_scratch_wr_b),.man_rd(man_scratch_rd),
  .man_data_a(man_scratch_data_a),.man_data_b(man_scratch_data_b),.man_q_a(man_scratch_q_a),.man_q_b(man_scratch_q_b));
 wire selected_event_valid=man_cfg_rx[0] ? man_event_valid : usb_cfg_rx[0] ? usb_event_valid : mil_cfg_rx[0] ? mil_event_valid : sent_cfg_rx[0] ? sent_event_valid : spi_cfg_rx[0] ? spi_event_valid : (i2c_cfg_rx[0] ? i2c_event_valid : (uart_byte_valid || uart_frame_error));
 wire [7:0] selected_event_kind=man_cfg_rx[0] ? man_event_kind : usb_cfg_rx[0] ? usb_event_kind : mil_cfg_rx[0] ? mil_event_kind : sent_cfg_rx[0] ? sent_event_kind : spi_cfg_rx[0] ? spi_event_kind : (i2c_cfg_rx[0] ? i2c_event_kind : (uart_frame_error ? 8'd4 : 8'd2));
 wire [31:0] selected_event_data=man_cfg_rx[0] ? {16'd0,man_event_data} : usb_cfg_rx[0] ? {24'd0,usb_event_data} : mil_cfg_rx[0] ? {16'd0,mil_event_data} : sent_cfg_rx[0] ? {24'd0,sent_event_data} : spi_cfg_rx[0] ? {24'd0,spi_event_data} : (i2c_cfg_rx[0] ? {24'd0,i2c_event_data} : {24'd0,uart_byte});
 wire [7:0] selected_event_protocol=man_cfg_rx[0] ? 8'd4 : usb_cfg_rx[0] ? 8'd9 : mil_cfg_rx[0] ? 8'd7 : sent_cfg_rx[0] ? 8'd5 : spi_cfg_rx[0] ? 8'd3 : (i2c_cfg_rx[0] ? 8'd2 : 8'd1);
 wire [7:0] selected_event_flags=((selected_event_kind==4 || selected_event_kind==5 || (man_cfg_rx[0] && selected_event_kind==3 && man_event_data!=0)) ? 8'd0 : 8'd4) |
  (man_cfg_rx[0] ? (man_cfg_rx[1] ? 8'd2 : 8'd1) : usb_cfg_rx[0] ? (usb_cfg_rx[1] ? 8'd2 : 8'd1) : mil_cfg_rx[0] ? (mil_cfg_rx[1] ? 8'd2 : 8'd1) : sent_cfg_rx[0] ? (sent_cfg_rx[1] ? 8'd2 : 8'd1) : (spi_cfg_rx[0] || i2c_cfg_rx[0]) ? 8'd3 : (uart_cfg_rx[1] ? 8'd2 : 8'd1));
 decoded_event_port event_port(
  .clk(clk),.reset(!locked),.write(wc),.read_pop(rp),.write_sel(ws),.read_sel(rs),.write_data(wd),
  .available(event_available),.error(event_error),.cursor(event_cursor),.data(event_data),
  .enabled(event_enabled),.epoch(event_epoch),.pop(event_pop),.rewind(event_rewind),.clear_error(event_clear),
  .read_hit(event_read_hit),.read_data(event_read_data),.complete_count(event_host_count));
 // Buffer host scheduling gaps without consuming the retained sample SRAM.
 wire [15:0] event_host_count;
 // Preserve full-width protocol values/counts. Both FIFO stages reconstruct
 // invariant epoch/sequence fields rather than duplicating them in RAM.
 decoded_event_transport #(.AW(10),.HOST_AW(9)) events(
  .reset(!locked || !event_enabled),.source_clk(halfclk),.host_clk(clk),.epoch(event_epoch),
  .in_valid(event_run_sync[2] && protocol_valid_sync[1] && selected_event_valid),.in_sample(event_sample),
  .in_kind(selected_event_kind),.in_protocol(selected_event_protocol),.in_flags(selected_event_flags),
  .in_record(32'd0),.in_value(selected_event_data),
  .in_count((man_cfg_rx[0] || usb_cfg_rx[0]) ? 32'd0 : mil_cfg_rx[0] ? {31'd0,mil_event_command} : sent_cfg_rx[0] ? {25'd0,sent_event_index} : (i2c_cfg_rx[0] && !spi_cfg_rx[0]) ? {30'd0,i2c_event_meta} : 32'd0),
  .host_pop(event_pop),.host_rewind(event_rewind),.host_clear_error(event_clear),
  .host_available(event_available),.host_error(event_error),.host_cursor(event_cursor),.host_data(event_data),
  .source_overflow(event_overflow),.host_count(event_host_count));
 i2c_trigger i2c(.clk(halfclk),.reset(protocol_receiver_reset),.enable(!man_cfg_rx[0] && !usb_cfg_rx[0] && i2c_cfg_rx[0] && !spi_cfg_rx[0] && !sent_cfg_rx[0] && !mil_cfg_rx[0]),.tick(1'b1),
  .scl(uart_line_sync[1]^i2c_cfg_rx[2]),.sda(i2c_line_sync[1]^i2c_cfg_rx[2]),
  .address(i2c_address_rx),.any_address(i2c_cfg_rx[5]),.direction(i2c_cfg_rx[4:3]),
  .pattern(protocol_pattern_rx[31:0]),.pattern_len(i2c_cfg_rx[8:6]),.match(i2c_match),
  .event_valid(i2c_event_valid),.event_kind(i2c_event_kind),.event_data(i2c_event_data),.event_meta(i2c_event_meta));
 wire protocol_fire=(man_cfg_l[0] || uart_cfg_l[0] || i2c_cfg_l[0] || spi_cfg_l[0] || sent_cfg_l[0] || mil_cfg_l[0] || usb_cfg_l[0]) ? uart_fired : (cfg[5]?fall_ch[cfg[6]]:rise_ch[cfg[6]]);
`else
 wire protocol_fire=cfg[5]?fall_ch[cfg[6]]:rise_ch[cfg[6]];
`endif
 wire il_crossing=!cfg[4] || force_trigger || protocol_fire;
 // Decode stable configuration before the hot edge-to-trigger path.
 reg [1:0] edge_rise_enable=0,edge_fall_enable=0;
 reg edge_auto=0,protocol_enable=0;
 wire protocol_configured=
`ifdef UART_TRIGGER
  man_cfg_l[0] || uart_cfg_l[0] || i2c_cfg_l[0] || spi_cfg_l[0] || sent_cfg_l[0] || mil_cfg_l[0] || usb_cfg_l[0];
`else
  1'b0;
`endif
 always @(posedge core)if(config_idle)begin
  edge_auto<=!cfg[4];
  protocol_enable<=cfg[0] && cfg[4] && protocol_configured;
  edge_rise_enable<=(!protocol_configured && cfg[0] && cfg[4] && !cfg[5]) ? (cfg[6] ? 2'b10 : 2'b01) : 2'b00;
  edge_fall_enable<=(!protocol_configured && cfg[0] && cfg[4] && cfg[5]) ? (cfg[6] ? 2'b10 : 2'b01) : 2'b00;
 end
 genvar tc;generate for(tc=0;tc<2;tc=tc+1)begin:trigger_compare
  sram_edge_pair #(.PIPELINED(1)) edges(.clk(core),.enable(frontend_run),.valid(frontend_run && selected_valid),
   .first(selected_trigger[8*tc+:8]),.second(selected_trigger[16+8*tc+:8]),
   .level(level_l),.low_level(level_low_l),.high_level(level_high_l),
   .rise(rise_ch[tc]),.fall(fall_ch[tc]));
 end endgenerate
 always @(posedge core)begin
  il_valid1<=source_word_valid;il_valid<=il_valid1;`ifdef PRECISION
  source_valid<=(cfg[0] || decim_active) ? il_valid1 : 1'b1;
`else
  source_valid<=cfg[0] ? il_valid1 : 1'b1;
`endif
  il_word1<=source_word;il_word<=il_word1;il_word_stage<=il_word;
  write_offer<=source_valid && write_ready;
`ifdef STREAM_CAPTURE
  il_fire_stage<=force_trigger || stream_auto || (|(rise_ch & stream_rise_enable)) || (|(fall_ch & stream_fall_enable));
`else
  il_fire_stage<=force_trigger || edge_auto || (|(rise_ch & edge_rise_enable)) || (|(fall_ch & edge_fall_enable))
`ifdef UART_TRIGGER
   || (protocol_enable && uart_fired)
`endif
   ;
`endif
  if(!frontend_run)begin il_valid1<=0;il_valid<=0;end
 end
 reg snap_request=0,snap_busy=0,snap_capture=0;wire snap_ack;
 reg [9:0] encode_enable=10'h3ff;
 wire frontend_consume=frontend_run && (write_ready
`ifdef UART_TRIGGER
  || event_core_sync[2]
`endif
 );
 adc_interleave frontend(.refclk(mclk_in),.memclk(core),.packclk(halfclk),.enable(frontend_run),.lane(lane),.encode_enable(encode_enable),
 .snapshot_request(snap_request),.snapshot_ack(snap_ack),.consume(frontend_consume),.word_data(il_raw_word),.valid(il_raw_valid),.fault(il_fault),.snapshot(cores),.enc_p(enc_p),.enc_n(enc_n),.locked(adc_locked));
`else
 genvar e;generate for(e=0;e<5;e=e+1)begin:enc
  ddio_pair pair(.outclock(core),.dh(locked),.dl(1'b0),.pad_p(enc_p[e]),.pad_n(enc_n[e]));
 end endgenerate
 wire [79:0] lane_q;
 // Use the existing pad-register primitive, then retime the selected core
 // into the memory clock domain. +4 ns is a candidate ADC capture phase;
 // every core must pass a fresh DC transition sweep before accepting it.
 lane_in #(.N(80)) adc_inputs(.clk(sample_clk),.pad(lane),.q(lane_q));
 adc_unpack unpack(lane_q,cores);
`endif
 wire wc,rp,drive;wire [7:0] ws,rs;wire [15:0] wd;wire [1:0] aux;reg [15:0] rd;
`ifdef HOST_READ_FIX
 gpmc_slave #(.QUALIFIED_READ(1)) busif(clk,nCS1,nOE,nWE,{sel,gpmc_a2,gpmc_b1},gpmc_d,wc,ws,wd,aux,rp,rs,rd,drive);
`else
 gpmc_slave busif(clk,nCS1,nOE,nWE,{sel,gpmc_a2,gpmc_b1},gpmc_d,wc,ws,wd,aux,rp,rs,rd,drive);
`endif
 reg request=0;reg [3:0] opcode=0;
 reg [19:0] pre_cfg=524271,post_cfg=17,count_cfg=512;
 reg [18:0] pre_start_host=0;
 reg [19:0] pre_plus_one_host=0,post_minus_one_host=0;
 // Configuration writes settle before the command handshake. Compute wide
 // arithmetic at the host clock instead of across the host/core boundary.
 always @(posedge clk)begin
  pre_start_host<=-pre_cfg[18:0];pre_plus_one_host<=pre_cfg+1'b1;post_minus_one_host<=post_cfg-1'b1;
 end
 reg [15:0] config_word=0;reg [7:0] level=128,hysteresis=4;reg [BUF_AW-1:0] buffer_index=0;
 reg [7:0] host_level_low=124,host_level_high=132;
 always @(posedge clk)begin
  host_level_low<=level>=hysteresis ? level-hysteresis : 8'd0;
  host_level_high<=({1'b0,level}+{1'b0,hysteresis})<=255 ? level+hysteresis : 8'd255;
 end
`ifdef BURST_RECALL
 reg [BUF_AW:0] burst_index=0;
 always @(posedge clk)begin
  if(wc && ws==17)burst_index<=wd[BUF_AW:0];
  else if(rp && rs==25)burst_index<=burst_index+1'b1;
 end
`endif
 always @(posedge clk) if(wc) case(ws)
  1:begin opcode<=wd[3:0];request<=~request;end
  2:pre_cfg[15:0]<=wd;3:pre_cfg[19:16]<=wd[3:0];
  4:post_cfg[15:0]<=wd;5:post_cfg[19:16]<=wd[3:0];
  6:config_word<=wd;7:begin level<=wd[7:0];hysteresis<=wd[15:8]!=0 ? wd[15:8] : 8'd4;end
`ifdef UART_TRIGGER
  48:uart_cfg<=wd;
  49:uart_ticks[15:0]<=wd;
  50:uart_ticks[23:16]<=wd[7:0];
  51:uart_pattern[15:0]<=wd;
  52:uart_pattern[31:16]<=wd;
  53:i2c_cfg<=wd;
  54:i2c_address<=wd[6:0];
  55:i2c_pattern[15:0]<=wd;
  56:i2c_pattern[31:16]<=wd;
  77:sent_cfg<=wd;78:sent_ticks[15:0]<=wd;79:sent_ticks[23:16]<=wd[7:0];
  80:sent_pattern[15:0]<=wd;81:sent_pattern[31:16]<=wd;82:sent_nibbles<=wd[6:0];
 97:usb_cfg<=wd;98:usb_ticks[15:0]<=wd;99:usb_ticks[23:16]<=wd[7:0];100:usb_pattern[15:0]<=wd;101:usb_pattern[31:16]<=wd;
  102:man_cfg<=wd;103:man_ticks[15:0]<=wd;104:man_ticks[23:16]<=wd[7:0];
  105:man_pattern[15:0]<=wd;106:man_pattern[31:16]<=wd;107:man_pattern[47:32]<=wd;108:man_pattern[63:48]<=wd;
  93:mil_cfg<=wd;94:mil_ticks[15:0]<=wd;95:mil_ticks[23:16]<=wd[7:0];96:mil_pattern<=wd;
  64:spi_cfg<=wd;
  65:spi_gap[15:0]<=wd;66:spi_gap[23:16]<=wd[7:0];
  67:spi_pattern[15:0]<=wd;68:spi_pattern[31:16]<=wd;
`endif
  8:count_cfg[15:0]<=wd;9:count_cfg[19:16]<=wd[3:0];
`ifdef INTERLEAVE
  15:encode_enable<=wd[9:0];
`endif
`ifdef PRECISION
  18:decim_request<=wd[4:0];
`endif
`ifdef STREAM_CAPTURE
  19:stream_cfg<=wd[1:0];
`endif
  16:buffer_index<=wd[BUF_AW-1:0];
 endcase
 reg arm_geometry_ok=0,read_geometry_ok=0,buffer_count_ok=0;
 // Evaluate host geometry before its request toggle crosses into core. This
 // removes the wide host-to-core addition from command acceptance timing.
 reg host_geometry_ok=0;
 always @(posedge clk)host_geometry_ok<=post_cfg!=0 &&
  ({1'b0,pre_cfg}+{1'b0,post_cfg})<=21'd524288 && config_word[3:1]<5;
`ifdef PRECISION
 wire decim_legal=(decim_log==0 || (decim_log>=4 && decim_log<=20))
`ifdef STREAM_CAPTURE
 && (!stream_mode[0] || decim_log>=8) && (!stream_mode[1] || stream_mode[0])
`endif
 ;
`else
 wire decim_legal=1;
`endif
 always @(posedge core)begin
  arm_geometry_ok<=decim_legal && host_geometry_ok;
  read_geometry_ok<=count_cfg!=0 && count_cfg<=524288;
  buffer_count_ok<=count_cfg<=BUF_WORDS;
 end
 reg [2:0] req_s=0;reg ack=0;
 wire pending=req_s[2]!=ack;
 always @(posedge core) req_s<={req_s[1:0],request};
 wire ready,write_ready,transport_done,read_valid;
`ifdef INTERLEAVE
 wire host_ready=ready && !snap_busy;
`else
 wire host_ready=ready;
`endif
 wire [31:0] read_data;wire [18:0] position;
 reg command=0,command_read=0,command_discard=0,command_continue=0,prefetch_valid=0;
 reg [19:0] command_count=0;
 reg arm=0,halt=0,force_trigger=0,command_error=0;
 reg dispatch=0,dispatch_arm_ok=0,dispatch_read_ok=0,config_idle=0;reg [3:0] dispatch_opcode=0;
 reg dispatch_discard=0,dispatch_continue=0;
 reg priming=0;
 reg prime_ready=0;always @(posedge core)prime_ready<=priming && write_ready;
`ifdef INTERLEAVE
 reg [3:0] prime_left=0;
`else
 reg [4:0] prime_left=0;
`endif
 reg [18:0] origin=0;
 wire running,record_done,triggered,config_error;
 wire [18:0] write_addr,record_start;
 wire [19:0] record_length,trigger_index,filled;
 reg [15:0] cfg=0;
 reg [31:0] ramp=0,packed_word=0;reg ramp_carry=0;
 reg [15:0] selected=0,first_sample=0;reg half=0,packed_valid=0;
 reg [7:0] previous=0;
`ifdef INTERLEAVE
 wire [31:0] capture_word=il_word_stage;
`else
 wire [31:0] capture_word=packed_word;
`endif
 wire [7:0] current=cfg[6] ? capture_word[15:8] : capture_word[7:0];
`ifdef INTERLEAVE
 wire crossing=il_crossing;
`else
 wire crossing=cfg[5] ? (previous>level_l && current<=level_l) : (previous<level_l && current>=level_l);
`endif
 `ifdef INTERLEAVE
 wire fire=il_fire_stage;
`else
 wire fire=!cfg[4] || force_trigger || crossing;
`endif
`ifdef INTERLEAVE
 wire step=write_offer;
`else
 wire step=packed_valid && write_ready;
`endif
`ifdef INTERLEAVE
 wire dummy_write=priming || arm;
`else
 wire dummy_write=priming;
`endif
 // The transport alone qualifies write_valid with its write-ready state.
 // Repeating that decode on priming adds a reconvergent control path.
 wire write_valid=(step && running && !halt) || dummy_write;
 // Priming words precede the record origin and carry no retained data.
 // Their value need not be zero; avoid placing arm/priming on the data mux.
 wire [31:0] write_data=cfg[0] ? capture_word : ramp;
 wire write_stop=!running && !priming && !arm;
`ifdef INTERLEAVE
 localparam SRAM_READ_DELAY=1;
`else
 localparam SRAM_READ_DELAY=0;
`endif
 sram_transport #(.CONTINUOUS_ONLY(1),.READ_DELAY(SRAM_READ_DELAY)) transport(.clk(core),.sample_clk(sample_clk),.reset(1'b0),.locked(locked),
  .command(command),.command_read(command_read),.command_discard(command_discard),.command_continue(command_continue),.command_count(command_count),
  .ready(ready),.write_data(write_data),.write_valid(write_valid),.write_stop(write_stop),.write_ready(write_ready),
  .read_data(read_data),.read_valid(read_valid),.done(transport_done),.position(position),.dq(dq),.k1(k1),.k2(k2),.g1(g1));
 wire record_fire=fire;
 sram_record #(.CONFIG_VALIDATED(1),.DERIVED_CONFIG(1)) record(.clk(core),.reset(1'b0),.arm(arm),.halt(halt),.pre_count(pre_cfg),.post_count(post_cfg),
  .pre_start_config(pre_start_host),.pre_plus_one_config(pre_plus_one_host),.post_minus_one_config(post_minus_one_host),
`ifdef UART_TRIGGER
  .history_ready(history_ready),
`endif
  .step(step),.trigger(record_fire),.running(running),.done(record_done),.triggered(triggered),.config_error(config_error),
  .write_addr(write_addr),.record_start(record_start),.record_length(record_length),.trigger_index(trigger_index),.filled(filled));
 reg [BUF_AW:0] received=0;
`ifdef INTERLEAVE
 reg [63:0] buffer_mem64[0:BUF_WORDS/2-1];reg [63:0] buffer_read64=0;reg buffer_half=0;
 wire [31:0] buffer_word=buffer_half ? buffer_read64[63:32] : buffer_read64[31:0];
 reg [31:0] read_first=0;reg read_half=0,packet_toggle=0,packet_seen=0;reg [63:0] packet_data=0;reg [BUF_AW-2:0] packet_addr=0;
 `ifdef BURST_RECALL
 reg [1:0] burst_half=0;
 always @(posedge clk)begin
  buffer_read64<=buffer_mem64[rs==25 ? burst_index[BUF_AW:2] : buffer_index[BUF_AW-1:1]];
  burst_half<=burst_index[1:0];buffer_half<=buffer_index[0];
 end
`else
 always @(posedge clk)begin buffer_read64<=buffer_mem64[buffer_index[BUF_AW-1:1]];buffer_half<=buffer_index[0];end
`endif
 always @(posedge core)begin
  // Payload may track continuously; read_half and read_valid own publication.
  read_first<=read_data;
  if(read_valid)begin
   read_half<=!read_half;
   if(read_half)begin packet_data<={read_data,read_first};packet_addr<=received[BUF_AW-1:1];packet_toggle<=!packet_toggle;end
  end else if(read_half)begin packet_data<={32'b0,read_first};packet_addr<=(received-1'b1)>>1;packet_toggle<=!packet_toggle;read_half<=0;end
 end
`ifdef STREAM_CAPTURE
 wire stream_write;wire [BUF_AW-2:0] stream_address;wire [63:0] stream_data;
 wire packetizer_fault,stream_finished,bank_overrun,host_bank_overrun;
 wire stream_packet_valid,stream_packet_single,stream_packet_seal;wire [63:0] stream_packet_data;
 wire [1:0] stream_available,stream_token,stream_single;
 wire [BUF_AW-2:0] stream_count0,stream_count1;
 wire [63:0] stream_first0,stream_first1;
 reg stream_was_running=0;
 always @(posedge core)stream_was_running<=running;
 wire stream_reset=!stream_mode[0] || priming || arm;
 // Isolate the live-stream tap from SRAM arm/priming decode fanout. Stop and
 // data validity take the same extra clock, retaining the last accepted word.
 (* preserve, dont_merge *) reg [31:0] stream_tap_data=0;
 (* preserve *) reg stream_tap_valid=0,stream_tap_stop=0;
 always @(posedge core)begin
  stream_tap_data<=cfg[0] ? capture_word : ramp; // priming data is invalid and need not select zero
  stream_tap_valid<=stream_mode[0] && write_valid && !dummy_write && !halt;
  stream_tap_stop<=stream_was_running && !running;
 end
 stream_packetizer stream_pack(.reset(stream_reset),.word_clk(core),.packet_clk(halfclk),
  .word_valid(stream_tap_valid),.word_data(stream_tap_data),
  .stop(stream_tap_stop),.fault(packetizer_fault),.finished(stream_finished),
  .packet_valid(stream_packet_valid),.packet_data(stream_packet_data),.packet_single(stream_packet_single),.packet_seal(stream_packet_seal));
 stream_banks #(.BANK_AW(BUF_AW-2)) stream_queue(.reset(stream_reset),.producer_clk(halfclk),.host_clk(clk),
  .packet_valid(stream_packet_valid),.packet_data(stream_packet_data),.packet_single(stream_packet_single),.seal(stream_packet_seal),
  .packet_ready(),.mem_write(stream_write),.mem_address(stream_address),.mem_data(stream_data),
  .overrun(bank_overrun),.host_overrun(host_bank_overrun),.host_release(wc && ws==20),.release_bank(wd[0]),.release_token(wd[1]),
  .host_ready(stream_available),.host_token(stream_token),.first_word0(stream_first0),.first_word1(stream_first1),
  .packets0(stream_count0),.packets1(stream_count1),.last_single(stream_single));
 (* async_reg = "true" *) reg [2:0] bank_fault_s=0,packet_fault_cpu=0,stream_finished_cpu=0,stream_enabled_cpu=0;
 always @(posedge core)if(stream_reset)bank_fault_s<=0;else bank_fault_s<={bank_fault_s[1:0],bank_overrun};
 always @(posedge clk)begin
  packet_fault_cpu<={packet_fault_cpu[1:0],packetizer_fault};
  stream_finished_cpu<={stream_finished_cpu[1:0],stream_finished};stream_enabled_cpu<={stream_enabled_cpu[1:0],stream_mode[0]};
 end
 always @(posedge core)if(stream_reset)streaming_fault<=0;else streaming_fault<=stream_mode[0] && (packetizer_fault || bank_fault_s[2]);
 always @(posedge halfclk)begin
  packet_seen<=packet_toggle;
  if(stream_mode[0])begin if(stream_write)buffer_mem64[stream_address]<=stream_data;end
  else if(packet_toggle!=packet_seen)buffer_mem64[packet_addr]<=packet_data;
 end
`else
 always @(posedge halfclk)if(packet_toggle!=packet_seen)begin buffer_mem64[packet_addr]<=packet_data;packet_seen<=packet_toggle;end
`endif
`else
 reg [31:0] buffer_mem[0:511];reg [31:0] buffer_word=0;
 always @(posedge clk) buffer_word<=buffer_mem[buffer_index];
`endif
 reg [79:0] snapshot=0;
`ifdef INTERLEAVE
`ifndef BURST_RECALL
 reg trace_first=0,trace_second=0,trace_third=0;reg [31:0] trace_data=0;
 always @(posedge core)begin
  trace_first<=read_valid && received==0;trace_second<=read_valid && received==1;trace_third<=read_valid && received==2;
  trace_data<=read_data;
 end
`endif
`endif
 always @(posedge core) begin
  command<=0;arm<=0;halt<=0;command_count<=count_cfg;
  // These flags are consumed only with command. Predecode them before the
  // acceptance mux instead of placing opcode comparison in its feedback path.
  command_discard<=dispatch_discard;command_continue<=dispatch_continue;
  dispatch_discard<=opcode==3;dispatch_continue<=opcode==7;
  dispatch<=0;
  if(pending && !command && !arm && !dispatch)begin dispatch<=1;dispatch_opcode<=opcode;dispatch_arm_ok<=ready && !running && arm_geometry_ok;dispatch_read_ok<=ready && record_done
`ifdef STREAM_CAPTURE
 && !stream_mode[0]
`endif
 && read_geometry_ok && (opcode==3 || buffer_count_ok) && (opcode!=7 || prefetch_valid);end
  config_idle<=!running && !priming;
  if(config_idle)begin cfg<=config_word;level_l<=level;
   level_low_l<=host_level_low;
   level_high_l<=host_level_high;
`ifdef STREAM_CAPTURE
   stream_mode<=stream_cfg;
   stream_auto<=!stream_cfg[1] && !config_word[4];
   stream_rise_enable[0]<=!stream_cfg[1] && config_word[0] && config_word[4] && !config_word[5] && !config_word[6];
   stream_rise_enable[1]<=!stream_cfg[1] && config_word[0] && config_word[4] && !config_word[5] && config_word[6];
   stream_fall_enable[0]<=!stream_cfg[1] && config_word[0] && config_word[4] && config_word[5] && !config_word[6];
   stream_fall_enable[1]<=!stream_cfg[1] && config_word[0] && config_word[4] && config_word[5] && config_word[6];
`endif
  end
`ifdef INTERLEAVE
  if(!priming)prime_left<=15;
`else
  if(!priming)prime_left<=16;
`endif
  if(!running)begin ramp<=0;ramp_carry<=0;end
  else if(step && !halt)begin ramp[15:0]<=ramp[15:0]+1'b1;ramp[31:16]<=ramp[31:16]+ramp_carry;ramp_carry<=ramp[15:0]==16'hfffe;end
`ifdef INTERLEAVE
  if(frontend_finished
`ifdef UART_TRIGGER
   && !event_core_sync[2]
`endif
  )begin frontend_run<=0;frontend_count<=0;end
`ifdef UART_TRIGGER
  if(event_core_sync[2] && !il_failed)begin frontend_run<=1;frontend_count<=1;end
`endif
  if(command && !command_read)begin frontend_run<=1;frontend_count<=1;end
  if(frontend_run && stream_fault)begin halt<=1;il_failed<=1;priming<=0;frontend_run<=0;frontend_count<=0;end
  if(arm)il_failed<=0;
  // Decode acknowledgement ahead of the wide snapshot write enable. The
  // frontend holds cores until the next request; busy covers this extra cycle.
  snap_capture<=snap_busy && snap_ack==snap_request;
  if(snap_capture)begin snapshot<=cores;snap_busy<=0;snap_capture<=0;end
`endif
  // Prime the write path before establishing the record origin, following
  // the successful benchmark. This alone did NOT resolve revision 1
  // hardware mismatches; read/write timing still needs qualification.
  if(priming && prime_ready) begin
   if(prime_left!=1)prime_left<=prime_left-1'b1;
`ifdef INTERLEAVE
   if(prime_left==1 && source_valid)begin priming<=0;arm<=1;end
`else
   if(prime_left==1)begin priming<=0;arm<=1;end
`endif
  end
  if(command)received<=0;
`ifdef INTERLEAVE
  `ifdef PRECISION
  // Board-qualified sparse-clock origin: counter-referenced write addressing
  // differs by one word from the continuous-clock path (origin probe, 2026-09-13).
  if(arm)origin<=position+(decim_active ? 2'd3 : 2'd2);
`else
  if(arm)origin<=position+2'd2;
`endif
`else
  if(arm)origin<=position+1'b1;
`endif
  `ifdef INTERLEAVE
  // Read-pipeline trace: an explicit snapshot command replaces these values.
`ifndef BURST_RECALL
  if(trace_first)snapshot[31:0]<=trace_data;
  if(trace_second)snapshot[63:32]<=trace_data;
  if(trace_third)snapshot[79:64]<=trace_data[15:0];
`endif
  `endif
  if(read_valid) begin
   `ifndef INTERLEAVE
   if(received<512) buffer_mem[received[8:0]]<=read_data;
`endif
   received<=received+1'b1;
  end
  selected<=cores[16*cfg[3:1]+:16];
  packed_valid<=0;
  if(!running || !write_ready) half<=0;
  else begin
   half<=!half;
   if(!half) first_sample<=selected;
   else begin packed_word<={selected,first_sample};packed_valid<=1;end
  end
  if(step && running && !halt) begin previous<=current;end
  if(dispatch) begin
   ack<=req_s[2];command_error<=0;
   case(dispatch_opcode)
    1:if(dispatch_arm_ok) begin
     force_trigger<=0;priming<=1;
     command<=1;command_read<=0;command_discard<=0;command_continue<=0;prefetch_valid<=0;
    end else command_error<=1;
    2,3,7:if(dispatch_read_ok) begin
     command<=1;command_read<=1;
     prefetch_valid<=dispatch_opcode!=3;
    end else command_error<=1;
    4:force_trigger<=1;
    5:halt<=1;
`ifdef INTERLEAVE
    6:begin snap_request<=!snap_request;snap_busy<=1;end
`else
    6:snapshot<=cores;
`endif
    default:command_error<=1;
   endcase
  end
 end
 // Frozen metadata / snapshot / completed buffer are stable while host reads.
 // ACK means command accepted/rejected, not completion. Host waits for ready.
 always @* begin
  rd=0;
  case(rs)
`ifdef UART_TRIGGER
   69:rd=retained_id[15:0];70:rd=retained_id[31:16];
   71:rd=retained_epoch[15:0];72:rd=retained_epoch[31:16];
   83:rd=16'h5401; // Raw ADC sample timeline, frozen identity and last-word ordinal
   84:rd={15'd0,timeline_valid};
   85:rd=timeline_last_word[15:0];86:rd=timeline_last_word[31:16];
   87:rd=timeline_last_word[47:32];88:rd=timeline_last_word[63:48];
   89:rd=timeline_id[15:0];90:rd=timeline_id[31:16];
   91:rd=timeline_epoch[15:0];92:rd=timeline_epoch[31:16];
   73:rd=16'h5201; // Stable retained record identity, epoch captured at arm
   48:rd=16'h5502; // UART 8N1, up to four bytes, 125 MHz receiver clock
   77:rd=16'h5e01; // SENT explicit tick, CRC-verified frame trigger
   97:rd=16'h5501; // USB NRZI/PID with delayed EOP exclusion
   102:rd=16'h4d01; // Manchester full-frame phase selection and word predicates
   93:rd=16'h4d01; // MIL-STD-1553 complete 16-bit word with odd parity
   64:rd=16'h5301; // SPI four modes, both bit orders, explicit idle gap
   53:rd=16'h4901; // I2C seven-bit address/direction, up to four data bytes
`endif
   0:rd=16'h5a52;
`ifdef INTERLEAVE
   1:rd={4'b0,snap_busy,prefetch_valid,ack,command_error,config_error,locked,triggered,record_done,running,host_ready,2'b0};
`else
   1:rd={5'b0,prefetch_valid,ack,command_error,config_error,locked,triggered,record_done,running,host_ready,2'b0};
`endif
   2:rd=record_length[15:0];3:rd={12'b0,record_length[19:16]};
   4:rd=record_start[15:0];5:rd={13'b0,record_start[18:16]};
   6:rd=trigger_index[15:0];7:rd={12'b0,trigger_index[19:16]};
   8:rd=position[15:0];9:rd={13'b0,position[18:16]};
   10:rd=origin[15:0];11:rd={13'b0,origin[18:16]};
   12:rd=received;
`ifdef INTERLEAVE
   `ifdef BURST_RECALL
`ifdef PRECISION
`ifdef HOST_READ_FIX
`ifdef STREAM_CAPTURE
   13:rd=16'h000b;
`else
   13:rd=16'h000a;
`endif
`else
   13:rd=16'h0009;
`endif
   28:rd={11'b0,decim_log};
   29:rd=decim_active ? 16'd16 : 16'd8;
`else
   13:rd=16'h0008;
`endif
   25:rd=buffer_read64[16*burst_half+:16];
   26:rd=BUF_WORDS;
   27:rd=burst_index;
`ifdef STREAM_CAPTURE
   32:rd={7'b0,stream_enabled_cpu[2],stream_finished_cpu[2],(host_bank_overrun || packet_fault_cpu[2]),stream_single,stream_token,stream_available};
   33:rd=stream_count0;34:rd=stream_count1;
   35:rd=stream_first0[15:0];36:rd=stream_first0[31:16];37:rd=stream_first0[47:32];38:rd=stream_first0[63:48];
   39:rd=stream_first1[15:0];40:rd=stream_first1[31:16];41:rd=stream_first1[47:32];42:rd=stream_first1[63:48];
`endif
`else
   13:rd=16'h0007;
`endif
`else
   13:rd=16'h0005;
`endif
   14:rd=`LANEMAP_SEED_ID;
`ifdef INTERLEAVE
   15:rd=16'd500;
   19:rd={14'b0,adc_locked,il_failed};
`endif
   17:rd=buffer_word[15:0];18:rd=buffer_word[31:16];
   20:rd=snapshot[15:0];21:rd=snapshot[31:16];22:rd=snapshot[47:32];23:rd=snapshot[63:48];24:rd=snapshot[79:64];
  endcase
`ifdef UART_TRIGGER
  if(event_read_hit)rd=event_read_data;
`endif
 end
endmodule

// Two chronological samples per word. Re-arm beyond the configured noise
// band; fire at the requested level, not at a shifted level.
// Direct mode compares then updates edge state. Pipelined mode absorbs the
// caller's input register into partial comparisons, preserving system latency.
// TRLC-LINKS: REQ-SDS-039, REQ-SDS-042
module sram_edge_pair #(parameter PIPELINED=0)(input clk,enable,valid,input [7:0] first,second,level,low_level,high_level,
 output reg rise=0,fall=0);
 // Local copies keep configuration fanout off the 250 MHz sample comparison.
 (* preserve, dont_merge *) reg [7:0] local_level=128,local_low=124,local_high=132;
 // TRLC-LINKS: REQ-SDS-039, REQ-SDS-042
 function [6:0] compare_parts(input [7:0] a,b);begin
  compare_parts={a[7:6]>b[7:6],a[7:6]==b[7:6],a[5:4]>b[5:4],a[5:4]==b[5:4],a[3:2]>b[3:2],a[3:2]==b[3:2],a[1:0]>=b[1:0]};
 end endfunction
 wire [7:0] comparisons;wire compared_valid;
 generate if(PIPELINED)begin:partial
  reg [6:0] parts[0:7];reg valid_part=0;
  always @(posedge clk)begin
   valid_part<=enable && valid;
   parts[0]<=compare_parts(first,local_level);parts[1]<=compare_parts(local_level,first);
   parts[2]<=compare_parts(second,local_level);parts[3]<=compare_parts(local_level,second);
   parts[4]<=compare_parts(local_low,first);parts[5]<=compare_parts(first,local_high);
   parts[6]<=compare_parts(local_low,second);parts[7]<=compare_parts(second,local_high);
  end
  genvar p;for(p=0;p<8;p=p+1)begin:combine
   assign comparisons[p]=parts[p][6] || (parts[p][5] && (parts[p][4] || (parts[p][3] && (parts[p][2] || (parts[p][1] && parts[p][0])))));
  end
  assign compared_valid=valid_part;
 end else begin:direct
  assign comparisons={second>=local_high,second<=local_low,first>=local_high,first<=local_low,
   local_level>=second,second>=local_level,local_level>=first,first>=local_level};
  assign compared_valid=valid;
 end endgenerate
 reg valid_d=0,first_low=0,first_high=0,second_low=0,second_high=0;
 reg first_far_low=0,first_far_high=0,second_far_low=0,second_far_high=0;
 reg rising_armed=0,falling_armed=0;
 wire rise_after_first=(rising_armed || first_far_low) && first_low;
 wire fall_after_first=(falling_armed || first_far_high) && first_high;
 always @(posedge clk)begin
  local_level<=level;local_low<=low_level;local_high<=high_level;
  valid_d<=compared_valid;
  first_low<=!comparisons[0];first_high<=!comparisons[1];
  second_low<=!comparisons[2];second_high<=!comparisons[3];
  first_far_low<=comparisons[4];first_far_high<=comparisons[5];
  second_far_low<=comparisons[6];second_far_high<=comparisons[7];
  rise<=0;fall<=0;
  if(valid_d)begin
   rise<=(rising_armed && !first_low) || (rise_after_first && !second_low);
   fall<=(falling_armed && !first_high) || (fall_after_first && !second_high);
   rising_armed<=(rise_after_first || second_far_low) && second_low;
   falling_armed<=(fall_after_first || second_far_high) && second_high;
  end
  if(!enable)begin valid_d<=0;rising_armed<=0;falling_armed<=0;rise<=0;fall<=0;end
 end
endmodule

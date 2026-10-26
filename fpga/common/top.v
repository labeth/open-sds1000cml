// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
`include "lanemap_seed.vh"
// ADR-PROTOCOL-PACKET-IMAGE: an image is a list of decoders. UART_TRIGGER
// alone keeps the general image's six; PROTOCOL_SET names them one by one.
`ifdef UART_TRIGGER
`ifndef PROTOCOL_SET
`define TRIG_UART
`define TRIG_I2C
`define TRIG_SPI
`define TRIG_SENT
`define TRIG_MIL
`define TRIG_USB
`endif
`endif
// USB and Manchester share one scratch RAM.
`ifdef TRIG_USB
`define PROTOCOL_SCRATCH
`endif
`ifdef TRIG_MAN
`define PROTOCOL_SCRATCH
`endif
// ADR-IMAGE-REGROUP-DECIMATION: acquisition images reduce live recalls to envelopes.
`ifdef UART_TRIGGER
`ifdef INTERLEAVE
`ifdef BURST_RECALL
`ifndef STREAM_CAPTURE
// ADR-STREAM-IMAGE: the stream image's host buffer holds stream banks instead.
`define ENVELOPE_RECALL
`endif
`endif
`endif
`endif
// ADR-HARDWARE-QUALIFIERS: the qualifier image reads live envelopes too.
`ifdef QUAL_TRIGGER
`ifdef INTERLEAVE
`ifdef BURST_RECALL
`ifndef ENVELOPE_RECALL
`define ENVELOPE_RECALL
`endif
`endif
`endif
`endif
// SEQUENCE_ONLY images match patterns with the sequence trigger alone: the
// decoders' own pattern inputs are tied off (ADR-IMAGE-REGROUP-DECIMATION).
`ifdef SEQUENCE_ONLY
`define NATIVE_LEN(x) 0
`define NATIVE_ANY(x) 1'b1
`else
`define NATIVE_LEN(x) x
`define NATIVE_ANY(x) x
`endif
// ADR-LINE-LEVELS: a two-line receiver (SPI, I2C) slices its second line at
// a level of its own (register 59), so the channels may sit at any offset
// and V/div.
`ifdef TRIG_SPI
`define LINE_LEVELS
`endif
`ifdef TRIG_I2C
`define LINE_LEVELS
`endif
`ifdef SYMBOL_TRIGGER
`define LINE_LEVELS
`endif
// ADR-PROTOCOL-SEQUENCE-TRIGGER: every raw-only protocol image has the matcher.
`ifdef PROTOCOL_SET
`define SEQUENCE_TRIGGER
`endif
`ifdef TRIG_ARINC
`define PACKET_TRIGGER
`endif
`ifdef TRIG_CAN
`define PACKET_TRIGGER
`endif
`ifdef TRIG_FLEXRAY
`define PACKET_TRIGGER
`endif
// Integration probe: full external record, 512-word host recall buffer only.
// Default mode selects one ADC pair. INTERLEAVE reproduces the factory
// five-phase schedule at 500 MS/s/channel. See README for qualification and ABI.
// TRLC-LINKS: REQ-SDS-039, REQ-SDS-040, REQ-SDS-041, REQ-SDS-042, REQ-SDS-043, REQ-SDS-044, REQ-SDS-049, REQ-SDS-052, REQ-SDS-082, REQ-SDS-083, REQ-SDS-084, REQ-SDS-186
module acq_sram_top(
 input clk,mclk_in,nCS1,nOE,nWE,input [6:2] sel,input gpmc_a2,gpmc_b1,input j1,
 inout [15:0] gpmc_d,inout [31:0] dq,input [79:0] lane,
 output k1,k2,g1,g2,d1,d2,f1,f2,j2,a11,output [4:0] enc_p,enc_n
);
 reg [7:0] level_l=128,level_low_l=124,level_high_l=132;
`ifdef LINE_LEVELS
 reg [7:0] level2_l=128,level2_low_l=124,level2_high_l=132;
`endif
`ifdef PACKET_TRIGGER
 // Bit n advertises protocol ID n in the packet block.
 localparam [15:0] PACKET_PROTOCOLS=
`ifdef TRIG_CAN
  16'h0040|
`endif
`ifdef TRIG_ARINC
  16'h0100|
`endif
`ifdef TRIG_FLEXRAY
  16'h0400|
`endif
  16'h0000;
`endif
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
 reg [4:0] stream_cfg=0,stream_mode=0; // [0] stream, [1] continuous (no trigger), [2] peak-detect words, [3]/[4] rising/falling edge marks
 reg [1:0] stream_rise_enable=0,stream_fall_enable=0;reg stream_auto=0;
 reg streaming_fault=0;
`ifdef STREAM_DRUM
 // ADR-STREAM-IMAGE: the stream runs through the SRAM as a FIFO (stream_drum.v).
 reg drum_active=0;
 // ADR-SOUND: the edge marks' directions on the trigger channel (stream_marks.v).
 reg [1:0] mark_rise_enable=0,mark_fall_enable=0;
 wire drum_cmd,drum_cmd_read,drum_cmd_discard,drum_cmd_continue,drum_wvalid,drum_wstop,drum_fault;
 wire [19:0] drum_cmd_count;wire [31:0] drum_wdata;
`endif
`endif
`ifdef INTERLEAVE
 wire halfclk;bench_pll pll(mclk_in,core,sample_clk,locked,halfclk);
`else
 bench_pll pll(mclk_in,core,sample_clk,locked);
`endif
 assign g2=0;assign d1=0;assign d2=0;assign a11=1;
 wire [79:0] cores;
`ifdef INTERLEAVE
 wire adc_locked,il_raw_valid,il_fault;reg il_failed=0,frontend_run=0;wire [31:0] il_raw_word;
 reg frontend_finished=0;
 (* preserve, dont_merge *) reg frontend_count=0;
 // A new write command cancels a stale frozen-record stop before priming.
 always @(posedge core)frontend_finished<=record_done && !priming && !arm && !(command && !command_read)
`ifdef STREAM_DRUM
  && !drum_active // the drum's stream has no frozen record to end it
`endif
  ;
`ifdef PRECISION
 reg [4:0] decim_request=0;
`ifdef PICK_ALT
 // register 18 bit 5: the record picks samples instead of filtering (below)
 reg decim_pick_request=0;
 (* preserve, dont_merge *) reg decim_pick=0,decim_pick_data=0;
 always @(posedge core)if(config_idle)begin decim_pick<=decim_pick_request;decim_pick_data<=decim_pick_request;end
`endif
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
`ifdef BOX_DECIM
 // No room for the precision filter: box averages (box_decim.v), on the
 // core-domain copy of the decimation (no host-clock path into it).
 box_decim precision(core,halfclk,mclk_in,precision_enable,decim_log,il_raw_word,il_raw_valid,precision_word,precision_valid,precision_fault);
`elsif PICK_DECIM
 // The fast image: every 2^n-th sample (pick_decim.v).
 pick_decim precision(core,halfclk,mclk_in,precision_enable,decim_log,il_raw_word,il_raw_valid,precision_word,precision_valid,precision_fault);
`elsif PICK_ALT
 // The qualify image: the record takes the precision filter's words, or
 // (register 18 bit 5, which BUS's sampled records ask for while a serial
 // trigger reads the raw samples) every 2^n-th sample (pick_decim.v). The
 // filter's band, a quarter of the output rate, flattens a fast bus's bits
 // the samples still hold (WS281x at 10ms/div). The decimated triggers read
 // the filter's words either way (no mux on their paths: timing).
 wire [31:0] pick_word;wire pick_valid,pick_fault;
 (* preserve, dont_merge *) reg pick_enable=0;
 always @(posedge core)pick_enable<=frontend_run && decim_active && decim_pick;
 adc_precision #(.SHARED_TAIL(1)) precision(core,halfclk,mclk_in,precision_enable,decim_request,il_raw_word,il_raw_valid,precision_word,precision_valid,precision_fault);
 pick_decim pick(core,halfclk,mclk_in,pick_enable,decim_log,il_raw_word,il_raw_valid,pick_word,pick_valid,pick_fault);
`else
 adc_precision #(.SHARED_TAIL(1)) precision(core,halfclk,mclk_in,precision_enable,decim_request,il_raw_word,il_raw_valid,precision_word,precision_valid,precision_fault);
`endif
`ifdef PICK_ALT
 wire [31:0] selected_word=!decim_data_active ? il_raw_word : decim_pick_data ? pick_word : precision_word;
 wire selected_valid=!decim_active ? il_raw_valid : decim_pick ? pick_valid : precision_valid;
`else
 wire [31:0] selected_word=!decim_data_active ? il_raw_word : precision_word;
 wire selected_valid=!decim_active ? il_raw_valid : precision_valid;
`endif
 wire [31:0] selected_trigger=!decim_trigger_active ? il_raw_word : {precision_word[31:24],precision_word[15:8],precision_word[31:24],precision_word[15:8]};
`ifdef QUAL_TRIGGER
 // The qualifier's and the zones' own selections of the same word (their
 // fanout off the edge trigger's net: timing).
 (* preserve, dont_merge *) reg decim_qual=0,decim_zone=0;
 always @(posedge core)if(config_idle)begin decim_qual<=decim_request!=0;decim_zone<=decim_request!=0;end
 wire [31:0] qual_trigger_word=!decim_qual ? il_raw_word : {precision_word[31:24],precision_word[15:8],precision_word[31:24],precision_word[15:8]};
 wire [31:0] zone_trigger_word=!decim_zone ? il_raw_word : {precision_word[31:24],precision_word[15:8],precision_word[31:24],precision_word[15:8]};
 wire qual_trigger_valid=!decim_qual ? il_raw_valid : precision_valid;
 wire zone_trigger_valid=!decim_zone ? il_raw_valid : precision_valid;
`endif
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
 wire [31:0] qual_trigger_word=il_raw_word,zone_trigger_word=il_raw_word;
 wire qual_trigger_valid=il_raw_valid,zone_trigger_valid=il_raw_valid;
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
 // Packet trigger block 109..117, shared by ARINC 429, CAN and FlexRay since
 // one decoder runs at a time. Control: enable, channel, inversion, pattern
 // bytes[6:3], protocol ID[11:8]. Ticks are Q8 for CAN/FlexRay and whole
 // receiver clocks for ARINC. Aux holds CAN's data-phase ticks or ARINC's
 // levels {exit_hi,exit_lo,hi,lo}; ARINC's mask is the pattern's upper half.
 reg [15:0] arinc_cfg=0,arinc_cfg_l=0,arinc_cfg_rx=0;
 reg [15:0] can_cfg=0,can_cfg_l=0,can_cfg_rx=0;
 reg [15:0] flex_cfg=0,flex_cfg_l=0,flex_cfg_rx=0;
 reg [23:0] packet_ticks=0;reg [31:0] packet_aux=0;reg [63:0] packet_pattern=0;
 reg [31:0] protocol_aux_host=0,protocol_aux_l=0,protocol_aux_rx=0;
 // Sequence trigger control: length[5:0] (0 off), qualify[6], end_bad_value[7].
 reg [7:0] seq_cfg=0,seq_cfg_l=0,seq_cfg_rx=0;
 // Envelope recall host settings (ADR-IMAGE-REGROUP-DECIMATION).
 reg [15:0] env_bucket_lo=0,env_control=0;reg [4:0] env_skip_host=0;
 reg [4:0] seq_addr=0;reg [31:0] seq_value=0,seq_mask=0;
 wire seq_write;wire seq_match;wire [15:0] seq_overflows;
 wire packet_host=arinc_cfg[0] || can_cfg[0] || flex_cfg[0];
 wire arinc_match,arinc_event_valid;wire [7:0] arinc_event_kind;
 wire [31:0] arinc_event_data;wire [63:0] arinc_event_sample;
 wire can_match,can_event_valid;wire [7:0] can_event_kind;wire [31:0] can_event_data;
 wire [15:0] can_event_count;wire [63:0] can_event_sample;
 wire flex_match,flex_event_valid;wire [7:0] flex_event_kind;wire [31:0] flex_event_data;
 wire [15:0] flex_event_count;wire [63:0] flex_event_sample;
 reg [2:0] arinc_match_sync=0,can_match_sync=0,flex_match_sync=0;
 always @(posedge core)begin
  arinc_match_sync<={arinc_match_sync[1:0],arinc_match};
  can_match_sync<={can_match_sync[1:0],can_match};
  flex_match_sync<={flex_match_sync[1:0],flex_match};
 end
 // ARINC slices ADC codes itself; the selected channel's code is held in
 // the core domain and read by the phase-aligned receiver clock.
 reg [7:0] arinc_code_core=128,arinc_code=128;
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
 wire uart_byte_valid,uart_frame_error;
`ifdef SAMPLE_BUS
 wire [8:0] uart_byte; // five to nine data bits
`define UART_VALUE {23'd0,uart_byte}
`else
 wire [7:0] uart_byte;
`define UART_VALUE {24'd0,uart_byte}
`endif
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
 wire [63:0] event_sample=packet_selected ? packet_event_sample : man_cfg_rx[0] ? man_event_sample : usb_cfg_rx[0] ? usb_event_sample : mil_cfg_rx[0] ? recent_sample : (sent_cfg_rx[0] || !(spi_cfg_rx[0] || i2c_cfg_rx[0])) ? delayed_sample : recent_sample;
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
 wire [1:0] i2c_event_meta;wire i2c_selected;
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
 reg protocol_channel_host=0;
 reg [9:0] protocol_match_enable_host=10'b0000000001;
 // Resolve host selection before the held configuration crosses into core.
 always @(posedge clk)begin
  protocol_ticks_host<=packet_host ? packet_ticks : man_cfg[0] ? man_ticks : usb_cfg[0] ? usb_ticks : mil_cfg[0] ? mil_ticks : sent_cfg[0] ? sent_ticks : spi_cfg[0] ? spi_gap : uart_ticks;
`ifdef SEQUENCE_ONLY
  protocol_pattern_host<=64'd0;
`else
  protocol_pattern_host<=packet_host ? packet_pattern : man_cfg[0] ? man_pattern : {32'd0,(usb_cfg[0] ? usb_pattern : mil_cfg[0] ? {16'd0,mil_pattern} : sent_cfg[0] ? sent_pattern : spi_cfg[0] ? spi_pattern : i2c_cfg[0] ? i2c_pattern : uart_pattern)};
`endif
  protocol_aux_host<=packet_aux;
  protocol_channel_host<=flex_cfg[0] ? flex_cfg[1] : can_cfg[0] ? can_cfg[1] : arinc_cfg[0] ? arinc_cfg[1] : man_cfg[0] ? man_cfg[1] : usb_cfg[0] ? usb_cfg[1] : mil_cfg[0] ? mil_cfg[1] : sent_cfg[0] ? sent_cfg[1] : spi_cfg[0] ? spi_cfg[1] : i2c_cfg[0] ? i2c_cfg[1] : uart_cfg[1];
  protocol_match_enable_host<=flex_cfg[0] ? 10'b1000000000 : can_cfg[0] ? 10'b0100000000 : arinc_cfg[0] ? 10'b0010000000 :
   man_cfg[0] ? 10'b0001000000 : usb_cfg[0] ? 10'b0000100000 : mil_cfg[0] ? 10'b0000010000 : sent_cfg[0] ? 10'b0000001000 :
   spi_cfg[0] ? 10'b0000000100 : i2c_cfg[0] ? 10'b0000000010 : 10'b0000000001;
 end
 always @(posedge halfclk)begin
  uart_line_sync<={uart_line_sync[0],uart_line};
  i2c_line_sync<={i2c_line_sync[0],i2c_line};
  protocol_valid_sync<={protocol_valid_sync[0],uart_valid};
  uart_run_sync<={uart_run_sync[0],running && !arm};
  if(!uart_run_sync[1] && !event_run_sync[2])begin
   man_cfg_rx<=man_cfg_l;usb_cfg_rx<=usb_cfg_l;mil_cfg_rx<=mil_cfg_l;
   arinc_cfg_rx<=arinc_cfg_l;can_cfg_rx<=can_cfg_l;flex_cfg_rx<=flex_cfg_l;protocol_aux_rx<=protocol_aux_l;
   seq_cfg_rx<=seq_cfg_l;
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
 (* preserve, dont_merge *) reg [9:0] protocol_match_enable=10'b0000000001;
 // The selected decoder's match is chosen in the receiver clock, by the same
 // held selection as its events, so one bit crosses to the core clock.
 reg protocol_match_rx=0;reg [2:0] protocol_match_sync=0;
 // With a sequence configured, its match replaces the decoder's own.
 // I2C sequences count only in transactions whose address and direction the
 // decoder selected; a transaction outlasts the matcher's decision latency.
 wire i2c_only=i2c_cfg_rx[0] && !spi_cfg_rx[0] && !sent_cfg_rx[0] && !mil_cfg_rx[0] && !usb_cfg_rx[0] && !man_cfg_rx[0] && !packet_selected;
 always @(posedge halfclk)protocol_match_rx<=seq_cfg_rx[5:0]!=0 ? seq_match && (!i2c_only || i2c_selected) : flex_cfg_rx[0] ? flex_match : can_cfg_rx[0] ? can_match : arinc_cfg_rx[0] ? arinc_match :
  man_cfg_rx[0] ? man_match : usb_cfg_rx[0] ? usb_match : mil_cfg_rx[0] ? mil_match : sent_cfg_rx[0] ? sent_match :
  spi_cfg_rx[0] ? spi_match : i2c_cfg_rx[0] ? i2c_match : uart_match;
 always @(posedge core)protocol_match_sync<={protocol_match_sync[1:0],protocol_match_rx};
 wire protocol_match_selected=protocol_match_sync[2];
 reg protocol_match_level=0,protocol_match_previous=0,protocol_match_ready=0;
 // Separate decoder selection from trigger eligibility and edge detection.
 always @(posedge core)begin
  protocol_match_level<=protocol_match_selected;protocol_match_ready<=running && !arm && history_ready;
 end
 wire history_ready;
 always @(posedge core)if(config_idle)begin
  protocol_channel<=protocol_channel_host;
  protocol_match_enable<=protocol_match_enable_host;
 end
 wire [7:0] uart_code=protocol_channel ? il_raw_word[31:24] : il_raw_word[23:16];
 wire [7:0] i2c_code=protocol_channel ? il_raw_word[23:16] : il_raw_word[31:24];
 (* preserve, dont_merge *) reg [7:0] protocol_low=0,protocol_high=0;
`ifdef LINE_LEVELS
 // The second line (SPI's data, I2C's SDA) at its own level.
 (* preserve, dont_merge *) reg [7:0] protocol_low2=0,protocol_high2=0;
 always @(posedge core)begin protocol_low2<=level2_low_l;protocol_high2<=level2_high_l;end
`endif
 // Partial comparison replaces the old sample register, retaining latency.
 // TRLC-LINKS: REQ-SDS-013, REQ-SDS-039
 function [6:0] compare_parts(input [7:0] a,b);begin
  compare_parts={a[7:6]>b[7:6],a[7:6]==b[7:6],a[5:4]>b[5:4],a[5:4]==b[5:4],a[3:2]>b[3:2],a[3:2]==b[3:2],a[1:0]>=b[1:0]};
 end endfunction
 always @(posedge core)begin protocol_low<=level_low_l;protocol_high<=level_high_l;end
 always @(posedge core)begin
  uart_sample_valid<=il_raw_valid;
  uart_high_parts<=compare_parts(uart_code,protocol_high);uart_low_parts<=compare_parts(protocol_low,uart_code);
`ifdef LINE_LEVELS
  i2c_high_parts<=compare_parts(i2c_code,protocol_high2);i2c_low_parts<=compare_parts(protocol_low2,i2c_code);
`else
  i2c_high_parts<=compare_parts(i2c_code,protocol_high);i2c_low_parts<=compare_parts(protocol_low,i2c_code);
`endif
  i2c_high<=i2c_high_parts[6] || (i2c_high_parts[5] && (i2c_high_parts[4] || (i2c_high_parts[3] && (i2c_high_parts[2] || (i2c_high_parts[1] && i2c_high_parts[0])))));
  i2c_low<=i2c_low_parts[6] || (i2c_low_parts[5] && (i2c_low_parts[4] || (i2c_low_parts[3] && (i2c_low_parts[2] || (i2c_low_parts[1] && i2c_low_parts[0])))));
  uart_high<=uart_high_parts[6] || (uart_high_parts[5] && (uart_high_parts[4] || (uart_high_parts[3] && (uart_high_parts[2] || (uart_high_parts[1] && uart_high_parts[0])))));
  uart_low<=uart_low_parts[6] || (uart_low_parts[5] && (uart_low_parts[4] || (uart_low_parts[3] && (uart_low_parts[2] || (uart_low_parts[1] && uart_low_parts[0])))));
  uart_compare_valid<=uart_sample_valid;uart_valid<=uart_compare_valid;
  if(uart_sample_valid)arinc_code_core<=uart_code;
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
   arinc_cfg_l<=arinc_cfg;can_cfg_l<=can_cfg;flex_cfg_l<=flex_cfg;protocol_aux_l<=protocol_aux_host;
   seq_cfg_l<=seq_cfg;
   protocol_ticks_l<=protocol_ticks_host;
   protocol_pattern_l<=protocol_pattern_host;
   sent_cfg_l<=sent_cfg;sent_nibbles_l<=sent_nibbles;
   spi_cfg_l<=spi_cfg;
   uart_cfg_l<=uart_cfg;
   i2c_cfg_l<=i2c_cfg;i2c_address_l<=i2c_address;
  end
 end
`ifdef SAMPLE_BUS
 // ADR-SAMPLE-DOMAIN-SPI: SPI, UART and I2C on the samples (sample_bus.v),
 // one enabled at a time; register 58 holds the first line's delay (SPI's
 // data, I2C's SDA after SCL), 1/16 ns.
 reg [7:0] sample_delay=8'd23;
 wire uart_event_valid;wire [7:0] uart_event_kind;
 sample_bus bus(.core(core),.clk(halfclk),.reset(protocol_receiver_reset),.word(il_raw_word),.valid(frontend_run && il_raw_valid),
  .level(level_l),.low(level_low_l),.high(level_high_l),.level2(level2_l),.low2(level2_low_l),.high2(level2_high_l),.delay(sample_delay),.ticks(protocol_ticks_rx),.pattern(protocol_pattern_rx[31:0]),
  .spi_en(!man_cfg_rx[0] && !usb_cfg_rx[0] && spi_cfg_rx[0] && !sent_cfg_rx[0] && !mil_cfg_rx[0]),.spi_ch(spi_cfg_rx[1]),.spi_inverted(spi_cfg_rx[2]),
  .cpol(spi_cfg_rx[3]),.cpha(spi_cfg_rx[4]),.msb(spi_cfg_rx[5]),.spi_len(`NATIVE_LEN(spi_cfg_rx[8:6])),
  .uart_en(!man_cfg_rx[0] && !usb_cfg_rx[0] && uart_cfg_rx[0] && !i2c_cfg_rx[0] && !spi_cfg_rx[0] && !sent_cfg_rx[0] && !mil_cfg_rx[0]),
  .uart_ch(uart_cfg_rx[1]),.uart_inverted(uart_cfg_rx[2]),.uart_resync(uart_cfg_rx[12]),.uart_bits(uart_cfg_rx[9:6]),.uart_parity(uart_cfg_rx[11:10]),.uart_len(`NATIVE_LEN(uart_cfg_rx[5:3])),
  .i2c_en(!man_cfg_rx[0] && !usb_cfg_rx[0] && i2c_cfg_rx[0] && !spi_cfg_rx[0] && !sent_cfg_rx[0] && !mil_cfg_rx[0]),.i2c_ch(i2c_cfg_rx[1]),.i2c_inverted(i2c_cfg_rx[2]),
  .i2c_address(i2c_address_rx),.i2c_any(i2c_cfg_rx[5]),.i2c_direction(i2c_cfg_rx[4:3]),.i2c_len(`NATIVE_LEN(i2c_cfg_rx[8:6])),
  .spi_match(spi_match),.spi_event_valid(spi_event_valid),.spi_event_kind(spi_event_kind),.spi_event_data(spi_event_data),
  .uart_match(uart_match),.uart_event_valid(uart_event_valid),.uart_event_kind(uart_event_kind),.uart_event_data(uart_byte),
  .i2c_match(i2c_match),.i2c_event_valid(i2c_event_valid),.i2c_event_kind(i2c_event_kind),.i2c_event_data(i2c_event_data),.i2c_event_meta(i2c_event_meta),.i2c_selected(i2c_selected));
 assign uart_byte_valid=uart_event_valid && uart_event_kind==8'd2,uart_frame_error=uart_event_valid && uart_event_kind==8'd4;
`elsif TRIG_UART
 uart_trigger uart(.clk(halfclk),.reset(protocol_receiver_reset),.enable(!man_cfg_rx[0] && !usb_cfg_rx[0] && uart_cfg_rx[0] && !i2c_cfg_rx[0] && !spi_cfg_rx[0] && !sent_cfg_rx[0] && !mil_cfg_rx[0]),
  .tick(1'b1),.line(uart_line_sync[1]),.inverted(uart_cfg_rx[2]),
  .bit_ticks(protocol_ticks_rx),.pattern(protocol_pattern_rx[31:0]),.pattern_len(`NATIVE_LEN(uart_cfg_rx[5:3])),
  .match(uart_match),.byte_valid(uart_byte_valid),.framing_error(uart_frame_error),.byte_data(uart_byte));
`else
 assign uart_match=0,uart_byte_valid=0,uart_frame_error=0,uart_byte=0;
`endif
`ifdef TRIG_SPI
`ifdef SAMPLE_BUS
 // on the samples: sample_bus.v above
`else
 spi_trigger spi(.clk(halfclk),.reset(protocol_receiver_reset),
  .enable(!man_cfg_rx[0] && !usb_cfg_rx[0] && spi_cfg_rx[0] && !sent_cfg_rx[0] && !mil_cfg_rx[0]),.tick(1'b1),.sck(uart_line_sync[1]^spi_cfg_rx[2]),.data(i2c_line_sync[1]^spi_cfg_rx[2]),
  .cpol(spi_cfg_rx[3]),.cpha(spi_cfg_rx[4]),.msb(spi_cfg_rx[5]),.gap_ticks(protocol_ticks_rx),
  .pattern(protocol_pattern_rx[31:0]),.pattern_len(`NATIVE_LEN(spi_cfg_rx[8:6])),.match(spi_match),
  .event_valid(spi_event_valid),.event_kind(spi_event_kind),.event_data(spi_event_data));
`endif
`else
 assign spi_match=0,spi_event_valid=0,spi_event_kind=0,spi_event_data=0;
`endif
`ifdef TRIG_SENT
 sent_trigger sent(.clk(halfclk),.reset(protocol_receiver_reset),.enable(!man_cfg_rx[0] && !usb_cfg_rx[0] && sent_cfg_rx[0] && !mil_cfg_rx[0]),.tick(1'b1),
  .line(uart_line_sync[1]),.inverted(sent_cfg_rx[2]),.pause_pulse(sent_cfg_rx[3]),
  .tick_ticks(protocol_ticks_rx),.nibbles(sent_nibbles_rx),.pattern(protocol_pattern_rx[31:0]),.pattern_len(`NATIVE_LEN(sent_cfg_rx[6:4])),
  .match(sent_match),.event_valid(sent_event_valid),.event_kind(sent_event_kind),.event_data(sent_event_data),.event_index(sent_event_index));
`else
 assign sent_match=0,sent_event_valid=0,sent_event_kind=0,sent_event_data=0,sent_event_index=0;
`endif
`ifdef TRIG_MIL
 mil1553_trigger mil(.clk(halfclk),.reset(protocol_receiver_reset),.enable(!man_cfg_rx[0] && !usb_cfg_rx[0] && mil_cfg_rx[0]),.tick(1'b1),
  .line(uart_line_sync[1]),.inverted(mil_cfg_rx[2]),.bit_ticks(protocol_ticks_rx),.pattern(protocol_pattern_rx[15:0]),.match_any(`NATIVE_ANY(mil_cfg_rx[3])),
  .match(mil_match),.event_valid(mil_event_valid),.event_kind(mil_event_kind),.event_data(mil_event_data),.event_command(mil_event_command));
`else
 assign mil_match=0,mil_event_valid=0,mil_event_kind=0,mil_event_data=0,mil_event_command=0;
`endif
`ifdef TRIG_USB
 usbls_trigger #(.EXTERNAL_RAM(1)) usb(.clk(halfclk),.reset(protocol_receiver_reset),.enable(!man_cfg_rx[0] && usb_cfg_rx[0]),.tick(1'b1),
  .line(uart_line_sync[1]),.inverted(usb_cfg_rx[2]),.bit_ticks_q8(protocol_ticks_rx),
  .pattern(protocol_pattern_rx[31:0]),.pattern_len(`NATIVE_LEN(usb_cfg_rx[5:3])),.sample(recent_sample),
  .match(usb_match),.event_valid(usb_event_valid),.event_kind(usb_event_kind),.event_data(usb_event_data),
  .event_count(usb_event_count),.event_sample(usb_event_sample),
  .scratch_write(usb_scratch_write),.scratch_wr(usb_scratch_wr),.scratch_rd(usb_scratch_rd),
  .scratch_data(usb_scratch_data),.scratch_q(usb_scratch_q));
`else
 assign usb_match=0,usb_event_valid=0,usb_event_kind=0,usb_event_data=0,usb_event_count=0,usb_event_sample=0;
 assign usb_scratch_write=0,usb_scratch_wr=0,usb_scratch_rd=0,usb_scratch_data=0;
`endif
`ifdef TRIG_MAN
 // One register on the line keeps its fan-out off the receiver's decision
 // logic; Manchester stamps are one receiver clock (four samples) later.
 reg man_line=1;
 always @(posedge halfclk)man_line<=uart_line_sync[1];
 manchester_trigger #(.EXTERNAL_RAM(1)) man(.clk(halfclk),.reset(protocol_receiver_reset),.enable(man_cfg_rx[0]),.tick(1'b1),
  .line(man_line),.inverted(man_cfg_rx[2]),.ieee(man_cfg_rx[3]),.msb(man_cfg_rx[4]),
  .word_bits(man_cfg_rx[9:5]),.bit_ticks(protocol_ticks_rx),.pattern(protocol_pattern_rx),.pattern_len(`NATIVE_LEN(man_cfg_rx[12:10])),
  .sample(recent_sample),.match(man_match),.event_valid(man_event_valid),.event_kind(man_event_kind),
  .event_data(man_event_data),.event_sample(man_event_sample),.overflow(man_overflow),
  .scratch_write(man_scratch_write),.scratch_wr_a(man_scratch_wr_a),.scratch_wr_b(man_scratch_wr_b),.scratch_rd(man_scratch_rd),
  .scratch_data_a(man_scratch_data_a),.scratch_data_b(man_scratch_data_b),.scratch_q_a(man_scratch_q_a),.scratch_q_b(man_scratch_q_b));
`else
 assign man_match=0,man_event_valid=0,man_overflow=0,man_event_kind=0,man_event_data=0,man_event_sample=0;
 assign man_scratch_write=0,man_scratch_wr_a=0,man_scratch_wr_b=0,man_scratch_rd=0,man_scratch_data_a=0,man_scratch_data_b=0;
`endif
 // ADR-PROTOCOL-PACKET-IMAGE decoders; each exists only in images that list it.
 // They take a registered copy of the receiver reset, a slow control, to keep
 // its fan-out off their event registers.
 (* preserve, dont_merge *) reg packet_receiver_reset=1;
 always @(posedge halfclk)packet_receiver_reset<=protocol_receiver_reset;
`ifdef TRIG_ARINC
 always @(posedge halfclk)arinc_code<=arinc_code_core;
 arinc429_trigger #(.STAMP(32)) arinc(.clk(halfclk),.reset(packet_receiver_reset),.enable(arinc_cfg_rx[0]),.tick(1'b1),
  .code(arinc_code),.threshold_hi(protocol_aux_rx[15:8]),.threshold_lo(protocol_aux_rx[7:0]),
  .exit_hi(protocol_aux_rx[31:24]),.exit_lo(protocol_aux_rx[23:16]),.bit_ticks(protocol_ticks_rx),.sample(recent_sample),
  .pattern(protocol_pattern_rx[31:0]),.pattern_mask(protocol_pattern_rx[63:32]),
  .match(arinc_match),.event_valid(arinc_event_valid),.event_kind(arinc_event_kind),.event_data(arinc_event_data),.event_sample(arinc_event_sample));
`else
 assign arinc_match=0,arinc_event_valid=0,arinc_event_kind=0,arinc_event_data=0,arinc_event_sample=0;
`endif
`ifdef TRIG_CAN
 can_trigger #(.STAMP(32)) can(.clk(halfclk),.reset(packet_receiver_reset),.enable(can_cfg_rx[0] && !flex_cfg_rx[0]),.tick(1'b1),
  .line(uart_line_sync[1]),.inverted(can_cfg_rx[2]),.bit_ticks_q8(protocol_ticks_rx),.data_ticks_q8(protocol_aux_rx[23:0]),
  .pattern(protocol_pattern_rx),.pattern_len(`NATIVE_LEN(can_cfg_rx[6:3])),.sample(recent_sample),
  .match(can_match),.event_valid(can_event_valid),.event_kind(can_event_kind),.event_data(can_event_data),
  .event_count(can_event_count),.event_sample(can_event_sample));
`else
 assign can_match=0,can_event_valid=0,can_event_kind=0,can_event_data=0,can_event_count=0,can_event_sample=0;
`endif
`ifdef TRIG_FLEXRAY
 flexray_trigger #(.STAMP(32)) flex(.clk(halfclk),.reset(packet_receiver_reset),.enable(flex_cfg_rx[0]),.tick(1'b1),
  .line(uart_line_sync[1]),.inverted(flex_cfg_rx[2]),.bit_ticks_q8(protocol_ticks_rx),
  .pattern(protocol_pattern_rx),.pattern_len(`NATIVE_LEN(flex_cfg_rx[6:3])),.sample(recent_sample),
  .match(flex_match),.event_valid(flex_event_valid),.event_kind(flex_event_kind),.event_data(flex_event_data),
  .event_count(flex_event_count),.event_sample(flex_event_sample));
`else
 assign flex_match=0,flex_event_valid=0,flex_event_kind=0,flex_event_data=0,flex_event_count=0,flex_event_sample=0;
`endif
`ifdef PROTOCOL_SCRATCH
 protocol_scratch scratch(.clk(halfclk),.reset(protocol_receiver_reset),.manchester(man_cfg_rx[0]),
  .usb_write(usb_scratch_write),.usb_wr(usb_scratch_wr),.usb_rd(usb_scratch_rd),.usb_data(usb_scratch_data),.usb_q(usb_scratch_q),
  .man_write(man_scratch_write),.man_wr_a(man_scratch_wr_a),.man_wr_b(man_scratch_wr_b),.man_rd(man_scratch_rd),
  .man_data_a(man_scratch_data_a),.man_data_b(man_scratch_data_b),.man_q_a(man_scratch_q_a),.man_q_b(man_scratch_q_b));
`else
 assign usb_scratch_q=0,man_scratch_q_a=0,man_scratch_q_b=0;
`endif
 wire packet_selected=flex_cfg_rx[0] || can_cfg_rx[0] || arinc_cfg_rx[0];
 // The packet decoders stamp events with the low 32 sample bits; one
 // registered stage selects the decoder and restores the upper bits from the
 // live ordinal (an event is at most milliseconds old).
 // Three stages: select the decoder; compare the stamp with the live low
 // bits; restore the upper bits from the live ordinal of that clock.
 reg packet_valid_s=0,packet_valid_t=0,packet_event_valid=0;reg [7:0] packet_kind_s=0,packet_kind_t=0,packet_event_kind=0;
 reg [31:0] packet_data_s=0,packet_data_t=0,packet_event_data=0,packet_stamp=0,packet_stamp_t=0,packet_upper=0;
 reg [15:0] packet_count_s=0,packet_count_t=0,packet_event_count=0;reg [63:0] packet_event_sample=0;reg packet_borrow=0;
 always @(posedge halfclk)begin
  packet_valid_s<=flex_cfg_rx[0] ? flex_event_valid : can_cfg_rx[0] ? can_event_valid : arinc_event_valid;
  packet_kind_s<=flex_cfg_rx[0] ? flex_event_kind : can_cfg_rx[0] ? can_event_kind : arinc_event_kind;
  packet_data_s<=flex_cfg_rx[0] ? flex_event_data : can_cfg_rx[0] ? can_event_data : arinc_event_data;
  packet_count_s<=flex_cfg_rx[0] ? flex_event_count : can_cfg_rx[0] ? can_event_count : 16'd0;
  packet_stamp<=flex_cfg_rx[0] ? flex_event_sample[31:0] : can_cfg_rx[0] ? can_event_sample[31:0] : arinc_event_sample[31:0];
  packet_valid_t<=packet_valid_s;packet_kind_t<=packet_kind_s;packet_data_t<=packet_data_s;packet_count_t<=packet_count_s;
  packet_stamp_t<=packet_stamp;packet_borrow<=packet_stamp>recent_sample[31:0];packet_upper<=recent_sample[63:32];
  packet_event_valid<=packet_valid_t;packet_event_kind<=packet_kind_t;packet_event_data<=packet_data_t;packet_event_count<=packet_count_t;
  packet_event_sample<={packet_upper-packet_borrow,packet_stamp_t};
 end
 wire packet_channel=flex_cfg_rx[0] ? flex_cfg_rx[1] : can_cfg_rx[0] ? can_cfg_rx[1] : arinc_cfg_rx[1];
 wire selected_event_valid=packet_selected ? packet_event_valid : man_cfg_rx[0] ? man_event_valid : usb_cfg_rx[0] ? usb_event_valid : mil_cfg_rx[0] ? mil_event_valid : sent_cfg_rx[0] ? sent_event_valid : spi_cfg_rx[0] ? spi_event_valid : (i2c_cfg_rx[0] ? i2c_event_valid : (uart_byte_valid || uart_frame_error));
 wire [7:0] selected_event_kind=packet_selected ? packet_event_kind : man_cfg_rx[0] ? man_event_kind : usb_cfg_rx[0] ? usb_event_kind : mil_cfg_rx[0] ? mil_event_kind : sent_cfg_rx[0] ? sent_event_kind : spi_cfg_rx[0] ? spi_event_kind : (i2c_cfg_rx[0] ? i2c_event_kind : (uart_frame_error ? 8'd4 : 8'd2));
 wire [31:0] selected_event_data=packet_selected ? packet_event_data : man_cfg_rx[0] ? {16'd0,man_event_data} : usb_cfg_rx[0] ? {24'd0,usb_event_data} : mil_cfg_rx[0] ? {16'd0,mil_event_data} : sent_cfg_rx[0] ? {24'd0,sent_event_data} : spi_cfg_rx[0] ? {24'd0,spi_event_data} : (i2c_cfg_rx[0] ? {24'd0,i2c_event_data} : `UART_VALUE);
 wire [7:0] selected_event_protocol=flex_cfg_rx[0] ? 8'd10 : can_cfg_rx[0] ? 8'd6 : arinc_cfg_rx[0] ? 8'd8 : man_cfg_rx[0] ? 8'd4 : usb_cfg_rx[0] ? 8'd9 : mil_cfg_rx[0] ? 8'd7 : sent_cfg_rx[0] ? 8'd5 : spi_cfg_rx[0] ? 8'd3 : (i2c_cfg_rx[0] ? 8'd2 : 8'd1);
 wire [7:0] selected_event_flags=((selected_event_kind==4 || selected_event_kind==5 || (man_cfg_rx[0] && selected_event_kind==3 && man_event_data!=0)) ? 8'd0 : 8'd4) |
  (packet_selected ? (packet_channel ? 8'd2 : 8'd1) : man_cfg_rx[0] ? (man_cfg_rx[1] ? 8'd2 : 8'd1) : usb_cfg_rx[0] ? (usb_cfg_rx[1] ? 8'd2 : 8'd1) : mil_cfg_rx[0] ? (mil_cfg_rx[1] ? 8'd2 : 8'd1) : sent_cfg_rx[0] ? (sent_cfg_rx[1] ? 8'd2 : 8'd1) : (spi_cfg_rx[0] || i2c_cfg_rx[0]) ? 8'd3 : (uart_cfg_rx[1] ? 8'd2 : 8'd1));
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
  .in_count(packet_selected ? {16'd0,packet_event_count} : (man_cfg_rx[0] || usb_cfg_rx[0]) ? 32'd0 : mil_cfg_rx[0] ? {31'd0,mil_event_command} : sent_cfg_rx[0] ? {25'd0,sent_event_index} : (i2c_cfg_rx[0] && !spi_cfg_rx[0]) ? {30'd0,i2c_event_meta} : 32'd0),
  .host_pop(event_pop),.host_rewind(event_rewind),.host_clear_error(event_clear),
  .host_available(event_available),.host_error(event_error),.host_cursor(event_cursor),.host_data(event_data),
  .source_overflow(event_overflow),.host_count(event_host_count));
`ifdef SEQUENCE_TRIGGER
 // The matched value carries the metadata the decoder publishes beside its
 // data: I2C address and NACK flags, the MIL command flag, the SENT index.
 wire [31:0] seq_event_value=i2c_cfg_rx[0] && !spi_cfg_rx[0] && !packet_selected && !man_cfg_rx[0] && !usb_cfg_rx[0] && !mil_cfg_rx[0] && !sent_cfg_rx[0] ?
   {22'd0,i2c_event_meta,selected_event_data[7:0]} : mil_cfg_rx[0] && !packet_selected && !man_cfg_rx[0] && !usb_cfg_rx[0] ?
   {15'd0,mil_event_command,selected_event_data[15:0]} : sent_cfg_rx[0] && !packet_selected && !man_cfg_rx[0] && !usb_cfg_rx[0] && !mil_cfg_rx[0] ?
   {17'd0,sent_event_index,selected_event_data[7:0]} : selected_event_data;
 event_sequence seq_trigger(.clk(halfclk),.reset(protocol_receiver_reset),.length(seq_cfg_rx[5:0]),.qualify(seq_cfg_rx[6]),.end_bad_value(seq_cfg_rx[7]),
  .in_valid(protocol_valid_sync[1] && selected_event_valid),.in_kind(selected_event_kind),.in_value(seq_event_value),
  .wr_clk(clk),.wr_en(seq_write),.wr_addr(seq_addr),.wr_data({wd[2:0],seq_mask,seq_value}),
  .match(seq_match),.overflows(seq_overflows));
 assign seq_write=wc && ws==124;
`else
 assign seq_match=0,seq_overflows=0,seq_write=0;
`endif
`ifdef SAMPLE_BUS
 // on the samples: sample_bus.v above
`elsif TRIG_I2C
 i2c_trigger i2c(.clk(halfclk),.reset(protocol_receiver_reset),.enable(!man_cfg_rx[0] && !usb_cfg_rx[0] && i2c_cfg_rx[0] && !spi_cfg_rx[0] && !sent_cfg_rx[0] && !mil_cfg_rx[0]),.tick(1'b1),
  .scl(uart_line_sync[1]^i2c_cfg_rx[2]),.sda(i2c_line_sync[1]^i2c_cfg_rx[2]),
  .address(i2c_address_rx),.any_address(i2c_cfg_rx[5]),.direction(i2c_cfg_rx[4:3]),
  .pattern(protocol_pattern_rx[31:0]),.pattern_len(`NATIVE_LEN(i2c_cfg_rx[8:6])),.match(i2c_match),
  .event_valid(i2c_event_valid),.event_kind(i2c_event_kind),.event_data(i2c_event_data),.event_meta(i2c_event_meta),.selected(i2c_selected));
`else
 assign i2c_match=0,i2c_event_valid=0,i2c_event_kind=0,i2c_event_data=0,i2c_event_meta=0,i2c_selected=0;
`endif
 wire protocol_fire=(arinc_cfg_l[0] || can_cfg_l[0] || flex_cfg_l[0] || man_cfg_l[0] || uart_cfg_l[0] || i2c_cfg_l[0] || spi_cfg_l[0] || sent_cfg_l[0] || mil_cfg_l[0] || usb_cfg_l[0]) ? uart_fired : (cfg[5]?fall_ch[cfg[6]]:rise_ch[cfg[6]]);
`else
 wire protocol_fire=cfg[5]?fall_ch[cfg[6]]:rise_ch[cfg[6]];
`endif
 wire il_crossing=!cfg[4] || force_trigger || protocol_fire;
 // Decode stable configuration before the hot edge-to-trigger path.
 reg [1:0] edge_rise_enable=0,edge_fall_enable=0;
 reg edge_auto=0,protocol_enable=0;
 wire protocol_configured=
`ifdef UART_TRIGGER
  arinc_cfg_l[0] || can_cfg_l[0] || flex_cfg_l[0] || man_cfg_l[0] || uart_cfg_l[0] || i2c_cfg_l[0] || spi_cfg_l[0] || sent_cfg_l[0] || mil_cfg_l[0] || usb_cfg_l[0];
`else
  1'b0;
`endif
`ifdef QUAL_TRIGGER
 // ADR-HARDWARE-QUALIFIERS: pulse, slope and video triggers (qual_trigger.v)
 // decided here, never in software. Host registers (free in every image; the
 // host bus has 7 address bits): 21 control {min_zero[7], channel[6],
 // condition[5:4], negative[3], kind[2:1] (0 pulse, 1 slope, 2 video),
 // enable[0]}; 22 the minimum less one and 24 the maximum in ticks; 26 the
 // slope levels {B[15:8], A[7:0]}; 27 the video line; 28 the clocks a tick
 // less one (a power of two less one). Read 74 the block, 75 its kinds.
 reg [15:0] qual_cfg=0,qual_levels=16'h8080,qual_video=0,qual_min=0,qual_max=0,qual_pre=0;
 reg [15:0] qual_cfg_l=0,qual_min_l=0,qual_max_l=0,qual_pre_l=0;reg [9:0] qual_line_l=0;
 // The slope levels in five-sample sums, their re-arm bands three codes a
 // sample (assigned below, from the host clock).
 reg [10:0] qual_a5=640,qual_a5_lo=625,qual_a5_hi=655,qual_b5=640,qual_b5_lo=625,qual_b5_hi=655;
 // Equal copies keep the channel select off one wide fanout net at 250 MHz.
 (* preserve, dont_merge *) reg qual_ch=0;
 always @(posedge core)if(config_idle)begin
  qual_cfg_l<=qual_cfg;qual_min_l<=qual_min;qual_max_l<=qual_max;qual_pre_l<=qual_pre;qual_line_l<=qual_video[9:0];qual_ch<=qual_cfg[6];
 end
 wire qual_configured=qual_cfg_l[0];
`ifndef UART_TRIGGER
`ifdef ENVELOPE_RECALL
 // Envelope recall host settings (without the protocol baseline that declares them).
 reg [15:0] env_bucket_lo=0,env_control=0;reg [4:0] env_skip_host=0;
`endif
`endif
 // The qualifier's own registered copies of its channel's samples and edges
 // keep the trigger word's fanout off the edge trigger's comparators
 // (timing): they come one clock later, alike. The zones keep theirs, and
 // the edge trigger's edges as their candidates.
 (* preserve, dont_merge *) reg [7:0] qual_first=0,qual_second=0;
 (* preserve, dont_merge *) reg [31:0] zone_word=0;
 (* preserve, dont_merge *) reg qual_word_valid=0,zone_word_valid=0,qual_run=0;
 reg rise_q=0,fall_q=0,rise_z=0,fall_z=0;
 always @(posedge core)begin
  qual_first<=qual_ch ? qual_trigger_word[15:8] : qual_trigger_word[7:0];
  qual_second<=qual_ch ? qual_trigger_word[31:24] : qual_trigger_word[23:16];
  qual_word_valid<=frontend_run && qual_trigger_valid;qual_run<=frontend_run && qual_configured;
  zone_word<=zone_trigger_word;zone_word_valid<=frontend_run && zone_trigger_valid;
  rise_q<=rise_ch[qual_ch];fall_q<=fall_ch[qual_ch];
  rise_z<=|(rise_ch & edge_rise_enable);fall_z<=|(fall_ch & edge_fall_enable);
 end
 // The slope's two levels compare the sum of five consecutive samples, one
 // from each interleaved converter, so the converters' raw offsets cancel:
 // on raw codes a slow edge's crossings moved by their spread over its slew
 // (bench: 18 ns edges measured as 8 to 20 ns).
 wire [10:0] qsum_first,qsum_second;wire qs_v3;
 five_sum sums(.clk(core),.valid(qual_word_valid),.first(qual_first),.second(qual_second),
  .sum_first(qsum_first),.sum_second(qsum_second),.out_valid(qs_v3));
 // the trigger level's own edges serve pulse and video
 wire rise_a,fall_a,rise_b,fall_b;
 sum_edge_pair #(.W(11)) level_a(.clk(core),.enable(qual_run),.valid(qs_v3),.first(qsum_first),.second(qsum_second),
  .level(qual_a5),.low_level(qual_a5_lo),.high_level(qual_a5_hi),.rise(rise_a),.fall(fall_a));
 sum_edge_pair #(.W(11)) level_b(.clk(core),.enable(qual_run),.valid(qs_v3),.first(qsum_first),.second(qsum_second),
  .level(qual_b5),.low_level(qual_b5_lo),.high_level(qual_b5_hi),.rise(rise_b),.fall(fall_b));
 // A pulse or video trigger fires three clocks after an edge trigger on its
 // closing edge would (the copies, and qual_trigger's edge and fire
 // registers); a slope six (and its sums' two samples of delay).
 wire qual_fire;
 qual_trigger #(.CLK_MHZ(250)) qualify(.clk(core),.enable(qual_run),
  .rise(rise_q),.fall(fall_q),.rise_a(rise_a),.fall_a(fall_a),.rise_b(rise_b),.fall_b(fall_b),
  .kind(qual_cfg_l[2:1]),.negative(qual_cfg_l[3]),.cond(qual_cfg_l[5:4]),.min_zero(qual_cfg_l[7]),
  .min_m1(qual_min_l),.max_t(qual_max_l),.prescale_m1(qual_pre_l),.line(qual_line_l),.fire(qual_fire));
 // The zone trigger (zone_qual.v): a candidate is the edge or the
 // qualifier's event; with zones on only the candidates the zones pass fire.
 // Host registers: 83 {avoid[11:8], channel[7:4], enable[3:0]} per zone;
 // 84 the decision's wait in buckets; 85 the half clocks a bucket less one (a
 // power of two less one); 86..89 zone codes {high[15:8], low[7:0]}; 90..92
 // and 69 window starts, 70..73 window ends, in signed buckets from the edge's.
 reg [15:0] zone_cfg=0,zone_cfg_l=0;reg [10:0] zone_wait=1,zone_wait_l=1;reg [11:0] zone_bucket=0,zone_bucket_l=0;
 reg [15:0] zone_codes[0:3];reg [10:0] zone_a[0:3],zone_b[0:3];
 reg [31:0] zone_lo_l=0,zone_hi_l=0;reg [43:0] zone_a_l=0,zone_b_l=0;
 integer zi;
 initial for(zi=0;zi<4;zi=zi+1)begin zone_codes[zi]=0;zone_a[zi]=0;zone_b[zi]=0;end
 always @(posedge core)if(config_idle)begin
  zone_cfg_l<=zone_cfg;zone_wait_l<=zone_wait;zone_bucket_l<=zone_bucket;
  for(zi=0;zi<4;zi=zi+1)begin
   zone_lo_l[8*zi+:8]<=zone_codes[zi][7:0];zone_hi_l[8*zi+:8]<=zone_codes[zi][15:8];
   zone_a_l[11*zi+:11]<=zone_a[zi];zone_b_l[11*zi+:11]<=zone_b[zi];
  end
 end
 (* preserve, dont_merge *) reg zone_on=0;
 always @(posedge core)if(config_idle)zone_on<=zone_cfg[3:0]!=0;
 wire zone_fire_half;reg zone_fire_seen=0,zone_fire=0;
 // the edges one clock later, with the zones' copy of the samples
 wire zone_candidate=rise_z || fall_z || qual_fire;
 // The zones decide on the half clock; one 250 MHz clock of their fire.
 always @(posedge core)begin zone_fire_seen<=zone_fire_half;zone_fire<=zone_fire_half && !zone_fire_seen;end
 zone_qual #(.AW(10)) zones(.clk(core),.half(halfclk),.enable(frontend_run && zone_on),.valid(zone_word_valid),
  .word(zone_word),.candidate(zone_candidate),.zen(zone_cfg_l[3:0]),.zch(zone_cfg_l[7:4]),.zavoid(zone_cfg_l[11:8]),
  .zlo(zone_lo_l),.zhi(zone_hi_l),.za(zone_a_l),.zb(zone_b_l),.wait_buckets(zone_wait_l),.bucket_mask(zone_bucket_l),.fire(zone_fire_half));
`else
 wire qual_configured=1'b0;
`endif
`ifdef SYMBOL_TRIGGER
 // ADR-SYMBOL-TRIGGER: the serial buses of timed pulses on one or two lines
 // (symbol_trigger.v), on the raw samples whatever the record's decimation.
 // Host registers: 93 control {prescale[13:11], leader runs[10:8],
 // mode[7:6], clock falling[5], A and B[4], invert B[3], invert A[2],
 // channel of line A[1], enable[0]}; 94/95 the unit in 1/256 receiver
 // clocks; 96 {period[13:8], frame units[5:0]}; 97 {phase0[15:10], k[9:0]};
 // 98 the table-loaded flag; 99 the table address and 100 an entry (the
 // address steps); 101..104 the pattern, 105..108 the mask. Read 76 the
 // block, 78 its matches (modulo 2^16), 98 the flag. The block reads its
 // settings straight from these registers: the host holds them while armed.
 reg [15:0] sym_cfg=0,sym_fp=0,sym_kp=0;reg [27:0] sym_unit=28'd2048;reg [63:0] sym_pattern=0,sym_mask=0;
 reg sym_loaded=0,sym_we=0;reg [10:0] sym_addr=0,sym_wa=0;reg [7:0] sym_wd=0;
 always @(posedge clk)begin sym_we<=wc && ws==100;sym_wa<=sym_addr;sym_wd<=wd[7:0];end
 (* preserve, dont_merge *) reg sym_ch=0,sym_on=0;
 always @(posedge core)if(config_idle)begin sym_ch<=sym_cfg[1];sym_on<=sym_cfg[0];end
 wire sym_configured=sym_on;
 // Line A on the selected channel at the trigger level (register 7), line B
 // on the other at its own (register 59), each with the trigger's re-arm band:
 // one sample a word, as the general image's UART takes it.
 (* preserve, dont_merge *) reg [7:0] sym_low=0,sym_high=0,sym_low2=0,sym_high2=0;
 reg [7:0] sym_code_a=0,sym_code_b=0;reg sym_code_valid=0,sym_line_a=1,sym_line_b=1;
 always @(posedge core)begin
  sym_low<=level_low_l;sym_high<=level_high_l;sym_low2<=level2_low_l;sym_high2<=level2_high_l;
  sym_code_a<=sym_ch ? il_raw_word[31:24] : il_raw_word[23:16];
  sym_code_b<=sym_ch ? il_raw_word[23:16] : il_raw_word[31:24];
  sym_code_valid<=frontend_run && il_raw_valid;
  if(sym_code_valid)begin
   if(sym_code_a>=sym_high)sym_line_a<=1;else if(sym_code_a<=sym_low)sym_line_a<=0;
   if(sym_code_b>=sym_high2)sym_line_b<=1;else if(sym_code_b<=sym_low2)sym_line_b<=0;
  end
 end
 reg [1:0] sym_a_sync=2'b11,sym_b_sync=2'b11,sym_run_sync=0;
 always @(posedge halfclk)begin
  sym_a_sync<={sym_a_sync[0],sym_line_a};sym_b_sync<={sym_b_sync[0],sym_line_b};
  sym_run_sync<={sym_run_sync[0],running && !arm && sym_on};
 end
 wire sym_match;
 symbol_trigger symbols(.clk(halfclk),.reset(!sym_run_sync[1]),.a(sym_a_sync[1]),.b(sym_b_sync[1]),
  .inv_a(sym_cfg[2]),.inv_b(sym_cfg[3]),.and_ab(sym_cfg[4]),.clk_fall(sym_cfg[5]),.mode(sym_cfg[7:6]),.lead_len(sym_cfg[10:8]),.prescale(sym_cfg[13:11]),
  .unit_q8(sym_unit),.frame_units(sym_fp[5:0]),.period(sym_fp[13:8]),.phase0(sym_kp[15:10]),.k(sym_kp[9:0]),
  .pattern(sym_pattern),.mask(sym_mask),.wr_clk(clk),.wr_en(sym_we),.wr_addr(sym_wa),.wr_data(sym_wd),.match(sym_match));
 // A match once the record holds its history fires and holds until the
 // next arm, as the protocol triggers do: the record takes a trigger only
 // with a word written (decimated, one clock in many). The host counts
 // matches through a toggle.
`ifndef UART_TRIGGER
 wire history_ready;
`endif
 reg [2:0] sym_match_sync=0;reg sym_fire=0,sym_toggle=0;
 always @(posedge core)begin
  sym_match_sync<={sym_match_sync[1:0],sym_match};
  if(!running || arm || !history_ready)sym_fire<=0;
  else if(sym_on && sym_match_sync[1] && !sym_match_sync[2])sym_fire<=1;
 end
 always @(posedge halfclk)if(sym_match)sym_toggle<=!sym_toggle;
 reg [2:0] sym_toggle_sync=0;reg [15:0] sym_count=0;
 always @(posedge clk)begin
  sym_toggle_sync<={sym_toggle_sync[1:0],sym_toggle};
  if(sym_toggle_sync[2]!=sym_toggle_sync[1])sym_count<=sym_count+1'b1;
 end
`endif
`ifdef QUAL_TRIGGER
 // The qualifier and the zones fire for one clock, a few after the edge
 // that closes their event, and the record takes a trigger only with a word
 // written (decimated, one clock in many: a pulse, slope or video trigger
 // never fired on a decimated record). Their fires hold until the next arm,
 // as the symbol trigger's.
 reg qual_hold=0,zone_hold=0;
 always @(posedge core)begin
  if(!running || arm || !history_ready)begin qual_hold<=0;zone_hold<=0;end
  else begin
   if(qual_fire)qual_hold<=1;
   if(zone_fire)zone_hold<=1;
  end
 end
`endif
 always @(posedge core)if(config_idle)begin
  edge_auto<=!cfg[4];
  protocol_enable<=cfg[0] && cfg[4] && protocol_configured;
  // A qualifier fires on its own event instead of every edge.
`ifdef SYMBOL_TRIGGER
  edge_rise_enable<=(!protocol_configured && !qual_configured && !sym_configured && cfg[0] && cfg[4] && !cfg[5]) ? (cfg[6] ? 2'b10 : 2'b01) : 2'b00;
  edge_fall_enable<=(!protocol_configured && !qual_configured && !sym_configured && cfg[0] && cfg[4] && cfg[5]) ? (cfg[6] ? 2'b10 : 2'b01) : 2'b00;
`else
  edge_rise_enable<=(!protocol_configured && !qual_configured && cfg[0] && cfg[4] && !cfg[5]) ? (cfg[6] ? 2'b10 : 2'b01) : 2'b00;
  edge_fall_enable<=(!protocol_configured && !qual_configured && cfg[0] && cfg[4] && cfg[5]) ? (cfg[6] ? 2'b10 : 2'b01) : 2'b00;
`endif
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
`ifdef QUAL_TRIGGER
  // With zones on, only the candidates the zones pass fire.
`ifdef SYMBOL_TRIGGER
  il_fire_stage<=force_trigger || edge_auto || (zone_on ? zone_hold : (|(rise_ch & edge_rise_enable)) || (|(fall_ch & edge_fall_enable)) || qual_hold || sym_fire)
`else
  il_fire_stage<=force_trigger || edge_auto || (zone_on ? zone_hold : (|(rise_ch & edge_rise_enable)) || (|(fall_ch & edge_fall_enable)) || qual_hold)
`endif
`ifdef UART_TRIGGER
   || (protocol_enable && uart_fired)
`endif
   ;
`else
  il_fire_stage<=force_trigger || edge_auto || (|(rise_ch & edge_rise_enable)) || (|(fall_ch & edge_fall_enable))
`ifdef UART_TRIGGER
   || (protocol_enable && uart_fired)
`endif
   ;
`endif
`endif
  if(!frontend_run)begin il_valid1<=0;il_valid<=0;end
 end
 reg snap_request=0,snap_busy=0,snap_capture=0;wire snap_ack;
 reg [9:0] encode_enable=10'h3ff;
 wire frontend_consume=frontend_run && (write_ready
`ifdef UART_TRIGGER
  || event_core_sync[2]
`endif
`ifdef STREAM_DRUM
  // The drum takes words continuously into its own FIFO; write_ready is up
  // only during its SRAM bursts (gating on it, the interleaver overflows at
  // once and stops the front end).
  || drum_active
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
`ifdef LINE_LEVELS
 // The second line's level; its band is register 7's hysteresis.
 reg [7:0] level2=128,host_level2_low=124,host_level2_high=132;
 always @(posedge clk)begin
  host_level2_low<=level2>=hysteresis ? level2-hysteresis : 8'd0;
  host_level2_high<=({1'b0,level2}+{1'b0,hysteresis})<=255 ? level2+hysteresis : 8'd255;
 end
`endif
`ifdef QUAL_TRIGGER
 // The slope levels in five-sample sums and their bands, at the host clock.
 reg [10:0] qual_a5_host=640,qual_b5_host=640;
 always @(posedge clk)begin
  qual_a5_host<={qual_levels[7:0],2'b00}+qual_levels[7:0];qual_b5_host<={qual_levels[15:8],2'b00}+qual_levels[15:8];
 end
 always @(posedge core)if(config_idle)begin
  qual_a5<=qual_a5_host;qual_a5_lo<=qual_a5_host>=11'd15 ? qual_a5_host-11'd15 : 11'd0;qual_a5_hi<=qual_a5_host+11'd15;
  qual_b5<=qual_b5_host;qual_b5_lo<=qual_b5_host>=11'd15 ? qual_b5_host-11'd15 : 11'd0;qual_b5_hi<=qual_b5_host+11'd15;
 end
`endif
`ifdef BURST_RECALL
 reg [BUF_AW:0] burst_index=0;
 always @(posedge clk)begin
  if(wc && ws==17)burst_index<=wd[BUF_AW:0];
  else if(rp && rs==25)burst_index<=burst_index+1'b1;
 end
`endif
 // Front-panel serial scan (ADR-PANEL-SERIAL-SCAN): write 31 config, 32 F2
 // half period, 33 frame gap (half periods), 34 read index; read 31 identity,
 // 44 the word at the index (0..3 frame bits, 4..7 knob counts, two a word,
 // 8 frames, 9 changed frames, 10 pins {J1,F1,J2,F2}).
 // Power-on: the bench-settled scan (F2 about 300 kHz at the 50 MHz GPMC clock).
 reg [15:0] panel_cfg=16'hC800,panel_half=16'd83,panel_gap=16'd200;reg [3:0] panel_index=0;
 wire [63:0] panel_frame,panel_knobs;wire [7:0] panel_frames,panel_changes;wire panel_j1;
 panel_scan panel(.clk(clk),.cfg(panel_cfg),.half(panel_half),.gap(panel_gap),.j1(j1),
 .load(j2),.sclk(f2),.f1(f1),.frame(panel_frame),.knobs(panel_knobs),.frames(panel_frames),.changes(panel_changes),.j1_level(panel_j1));
 reg [15:0] panel_word=0;
 always @(posedge clk)
  case(panel_index)
   8:panel_word<={8'd0,panel_frames};9:panel_word<={8'd0,panel_changes};10:panel_word<={12'd0,panel_j1,f1,j2,f2};
   default:panel_word<=panel_index<4 ? panel_frame[16*panel_index[1:0]+:16] :
    panel_index<8 ? panel_knobs[16*panel_index[1:0]+:16] : 16'd0;
  endcase
 always @(posedge clk) if(wc) case(ws)
  1:begin opcode<=wd[3:0];request<=~request;end
  31:panel_cfg<=wd;32:panel_half<=wd;33:panel_gap<=wd;34:panel_index<=wd[3:0];
  2:pre_cfg[15:0]<=wd;3:pre_cfg[19:16]<=wd[3:0];
  4:post_cfg[15:0]<=wd;5:post_cfg[19:16]<=wd[3:0];
  6:config_word<=wd;7:begin level<=wd[7:0];hysteresis<=wd[15:8]!=0 ? wd[15:8] : 8'd4;end
`ifdef LINE_LEVELS
  59:level2<=wd[7:0];
`endif
`ifdef TRIG_UART
  48:uart_cfg<=wd;
  49:uart_ticks[15:0]<=wd;
  50:uart_ticks[23:16]<=wd[7:0];
  51:uart_pattern[15:0]<=wd;
  52:uart_pattern[31:16]<=wd;
`endif
`ifdef TRIG_I2C
  53:i2c_cfg<=wd;
  54:i2c_address<=wd[6:0];
  55:i2c_pattern[15:0]<=wd;
  56:i2c_pattern[31:16]<=wd;
`endif
`ifdef TRIG_SENT
  77:sent_cfg<=wd;78:sent_ticks[15:0]<=wd;79:sent_ticks[23:16]<=wd[7:0];
  80:sent_pattern[15:0]<=wd;81:sent_pattern[31:16]<=wd;82:sent_nibbles<=wd[6:0];
`endif
`ifdef TRIG_USB
 97:usb_cfg<=wd;98:usb_ticks[15:0]<=wd;99:usb_ticks[23:16]<=wd[7:0];100:usb_pattern[15:0]<=wd;101:usb_pattern[31:16]<=wd;
`endif
`ifdef TRIG_MAN
  102:man_cfg<=wd;103:man_ticks[15:0]<=wd;104:man_ticks[23:16]<=wd[7:0];
  105:man_pattern[15:0]<=wd;106:man_pattern[31:16]<=wd;107:man_pattern[47:32]<=wd;108:man_pattern[63:48]<=wd;
`endif
`ifdef TRIG_MIL
  93:mil_cfg<=wd;94:mil_ticks[15:0]<=wd;95:mil_ticks[23:16]<=wd[7:0];96:mil_pattern<=wd;
`endif
`ifdef TRIG_SPI
  64:spi_cfg<=wd;
  65:spi_gap[15:0]<=wd;66:spi_gap[23:16]<=wd[7:0];
  67:spi_pattern[15:0]<=wd;68:spi_pattern[31:16]<=wd;
`ifdef SAMPLE_BUS
  58:sample_delay<=wd[7:0];
`endif
`endif
`ifdef ENVELOPE_RECALL
  43:env_bucket_lo<=wd;44:env_control<=wd;45:env_skip_host<=wd[4:0];
`endif
`ifdef SEQUENCE_TRIGGER
  118:seq_cfg<=wd[7:0];119:seq_addr<=wd[4:0];
  120:seq_value[15:0]<=wd;121:seq_value[31:16]<=wd;122:seq_mask[15:0]<=wd;123:seq_mask[31:16]<=wd;
`endif
`ifdef PACKET_TRIGGER
  109:begin
`ifdef TRIG_ARINC
   arinc_cfg<=wd[11:8]==8 ? wd : 16'd0;
`endif
`ifdef TRIG_CAN
   can_cfg<=wd[11:8]==6 ? wd : 16'd0;
`endif
`ifdef TRIG_FLEXRAY
   flex_cfg<=wd[11:8]==10 ? wd : 16'd0;
`endif
  end
  110:packet_ticks[15:0]<=wd;111:packet_ticks[23:16]<=wd[7:0];
  112:packet_aux[15:0]<=wd;113:packet_aux[31:16]<=wd;
  114:packet_pattern[15:0]<=wd;115:packet_pattern[31:16]<=wd;116:packet_pattern[47:32]<=wd;117:packet_pattern[63:48]<=wd;
`endif
`ifdef SYMBOL_TRIGGER
  93:sym_cfg<=wd;94:sym_unit[15:0]<=wd;95:sym_unit[27:16]<=wd[11:0];96:sym_fp<=wd;97:sym_kp<=wd;98:sym_loaded<=wd[0];
  99:sym_addr<=wd[10:0];100:sym_addr<=sym_addr+1'b1;
  101:sym_pattern[15:0]<=wd;102:sym_pattern[31:16]<=wd;103:sym_pattern[47:32]<=wd;104:sym_pattern[63:48]<=wd;
  105:sym_mask[15:0]<=wd;106:sym_mask[31:16]<=wd;107:sym_mask[47:32]<=wd;108:sym_mask[63:48]<=wd;
`endif
`ifdef QUAL_TRIGGER
  21:qual_cfg<=wd;22:qual_min<=wd;24:qual_max<=wd;26:qual_levels<=wd;27:qual_video<=wd;28:qual_pre<=wd;
  83:zone_cfg<=wd;84:zone_wait<=wd[10:0];85:zone_bucket<=wd[11:0];
  86:zone_codes[0]<=wd;87:zone_codes[1]<=wd;88:zone_codes[2]<=wd;89:zone_codes[3]<=wd;
  90:zone_a[0]<=wd[10:0];91:zone_a[1]<=wd[10:0];92:zone_a[2]<=wd[10:0];69:zone_a[3]<=wd[10:0];
  70:zone_b[0]<=wd[10:0];71:zone_b[1]<=wd[10:0];72:zone_b[2]<=wd[10:0];73:zone_b[3]<=wd[10:0];
`endif
  8:count_cfg[15:0]<=wd;9:count_cfg[19:16]<=wd[3:0];
`ifdef INTERLEAVE
  15:encode_enable<=wd[9:0];
`endif
`ifdef PRECISION
  18:begin
   decim_request<=wd[4:0];
`ifdef PICK_ALT
   decim_pick_request<=wd[5];
`endif
  end
`endif
`ifdef STREAM_CAPTURE
  19:stream_cfg<=wd[4:0];
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
`ifdef ENVELOPE_RECALL
  // An envelope recall reduces any record window into the buffer.
  buffer_count_ok<=count_cfg<=BUF_WORDS || env_control[15];
`else
  buffer_count_ok<=count_cfg<=BUF_WORDS;
`endif
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
`ifdef STACK_ENGINE
 // ADR-STACKING-IMAGE-SPLIT: the stacking port borrows the transport only for
 // a frozen, idle record while no host command is outstanding. Arm and host
 // recall are rejected while it is granted; release is immediate.
 localparam STACK_BINS=32;
 wire stack_grant_request,stack_hit;wire [15:0] stack_rd;
 wire stack_command,stack_command_read,stack_command_discard,stack_command_continue;
 wire [19:0] stack_command_count;
 (* async_reg="true" *) reg [1:0] stack_grant_s=0;
 // The record id only distinguishes records between cache resets, and every
 // stacking operation resets the cache, so 16 bits suffice at 250 MHz.
 reg stack_grant=0;reg [15:0] stack_record_id=0;reg [18:0] stack_start=0,stack_record_start=0;
 // Equal copies keep the grant off one wide fanout net at 250 MHz.
 (* preserve, dont_merge *) reg stack_select=0,stack_owned=0;
 reg stack_command_q=0,stack_command_read_q=0,stack_command_discard_q=0,stack_command_continue_q=0;
 // A grant is taken only while the transport is idle with no host command in
 // flight, and released only once the transport is idle with no queued
 // stacking command, so the two command sources never overlap.
 // The slow grant conditions are registered a clock early. A host command
 // passes pending -> dispatch -> command -> transport busy; each step blocks
 // the grant either live (pending, dispatch) or in stack_grant_ok.
 reg stack_grant_ok=0;
 always @(posedge core)stack_grant_ok<=ready && record_done && !running && !priming && !arm && !command && !dispatch;
 wire stack_grant_next=stack_grant ? (stack_grant_s[1] || !ready || stack_command_q) :
  (stack_grant_s[1] && stack_grant_ok && !dispatch && !pending);
 reg [19:0] stack_words=0;
 // Frozen-record metadata is registered before it reaches the stacking clock.
 always @(posedge core)begin
  stack_grant_s<={stack_grant_s[0],stack_grant_request};
  stack_grant<=stack_grant_next;stack_select<=stack_grant_next;stack_owned<=stack_grant_next;
  // One register stage keeps the cache's command decode off the transport's
  // acceptance path; the recall waits for done/data, never re-samples ready.
  stack_command_q<=stack_command;stack_command_read_q<=stack_command_read;
  stack_command_discard_q<=stack_command_discard;stack_command_continue_q<=stack_command_continue;
  if(arm)stack_record_id<=stack_record_id+1'b1;
  stack_record_start<=record_start;stack_start<=origin+stack_record_start;stack_words<=record_length;
 end
 stack_engine_port #(.BINS(STACK_BINS)) stack_port(.host_clk(clk),.engine_clk(halfclk),.transport_clk(core),
  .wc(wc),.ws(ws),.wd(wd),.rp(rp),.rs(rs),.rd(stack_rd),.hit(stack_hit),
  .grant_request(stack_grant_request),.transport_owned(stack_owned),
  .frozen(record_done),.record_id({16'd0,stack_record_id}),.record_start(stack_start),.record_words(stack_words),
  .transport_ready(ready),.transport_done(transport_done),.transport_read_valid(read_valid),.transport_read_data(read_data),.position(position),
  .command(stack_command),.command_read(stack_command_read),.command_discard(stack_command_discard),
  .command_continue(stack_command_continue),.command_count(stack_command_count));
 // The strobes are exclusive: see stack_grant_next; dispatch rejects host
 // commands while granted and the cache gates its strobe on ownership.
 wire transport_command=command || stack_command_q;
 wire transport_command_read=stack_select ? stack_command_read_q : command_read;
 wire transport_command_discard=stack_select ? stack_command_discard_q : command_discard;
 wire transport_command_continue=stack_select ? stack_command_continue_q : command_continue;
 // The count is registered from both sources, one stage like the strobes.
 // The host count follows configuration; the cache holds its count from its
 // strobe until the next command (stack_record_cache.v).
 reg [19:0] transport_count_q=0;
 always @(posedge core)transport_count_q<=stack_select ? stack_command_count : command_count;
 wire [19:0] transport_command_count=transport_count_q;
 wire [31:0] transport_write_data=write_data;wire transport_write_valid=write_valid,transport_write_stop=write_stop;
`else
`ifdef STREAM_DRUM
 // While the drum runs it alone commands the SRAM (no host recalls in stream mode).
 // The transport's command mux selects on its own copy of drum_active (fanout).
 (* preserve *) reg drum_sel=0;
 always @(posedge core)drum_sel<=drum_active;
 wire transport_command=drum_sel ? drum_cmd : command,transport_command_read=drum_sel ? drum_cmd_read : command_read;
 wire transport_command_discard=drum_sel ? drum_cmd_discard : command_discard;
 wire transport_command_continue=drum_sel ? drum_cmd_continue : command_continue;
 // Registered (timing): both sources hold their count for clocks before a command.
 reg [19:0] transport_count_q=0;
 always @(posedge core)transport_count_q<=drum_sel ? drum_cmd_count : command_count;
 wire [19:0] transport_command_count=transport_count_q;
 wire [31:0] transport_write_data=drum_sel ? drum_wdata : write_data;
 wire transport_write_valid=drum_sel ? drum_wvalid : write_valid,transport_write_stop=drum_sel ? drum_wstop : write_stop;
`else
 wire transport_command=command,transport_command_read=command_read,transport_command_discard=command_discard;
 wire transport_command_continue=command_continue;wire [19:0] transport_command_count=command_count;
 wire [31:0] transport_write_data=write_data;wire transport_write_valid=write_valid,transport_write_stop=write_stop;
`endif
`endif
 sram_transport #(.CONTINUOUS_ONLY(1),.READ_DELAY(SRAM_READ_DELAY)) transport(.clk(core),.sample_clk(sample_clk),.reset(1'b0),.locked(locked),
  .command(transport_command),.command_read(transport_command_read),.command_discard(transport_command_discard),.command_continue(transport_command_continue),.command_count(transport_command_count),
  .ready(ready),.write_data(transport_write_data),.write_valid(transport_write_valid),.write_stop(transport_write_stop),.write_ready(write_ready),
  .read_data(read_data),.read_valid(read_valid),.done(transport_done),.position(position),.dq(dq),.k1(k1),.k2(k2),.g1(g1));
 wire record_fire=fire;
 sram_record #(.CONFIG_VALIDATED(1),.DERIVED_CONFIG(1)) record(.clk(core),.reset(1'b0),.arm(arm),.halt(halt),.pre_count(pre_cfg),.post_count(post_cfg),
  .pre_start_config(pre_start_host),.pre_plus_one_config(pre_plus_one_host),.post_minus_one_config(post_minus_one_host),
`ifdef UART_TRIGGER
  .history_ready(history_ready),
`elsif SYMBOL_TRIGGER
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
 wire stream_write;wire [BUF_AW-2:0] stream_address;wire [63:0] stream_data;wire stream_seal_ok;
 wire packetizer_fault,stream_finished,bank_overrun,host_bank_overrun;
 wire stream_packet_valid,stream_packet_single,stream_packet_seal;wire [63:0] stream_packet_data;
 wire [1:0] stream_available,stream_token,stream_single;
 wire [BUF_AW-2:0] stream_count0,stream_count1;
 wire [63:0] stream_first0,stream_first1;
 reg stream_was_running=0;
 always @(posedge core)stream_was_running<=running;
 // Registered: the packetizer and banks synchronize it, and a wire would put
 // the arm/priming decode on their reset fanout at 250 MHz.
 (* preserve *) reg stream_reset=1;
`ifdef STREAM_DRUM
 always @(posedge core)stream_reset<=!stream_mode[0] || !drum_active; // a new stream starts at ordinal 0
`else
 always @(posedge core)stream_reset<=!stream_mode[0] || priming || arm;
`endif
 // The 125 MHz buffer write port selects stream or recall words with its own
 // copy of the mode, so no core-clock register drives the RAM's port mux.
 (* preserve *) reg stream_on_h=0;
 always @(posedge halfclk)stream_on_h<=stream_mode[0];
 wire peak_valid;wire [31:0] peak_word;
 (* preserve *) reg peak_on=0;
 always @(posedge core)peak_on<=stream_mode[2] && stream_mode[0] && frontend_run;
 // A bucket spans as many raw words (two samples each) as the precision
 // decimator's output word does samples: equal stream rates.
 (* preserve *) reg [4:0] peak_log=0;
 always @(posedge core)peak_log<=decim_log-1'b1; // static while running: registered
 stream_peak peak(.clk(core),.enable(peak_on),.log_words(peak_log),.in_valid(il_raw_valid),.in_word(il_raw_word),
  .out_valid(peak_valid),.out_word(peak_word));
`ifdef STREAM_DRUM
 // The drum's input: the bucket extremes (peak) or the decimated words, or a
 // counter ramp with the counter source (bench checks); not the record's path.
 // One register stage first (timing at 250 MHz): the ramp steps on a register.
 (* preserve *) reg drum_in_valid=0;(* preserve *) reg [31:0] drum_in_data=0;reg [31:0] drum_ramp=0;reg drum_ramp_c=0;
 reg drum_s_valid=0,drum_p_valid=0,drum_peak=0,drum_il=0;reg [31:0] drum_s_word=0,drum_p_word=0;
 always @(posedge core)begin
  drum_s_valid<=source_valid && frontend_run;drum_s_word<=il_word;
  drum_p_valid<=peak_valid;drum_p_word<=peak_word;
  drum_peak<=stream_mode[2];drum_il<=cfg[0];
  drum_in_valid<=drum_active && (drum_peak ? drum_p_valid : drum_s_valid);
  drum_in_data<=drum_peak ? drum_p_word : drum_il ? drum_s_word : drum_ramp;
  // The ramp in two 16-bit halves: the high half steps a clock after the low
  // half wraps (a stream is decimated by 2^8 or more: words are clocks apart).
  drum_ramp_c<=0;
  if(!drum_active)begin drum_ramp<=0;end
  else begin
   if(!drum_peak && drum_s_valid)begin drum_ramp[15:0]<=drum_ramp[15:0]+1'b1;drum_ramp_c<=&drum_ramp[15:0];end
   if(drum_ramp_c)drum_ramp[31:16]<=drum_ramp[31:16]+1'b1;
  end
 end
 wire drum_bank_ready;
 stream_drum #(.AW(19),.WF_AW(12),.DESC_AW(9),.BANK_AW(BUF_AW-2)) drum(.clk(core),.active(drum_active),
  .in_valid(drum_in_valid),.in_data(drum_in_data),
  .cmd(drum_cmd),.cmd_read(drum_cmd_read),.cmd_discard(drum_cmd_discard),.cmd_continue(drum_cmd_continue),.cmd_count(drum_cmd_count),
  .t_ready(ready),.t_write_ready(write_ready),.t_done(transport_done),.wdata(drum_wdata),.wvalid(drum_wvalid),.wstop(drum_wstop),
  .rdata(read_data),.rvalid(read_valid),.position(position),.fault(drum_fault),
  .hclk(halfclk),.bank_ready(drum_bank_ready),.packet_valid(stream_packet_valid),.packet_data(stream_packet_data),
  .packet_single(stream_packet_single),.packet_seal(stream_packet_seal));
 assign packetizer_fault=drum_fault,stream_finished=!drum_active,stream_seal_ok=1'b1;
 // ADR-SOUND: the trigger comparator's crossings on the streamed words, as
 // the ordinals of the words they fall in (registered: timing at 250 MHz).
 reg mark_rise=0,mark_fall=0;
 always @(posedge core)begin mark_rise<=|(rise_ch & mark_rise_enable);mark_fall<=|(fall_ch & mark_fall_enable);end
 wire [31:0] mark_rise_word,mark_fall_word;wire [15:0] mark_rise_count,mark_fall_count;
 stream_marks marks(.clk(core),.active(drum_active),.word(drum_in_valid),.rise(mark_rise),.fall(mark_fall),
  .rise_word(mark_rise_word),.fall_word(mark_fall_word),.rise_count(mark_rise_count),.fall_count(mark_fall_count));
`else
 // Isolate the live-stream tap from SRAM arm/priming decode fanout. Stop and
 // data validity take the same extra clock, retaining the last accepted word.
 (* preserve, dont_merge *) reg [31:0] stream_tap_data=0;
 (* preserve *) reg stream_tap_valid=0,stream_tap_stop=0;
 always @(posedge core)begin
  // Peak mode streams the raw words' bucket extremes instead of the record's
  // (averaged) decimated words (stream_peak.v); the record itself is unchanged.
  stream_tap_data<=stream_mode[2] ? peak_word : cfg[0] ? capture_word : ramp; // priming data is invalid and need not select zero
  stream_tap_valid<=stream_mode[0] && !halt && (stream_mode[2] ? peak_valid && running : write_valid && !dummy_write);
  stream_tap_stop<=stream_was_running && !running;
 end
 stream_packetizer stream_pack(.reset(stream_reset),.word_clk(core),.packet_clk(halfclk),
  .word_valid(stream_tap_valid),.word_data(stream_tap_data),
  .stop(stream_tap_stop),.seal_ok(stream_seal_ok),.fault(packetizer_fault),.finished(stream_finished),
  .packet_valid(stream_packet_valid),.packet_data(stream_packet_data),.packet_single(stream_packet_single),.packet_seal(stream_packet_seal));
`endif
 stream_banks #(.BANK_AW(BUF_AW-2)) stream_queue(.reset(stream_reset),.producer_clk(halfclk),.host_clk(clk),
  .packet_valid(stream_packet_valid),.packet_data(stream_packet_data),.packet_single(stream_packet_single),.seal(stream_packet_seal),
`ifdef STREAM_DRUM
  .packet_ready(),.cur_ready(drum_bank_ready),
`else
  .packet_ready(stream_seal_ok),.cur_ready(),
`endif.mem_write(stream_write),.mem_address(stream_address),.mem_data(stream_data),
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
  if(stream_on_h)begin if(stream_write)buffer_mem64[stream_address]<=stream_data;end
  else if(packet_toggle!=packet_seen)buffer_mem64[packet_addr]<=packet_data;
 end
`else
 // With an envelope recall the 125 MHz side reduces each bucket of recalled
 // packets to per-channel extremes and writes those into the host buffer.
`ifdef ENVELOPE_RECALL
 (* preserve, dont_merge *) reg env_on=0;reg env_q8=0;reg [18:0] env_bucket=1;reg [3:0] env_skip=0;
 reg env_start_toggle=0,env_start_seen=0;
 always @(posedge core)if(command)env_start_toggle<=!env_start_toggle;
 // The host holds these from before the recall command until it completes;
 // registers 43..45 count words, the reducer counts two-word packets.
 always @(posedge halfclk)begin
  env_on<=env_control[15];env_q8<=env_control[14];env_bucket<={env_control[3:0],env_bucket_lo[15:1]};env_skip<=env_skip_host[4:1];
  env_start_seen<=env_start_toggle;
 end
 wire env_write;wire [BUF_AW-2:0] env_addr;wire [63:0] env_data;wire [BUF_AW-1:0] env_words;
`ifdef BOX_DECIM
 // box averages: extremes on the integer bytes (no room for 16-bit ones)
 envelope_reduce #(.AW(BUF_AW-1),.Q8_INT(1)) envelope(
`elsif PICK_DECIM
 // picked samples: integer bytes, the fraction 0
 envelope_reduce #(.AW(BUF_AW-1),.Q8_INT(1)) envelope(
`else
 envelope_reduce #(.AW(BUF_AW-1)) envelope(
`endif
  .clk(halfclk),.start(env_start_seen!=env_start_toggle),
`ifdef PRECISION
  .q8(env_q8),
`else
  // Raw-only images never recall Q8.8 records.
  .q8(1'b0),
`endif
  .bucket(env_bucket),.skip(env_skip),
  .in_valid(env_on && packet_toggle!=packet_seen),.in_data(packet_data),
  .out_write(env_write),.out_addr(env_addr),.out_data(env_data),.words(env_words));
`endif
`ifdef ENVELOPE_RECALL
 always @(posedge halfclk)begin
  if(packet_toggle!=packet_seen)packet_seen<=packet_toggle;
  if(env_on)begin if(env_write)buffer_mem64[env_addr]<=env_data;end
  else if(packet_toggle!=packet_seen)buffer_mem64[packet_addr]<=packet_data;
 end
`else
 always @(posedge halfclk)if(packet_toggle!=packet_seen)begin buffer_mem64[packet_addr]<=packet_data;packet_seen<=packet_toggle;end
`endif
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
  if(pending && !command && !arm && !dispatch)begin dispatch<=1;dispatch_opcode<=opcode;dispatch_arm_ok<=ready && !running && arm_geometry_ok
`ifdef STACK_ENGINE
 && !stack_grant
`endif
 ;dispatch_read_ok<=ready && record_done
`ifdef STACK_ENGINE
 && !stack_grant
`endif

`ifdef STREAM_CAPTURE
 && !stream_mode[0]
`endif
 && read_geometry_ok && (opcode==3 || buffer_count_ok) && (opcode!=7 || prefetch_valid);end
  config_idle<=!running && !priming;
  if(config_idle)begin cfg<=config_word;level_l<=level;
   level_low_l<=host_level_low;
   level_high_l<=host_level_high;
`ifdef LINE_LEVELS
   level2_l<=level2;level2_low_l<=host_level2_low;level2_high_l<=host_level2_high;
`endif
`ifdef STREAM_CAPTURE
   stream_mode<=stream_cfg;
   stream_auto<=!stream_cfg[1] && !config_word[4];
   stream_rise_enable[0]<=!stream_cfg[1] && config_word[0] && config_word[4] && !config_word[5] && !config_word[6];
   stream_rise_enable[1]<=!stream_cfg[1] && config_word[0] && config_word[4] && !config_word[5] && config_word[6];
   stream_fall_enable[0]<=!stream_cfg[1] && config_word[0] && config_word[4] && config_word[5] && !config_word[6];
   stream_fall_enable[1]<=!stream_cfg[1] && config_word[0] && config_word[4] && config_word[5] && config_word[6];
`ifdef STREAM_DRUM
   mark_rise_enable<=stream_cfg[3] ? (config_word[6] ? 2'b10 : 2'b01) : 2'b00;
   mark_fall_enable<=stream_cfg[4] ? (config_word[6] ? 2'b10 : 2'b01) : 2'b00;
`endif
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
  // differs by one word from the continuous-clock path (origin probe).
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
`ifdef STREAM_DRUM
     // Stream mode: the drum takes the SRAM and the front end runs for it.
     if(stream_mode[0])begin drum_active<=1;frontend_run<=1;frontend_count<=1;end
     else begin
`endif
     force_trigger<=0;priming<=1;
     command<=1;command_read<=0;command_discard<=0;command_continue<=0;prefetch_valid<=0;
`ifdef STREAM_DRUM
     end
`endif
    end else command_error<=1;
    2,3,7:if(dispatch_read_ok) begin
     command<=1;command_read<=1;
     prefetch_valid<=dispatch_opcode!=3;
    end else command_error<=1;
    4:force_trigger<=1;
`ifdef STREAM_DRUM
    5:begin halt<=1;if(drum_active)begin drum_active<=0;frontend_run<=0;frontend_count<=0;end end
`else
    5:halt<=1;
`endif
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
`ifdef SAMPLE_BUS
   48:rd=16'h5503; // UART on the samples: 5-9 bits, parity, bit time 1/16 ns, resync
`elsif TRIG_UART
   48:rd=16'h5502; // UART 8N1, up to four bytes, 125 MHz receiver clock
`endif
`ifdef TRIG_SENT
   77:rd=16'h5e01; // SENT explicit tick, CRC-verified frame trigger
`endif
`ifdef TRIG_USB
   97:rd=16'h5501; // USB NRZI/PID with delayed EOP exclusion
`endif
`ifdef TRIG_MAN
   102:rd=16'h4d01; // Manchester full-frame phase selection and word predicates
`endif
`ifdef TRIG_MIL
   93:rd=16'h4d01; // MIL-STD-1553 complete 16-bit word with odd parity
`endif
`ifdef TRIG_SPI
`ifdef SAMPLE_BUS
   64:rd=16'h5303; // SPI on the samples: four modes, both bit orders, idle gap, data delay 58
`else
   64:rd=16'h5301; // SPI four modes, both bit orders, explicit idle gap
`endif
`endif
`ifdef SAMPLE_BUS
   53:rd=16'h4902; // I2C on the samples: as 0x4901, SDA delay 58
`elsif TRIG_I2C
   53:rd=16'h4901; // I2C seven-bit address/direction, up to four data bytes
`endif
`ifdef ENVELOPE_RECALL
   // Envelope recall: bucket words 43/44[3:0] (even), enable 44[15], Q8.8
   // 44[14], skip words 45 (even); 46 reads the buffer words written.
   43:rd=16'h454e;46:rd=env_words;
`endif
`ifdef SEQUENCE_ONLY
   // Decoder pattern registers are ignored; patterns need the sequence trigger.
   47:rd=16'h534f;
`endif
`ifdef SEQUENCE_TRIGGER
   // Sequence block: write 124 stores {kind[2:0]} with the staged value/mask
   // at the staged index; 119 reads the element capacity, 120 queue overflows.
   118:rd=16'h5351;119:rd=16'd32;120:rd=seq_overflows;
`endif
`ifdef PACKET_TRIGGER
   // Packet block: ARINC 429 tri-level words with a masked 32-bit predicate;
   // CAN / CAN FD and FlexRay validated frames with up to eight contiguous bytes.
   109:rd=16'h5001;
   110:rd=PACKET_PROTOCOLS;
`endif
`endif
`ifdef QUAL_TRIGGER
   // ADR-HARDWARE-QUALIFIERS: the qualifier block and its kinds {zone, video, slope, pulse}.
   74:rd=16'h5154;75:rd=16'h000f;
`ifdef SYMBOL_TRIGGER
   // ADR-SYMBOL-TRIGGER: the symbol block, its matches and its table flag.
   76:rd=16'h5359;78:rd=sym_count;98:rd={15'd0,sym_loaded};
`endif
`ifndef UART_TRIGGER
`ifdef ENVELOPE_RECALL
   43:rd=16'h454e;46:rd=env_words;
`endif
`endif
`endif
   0:rd=16'h5a52;
   31:rd=16'h5041;44:rd=panel_word;
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
`ifdef BOX_DECIM
   // the decimation is box averages, not the precision filter
   30:rd=16'h4258;
`elsif PICK_DECIM
   // the decimation picks every 2^n-th sample
   30:rd=16'h5053;
`elsif PICK_ALT
   // the precision filter, or every 2^n-th sample on asking (register 18 bit 5)
   30:rd=16'h5041;
`endif
`else
`ifdef PROTOCOL_SET
   // ADR-PROTOCOL-PACKET-IMAGE: the revision-10 record contract with raw
   // samples only; register 30 tells the host decimation is absent.
   13:rd=16'h000a;28:rd=0;29:rd=16'd8;30:rd=16'h4e44;
`else
   13:rd=16'h0008;
`endif
`endif
   25:rd=buffer_read64[16*burst_half+:16];
   26:rd=BUF_WORDS;
   27:rd=burst_index;
`ifdef STREAM_CAPTURE
   32:rd={7'b0,stream_enabled_cpu[2],stream_finished_cpu[2],(host_bank_overrun || packet_fault_cpu[2]),stream_single,stream_token,stream_available};
   33:rd=stream_count0;34:rd=stream_count1;
   35:rd=stream_first0[15:0];36:rd=stream_first0[31:16];37:rd=stream_first0[47:32];38:rd=stream_first0[63:48];
   39:rd=stream_first1[15:0];40:rd=stream_first1[31:16];41:rd=stream_first1[47:32];42:rd=stream_first1[63:48];
`ifdef STREAM_DRUM
   54:rd=16'h4d01;55:rd=mark_rise_count;56:rd=mark_rise_word[15:0];57:rd=mark_rise_word[31:16];
   58:rd=mark_fall_count;59:rd=mark_fall_word[15:0];60:rd=mark_fall_word[31:16];
`endif
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
`ifdef STACK_ENGINE
  if(stack_hit)rd=stack_rd;
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

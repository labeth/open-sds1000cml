`timescale 1ns/1ps
`include "lanemap_seed.vh"
// Integration probe: full external record, 512-word host recall buffer only.
// Default mode selects one ADC pair. INTERLEAVE reproduces the factory
// five-phase schedule at 500 MS/s/channel. See README for qualification and ABI.
module acq_sram_top(
 input clk,mclk_in,nCS1,nOE,nWE,input [6:2] sel,input gpmc_a2,gpmc_b1,
 inout [15:0] gpmc_d,inout [31:0] dq,input [79:0] lane,
 output k1,k2,g1,g2,d1,d2,f1,f2,j2,a11,output [4:0] enc_p,enc_n
);
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
`ifdef PRECISION
 reg [4:0] decim_log=0;
 wire [31:0] precision_word;wire precision_valid,precision_fault;
 (* preserve, dont_merge *) reg precision_enable=0;
 always @(posedge core)precision_enable<=frontend_run && decim_log!=0;
 adc_precision precision(core,halfclk,mclk_in,precision_enable,decim_log,il_raw_word,il_raw_valid,precision_word,precision_valid,precision_fault);
 wire [31:0] selected_word=decim_log==0 ? il_raw_word : precision_word;
 wire selected_valid=decim_log==0 ? il_raw_valid : precision_valid;
 wire [31:0] selected_trigger=decim_log==0 ? il_raw_word : {precision_word[31:24],precision_word[15:8],precision_word[31:24],precision_word[15:8]};
`ifdef STREAM_CAPTURE
 // Faults invalidate the whole record. One extra detection clock does not
 // turn an overflow into valid data and isolates the shutdown fanout.
 reg stream_fault=0;
 always @(posedge core)if(!frontend_run)stream_fault<=0;else stream_fault<=il_fault || precision_fault || streaming_fault;
`else
 wire stream_fault=il_fault || precision_fault;
`endif
`else
 wire [31:0] selected_word=il_raw_word,selected_trigger=il_raw_word;
 wire selected_valid=il_raw_valid,stream_fault=il_fault;
`endif
 // Keep source selection out of the threshold-comparator path. Data and
 // validity receive the same stage, preserving trigger/sample alignment.
 reg [31:0] source_word=0,trigger_word=0;reg source_word_valid=0;
 always @(posedge core)begin
  source_word<=selected_word;trigger_word<=selected_trigger;
  source_word_valid<=frontend_run && selected_valid;
 end
 reg [31:0] il_word1=0,il_word=0,il_word_stage=0;reg write_offer=0,il_fire_stage=0;reg il_valid1=0,il_valid=0,source_valid=0;reg [1:0] prev_low=0,prev_high=0,rise_ch=0,fall_ch=0;
 reg [1:0] first_low_ch=0,first_high_ch=0,second_low_ch=0,second_high_ch=0;
 wire il_crossing=!cfg[4] || force_trigger || (cfg[5]?fall_ch[cfg[6]]:rise_ch[cfg[6]]);
 genvar tc;generate for(tc=0;tc<2;tc=tc+1)begin:trigger_compare
  always @(posedge core)begin
   first_low_ch[tc]<=trigger_word[8*tc+:8]<level_l;first_high_ch[tc]<=trigger_word[8*tc+:8]>level_l;
   second_low_ch[tc]<=trigger_word[16+8*tc+:8]<level_l;second_high_ch[tc]<=trigger_word[16+8*tc+:8]>level_l;
   if(il_valid1)begin
    prev_low[tc]<=second_low_ch[tc];prev_high[tc]<=second_high_ch[tc];
    rise_ch[tc]<=(prev_low[tc] && !first_low_ch[tc]) || (first_low_ch[tc] && !second_low_ch[tc]);
    fall_ch[tc]<=(prev_high[tc] && !first_high_ch[tc]) || (first_high_ch[tc] && !second_high_ch[tc]);
   end
   if(!frontend_run)begin prev_low[tc]<=0;prev_high[tc]<=0;end
  end
 end endgenerate
 always @(posedge core)begin
  il_valid1<=source_word_valid;il_valid<=il_valid1;`ifdef PRECISION
  source_valid<=(cfg[0] || decim_log!=0) ? il_valid1 : 1'b1;
`else
  source_valid<=cfg[0] ? il_valid1 : 1'b1;
`endif
  il_word1<=source_word;il_word<=il_word1;il_word_stage<=il_word;
  write_offer<=source_valid && write_ready;
`ifdef STREAM_CAPTURE
  il_fire_stage<=force_trigger || stream_auto || (|(rise_ch & stream_rise_enable)) || (|(fall_ch & stream_fall_enable));
`else
  il_fire_stage<=cfg[0] ? il_crossing : (!cfg[4] || force_trigger);
`endif
  if(!frontend_run)begin il_valid1<=0;il_valid<=0;end
 end
 reg snap_request=0,snap_busy=0;wire snap_ack;
 reg [9:0] encode_enable=10'h3ff;
 adc_interleave frontend(.refclk(mclk_in),.memclk(core),.packclk(halfclk),.enable(frontend_run),.lane(lane),.encode_enable(encode_enable),
 .snapshot_request(snap_request),.snapshot_ack(snap_ack),.consume(frontend_run && write_ready),.word_data(il_raw_word),.valid(il_raw_valid),.fault(il_fault),.snapshot(cores),.enc_p(enc_p),.enc_n(enc_n),.locked(adc_locked));
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
 reg [15:0] config_word=0;reg [7:0] level=128;reg [BUF_AW-1:0] buffer_index=0;
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
  6:config_word<=wd;7:level<=wd[7:0];
  8:count_cfg[15:0]<=wd;9:count_cfg[19:16]<=wd[3:0];
`ifdef INTERLEAVE
  15:encode_enable<=wd[9:0];
`endif
`ifdef PRECISION
  18:decim_log<=wd[4:0];
`endif
`ifdef STREAM_CAPTURE
  19:stream_cfg<=wd[1:0];
`endif
  16:buffer_index<=wd[BUF_AW-1:0];
 endcase
 reg arm_geometry_ok=0,read_geometry_ok=0,buffer_count_ok=0;
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
  arm_geometry_ok<=decim_legal && post_cfg!=0 && ({1'b0,pre_cfg}+{1'b0,post_cfg})<=21'd524288 && config_word[3:1]<5;
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
 reg [15:0] cfg=0;reg [7:0] level_l=128;
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
 wire write_valid=(step && running && !halt) || (dummy_write && write_ready);
 wire [31:0] write_data=dummy_write ? 32'b0 : (cfg[0] ? capture_word : ramp);
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
 sram_record #(.CONFIG_VALIDATED(1)) record(.clk(core),.reset(1'b0),.arm(arm),.halt(halt),.pre_count(pre_cfg),.post_count(post_cfg),
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
  if(read_valid)begin
   read_half<=!read_half;
   if(!read_half)read_first<=read_data;
   else begin packet_data<={read_data,read_first};packet_addr<=received[BUF_AW-1:1];packet_toggle<=!packet_toggle;end
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
  if(record_done && !priming && !arm)frontend_run<=0;
  if(command && !command_read)frontend_run<=1;
  if(frontend_run && stream_fault)begin halt<=1;il_failed<=1;priming<=0;frontend_run<=0;end
  if(arm)il_failed<=0;
  if(snap_busy && snap_ack==snap_request)begin snapshot<=cores;snap_busy<=0;end
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
  if(arm)origin<=position+(decim_log!=0 ? 2'd3 : 2'd2);
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
   29:rd=decim_log!=0 ? 16'd16 : 16'd8;
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
 end
endmodule

`define INTERLEAVE
`define BENCH_MHZ 250
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
 wire core,sample_clk,locked;
`ifdef INTERLEAVE
 wire halfclk;bench_pll pll(mclk_in,core,sample_clk,locked,halfclk);
`else
 bench_pll pll(mclk_in,core,sample_clk,locked);
`endif
 assign g2=0;assign d1=0;assign d2=0;assign f1=1;assign f2=1;assign j2=1;assign a11=1;
 wire [79:0] cores;
`ifdef INTERLEAVE
 wire adc_locked,il_raw_valid,il_fault;reg il_failed=0;wire [31:0] il_raw_word;
 reg [31:0] il_word1=0,il_word=0,il_word_stage=0;reg write_offer=0,il_fire_stage=0;reg il_valid1=0,il_valid=0,source_valid=0;reg [1:0] prev_low=0,prev_high=0,rise_ch=0,fall_ch=0;
 reg [1:0] first_low_ch=0,first_high_ch=0,second_low_ch=0,second_high_ch=0;
 wire il_crossing=!cfg[4] || force_trigger || (cfg[5]?fall_ch[cfg[6]]:rise_ch[cfg[6]]);
 genvar tc;generate for(tc=0;tc<2;tc=tc+1)begin:trigger_compare
  always @(posedge core)begin
   first_low_ch[tc]<=il_raw_word[8*tc+:8]<level_l;first_high_ch[tc]<=il_raw_word[8*tc+:8]>level_l;
   second_low_ch[tc]<=il_raw_word[16+8*tc+:8]<level_l;second_high_ch[tc]<=il_raw_word[16+8*tc+:8]>level_l;
   if(il_valid1)begin
    prev_low[tc]<=second_low_ch[tc];prev_high[tc]<=second_high_ch[tc];
    rise_ch[tc]<=(prev_low[tc] && !first_low_ch[tc]) || (first_low_ch[tc] && !second_low_ch[tc]);
    fall_ch[tc]<=(prev_high[tc] && !first_high_ch[tc]) || (first_high_ch[tc] && !second_high_ch[tc]);
   end
   if(!running)begin prev_low[tc]<=0;prev_high[tc]<=0;end
  end
 end endgenerate
 always @(posedge core)begin
  il_valid1<=il_raw_valid;il_valid<=il_valid1;source_valid<=cfg[0] ? il_valid1 : 1'b1;
  il_word1<=il_raw_word;il_word<=il_word1;il_word_stage<=il_word;
  write_offer<=source_valid && write_ready;
  il_fire_stage<=cfg[0] ? il_crossing : (!cfg[4] || force_trigger);
  if(!running)begin il_valid1<=0;il_valid<=0;end
 end
 reg snap_request=0,snap_busy=0;wire snap_ack;
 reg [9:0] encode_enable=10'h3ff;
 adc_interleave frontend(.refclk(mclk_in),.memclk(core),.packclk(halfclk),.enable(running),.lane(lane),.encode_enable(encode_enable),
 .snapshot_request(snap_request),.snapshot_ack(snap_ack),.consume(running && write_ready),.word_data(il_raw_word),.valid(il_raw_valid),.fault(il_fault),.snapshot(cores),.enc_p(enc_p),.enc_n(enc_n),.locked(adc_locked));
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
 gpmc_slave busif(clk,nCS1,nOE,nWE,{sel,gpmc_a2,gpmc_b1},gpmc_d,wc,ws,wd,aux,rp,rs,rd,drive);
 reg request=0;reg [3:0] opcode=0;
 reg [19:0] pre_cfg=524271,post_cfg=17,count_cfg=512;
 reg [15:0] config_word=0;reg [7:0] level=128;reg [8:0] buffer_index=0;
 always @(posedge clk) if(wc) case(ws)
  1:begin opcode<=wd[3:0];request<=~request;end
  2:pre_cfg[15:0]<=wd;3:pre_cfg[19:16]<=wd[3:0];
  4:post_cfg[15:0]<=wd;5:post_cfg[19:16]<=wd[3:0];
  6:config_word<=wd;7:level<=wd[7:0];
  8:count_cfg[15:0]<=wd;9:count_cfg[19:16]<=wd[3:0];
`ifdef INTERLEAVE
  15:encode_enable<=wd[9:0];
`endif
  16:buffer_index<=wd[8:0];
 endcase
 reg arm_geometry_ok=0,read_geometry_ok=0,buffer_count_ok=0;
 always @(posedge core)begin
  arm_geometry_ok<=post_cfg!=0 && ({1'b0,pre_cfg}+{1'b0,post_cfg})<=21'd524288 && config_word[3:1]<5;
  read_geometry_ok<=count_cfg!=0 && count_cfg<=524288;
  buffer_count_ok<=count_cfg<=512;
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
 reg priming=0;reg [4:0] prime_left=0;
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
 wire write_valid=(step && running && !halt) || (priming && write_ready);
 wire [31:0] write_data=priming ? 32'b0 : (cfg[0] ? capture_word : ramp);
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
 sram_record #(.CONFIG_VALIDATED(1)) record(.clk(core),.reset(1'b0),.arm(arm),.halt(halt),.pre_count(pre_cfg),.post_count(post_cfg),
  .step(step),.trigger(fire),.running(running),.done(record_done),.triggered(triggered),.config_error(config_error),
  .write_addr(write_addr),.record_start(record_start),.record_length(record_length),.trigger_index(trigger_index),.filled(filled));
 reg [9:0] received=0;
`ifdef INTERLEAVE
 reg [63:0] buffer_mem64[0:255];reg [63:0] buffer_read64=0;reg buffer_half=0;
 wire [31:0] buffer_word=buffer_half ? buffer_read64[63:32] : buffer_read64[31:0];
 reg [31:0] read_first=0;reg read_half=0,packet_toggle=0,packet_seen=0;reg [63:0] packet_data=0;reg [7:0] packet_addr=0;
 always @(posedge clk)begin buffer_read64<=buffer_mem64[buffer_index[8:1]];buffer_half<=buffer_index[0];end
 always @(posedge core)begin
  if(read_valid)begin
   read_half<=!read_half;
   if(!read_half)read_first<=read_data;
   else begin packet_data<={read_data,read_first};packet_addr<=received[8:1];packet_toggle<=!packet_toggle;end
  end else if(read_half)begin packet_data<={32'b0,read_first};packet_addr<=(received-1'b1)>>1;packet_toggle<=!packet_toggle;read_half<=0;end
 end
 always @(posedge halfclk)if(packet_toggle!=packet_seen)begin buffer_mem64[packet_addr]<=packet_data;packet_seen<=packet_toggle;end
`else
 reg [31:0] buffer_mem[0:511];reg [31:0] buffer_word=0;
 always @(posedge clk) buffer_word<=buffer_mem[buffer_index];
`endif
 reg [79:0] snapshot=0;
 always @(posedge core) begin
  command<=0;arm<=0;halt<=0;command_count<=count_cfg;
  dispatch<=0;
  if(pending && !command && !arm && !dispatch)begin dispatch<=1;dispatch_opcode<=opcode;dispatch_arm_ok<=ready && !running && arm_geometry_ok;dispatch_read_ok<=ready && record_done && read_geometry_ok && (opcode==3 || buffer_count_ok) && (opcode!=7 || prefetch_valid);end
  config_idle<=!running && !priming;
  if(config_idle)begin cfg<=config_word;level_l<=level;end
  if(!priming)prime_left<=16;
  if(!running)begin ramp<=0;ramp_carry<=0;end
  else if(step && !halt)begin ramp[15:0]<=ramp[15:0]+1'b1;ramp[31:16]<=ramp[31:16]+ramp_carry;ramp_carry<=ramp[15:0]==16'hfffe;end
`ifdef INTERLEAVE
  if(running && il_fault)begin halt<=1;il_failed<=1;end
  if(arm)il_failed<=0;
  if(snap_busy && snap_ack==snap_request)begin snapshot<=cores;snap_busy<=0;end
`endif
  // Prime the write path before establishing the record origin, following
  // the successful benchmark. This alone did NOT resolve revision 1
  // hardware mismatches; read/write timing still needs qualification.
  if(priming && write_ready) begin
   prime_left<=prime_left-1'b1;
   if(prime_left==1) begin priming<=0;arm<=1;end
  end
  if(command)received<=0;
  if(arm)origin<=position+1'b1;
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
     command<=1;command_read<=1;command_discard<=dispatch_opcode==3;command_continue<=dispatch_opcode==7;
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
   12:rd={6'b0,received};
`ifdef INTERLEAVE
   13:rd=16'h0006;
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

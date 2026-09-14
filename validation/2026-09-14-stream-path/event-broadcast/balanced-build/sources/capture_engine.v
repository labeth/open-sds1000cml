// Select continuous acquisition or frozen recall over ONE host buffer and
// external SRAM transport. A mode is latched only on an accepted start and
// cannot change while active or while any producer still owns a host bank.
// Reset aborts the complete producer/host epoch; the board must coordinate
// transport quiescence. Finite capture itself remains the board's write client.
// committed/unread report the streaming producer; read_ordinal is mode-selected.
module sram_capture_engine #(parameter AW=19,CONTINUE_READS=1)(
 input wire reset,core_clk,ram_clk,host_clk,
 input wire start,finite_mode,stop,source_finished,
 input wire frozen,input wire [AW-1:0] record_start,read_bias,
 input wire [AW:0] record_words,offset,length,
 input wire source_valid,source_fault,input wire [35:0] source_data,
 output wire [AW:0] record_words_sampled,
 output wire source_ready,start_ready,source_enable,
 output wire active,done,capture_done,fault,request_error,
 output reg start_rejected=0,
 output wire selected_finite,
 output wire [3:0] error_code,output wire [63:0] committed,read_ordinal,
 output wire [AW:0] unread,output wire [1:0] bank_busy,
 input wire host_release,release_bank,release_token,
 output wire host_fault,output wire [1:0] host_ready,host_token,
 output wire [63:0] host_first0,host_first1,
 output wire [11:0] host_words0,host_words1,
 input wire read_enable,input wire [13:0] read_halfword,
 output wire read_valid,read_error,output wire [15:0] read_data,
 input wire transport_ready,transport_write_ready,transport_done,transport_read_valid,
 input wire [31:0] transport_read_data,input wire [AW-1:0] position,
 output wire command,command_read,command_discard,command_continue,write_valid,write_stop,
 output wire [AW:0] command_count,output wire [31:0] write_data
);
 wire sa,sd,sc,sf,sready,src_ready,sen,fa,fd,ff,fready,ferr;
 wire [3:0] se,fe;wire [63:0] sr,fr;
 wire [1:0] sbusy,fbusy,sbd,fbd,released;
 wire sv,sb,fv,fb;wire [31:0] sw,fw;wire [11:0] si,fi;
 wire [63:0] sfirst0,sfirst1,ffirst0,ffirst1;
 wire [AW:0] swords0,swords1,fwords0,fwords1;
 wire scmd,sread,sdiscard,scontinue,swvalid,swstop,fcmd,fread,fdiscard,fcontinue;
 wire [AW:0] scount,fcount;wire [31:0] swdata;
 wire host_core_fault;
 reg launch_stream=0,launch_finite=0;
 reg selected_finite_l=0;
 // Launch pulses already register acceptance. Bypass them during their one
 // pending clock, then retain the mode without a second acceptance-driven FF.
 assign selected_finite=launch_finite || (selected_finite_l && !launch_stream);
 wire launch_pending=launch_stream || launch_finite;
 // Capture settings unconditionally; the delayed launch consumes the values
 // sampled on the accepted-start edge. Avoid a wide accept-gated enable.
 reg [AW-1:0] record_start_q=0,read_bias_q=0;
 reg [AW:0] record_words_q=0,offset_q=0,length_q=0;
 assign record_words_sampled=record_words_q;
 always @(posedge core_clk)begin
  record_start_q<=record_start;read_bias_q<=read_bias;
  record_words_q<=record_words;offset_q<=offset;length_q<=length;
 end
 assign active=sa || fa || launch_pending;
 assign bank_busy=sbusy | fbusy;
 assign fault=sf || ff || host_core_fault;
 assign start_ready=!reset && !fault && !active && bank_busy==0 && transport_ready &&
                    (finite_mode ? fready : sready);
 wire accept_start=start && start_ready;
 always @(posedge core_clk)begin
  if(reset)begin selected_finite_l<=0;start_rejected<=0;launch_stream<=0;launch_finite<=0;end
  else begin
   start_rejected<=start && !start_ready;
   launch_stream<=accept_start && !finite_mode;launch_finite<=accept_start && finite_mode;
   if(launch_pending)selected_finite_l<=launch_finite;
  end
 end
 assign source_ready=!selected_finite && sa && src_ready;
 assign source_enable=!selected_finite && sen;
 assign done=!launch_pending && !fault && (selected_finite ? fd : sd) && bank_busy==0;
 assign capture_done=selected_finite ? frozen : (!launch_pending && sc);
 assign request_error=selected_finite && ferr;
 assign error_code=host_core_fault ? 4'hf : selected_finite ? fe : se;
 assign read_ordinal=selected_finite ? fr : sr;
 // Transport events and validated releases are broadcast. An inactive client
 // is idle (or failed) and ignores events; its bank mask is empty at handoff.
 // Mode still arbitrates commands, writes and host publication below.
 sram_stream_engine #(.AW(AW)) stream(
  .reset(reset),.core_clk(core_clk),.ram_clk(ram_clk),
  .start(launch_stream),.stop(stop),.source_finished(source_finished),.read_bias(read_bias_q),
  .source_valid(source_valid && !selected_finite && sa),.source_fault(source_fault && !selected_finite),.source_data(source_data),
  .source_ready(src_ready),.start_ready(sready),.source_enable(sen),
  .active(sa),.done(sd),.capture_done(sc),.fault(sf),.error_code(se),
  .committed(committed),.read_ordinal(sr),.unread(unread),.bank_busy(sbusy),
  .host_core_fault(host_core_fault),.bank_release(released),
  .word_valid(sv),.word_bank(sb),.word_data(sw),.word_index(si),.bank_done(sbd),
  .first0(sfirst0),.first1(sfirst1),.words0(swords0),.words1(swords1),
  .transport_ready(transport_ready),.transport_write_ready(transport_write_ready && !selected_finite),
  .transport_done(transport_done),.transport_read_valid(transport_read_valid),
  .transport_read_data(transport_read_data),.position(position),
  .command(scmd),.command_read(sread),.command_discard(sdiscard),.command_continue(scontinue),
  .command_count(scount),.write_valid(swvalid),.write_stop(swstop),.write_data(swdata));
 sram_finite_recall #(.AW(AW),.CONTINUE_READS(CONTINUE_READS)) finite(
  .clk(core_clk),.reset(reset),.start(launch_finite),.frozen(frozen),
  .record_start(record_start_q),.read_bias(read_bias_q),.record_words(record_words_q),.offset(offset_q),.length(length_q),
  .start_ready(fready),.active(fa),.done(fd),.request_error(ferr),.fault(ff),.error_code(fe),.recalled(fr),
  .host_core_fault(host_core_fault),.bank_release(released),
  .bank_busy(fbusy),.bank_done(fbd),.word_valid(fv),.word_bank(fb),.word_data(fw),.word_index(fi),
  .first0(ffirst0),.first1(ffirst1),.words0(fwords0),.words1(fwords1),
  .transport_ready(transport_ready),.transport_done(transport_done),
  .transport_read_valid(transport_read_valid),.transport_read_data(transport_read_data),.position(position),
  .command(fcmd),.command_read(fread),.command_discard(fdiscard),.command_continue(fcontinue),.command_count(fcount));
 assign command=selected_finite ? fcmd : scmd;
 assign command_read=selected_finite ? fread : sread;
 assign command_discard=selected_finite ? fdiscard : sdiscard;
 assign command_continue=selected_finite ? fcontinue : scontinue;
 assign command_count=selected_finite ? fcount : scount;
 assign write_valid=!selected_finite && swvalid;
 assign write_stop=selected_finite || swstop;
 assign write_data=swdata;
 wire [19:0] host_count0=selected_finite ? fwords0 : swords0;
 wire [19:0] host_count1=selected_finite ? fwords1 : swords1;
 sram_host_path host(
  .reset(reset),.core_clk(core_clk),.ram_clk(ram_clk),.host_clk(host_clk),
  .word_valid(selected_finite ? fv : sv),.word_bank(selected_finite ? fb : sb),
  .word_data(selected_finite ? fw : sw),.word_index(selected_finite ? fi : si),
  .bank_done(selected_finite ? fbd : sbd),
  .first0(selected_finite ? ffirst0 : sfirst0),.first1(selected_finite ? ffirst1 : sfirst1),
  .words0(host_count0),.words1(host_count1),.core_fault(host_core_fault),.host_fault(host_fault),.core_release(released),
  .host_release(host_release),.release_bank(release_bank),.release_token(release_token),
  .host_ready(host_ready),.host_token(host_token),.host_first0(host_first0),.host_first1(host_first1),
  .host_words0(host_words0),.host_words1(host_words1),
  .read_enable(read_enable),.read_halfword(read_halfword),.read_valid(read_valid),.read_error(read_error),.read_data(read_data));
endmodule

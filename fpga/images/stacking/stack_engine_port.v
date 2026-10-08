// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// GPMC register port for edge-locked FPGA stacking (ADR-STACKING-IMAGE-SPLIT).
// host_clk owns registers and the tile mailbox; engine_clk (125 MHz) runs the
// operation sequencer, hit detector, accumulator and retained-record cache;
// transport_clk (250 MHz) owns SRAM transport commands. The host holds all
// configuration stable from the operation write until the acknowledgement
// toggle returns; the engine side samples it only after a three-stage
// synchronizer, so these bundles cross without per-bit synchronizers.
// Mailbox words are written/read only while no operation is outstanding.
// Record metadata comes from the transport domain and is stable while frozen.
//
// Writes: 109 bit0 SRAM grant request (level); 110 mailbox index;
//  111 mailbox word (index++); 112 operation (1 scan, 2 peek, 3 tile read,
//  4 tile write, 5 reset); 113 level[7:0], hysteresis[15:8];
//  114 bit0 channel, bit1 falling, bits3:2 accumulate mask; 115/116 pre;
//  117/118 separation; 119 bins; 120/121 first bin; 122 factor;
//  123/124 initial hits; 125/126 first index; 127 {tile bin, channel}.
// Reads: 109 {engine owned, grant request}; 110 mailbox index; 111 mailbox
//  word (index++); 112 status; 113 capability 0x5342; 114 BINS;
//  115/116 accepted hits; 117/118 crossings; 119/120 peek pair;
//  121 bit0 power-of-two factors only; 122/123 rejected candidates;
//  124 template capacity (1024 samples).
// Template qualifier (ADR-STACKING-TEMPLATE-QUALIFIER) writes: 96 template
//  index; 97 template byte (index++); 98 bit0 qualify enable; 99 count;
//  100 stride; 101/102 pre (samples before the crossing); 103/104 threshold.
// TRLC-LINKS: REQ-SDS-141
module stack_engine_port #(parameter BINS=32,POW2_FACTOR=1)(
 input wire host_clk,engine_clk,transport_clk,
 input wire wc,input wire [7:0] ws,input wire [15:0] wd,input wire rp,input wire [7:0] rs,
 output reg [15:0] rd,output wire hit,
 output reg grant_request=0,input wire transport_owned,
 input wire frozen,input wire [31:0] record_id,input wire [18:0] record_start,input wire [19:0] record_words,
 input wire transport_ready,transport_done,transport_read_valid,input wire [31:0] transport_read_data,input wire [18:0] position,
 output wire command,command_read,command_discard,command_continue,output wire [19:0] command_count
);
 // Host domain.
 reg request=0;reg [3:0] opcode=0;reg [4:0] mail_index=0;
 reg [7:0] level=128,hysteresis=4;reg channel=0,falling=0;reg [1:0] mask=3;
 reg [31:0] pre=0,separation=0,first_bin=0,initial_hits=0,first_index=0;
 reg [15:0] bin_count=BINS,factor=1,tile=0;
 reg qualify_enable=0;reg [10:0] qualify_count=0;reg [7:0] qualify_stride=1;
 reg [31:0] qualify_pre=0,qualify_threshold=0;reg [9:0] template_index=0;
 (* ramstyle="M9K" *) reg [7:0] template_ram[0:1023];
 reg [7:0] template_q=0;wire [9:0] template_address;
 (* ramstyle="M9K" *) reg [15:0] upload_ram[0:31];
 (* ramstyle="M9K" *) reg [15:0] download_ram[0:31];
 reg [15:0] download_q=0;
 always @(posedge host_clk)begin
  download_q<=download_ram[mail_index];
  if(wc)case(ws)
   109:grant_request<=wd[0];
   110:mail_index<=wd[4:0];
   111:begin upload_ram[mail_index]<=wd;mail_index<=mail_index+1'b1;end
   112:begin opcode<=wd[3:0];request<=~request;end
   113:begin level<=wd[7:0];hysteresis<=wd[15:8];end
   114:begin channel<=wd[0];falling<=wd[1];mask<=wd[3:2];end
   115:pre[15:0]<=wd;116:pre[31:16]<=wd;
   117:separation[15:0]<=wd;118:separation[31:16]<=wd;
   119:bin_count<=wd;
   120:first_bin[15:0]<=wd;121:first_bin[31:16]<=wd;
   122:factor<=wd;
   123:initial_hits[15:0]<=wd;124:initial_hits[31:16]<=wd;
   125:first_index[15:0]<=wd;126:first_index[31:16]<=wd;
   127:tile<=wd;
   96:template_index<=wd[9:0];
   97:begin template_ram[template_index]<=wd[7:0];template_index<=template_index+1'b1;end
   98:qualify_enable<=wd[0];
   99:qualify_count<=wd[10:0];
   100:qualify_stride<=wd[7:0];
   101:qualify_pre[15:0]<=wd;102:qualify_pre[31:16]<=wd;
   103:qualify_threshold[15:0]<=wd;104:qualify_threshold[31:16]<=wd;
  endcase
  else if(rp && rs==111)mail_index<=mail_index+1'b1;
 end

 // Engine domain.
 (* async_reg="true" *) reg [2:0] request_sync=0;
 (* async_reg="true" *) reg [1:0] owned_sync=0,frozen_sync=0;
 reg acknowledge=0,op_error=0,engine_reset=1;
 // Separate copies of the reset keep its engine and cache fanout off the
 // sequencer's own control paths.
 // reset_sequencer tracks the engine's reset exactly, for the checks below.
 (* preserve, dont_merge *) reg reset_engine=1,reset_cache=1,reset_sequencer=1;
 always @(posedge engine_clk)begin reset_engine<=engine_reset;reset_cache<=engine_reset;reset_sequencer<=engine_reset;end
 reg [4:0] reset_count=31;
 localparam IDLE=0,RUN=1,COMMAND=2,DOWNLOAD=3,UPLOAD_ADDRESS=4,UPLOAD_WAIT=5,UPLOAD_SEND=6,COMPLETE=7,RESET=8,FINISH=9;
 reg [3:0] state=IDLE;
 reg [3:0] op=0;reg [4:0] word=0;
 reg start=0,peek=0,command_valid=0,command_write=0,upload_valid=0,completion_ready=0;
 reg [15:0] upload_data=0,upload_q=0;
 // Record metadata is stable while frozen; each operation latches it once so
 // no transport-clock path reaches the scanner or accumulator geometry.
 reg [31:0] record_id_l=0;reg [18:0] record_start_l=0;reg [19:0] record_words_l=0;
 wire start_ready,engine_busy,engine_done,engine_invalid,command_ready,upload_ready,download_valid;
 wire completion_valid,completion_error,host_busy,initialized;
 wire [15:0] download_data;
 wire [31:0] accepted_hits,crossings,peek_data,rejected;
 always @(posedge engine_clk)begin
  request_sync<={request_sync[1:0],request};
  owned_sync<={owned_sync[0],transport_owned};frozen_sync<={frozen_sync[0],frozen};
  upload_q<=upload_ram[word];
  template_q<=template_ram[template_address];
  if(state==DOWNLOAD && download_valid)download_ram[word]<=download_data;
 end
 always @(posedge engine_clk)begin
  start<=0;peek<=0;completion_ready<=0;
  if(reset_count!=0)reset_count<=reset_count-1'b1;
  else engine_reset<=0;
  case(state)
   IDLE:if(request_sync[2]!=acknowledge && !engine_reset && !reset_sequencer)begin
    op<=opcode;word<=0;op_error<=0;
    record_id_l<=record_id;record_start_l<=record_start;record_words_l<=record_words;
    case(opcode)
     1,2:if(start_ready && owned_sync[1] && frozen_sync[1])begin start<=opcode==1;peek<=opcode==2;state<=RUN;end
      else begin op_error<=1;state<=FINISH;end
     3,4:begin command_valid<=1;command_write<=opcode==4;state<=COMMAND;end
     5:begin engine_reset<=1;reset_count<=31;state<=RESET;end
     default:begin op_error<=1;state<=FINISH;end
    endcase
   end
   // Losing the grant or the frozen record mid-run stalls cache reads, so it
   // resets the engine: accumulated tile state is discarded and reported.
   RUN:if(engine_done)begin op_error<=engine_invalid;state<=FINISH;end
   else if(!owned_sync[1] || !frozen_sync[1])begin op_error<=1;engine_reset<=1;reset_count<=31;state<=RESET;end
   COMMAND:if(command_ready)begin command_valid<=0;state<=command_write ? UPLOAD_ADDRESS : DOWNLOAD;end
   DOWNLOAD:begin
    if(download_valid)word<=word+1'b1;
    if(completion_valid)state<=COMPLETE;
   end
   UPLOAD_ADDRESS:state<=UPLOAD_WAIT;
   UPLOAD_WAIT:begin upload_data<=upload_q;upload_valid<=1;state<=UPLOAD_SEND;end
   UPLOAD_SEND:if(upload_ready)begin
    upload_valid<=0;word<=word+1'b1;state<=word==19 ? COMPLETE : UPLOAD_ADDRESS;
   end
   COMPLETE:if(completion_valid)begin op_error<=completion_error;completion_ready<=1;state<=FINISH;end
   RESET:if(reset_count==0 && !engine_reset && !reset_sequencer && initialized)begin acknowledge<=request_sync[2];state<=IDLE;end
   FINISH:begin acknowledge<=request_sync[2];state<=IDLE;end
   default:state<=IDLE;
  endcase
 end
 wire request_valid,request_ready,response_valid,response_error,response_ready,cache_busy,cache_fault;
 wire [31:0] sample_index;wire [7:0] ch0_left,ch0_right,ch1_left,ch1_right;
 stack_edge_accumulator #(.BINS(BINS),.POW2_FACTOR(POW2_FACTOR)) engine(.clk(engine_clk),.reset(reset_engine),.start(start),.peek(peek),
 .start_ready(start_ready),.busy(engine_busy),.done(engine_done),.invalid(engine_invalid),
 .first_index(first_index),.record_samples({11'd0,record_words_l,1'b0}),.pre_samples(pre),.min_separation(separation),
 .channel(channel),.falling(falling),.level(level),.hysteresis(hysteresis),
 .bin_count({16'd0,bin_count}),.first_bin(first_bin),.factor({16'd0,factor}),.initial_hits(initial_hits),.channel_mask(mask),
 .accepted_hits(accepted_hits),.crossings(crossings),.peek_data(peek_data),
 .qualify_enable(qualify_enable),.qualify_count(qualify_count),.qualify_stride(qualify_stride),
 .qualify_pre(qualify_pre),.qualify_threshold(qualify_threshold),
 .template_address(template_address),.template_data(template_q),.rejected(rejected),
 .request_valid(request_valid),.request_ready(request_ready),.request_index(sample_index),
 .response_valid(response_valid),.response_error(response_error),.response_ready(response_ready),
 .ch0_left(ch0_left),.ch0_right(ch0_right),.ch1_left(ch1_left),.ch1_right(ch1_right),
 .command_valid(command_valid),.command_ready(command_ready),.command_write(command_write),
 .command_bin({17'd0,tile[15:1]}),.command_channel(tile[0]),
 .upload_valid(upload_valid),.upload_ready(upload_ready),.upload_data(upload_data),
 .download_valid(download_valid),.download_ready(state==DOWNLOAD),.download_data(download_data),
 .completion_valid(completion_valid),.completion_ready(completion_ready),.completion_error(completion_error),
 .host_busy(host_busy),.initialized(initialized));
 stack_record_cache #(.AW(19),.CACHE_AW(8)) cache(.clk(engine_clk),.transport_clk(transport_clk),.reset(reset_cache),
 .owned(owned_sync[1]),.frozen(frozen_sync[1]),.raw8(1'b1),.transport_owned(transport_owned),
 .epoch(32'd0),.record_id(record_id_l),.record_start(record_start_l),.read_bias(19'd0),.record_words(record_words_l),
 .request_valid(request_valid),.request_ready(request_ready),.sample_index(sample_index),.adjacent(1'b1),
 .response_valid(response_valid),.response_ready(response_ready),.response_error(response_error),
 .ch0_left(ch0_left),.ch0_right(ch0_right),.ch1_left(ch1_left),.ch1_right(ch1_right),.busy(cache_busy),.fault(cache_fault),
 .transport_ready(transport_ready),.transport_done(transport_done),.transport_read_valid(transport_read_valid),
 .transport_read_data(transport_read_data),.position(position),
 .command(command),.command_read(command_read),.command_discard(command_discard),.command_continue(command_continue),.command_count(command_count));

 // Status returns to the host domain. Results are stable once acknowledged.
 (* async_reg="true" *) reg [2:0] acknowledge_sync=0;
 (* async_reg="true" *) reg [1:0] status_sync0=0,status_sync1=0,status_sync2=0,status_sync3=0;
 always @(posedge host_clk)begin
  acknowledge_sync<={acknowledge_sync[1:0],acknowledge};
  status_sync0<={status_sync0[0],engine_invalid};status_sync1<={status_sync1[0],initialized};
  status_sync2<={status_sync2[0],owned_sync[1]};status_sync3<={status_sync3[0],cache_fault};
 end
 wire outstanding=acknowledge_sync[2]!=request;
 assign hit=rs>=109 && rs<=127;
 always @* begin
  rd=0;
  case(rs)
   109:rd={14'd0,status_sync2[1],grant_request};
   110:rd={11'd0,mail_index};
   111:rd=download_q;
   112:rd={8'd0,status_sync3[1],frozen_sync[1],status_sync2[1],status_sync1[1],status_sync0[1],op_error,acknowledge_sync[2],outstanding};
   113:rd=16'h5342;
   114:rd=BINS;
   115:rd=accepted_hits[15:0];116:rd=accepted_hits[31:16];
   117:rd=crossings[15:0];118:rd=crossings[31:16];
   119:rd=peek_data[15:0];120:rd=peek_data[31:16];
   121:rd={15'd0,POW2_FACTOR[0]};
   122:rd=rejected[15:0];123:rd=rejected[31:16];
   124:rd=16'd1024;
   default:rd=0;
  endcase
 end
endmodule

// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// ADR-STREAM-IMAGE: the external SRAM as the stream's FIFO ("drum").
//
// The SRAM has no address bus: an external counter, stepped by each K2 pulse,
// is the only pointer, shared by reads and writes and moving forward only
// (spec 12 §5). So the ring is served like a drum: the pointer sweeps forward
// and, as it passes the write head, the words gathered in an on-chip FIFO are
// written; as it passes the read head, a chunk is read into the host's next
// stream bank. Between the two it seeks with discard pulses (a whole lap is
// 2^19 pulses, 2.1 ms at 250 MHz). The SRAM's 512K words are the stream's
// slack, so a host that stalls for hundreds of milliseconds loses nothing.
//
// Burst rules from spec 12 §5.2: a write burst begins with 16 discarded words
// (so each burst is laid down as 16 junk words then its data, and a
// descriptor records the data's place and length); the first 16 words of a
// fresh read burst are unreliable (so a fresh read starts 16 words early,
// exactly over the burst's junk words); a word offered when the counter is P
// is stored at P+2. Each burst is at most a host bank and is read back whole
// by one fresh read; a gap after it keeps the next burst's read start ahead
// of the counter, so consecutive bursts are read with short seeks.
//
// Clocks: the control, the FIFO and the descriptor RAMs run on hclk (the
// PLL's 125 MHz half clock, in phase with clk): block RAM cannot run at
// 250 MHz (4.2 ns minimum period). Only thin register stages run on clk,
// moving words in pairs: the transport's read words are paired for hclk, and
// write pairs from hclk are offered a word at a time (the transport pulses K2
// only for a taken word, so a gap in a write burst is harmless, as in a
// decimated record). Everything is counted in pairs: bursts, chunks and junk
// are even, and a lone input word waits in the FIFO for its partner. The two
// clocks are related, so the crossings are plain timed paths: what one side
// reads of the other was registered at least a clock before.
//
// Each chunk fills exactly one host bank (at most 2^BANK_AW packets) and its
// last packet carries the seal; the next chunk waits until the next bank is
// free. A word that finds the input FIFO full raises fault (sticky): loss is
// never silent. All of this runs only while active; inactive, the transport
// is free.
// TRLC-LINKS: REQ-SDS-035
module stream_drum #(parameter AW=19,WF_AW=12,DESC_AW=8,BANK_AW=10,
 parameter RD_SKEW=1,                // read words arrive this many pulses late (board: 1)
 parameter SEG=8192,                 // longest seek between decisions (33 us)
 parameter WRITE_AT=512,             // FIFO words that ask for a write burst
 parameter WRITE_URGENT=3072,        // FIFO words at which writes pre-empt reading
 parameter AGE_CYCLES=2097152)(      // oldest buffered pair: 16.8 ms of hclk
 input clk,active,
 input in_valid,input [31:0] in_data,
 // sram_transport (CONTINUOUS_ONLY), clk
 output reg cmd=0,output reg cmd_read=0,output reg cmd_discard=0,output reg cmd_continue=0,
 output reg [AW:0] cmd_count=0,
 input t_ready,t_write_ready,t_done,
 output [31:0] wdata,output wvalid,output reg wstop=0,
 input [31:0] rdata,input rvalid,
 input [AW-1:0] position,
 output reg fault=0,
 // hclk: control, and stream_banks
 input hclk,
 input bank_ready,                   // stream_banks.cur_ready: the bank to fill is free and empty
 output reg packet_valid=0,output reg [63:0] packet_data=0,output reg packet_single=0,output reg packet_seal=0
);
 localparam [AW:0] RING=1<<AW;
 localparam CHUNK=2<<BANK_AW;        // words in a bank
 localparam PW=WF_AW-1;              // FIFO address width, in pairs

 // =================== clk: the transport's side ===================
 (* preserve *) reg act_c=0;
 // Input words to hclk: an 8-word register FIFO (the decimators can deliver
 // a few words clocks apart; hclk takes one a clock).
 reg [31:0] iq[0:7];reg [3:0] iq_wp=0;reg in_over=0;
 reg [3:0] iq_rp=0;                  // hclk
 // Commands: hclk toggles h_cmd_t with the fields set; the pulse follows
 // three clocks on (top registers the count a clock before it is used).
 reg h_cmd_t=0,h_read=0,h_discard=0,h_continue=0;reg [AW:0] h_count=0; // hclk
 reg cmd_t_q=0,cmd_seen=0,cmd_go=0;
 // Write pairs from hclk: a 2-entry register FIFO (a small fetch mux at
 // 250 MHz; a gap in a write burst costs only time).
 reg [63:0] wq[0:1];reg [2:0] wq_wp=0;reg h_wend=0; // hclk
 // The offer is all registers: write_ready, once up, falls only after our
 // wstop, so it is sampled (wr_q). A word is offered at most every other
 // clock (w_ph): fetching it (fp, a word index) is then a short step, and
 // half rate is still far beyond any stream's input.
 reg [2:0] wq_wp_c=0;reg [3:0] fp=0;
 reg w_ok=0,w_ph=0,wr_q=0;reg [31:0] w_data=0;
 wire [2:0] wq_rp=fp[3:1];           // pairs fetched: free for hclk
 assign wvalid=w_ok;
 assign wdata=w_data;
 wire f_av=wq_wp_c!=fp[3:1];
 wire [31:0] f_word=fp[0] ? wq[fp[1]][63:32] : wq[fp[1]][31:0];
 reg in_full=0;
 // Read words, paired for hclk: a 4-entry register FIFO.
 reg [63:0] rq[0:3];reg [2:0] rq_wp=0,rq_rp_c=0;reg [31:0] r_low=0,rd_q=0;reg r_half=0,r_over=0,rv_q=0;
 reg [3:0] iq_rp_c=0;
 reg [2:0] rq_rp=0;                  // hclk
 reg done_t=0;                       // toggles on each transport done
 reg h_fault=0;                      // hclk
 always @(posedge clk)begin
  act_c<=active;
  // Input.
  if(in_valid)iq[iq_wp[2:0]]<=in_data;
  if(!act_c)begin iq_wp<=0;in_over<=0;end
  else if(in_valid)begin
   if(in_full)in_over<=1; // hclk fell behind: say so
   else iq_wp<=iq_wp+1'b1;
  end
  // Commands.
  cmd_t_q<=h_cmd_t;cmd_seen<=cmd_t_q;cmd_go<=act_c && cmd_t_q!=cmd_seen;cmd<=cmd_go;
  cmd_read<=h_read;cmd_discard<=h_discard;cmd_continue<=h_continue;cmd_count<=h_count;
  // Write words: offered while one is held (w_ok is for the next clock).
  // hclk's pointers, registered here first (crossing paths stay short).
  wq_wp_c<=wq_wp;iq_rp_c<=iq_rp;rq_rp_c<=rq_rp;
  // A clock behind iq_wp, with room for a word arriving meanwhile.
  in_full<=iq_wp-iq_rp_c>=4'd6;
  wr_q<=t_write_ready;w_ph<=!w_ph;w_ok<=0;
  if(!w_ph)begin
   w_data<=f_word;
   if(f_av && wr_q && act_c)begin fp<=fp+1'b1;w_ok<=1;end
  end
  wstop<=act_c && h_wend && wq_wp_c==fp[3:1] && !fp[0] && !w_ok;
  if(!act_c)begin fp<=0;w_ok<=0;end
  // Read words: pair consecutive words (every read is an even count).
  // (registered first: read_valid has a wide fanout).
  rv_q<=rvalid;rd_q<=rdata;
  if(rv_q && r_half)rq[rq_wp[1:0]]<={rd_q,r_low};
  if(rv_q && !r_half)r_low<=rd_q;
  if(!act_c || cmd)begin r_half<=0;if(!act_c)begin rq_wp<=0;r_over<=0;end end
  else if(rv_q)begin
   r_half<=!r_half;
   if(r_half)begin rq_wp<=rq_wp+1'b1;if(rq_wp-rq_rp_c>=3'd3)r_over<=1;end
  end
  if(t_done)done_t<=!done_t;
  fault<=in_over || r_over || h_fault;
 end

 // =================== hclk: control ===================
 (* preserve *) reg act=0;
 reg done_q=0,done_seen=0,ready_q=0;reg [AW-1:0] pos_q=0;
 reg [2:0] wq_rp_h=0,rq_wp_h=0;reg [3:0] iq_wp_h=0; // clk's pointers, registered here first
 // Input FIFO, in pairs, read show-ahead (wf_q is the head pair).
 reg [63:0] wf[0:(1<<PW)-1];
 reg [PW:0] wf_wr=0,wf_rd=0,wf_level=0;
 reg [31:0] in_lo=0;reg in_have=0;
 reg wf_push=0;reg [63:0] wf_in=0;
 reg [63:0] wf_q=0;
 wire wf_pop;
 wire [PW:0] wf_rd_next=wf_rd+wf_pop;
 always @(posedge hclk)begin
  if(wf_push)wf[wf_wr[PW-1:0]]<=wf_in;
  wf_q<=wf[wf_rd_next[PW-1:0]];
 end
 reg [15:0] age=0;reg [7:0] age_pre=0;reg age_tick=0,age_clr=0; // age in 256-clock ticks
 // Descriptors.
 reg [AW-1:0] dsc_addr[0:(1<<DESC_AW)-1];reg [AW:0] dsc_len[0:(1<<DESC_AW)-1];
 reg [DESC_AW:0] dsc_wr=0,dsc_rd=0;
 wire dsc_empty=dsc_wr==dsc_rd;
 reg dsc_full=0;wire [DESC_AW:0] dsc_n=dsc_wr-dsc_rd;
 reg dsc_push=0;reg [AW-1:0] dsc_push_addr=0;reg [AW:0] dsc_push_len=0;
 reg [AW-1:0] dsc_r_addr=0,dsc_q_addr=0;reg [AW:0] dsc_r_len=0,dsc_q_len=0; // the head descriptor
 always @(posedge hclk)begin
  if(dsc_push)begin dsc_addr[dsc_wr[DESC_AW-1:0]]<=dsc_push_addr;dsc_len[dsc_wr[DESC_AW-1:0]]<=dsc_push_len;end
  dsc_r_addr<=dsc_addr[dsc_rd[DESC_AW-1:0]];dsc_r_len<=dsc_len[dsc_rd[DESC_AW-1:0]];
  dsc_q_addr<=dsc_r_addr;dsc_q_len<=dsc_r_len; // the RAM's output, registered (timing)
 end
 // Ring state. Each burst is laid down as 16 junk words, its data, and GAP
 // spare words, and is read back whole by one fresh read (at most a bank):
 // with the board's read skew a continued read loses a word (the previous
 // read's extra word is its first), so the drum never continues a read. The
 // gap keeps the next burst's read start ahead of where a read leaves the
 // counter, so reading burst after burst needs only a short seek.
 //
 // Reading back what was just written takes a lap (2.1 ms): the FIFO must
 // hold a lap of input, so the drum sustains streams up to about 2 MB/s
 // (decimation 2^9 and slower; at 2^8 a lap brings more than the FIFO).
 // Seeks go at most SEG pulses between decisions, so a bank freed meanwhile
 // is used at once.
 localparam GAP=4;
 reg [AW-1:0] whead=0;               // where the next burst's junk words go
 reg [AW:0] used=0;                  // ring words holding unread bursts (with junk and gap)
 reg [AW:0] rd_len=0;
 // Control.
 localparam S_IDLE=0,S_SEEK=1,S_ISS=3,S_WRITE=4,S_WDONE=5,S_READ=6,S_RDONE=7;
 localparam K_SEEK=0,K_WRITE=1,K_READ=2;
 reg [2:0] st=S_IDLE;reg [1:0] d_kind=0;reg [AW-1:0] seek_cnt=0;
 reg [PW:0] burst_p=0,push_left=0;reg [3:0] junk_left=0; // write burst, in pairs
 reg [AW:0] burst_span=0;            // its ring words: junk, data and gap (S_WRITE runs clocks)
 reg [AW-1:0] pairs_left=0;reg [3:0] drop_p=0;           // read, in pairs
 wire done_new=done_q!=done_seen;
 wire wq_room=(wq_wp-wq_rp_h)<3'd2;
 assign wf_pop=st==S_WRITE && junk_left==0 && push_left!=0 && wq_room;
 // Decision pipeline: the operands change only between transport
 // operations, so the drum decides after a few settled idle clocks.
 reg [AW-1:0] p1_w_target=0,p1_base=0;reg [AW:0] p1_free=0,p1_need=0;reg p1_lvl=0,p1_age=0,p1_urg=0;
 reg [AW-1:0] p2_dist_w=0,p2_dist_r=0;reg p2_wdue=0,p2_urg=0,p2_full=0;
 reg p1_full=0;
 reg p3_go=0;reg [1:0] p3_kind=0;reg [AW-1:0] p3_seek=0;
 reg [2:0] settle=0;
 wire can_read=!dsc_empty && bank_ready;
 always @(posedge hclk)begin
  done_q<=done_t;ready_q<=t_ready;pos_q<=position;
  wq_rp_h<=wq_rp;rq_wp_h<=rq_wp;iq_wp_h<=iq_wp;
  dsc_full<=dsc_n[DESC_AW];          // a sized difference: (1<<DESC_AW) would widen it
  p1_w_target<=whead-2'd2;
  p1_base<=dsc_q_addr-5'd16-RD_SKEW;
  p1_free<=RING-used;p1_need<={wf_level,1'b0}+7'd64;
  p1_lvl<=wf_level>=WRITE_AT/2;p1_full<=wf_level>=CHUNK/2;p1_age<=wf_level!=0 && age>=AGE_CYCLES/256;p1_urg<=wf_level>=WRITE_URGENT/2;
  p2_dist_w<=p1_w_target-pos_q;p2_dist_r<=p1_base-pos_q;
  p2_wdue<=(p1_lvl || p1_age) && !dsc_full && p1_free>p1_need;p2_urg<=p1_urg;p2_full<=p1_full;
  // The decision: a due write or a waiting read, the nearer first (reading
  // the bursts on the way to the write head while banks are free); a seek
  // goes at most SEG.
  p3_go<=1;p3_kind<=K_SEEK;
  p3_seek<=p2_dist_r>SEG ? SEG : p2_dist_r;
  // At the write head with the host not taking banks, a burst waits until
  // it is a full bank (or urgent): bursts, and so descriptors, stay large
  // while the backlog grows.
  if(p2_wdue && (!can_read || p2_dist_w<=p2_dist_r || p2_urg))begin
   if(p2_dist_w!=0)p3_seek<=p2_dist_w>SEG ? SEG : p2_dist_w;
   else if(bank_ready || p2_full || p2_urg)p3_kind<=K_WRITE;
   else p3_go<=0;
  end else if(can_read)begin if(p2_dist_r==0)p3_kind<=K_READ;end
  else p3_go<=0;
 end
 always @(posedge hclk)begin
  act<=active;
  dsc_push<=0;packet_valid<=0;packet_seal<=0;packet_single<=0;
  // Input words, from clk, paired into the FIFO.
  wf_push<=0;
  if(iq_rp!=iq_wp_h)begin
   iq_rp<=iq_rp+1'b1;
   if(in_have)begin wf_push<=1;wf_in<={iq[iq_rp[2:0]],in_lo};in_have<=0;end
   else begin in_lo<=iq[iq_rp[2:0]];in_have<=1;end
  end
  // wf_level runs a clock behind: "full" keeps two pairs spare.
  if(wf_push)begin
   if(wf_level>=(1<<PW)-2)h_fault<=1; // full: a pair is lost, and the host is told
   else wf_wr<=wf_wr+1'b1;
  end
  wf_rd<=wf_rd_next;
  wf_level<=wf_wr-wf_rd;
  burst_span<={burst_p,1'b0}+5'd16+GAP;
  age_pre<=age_pre+1'b1;age_tick<=&age_pre;age_clr<=0;
  if(wf_level==0 || age_clr)age<=0;else if(age_tick && !age[15])age<=age+1'b1;
  if(st==S_IDLE && ready_q && !done_new)begin if(settle!=7)settle<=settle+1'b1;end else settle<=0;
  // Pairs read from the ring, from clk (the read's odd extra word never
  // completes a pair).
  if(st==S_READ && rq_rp!=rq_wp_h)begin
   rq_rp<=rq_rp+1'b1;pairs_left<=pairs_left-1'b1;
   if(drop_p!=0)drop_p<=drop_p-1'b1;
   else begin packet_valid<=1;packet_data<=rq[rq_rp[1:0]];packet_seal<=pairs_left==1;end
  end
  if(!act)begin
   st<=S_IDLE;h_wend<=0;h_fault<=0;wf_wr<=0;wf_rd<=0;in_have<=0;iq_rp<=0;wq_wp<=0;rq_rp<=0;
   dsc_wr<=0;dsc_rd<=0;done_seen<=done_q;
   whead<=5'd16;used<=0;
  end else case(st)
   S_IDLE:if(settle==7 && p3_go)begin
    d_kind<=p3_kind;seek_cnt<=p3_seek;st<=S_ISS;
   end
   S_SEEK:if(done_new)begin done_seen<=done_q;st<=S_IDLE;end // and decide again
   S_ISS:begin                       // load the command and toggle it to clk
    h_cmd_t<=!h_cmd_t;h_continue<=0;h_read<=1;h_discard<=0;
    case(d_kind)
     K_SEEK:begin h_discard<=1;h_count<=seek_cnt;st<=S_SEEK;end
     K_WRITE:begin
      // At most a bank's worth: a burst is read back by one read.
      h_read<=0;h_count<=0;st<=S_WRITE;
      burst_p<=wf_level>CHUNK/2 ? CHUNK/2 : wf_level;push_left<=wf_level>CHUNK/2 ? CHUNK/2 : wf_level;junk_left<=8;
     end
     default:begin                   // K_READ: from 16 (+skew) words early, over the burst's junk
      rd_len<=dsc_q_len+5'd16+GAP;dsc_rd<=dsc_rd+1'b1;
      h_count<=dsc_q_len+5'd16+RD_SKEW;pairs_left<=(dsc_q_len+5'd16)>>1;drop_p<=8;st<=S_READ;
     end
    endcase
   end
   S_WRITE:begin
    // 8 junk pairs, then the burst, into clk's write FIFO.
    if(wq_room && (junk_left!=0 || push_left!=0))begin
     wq[wq_wp[0]]<=junk_left!=0 ? 64'd0 : wf_q;wq_wp<=wq_wp+1'b1;
     if(junk_left!=0)junk_left<=junk_left-1'b1;else push_left<=push_left-1'b1;
    end
    if(junk_left==0 && push_left==0)begin
     h_wend<=1;st<=S_WDONE;
     dsc_push<=1;dsc_push_addr<=whead+5'd16;dsc_push_len<={burst_p,1'b0};
     whead<=whead+burst_span[AW-1:0];used<=used+burst_span;age_clr<=1;
    end
   end
   S_WDONE:if(done_new)begin done_seen<=done_q;h_wend<=0;st<=S_IDLE;end
   S_READ:if(pairs_left==0)st<=S_RDONE;
   S_RDONE:if(done_new)begin done_seen<=done_q;used<=used-rd_len;st<=S_IDLE;end
  endcase
  if(dsc_push)dsc_wr<=dsc_wr+1'b1; // the RAM block writes at the old index this same clock
 end
endmodule

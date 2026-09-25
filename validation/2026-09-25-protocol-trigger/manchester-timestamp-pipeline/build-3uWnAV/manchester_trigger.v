// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Full-frame Manchester qualification. Raw ADC ordinals advance four samples
// per receiver tick; configuration is held for the enabled epoch. Decisions
// may follow wire completion, especially idle-symmetry ties. Only qualified
// END emits match; candidate words never trigger. Loss requires a new epoch.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018, REQ-SDS-039
module manchester_trigger #(parameter AW=7,EXTERNAL_RAM=0)(
 input wire clk,reset,enable,tick,line,inverted,ieee,msb,
 input wire [23:0] bit_ticks,input wire [4:0] word_bits,
 input wire [63:0] pattern,input wire [2:0] pattern_len,
 input wire [63:0] sample,
 output wire match,event_valid,overflow,
 output wire [7:0] event_kind,output wire [15:0] event_data,
 output wire [63:0] event_sample,
 output wire [1:0] scratch_write,
 output wire [AW:0] scratch_wr_a,scratch_wr_b,scratch_rd,
 output wire [32:0] scratch_data_a,scratch_data_b,
 input wire [32:0] scratch_q_a,scratch_q_b
);
 reg primed=0,previous=0,active=0,closing=0,valid_config=0,starting=0,edge_before=0;
 reg [63:0] edge_sample=0;reg [31:0] edge_gap=0;
 reg [31:0] gap=0,gap_limit=0;
 wire level=line^inverted;
 wire edge_seen=primed && previous!=level;
 wire start=enable && valid_config && tick && edge_seen && gap>gap_limit;
 wire finish=active && tick && gap>gap_limit;
 wire clear=reset || !enable || !valid_config;
 wire [1:0] candidates,errors,done,receiver_overflow;
 wire [31:0] words,cells;wire signed [19:0] score_a,score_b;
 wire [16:0] good_a,good_b;
 always @(posedge clk)begin
  valid_config<=bit_ticks>=8 && bit_ticks<=24'h3fffff && word_bits>=1 && word_bits<=16 && pattern_len<=4;
  gap_limit<=({8'd0,bit_ticks}<<1)+({8'd0,bit_ticks}>>1);
  closing<=finish;starting<=start;
  if(start)begin edge_before<=previous;edge_sample<=sample;edge_gap<=gap;end
  if(clear)begin primed<=0;previous<=0;gap<=0;active<=0;closing<=0;starting<=0;end
  else if(tick)begin
   primed<=1;previous<=level;
   if(edge_seen)gap<=1;else if(gap!=32'hffffffff)gap<=gap+1'b1;
   if(start)active<=1;else if(finish)active<=0;
  end
 end
 // A one-clock handoff lets a boundary edge finish the old hypothesis before
 // resetting for the new one. Compensate that clock in the quarter countdown.
 manchester_receive #(.START_DELAY(1)) receiver(.clk(clk),.reset(clear),.start(starting),.finish(finish),
  .tick(tick),.line(level),.previous_level(edge_before),.ieee(ieee),.msb(msb),
  .bit_ticks(bit_ticks),.word_bits(word_bits),.event_valid(candidates),.event_error(errors),
  .done(done),.overflow(receiver_overflow),.event_data(words),.event_cell(cells),
  .score_a(score_a),.score_b(score_b),.good_a(good_a),.good_b(good_b));
 manchester_frames #(.AW(AW),.EXTERNAL_RAM(EXTERNAL_RAM)) frames(.clk(clk),.reset(clear),.begin_frame(starting),.close_frame(closing),
  .gap(starting ? edge_gap : gap),.sample(starting ? edge_sample : sample),.bit_ticks(bit_ticks),.word_bits(word_bits),
  .candidate_valid(candidates),.candidate_error(errors),.candidate_data(words),.candidate_cell(cells),
  .receiver_overflow(receiver_overflow),.score_a(score_a),.score_b(score_b),.good_a(good_a),.good_b(good_b),
  .pattern(pattern),.pattern_len(pattern_len),.match(match),.event_valid(event_valid),
  .event_kind(event_kind),.event_data(event_data),.event_sample(event_sample),.overflow(overflow),
  .scratch_write(scratch_write),.scratch_wr_a(scratch_wr_a),.scratch_wr_b(scratch_wr_b),.scratch_rd(scratch_rd),
  .scratch_data_a(scratch_data_a),.scratch_data_b(scratch_data_b),.scratch_q_a(scratch_q_a),.scratch_q_b(scratch_q_b));
endmodule

// Double buffering preserves reception while a selected frame is published.
// Each phase has one dual-port RAM; words carry cell indices rather than
// duplicated 64-bit timestamps. Full timestamps are reconstructed on readout.
// begin/close may coincide, including a last candidate from the closing frame.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018, REQ-SDS-039
module manchester_frames #(parameter AW=7,EXTERNAL_RAM=0)(
 input wire clk,reset,begin_frame,close_frame,
 input wire [31:0] gap,input wire [63:0] sample,
 input wire [23:0] bit_ticks,input wire [4:0] word_bits,
 input wire [1:0] candidate_valid,candidate_error,receiver_overflow,
 input wire [31:0] candidate_data,candidate_cell,
 input wire signed [19:0] score_a,score_b,input wire [16:0] good_a,good_b,
 input wire [63:0] pattern,input wire [2:0] pattern_len,
 output reg match=0,event_valid=0,overflow=0,
 output reg [7:0] event_kind=0,output reg [15:0] event_data=0,
 output reg [63:0] event_sample=0,
 output wire [1:0] scratch_write,
 output wire [AW:0] scratch_wr_a,scratch_wr_b,scratch_rd,
 output wire [32:0] scratch_data_a,scratch_data_b,
 input wire [32:0] scratch_q_a,scratch_q_b
);
 localparam DEPTH=1<<AW,IDLE=0,START=1,READ=2,WAIT_RAM=3,TIMESTAMP=4,EMIT=5,END=6,
  OFFSET=7,START_LOW=8,PRODUCT=9,SAMPLE_LOW=10;
 wire [32:0] read_a,read_b;reg [32:0] entry=0;
 reg write_bank=1,read_bank=0,capturing=0;
 reg [1:0] busy=0,ready=0,chosen=0,accepted=0;
 reg [AW:0] count_a=0,count_b=0;
 reg bad_a=0,bad_b=0;
 reg [AW:0] sizes_a[0:1],sizes_b[0:1];
 reg [1:0] bank_bad_a=0,bank_bad_b=0;
 reg [63:0] anchors[0:1];
 reg [31:0] lead=0,tie_lead=0;
 reg tied=0,tie_bank=0;
 reg tie_accept_a=0,tie_accept_b=0;
 reg [3:0] state=IDLE;
 reg [AW:0] position=0;
 reg [63:0] history=0;
 reg [2:0] history_count=0;
 reg matched=0;
 reg [39:0] product=0;
 reg [27:0] product_low=0,product_high=0;
 reg sample_carry=0;reg [31:0] sample_high=0;
 wire [63:0] anchor=anchors[read_bank];
 wire [32:0] start_low={1'b0,anchor[31:0]}-
  (chosen[read_bank] ? {8'd0,bit_ticks,1'b0} : 33'd0);
 wire [32:0] data_low={1'b0,anchor[31:0]}+{1'b0,product[29:0],2'b0};
 // Configuration is held throughout the epoch. Register the rounded phase
 // offsets before replay, rather than cascading their arithmetic into a
 // 64-bit timestamp addition. Store offsets in receiver ticks (four samples).
 reg [23:0] phase_a_ticks=0,phase_b_ticks=0;
 wire [25:0] phase_a_numerator={2'b0,bit_ticks}+{1'b0,bit_ticks,1'b0}+26'd3;
 wire [25:0] phase_b_numerator={2'b0,bit_ticks}+26'd3;
 always @(posedge clk)begin
  phase_a_ticks<=phase_a_numerator[25:2];
  phase_b_ticks<=phase_b_numerator[25:2];
 end
 wire [AW:0] total=chosen[read_bank] ? sizes_b[read_bank] : sizes_a[read_bank];
 wire frame_bad=chosen[read_bank] ? bank_bad_b[read_bank] : bank_bad_a[read_bank];
 wire [AW:0] final_a=count_a+candidate_valid[0],final_b=count_b+candidate_valid[1];
 wire select_b=score_b>score_a;
 wire equal_score=score_a==score_b;
 wire [AW:0] read_position=position;
 wire [AW:0] write_address_a={write_bank,count_a[AW-1:0]};
 wire [AW:0] write_address_b={write_bank,count_b[AW-1:0]};
 wire [AW:0] read_address={read_bank,read_position[AW-1:0]};
 wire write_a=capturing && candidate_valid[0] && count_a<DEPTH && !overflow;
 wire write_b=capturing && candidate_valid[1] && count_b<DEPTH && !overflow;
 wire fail=(capturing && ((candidate_valid[0] && count_a==DEPTH) ||
    (candidate_valid[1] && count_b==DEPTH) || |receiver_overflow)) ||
    (begin_frame && (busy[!write_bank] || (capturing && !close_frame))) ||
    (close_frame && !capturing);
 wire [63:0] next_history={history[47:0],entry[15:0]};
 reg predicate;
 always @*case(pattern_len)
  0:predicate=1;
  1:predicate=next_history[15:0]==pattern[15:0];
  2:predicate=history_count>=1 && next_history[31:0]==pattern[31:0];
  3:predicate=history_count>=2 && next_history[47:0]==pattern[47:0];
  4:predicate=history_count>=3 && next_history==pattern;
  default:predicate=0;
 endcase
 assign scratch_write={!reset && write_b,!reset && write_a};
 assign scratch_wr_a=write_address_a;assign scratch_wr_b=write_address_b;assign scratch_rd=read_address;
 assign scratch_data_a={candidate_error[0],candidate_cell[15:0],candidate_data[15:0]};
 assign scratch_data_b={candidate_error[1],candidate_cell[31:16],candidate_data[31:16]};
 // Synchronous read is held through WAIT_RAM. Neither RAM is reset.
 generate if(EXTERNAL_RAM)begin:shared_memory
  assign read_a=scratch_q_a;assign read_b=scratch_q_b;
 end else begin:private_memory
  (* ramstyle="M9K" *) reg [32:0] phase_a[0:2*DEPTH-1],phase_b[0:2*DEPTH-1];
  reg [32:0] qa=0,qb=0;
  always @(posedge clk)begin
   qa<=phase_a[read_address];qb<=phase_b[read_address];
   if(scratch_write[0])phase_a[write_address_a]<=scratch_data_a;
   if(scratch_write[1])phase_b[write_address_b]<=scratch_data_b;
  end
  assign read_a=qa;assign read_b=qb;
 end endgenerate
 integer i;
 initial for(i=0;i<2;i=i+1)begin sizes_a[i]=0;sizes_b[i]=0;anchors[i]=0;end
 always @(posedge clk)begin
  match<=0;event_valid<=0;
  if(reset)begin
   write_bank<=1;read_bank<=0;capturing<=0;busy<=0;ready<=0;chosen<=0;accepted<=0;
   count_a<=0;count_b<=0;bad_a<=0;bad_b<=0;bank_bad_a<=0;bank_bad_b<=0;
   tied<=0;state<=IDLE;position<=0;history<=0;history_count<=0;matched<=0;overflow<=0;
  end else if(!overflow)begin
   if(fail)begin
    overflow<=1;event_valid<=1;event_kind<=5;event_data<=0;event_sample<=sample;
    state<=IDLE;busy<=0;ready<=0;capturing<=0;tied<=0;
   end else begin
    if(capturing)begin
     if(candidate_valid[0])begin count_a<=final_a;bad_a<=bad_a || candidate_error[0];end
     if(candidate_valid[1])begin count_b<=final_b;bad_b<=bad_b || candidate_error[1];end
    end
    if(tied && (begin_frame || gap>=tie_lead))begin
     chosen[tie_bank]<=gap<tie_lead;
     accepted[tie_bank]<=gap<tie_lead ? tie_accept_b : tie_accept_a;
     ready[tie_bank]<=1;tied<=0;
    end
    if(close_frame)begin
     capturing<=0;
     sizes_a[write_bank]<=final_a;sizes_b[write_bank]<=final_b;
     bank_bad_a[write_bank]<=bad_a || (candidate_valid[0] && candidate_error[0]);
     bank_bad_b[write_bank]<=bad_b || (candidate_valid[1] && candidate_error[1]);
     if(equal_score && score_a>0 && gap<lead && !begin_frame)begin
      tied<=1;tie_bank<=write_bank;tie_lead<=lead;
      tie_accept_a<=good_a>=word_bits;tie_accept_b<=good_b>=word_bits;
     end else begin
      chosen[write_bank]<=equal_score ? gap<lead : select_b;
      accepted[write_bank]<=equal_score ? score_a>0 && (gap<lead ? good_b : good_a)>=word_bits :
         (select_b ? score_b>0 && good_b>=word_bits : score_a>0 && good_a>=word_bits);
      ready[write_bank]<=1;
     end
    end
    if(begin_frame)begin
     write_bank<=!write_bank;busy[!write_bank]<=1;capturing<=1;
     count_a<=0;count_b<=0;bad_a<=0;bad_b<=0;
     anchors[!write_bank]<=sample;lead<=gap;
    end
    case(state)
     IDLE:if(ready[read_bank])begin
      if(accepted[read_bank] && total!=0)state<=START_LOW;
      else begin ready[read_bank]<=0;busy[read_bank]<=0;read_bank<=!read_bank;end
     end
     START_LOW:begin
      event_sample[31:0]<=start_low[31:0];sample_carry<=start_low[32];state<=START;
     end
     START:begin
      event_valid<=1;event_kind<=1;event_data<=0;
      event_sample[63:32]<=anchor[63:32]-sample_carry;
      position<=0;history<=0;history_count<=0;matched<=0;state<=READ;
     end
     READ:state<=WAIT_RAM;
     WAIT_RAM:begin entry<=chosen[read_bank] ? read_b : read_a;state<=TIMESTAMP;end
     // Two native-width products, then their sum, avoid a DSP cascade plus
     // full carry chain on one receiver clock. Replay takes seven clocks per
     // word, below the minimum supported eight clocks per received cell.
     TIMESTAMP:begin
      product_low<=entry[31:16]*bit_ticks[11:0];
      product_high<=entry[31:16]*bit_ticks[23:12];state<=PRODUCT;
     end
     PRODUCT:begin product<={product_high,12'd0}+product_low;state<=OFFSET;end
     OFFSET:begin
      product<=product+(chosen[read_bank] ? phase_b_ticks : phase_a_ticks);
      state<=SAMPLE_LOW;
     end
     SAMPLE_LOW:begin
      // Payload is meaningful only with event_valid. Assemble the low half
      // first, retaining carry for the high half published on the next clock.
      event_sample[31:0]<=data_low[31:0];sample_carry<=data_low[32];
      sample_high<=anchor[63:32]+{22'd0,product[39:30]};state<=EMIT;
     end
     EMIT:begin
      event_valid<=1;event_kind<=entry[32] ? 8'd4 : 8'd2;event_data<=entry[15:0];
      event_sample[63:32]<=sample_high+sample_carry;
      if(entry[32])begin history<=0;history_count<=0;end
      else begin
       history<=next_history;if(history_count<4)history_count<=history_count+1'b1;
       if(predicate && !frame_bad)matched<=1;
      end
      position<=position+1'b1;state<=position+1'b1==total ? END : READ;
     end
     END:begin
      // No other frame can publish between its last word and END. Holding the
      // event timestamp preserves that word's ordinal without a duplicate register.
      event_valid<=1;event_kind<=3;event_data<=frame_bad ? 16'd1 : 16'd0;
      match<=matched && !frame_bad;
      ready[read_bank]<=0;busy[read_bank]<=0;read_bank<=!read_bank;state<=IDLE;
     end
    endcase
   end
  end
 end
endmodule

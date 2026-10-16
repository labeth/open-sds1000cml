// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// UART receiver on the sample-domain edges of sample_edges.v (both
// directions): a falling crossing of the idle line starts a frame at its
// interpolated time t0, and bit k (0 the start bit) is the line's sample
// nearest t0 + (k + 1/2) bit times, the bit time in sixteenths of a
// nanosecond. With resync, every crossing inside the frame moves the next
// bit to half a bit time after it, so a baud error accumulates only over a
// run of equal bits. 5 to 9 data bits LSB first, no, even or odd parity, one
// stop bit checked; a bit time of at least 16 ns (62.5 MBd), so at most one
// bit is taken a receiver clock; what the clock's crossing gives on its own
// is worked out a clock ahead. A frame whose stop bit is high and whose
// parity holds publishes its data (kind 2) and the pattern match over its
// low eight bits, as uart_trigger.v; otherwise an error (kind 4). The app's
// Go model (internal/decode sample_bus.go SampleUART) is this receiver clock
// for clock. Configuration is held while enabled.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-229
module uart_sample(
 input wire clk,reset,enable,
 input wire live,found,rising,         // sample_edges.v: a crossing in the clock, its direction
 input wire signed [7:0] when,         // its time after the clock's first sample, 1/16 ns
 input wire [3:0] levels,              // the line's levels in the clock
 input wire inverted,resync,
 input wire [23:0] period,             // the bit time, 1/16 ns
 input wire [3:0] bits,
 input wire [1:0] parity,              // 0 none, 1 even, 2 odd
 input wire [31:0] pattern,input wire [2:0] pattern_len,
 output reg match=0,event_valid=0,
 output reg [7:0] event_kind=0,
 output reg [8:0] event_data=0
);
 // Held configuration and the sums the clock's choices take from it.
 reg valid_config=0;
 reg [3:0] last=9;                     // the stop bit's index
 reg signed [26:0] tm=0;               // a bit time less the clock
 reg signed [26:0] htm=0;              // half a bit time less the clock
 reg signed [26:0] hptm=0;             // a bit and a half less the clock
 reg h_small=0;reg signed [8:0] h_lim=0;// half a bit under 144: an edge before h_lim samples in its own clock
 reg h_near=0;reg signed [9:0] h_next=0;// half a bit under 272: an edge before h_next samples in the next clock
 always @(posedge clk)begin
  valid_config<=period>=24'd256 && bits>=4'd5 && bits<=4'd9 && parity<=2'd2 && pattern_len<=3'd4;
  last<=bits+4'd1+(parity!=0);
  tm<=$signed({3'd0,period})-27'sd128;
  htm<=$signed({4'd0,period[23:1]})-27'sd128;
  hptm<=$signed({3'd0,period})+$signed({4'd0,period[23:1]})-27'sd128;
  h_small<=period[23:1]<23'd144;
  h_lim<=9'sd112-$signed({1'b0,period[8:1]});
  h_near<=period[23:1]<23'd272;
  h_next<=10'sd240-$signed({1'b0,period[9:1]});
 end
 // The edge stage: what the clock's crossing gives on its own, a clock
 // before the receiver takes it (timing).
 reg found_s=0,fall_s=0,here_s=0,near_s=0,b_edge_s=0;
 reg signed [7:0] when_s=0;
 reg [3:0] levels_s=0;
 reg signed [26:0] n_edge=0,n_both=0;  // half a bit, and a bit and a half, after the crossing less the clock
 wire [7:0] edge_slot=when+period[8:1]+8'd16;            // when + half a bit + 16, low bits (half < 144)
 always @(posedge clk)begin
  found_s<=found && live;fall_s<=found && live && (rising==inverted);when_s<=when;levels_s<=levels;
  here_s<=h_small && $signed({when[7],when})<h_lim;
  near_s<=h_near && $signed({{2{when[7]}},when})<h_next;
  b_edge_s<=levels[edge_slot[6:5]]^inverted;
  n_edge<=$signed({{19{when[7]}},when})+htm;
  n_both<=$signed({{19{when[7]}},when})+hptm;
 end
 reg busy=0,pend=0,ones=0;
 reg signed [26:0] next=0;             // the pending bit's time after the clock's first sample
 reg [3:0] index=0;
 reg [8:0] value=0;
 reg [31:0] history=0;
 reg [2:0] count=0;
 // pend: the pending bit is in this clock (-16 <= next < 112), held from the
 // last clock so the choices below start from a register.
 wire signed [7:0] next8=next[7:0];
 wire edge_first=found_s && (!pend || when_s<=next8);
 // Edge first: does the edge (re)time the frame, and is its bit in this clock?
 wire starts=!busy && fall_s;
 wire times=starts || (busy && resync);
 wire edge_here=times && here_s;
 wire take_a=edge_first && (times ? edge_here : pend);
 wire [3:0] index_a=starts ? 4'd0 : index;
 // The bit and what it does.
 wire take=edge_first ? take_a : pend;
 wire [6:0] slot=next8[6:0]+7'd16;
 wire b=edge_first && times ? b_edge_s : levels_s[slot[6:5]]^inverted;
 wire [3:0] idx=edge_first ? index_a : index;
 wire is_stop=idx==last;
 wire done=take && (is_stop || (idx==0 && b));          // the frame ends here
 wire good=b && (parity==0 || ones==parity[1]);         // even: the data and parity ones even
 wire [31:0] next_history={history[23:0],value[7:0]};
 wire hit=pattern_len==0 ||
  (pattern_len==1 && next_history[7:0]==pattern[7:0]) ||
  (pattern_len==2 && count>=1 && next_history[15:0]==pattern[15:0]) ||
  (pattern_len==3 && count>=2 && next_history[23:0]==pattern[23:0]) ||
  (pattern_len==4 && count>=3 && next_history==pattern);
 // After a bit taken first, the edge acts on the state the bit left.
 wire busy_b=busy && !done;
 wire starts_b=found_s && !busy_b && fall_s;
 wire times_b=starts_b || (found_s && busy_b && resync);
 // The next clock's bit time, from four sums in parallel: a bit taken
 // (never in the next clock: a bit is at least two clocks), an edge's half
 // bit, both, or neither; and whether it falls in that clock.
 wire signed [26:0] n_wait=next-27'sd128,n_bit=next+tm;
 wire wait_here=next<27'sd240;
 reg signed [26:0] n_new;reg near_new;reg busy_new;
 always @*begin
  n_new=next;near_new=0;
  if(edge_first)begin
   busy_new=(busy || starts) && !done;
   if(times)begin n_new=take ? n_both : n_edge;near_new=!take && near_s;end
   else if(busy)begin n_new=take ? n_bit : n_wait;near_new=!take && wait_here;end
  end else begin
   busy_new=busy_b || starts_b;
   if(times_b)begin n_new=n_edge;near_new=near_s;end
   else if(take)n_new=n_bit;
   else if(busy)begin n_new=n_wait;near_new=wait_here;end
  end
 end
 always @(posedge clk)begin
  match<=0;event_valid<=0;
  if(reset || !enable || !valid_config || !live)begin
   busy<=0;pend<=0;next<=0;index<=0;value<=0;ones<=0;history<=0;count<=0;
  end else begin
   busy<=busy_new;pend<=busy_new && near_new;next<=n_new;
   if(edge_first && starts)begin value<=0;ones<=0;end
   if(take)begin
    index<=idx+4'd1;
    if(idx!=0 && idx<=bits)begin value[idx-4'd1]<=b;ones<=ones^b;end
    else if(idx!=0 && !is_stop)ones<=ones^b;
    if(is_stop)begin
     event_valid<=1;
     if(good)begin
      event_kind<=2;event_data<=value;match<=hit;
      history<=next_history;if(count<4)count<=count+1'b1;
     end else begin
      event_kind<=4;event_data<=0;history<=0;count<=0;
     end
    end
   end
   if(!edge_first && starts_b)begin index<=0;value<=0;ones<=0;end
   if(edge_first && starts)index<=take ? 4'd1 : 4'd0;
  end
 end
endmodule

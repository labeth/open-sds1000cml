// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Single-ended repository USB contract: SYNC, complemented PID, NRZI and
// stuffing. CRC bytes remain payload, as in the software decoder. Sixteen
// delayed raw cells let packet-end detection discard both EOP cells before
// destuffing. The queue preserves each sampled cell's ADC ordinal.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018, REQ-SDS-039
module usbls_trigger #(parameter EXTERNAL_RAM=0)(
 input wire clk,reset,enable,tick,line,inverted,
 input wire [23:0] bit_ticks_q8,input wire [31:0] pattern,
 input wire [2:0] pattern_len,input wire [63:0] sample,
 output reg match=0,event_valid=0,output reg [7:0] event_kind=0,event_data=0,
 output reg [15:0] event_count=0,output reg [63:0] event_sample=0,
 output wire scratch_write,output wire [3:0] scratch_wr,scratch_rd,
 output wire [63:0] scratch_data,input wire [63:0] scratch_q
);
 localparam IDLE=0,RECEIVE=1,FLUSH=2,FINISH=3;
 reg [1:0] state=IDLE,parse_stage=0;
 reg primed=0,previous=1,cell_previous=1,packet_valid=0,hit=0,framed=0;
 reg [23:0] remaining=0,cell_reload=0,half_reload=0;
 reg [2:0] history_count=0;
 reg [19:0] gap=0,idle_ticks=0;
 reg valid_config=0;
 reg [15:0] raw_count=0,processed=0,last_edge_cells=0,limit=0,bytes_count=0;
 reg [3:0] wr=0,rd=0;
 reg [15:0] cell_bits=0;wire [63:0] head_sample;
 reg [2:0] bit_index=0,ones=0;
 reg [7:0] byte_shift=0,pid=0;
 reg [31:0] window=0;
 wire level=line^inverted;
 wire edge_seen=primed && previous!=level;
 wire timeout=gap>=idle_ticks;
 wire sampling=tick && state==RECEIVE && !timeout && !edge_seen && remaining<256;
 wire pop=tick && ((sampling && raw_count>=16) || (state==FLUSH && processed<limit));
 wire [3:0] next_rd=pop ? rd+1'b1 : rd;
 assign scratch_write=!reset && enable && sampling;
 assign scratch_wr=wr;assign scratch_rd=next_rd;assign scratch_data=sample;
 generate if(EXTERNAL_RAM)begin:shared_memory
  assign head_sample=scratch_q;
 end else begin:private_memory
  (* ramstyle="M9K" *) reg [63:0] cells[0:15];reg [63:0] q=0;
  always @(posedge clk)begin
   q<=cells[next_rd];if(scratch_write)cells[wr]<=sample;
  end
  assign head_sample=q;
 end endgenerate
 reg decode_valid=0,decode_bit=0;reg [63:0] decode_sample=0;
 wire bit_value=decode_bit;
 wire [7:0] complete_byte={bit_value,byte_shift[7:1]};
 wire [31:0] next_window={window[23:0],complete_byte};
 reg byte_matches=0;
 always @*begin
  case(pattern_len)
   0:byte_matches=1;
   1:byte_matches=next_window[7:0]==pattern[7:0];
   2:byte_matches=history_count>=1 && next_window[15:0]==pattern[15:0];
   3:byte_matches=history_count>=2 && next_window[23:0]==pattern[23:0];
   4:byte_matches=history_count>=3 && next_window==pattern;
   default:byte_matches=0;
  endcase
 end
 always @(posedge clk)begin
  valid_config<=bit_ticks_q8>=24'd2048 && pattern_len<=4;
  // Fraction is stable between samples (at least eight clocks per cell).
  // Prepare the carry-heavy reload before the sample-decision cycle.
  cell_reload<={16'd0,remaining[7:0]}+bit_ticks_q8-256;
  half_reload<=(bit_ticks_q8>>1)-256;
  idle_ticks<=({4'd0,bit_ticks_q8}*10+255)>>8;
  decode_valid<=!reset && enable && valid_config && pop;
  // Logic cells have an asynchronous read; only the timestamp RAM needs lookahead.
  decode_bit<=cell_bits[rd];decode_sample<=head_sample;
  if(scratch_write)cell_bits[wr]<=level==cell_previous;
 end
 always @(posedge clk)begin
  match<=0;event_valid<=0;
  // Payload is meaningful only with event_valid; avoid a wide gated hold mux.
  event_sample<=(state==FINISH || (state==RECEIVE && raw_count==16'hffff)) ? sample : decode_sample;
  if(reset || !enable || !valid_config)begin
   state<=IDLE;primed<=0;previous<=1;cell_previous<=1;gap<=0;framed<=0;
   raw_count<=0;processed<=0;last_edge_cells<=0;limit<=0;wr<=0;rd<=0;
   parse_stage<=0;bit_index<=0;ones<=0;byte_shift<=0;pid<=0;
   bytes_count<=0;history_count<=0;window<=0;hit<=0;packet_valid<=0;remaining<=0;
  end else if(tick)begin
   primed<=1;previous<=level;
   if(edge_seen)gap<=0;else if(gap!=20'hfffff)gap<=gap+1'b1;
   if(timeout)framed<=1;
   case(state)
    IDLE:if(edge_seen && framed)begin
     state<=RECEIVE;remaining<=half_reload;cell_previous<=previous;
     raw_count<=0;processed<=0;last_edge_cells<=0;wr<=0;rd<=0;
     parse_stage<=0;bit_index<=0;ones<=0;byte_shift<=0;pid<=0;
     bytes_count<=0;history_count<=0;window<=0;hit<=0;packet_valid<=0;
    end
    RECEIVE:begin
     // Recenter on each NRZI transition: stuffing bounds the open-loop
     // interval to seven cells even when clocks have fractional error.
     if(edge_seen)begin last_edge_cells<=raw_count;remaining<=half_reload;end
     else if(timeout)begin
      if(last_edge_cells>=18)begin limit<=last_edge_cells-2;state<=FLUSH;end
      else begin state<=IDLE;packet_valid<=0;end
     end else if(sampling)begin
      if(raw_count==16'hffff)begin
       state<=IDLE;packet_valid<=0;parse_stage<=3;framed<=0;event_valid<=1;event_kind<=4;event_data<=pid;
       event_count<=bytes_count;
      end else begin
       remaining<=cell_reload;
       raw_count<=raw_count+1'b1;wr<=wr+1'b1;cell_previous<=level;
      end
     end else remaining<=remaining-256;
    end
    FLUSH:if(processed>=limit)state<=FINISH;
    FINISH:begin
     state<=IDLE;
     if(packet_valid)begin
      event_valid<=1;event_kind<=3;event_data<=pid;event_count<=bytes_count;
      match<=hit;
     end
    end
   endcase
   if(pop)begin rd<=rd+1'b1;processed<=processed+1'b1;end
   if(decode_valid)begin
    if(parse_stage!=3)begin
     if(ones==6)begin
      ones<=0;
      if(bit_value)begin
       parse_stage<=3;packet_valid<=0;
       if(packet_valid)begin
        event_valid<=1;event_kind<=4;event_data<=pid;event_count<=bytes_count;
       end
      end
     end else begin
      ones<=bit_value ? ones+1'b1 : 3'd0;
      byte_shift<=complete_byte;bit_index<=bit_index+1'b1;
      if(bit_index==7)begin
       case(parse_stage)
        0:parse_stage<=complete_byte==8'h80 ? 1 : 3;
        1:begin
         pid<=complete_byte;event_valid<=1;event_data<=complete_byte;event_count<=0;
         if(complete_byte[7:4]==~complete_byte[3:0])begin
          parse_stage<=2;packet_valid<=1;hit<=0;event_kind<=1;
         end else begin parse_stage<=3;event_kind<=4;end
        end
        2:begin
         window<=next_window;bytes_count<=bytes_count+1'b1;hit<=hit || byte_matches;
         if(history_count<4)history_count<=history_count+1'b1;
         event_valid<=1;event_kind<=2;event_data<=complete_byte;event_count<=bytes_count;
        end
       endcase
      end
     end
    end
   end
  end
 end
endmodule

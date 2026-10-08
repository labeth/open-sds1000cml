// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// SENT fast-channel framing, using an explicit nominal tick at 125 MHz.
// DATA includes status and data nibbles; END validates the trailing CRC.
// A payload hit is provisional until that CRC passes. Count is nibble index.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
module sent_trigger(
 input wire clk,reset,enable,tick,line,inverted,pause_pulse,
 input wire [23:0] tick_ticks,input wire [6:0] nibbles,
 input wire [31:0] pattern,input wire [2:0] pattern_len,
 output reg match=0,event_valid=0,output reg [7:0] event_kind=0,event_data=0,
 output reg [6:0] event_index=0
);
 reg primed=0,previous=1,have_edge=0,active=0,skip_pause=0,pending_start=0;
 reg [23:0] phase=0;reg [6:0] whole=0,index=0;
 reg [3:0] crc=5;
 reg [31:0] history=0;reg [2:0] count=0;reg matched=0;
 wire level=line^inverted;
 wire falling=primed && previous && !level;
 reg [23:0] tick_last=0;
 always @(posedge clk)tick_last<=tick_ticks-1'b1;
 wire rollover=phase==tick_last;
 reg [7:0] rounded=0;reg edge_pending=0;
 wire sync_pulse=rounded>=45 && rounded<=67;
 wire nibble_ok=rounded>=12 && rounded<=27;
 wire [3:0] nibble=rounded-8'd12;
 wire [31:0] next_history={history[23:0],4'd0,nibble};
 wire hit=pattern_len==0 ||
  (pattern_len==1 && next_history[7:0]==pattern[7:0]) ||
  (pattern_len==2 && count>=1 && next_history[15:0]==pattern[15:0]) ||
  (pattern_len==3 && count>=2 && next_history[23:0]==pattern[23:0]) ||
  (pattern_len==4 && count>=3 && next_history==pattern);
 // Same augmented CRC as DecodeSENT: data only, seed 5, final table step.
 function [3:0] crc_table(input [3:0] v);
  begin case(v)
   0:crc_table=0;1:crc_table=13;2:crc_table=7;3:crc_table=10;
   4:crc_table=14;5:crc_table=3;6:crc_table=9;7:crc_table=4;
   8:crc_table=1;9:crc_table=12;10:crc_table=6;11:crc_table=11;
   12:crc_table=15;13:crc_table=2;14:crc_table=8;15:crc_table=5;
  endcase end
 endfunction
 always @(posedge clk)begin
  match<=0;event_valid<=0;
  if(reset || !enable || tick_ticks<4 || nibbles<1 || nibbles>64 || pattern_len>4)begin
   primed<=0;previous<=1;have_edge<=0;active<=0;skip_pause<=0;pending_start<=0;
   phase<=0;whole<=0;edge_pending<=0;rounded<=0;index<=0;crc<=5;history<=0;count<=0;matched<=0;
  end else if(tick)begin
   primed<=1;previous<=level;edge_pending<=falling && have_edge;
   if(falling)rounded<={1'b0,whole}+(rollover ? 8'd1:8'd0);
   if(rollover)begin phase<=0;if(whole<127)whole<=whole+1'b1;end
   else phase<=phase+1'b1;
   if(pending_start)begin
    pending_start<=0;event_valid<=1;event_kind<=1;event_data<=0;event_index<=0;
   end
   if(active && whole>=68)begin
    active<=0;event_valid<=1;event_kind<=4;event_data<=0;event_index<=index;
   end
   if(falling)begin
    have_edge<=1;phase<=tick_ticks>>1;whole<=0;
   end
   if(edge_pending)begin
     if(skip_pause && !sync_pulse)skip_pause<=0;
     else if(sync_pulse)begin
      skip_pause<=0;active<=1;index<=0;crc<=5;history<=0;count<=0;matched<=0;
      event_valid<=1;event_kind<=active ? 8'd4:8'd1;event_data<=0;event_index<=0;
      pending_start<=active;
     end else if(active)begin
      event_valid<=1;event_data<={4'd0,nibble};event_index<=index;
      if(!nibble_ok)begin active<=0;event_kind<=4;end
      else if(index==nibbles-1'b1)begin
       active<=0;skip_pause<=pause_pulse;
       event_kind<=nibble==crc_table(crc) ? 8'd3:8'd4;
       match<=nibble==crc_table(crc) && (matched || pattern_len==0);
      end else begin
       event_kind<=2;index<=index+1'b1;
       history<=next_history;if(count<4)count<=count+1'b1;
       if(hit)matched<=1;
       if(index!=0)crc<=crc_table(crc)^nibble;
      end
     end
   end
  end
 end
endmodule

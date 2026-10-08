// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Repository MIL-STD-1553 contract: two-sided sync hold, sixteen Thomas-
// Manchester bits, then odd parity. One DATA/ERROR event per complete word.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
module mil1553_trigger(
 input wire clk,reset,enable,tick,line,inverted,
 input wire [23:0] bit_ticks,input wire [15:0] pattern,input wire match_any,
 output reg match=0,event_valid=0,output reg [7:0] event_kind=0,
 output reg [15:0] event_data=0,output reg event_command=0
);
 localparam IDLE=0,CONFIRM=1,FIRST=2,SECOND=3;
 reg [1:0] state=IDLE;
 reg primed=0,previous=0,first_level=0,parity=0,command_word=0;
 reg [23:0] gap=0,remaining=0;
 reg [4:0] index=0;reg [15:0] word_data=0;
 reg valid_config=0;
 reg [23:0] sync_low=0,sync_high=0,confirm_reload=0,first_reload=0,second_reload=0;
 wire level=line^inverted;
 wire edge_seen=primed && previous!=level;
 always @(posedge clk)begin
  valid_config<=bit_ticks>=8 && bit_ticks<=24'h3fffff;
  sync_low<=bit_ticks+(bit_ticks>>2);
  sync_high<=(bit_ticks<<1)+(bit_ticks>>1);
  confirm_reload<=bit_ticks+(bit_ticks>>2)-1'b1;
  first_reload<=(bit_ticks>>1)-1'b1;
  second_reload<=bit_ticks-(bit_ticks>>1)-1'b1;
 end
 always @(posedge clk)begin
  match<=0;event_valid<=0;
  if(reset || !enable || !valid_config)begin
   state<=IDLE;primed<=0;previous<=0;gap<=0;remaining<=0;
   index<=0;word_data<=0;parity<=0;first_level<=0;command_word<=0;
  end else if(tick)begin
   primed<=1;previous<=level;
   if(edge_seen)gap<=1;else if(gap!=24'hffffff)gap<=gap+1'b1;
   if(remaining!=0)remaining<=remaining-1'b1;else remaining<=first_reload;
   case(state)
    IDLE:begin
     // Prepare word state throughout idle; keep the wide sync comparison
     // off the reset-enable paths of every payload and parity register.
     index<=0;word_data<=0;parity<=0;
     if(edge_seen && gap>=sync_low && gap<=sync_high)begin
      state<=CONFIRM;remaining<=confirm_reload;command_word<=previous;
     end
    end
    CONFIRM:begin
     if(remaining==0)begin state<=FIRST;end
     else if(edge_seen)state<=IDLE;
    end
    FIRST:if(remaining==0)begin
     first_level<=level;remaining<=second_reload;state<=SECOND;
    end
    SECOND:if(remaining==0)begin
     if(first_level==level)begin
      state<=IDLE;event_valid<=1;event_kind<=4;event_data<=word_data;event_command<=command_word;
     end else if(index==16)begin
      state<=IDLE;event_valid<=1;event_data<=word_data;event_command<=command_word;
      event_kind<=(parity^first_level) ? 8'd2 : 8'd4;
      match<=(parity^first_level) && (match_any || word_data==pattern);
     end else begin
      word_data<={word_data[14:0],first_level};parity<=parity^first_level;
      index<=index+1'b1;state<=FIRST;
     end
    end
   endcase
  end
 end
endmodule

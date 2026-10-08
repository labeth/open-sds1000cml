// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Event-sequence trigger (ADR-PROTOCOL-SEQUENCE-TRIGGER). Matches up to 32
// contiguous decoder events; element k is {kind (0 any), value, mask} and
// matches when the kinds agree and ((event ^ value) & mask) == 0. A shift-and
// state vector holds every partial match: for each event, elements are read
// from the pattern RAM one per clock from the last to the first, so
// state[k] <= state[k-1] & element k matches, with state[-1] = 1.
// With qualify, a hit waits for the frame's END (not a bad END when
// end_bad_value flags a nonzero END value) and is dropped by ERROR, START or
// LOSS. Events queue in a 256-entry RAM; an event arriving at a full queue is
// dropped, counted, and clears all partial matches so none spans the gap.
// Configuration is held while enabled; the pattern RAM is written by the host
// clock only while the matcher is disabled.
// TRLC-LINKS: REQ-SDS-013, REQ-SDS-018
module event_sequence(
 input wire clk,reset,
 input wire [5:0] length,input wire qualify,end_bad_value,
 input wire in_valid,input wire [7:0] in_kind,input wire [31:0] in_value,
 input wire wr_clk,wr_en,input wire [4:0] wr_addr,input wire [66:0] wr_data,
 output reg match=0,output reg [15:0] overflows=0
);
 localparam IDLE=2'd0,LOAD=2'd1,SCAN=2'd2,DONE=2'd3;
 wire enabled=!reset && length!=0 && length<=32;
 // Pattern: {kind[2:0], mask[31:0], value[31:0]}.
 (* ramstyle="M9K" *) reg [66:0] elements[0:31];
 reg [66:0] element=0;
 reg [4:0] element_addr=0;
 always @(posedge wr_clk)if(wr_en)elements[wr_addr]<=wr_data;
 always @(posedge clk)element<=elements[element_addr];
 // Event queue: {kind[2:0], value[31:0]}.
 (* ramstyle="M9K" *) reg [34:0] queue[0:255];
 reg [34:0] head=0;
 reg [7:0] wr=0,rd=0;reg [8:0] count=0;
 wire full=count==9'd256;
 wire push=enabled && in_valid && !full;
 reg pop=0;
 always @(posedge clk)begin
  if(push)queue[wr]<=({in_kind[2:0],in_value});
  head<=queue[rd];
 end
 reg [1:0] state=IDLE;
 reg [31:0] partial=0;
 reg [4:0] k=0;
 reg [2:0] kind=0;reg [31:0] value=0;
 reg pending=0;
 wire [2:0] element_kind=element[66:64];
 wire element_match=(element_kind==0 || element_kind==kind) && ((value^element[31:0])&element[63:32])==0;
 wire previous=k==0 ? 1'b1 : partial[k-1'b1];
 wire [4:0] last=length[4:0]-1'b1;
 always @(posedge clk)begin
  match<=0;pop<=0;
  if(!enabled)begin
   state<=IDLE;wr<=0;rd<=0;count<=0;partial<=0;pending<=0;
  end else begin
   if(in_valid && full)begin
    // A dropped event breaks contiguity: no partial or pending match survives.
    if(overflows!=16'hffff)overflows<=overflows+1'b1;
    partial<=0;pending<=0;
   end
   if(push)wr<=wr+1'b1;
   count<=count+push-(pop ? 1'b1 : 1'b0);
   case(state)
    // The queue RAM presents head one clock after rd settles.
    IDLE:if(count!=0 && !pop)begin state<=LOAD;element_addr<=last;end
    LOAD:begin
     kind<=head[34:32];value<=head[31:0];rd<=rd+1'b1;pop<=1;
     k<=last;element_addr<=last==0 ? 5'd0 : last-1'b1;state<=SCAN;
    end
    SCAN:begin
     // element holds element k (address issued the previous clock).
     if(!(in_valid && full))partial[k]<=previous && element_match;
     if(k==0)state<=DONE;
     else begin k<=k-1'b1;element_addr<=k<2 ? 5'd0 : k-5'd2;end
    end
    DONE:begin
     state<=IDLE;
     if(!(in_valid && full))begin
      if(pending)begin
       if(kind==3'd3)begin pending<=0;match<=!(end_bad_value && value!=0);end
       else if(kind==3'd4 || kind==3'd1 || kind==3'd5)pending<=0;
      end
      if(partial[last])begin
       if(qualify)pending<=1;else match<=1;
      end
     end
    end
   endcase
  end
  if(reset)overflows<=0;
 end
endmodule

// Three dual-channel CIC3 stages after the fixed /16 and configurable /1..16
// stages. remaining_log=0 bypasses; 1..12 selects the same successive /2..16
// stages as cic_stage. Hold configuration throughout enable. At nonzero log,
// the intended input rate is <=1.953125 Mwords/s at clk=100 MHz. A four-word
// queue absorbs uneven execution latency; overload is sticky until !enable.
// One synchronous state RAM and one 28-bit add/subtract datapath serve all
// six channel/stage contexts. Arithmetic and the first eight discarded comb
// outputs match cic_stage; output latency is different, sample order is not.
module cic_precision_tail(
 input wire clk,enable,valid,input wire [3:0] remaining_log,
 input wire [31:0] data,
 output wire ready,out_valid,output reg [31:0] q=0,output reg fault=0
);
 localparam CLEAR=0,IDLE=1,BEGIN_STAGE=2,FETCH=3,APPLY=4;
 reg [2:0] state=CLEAR;
 reg [5:0] clear_index=0;
 reg [1:0] stage=0;
 reg channel=0;reg [2:0] op=0;
 reg [3:0] count[0:2],warm[0:2];
 reg emit_comb=0,deliver=0,valid_q=0;
 reg [31:0] pair_data=0;
 reg [27:0] accumulator=0;
 (* ramstyle="M9K" *) reg [27:0] state_mem[0:63];
 reg [27:0] state_q=0;
 wire [5:0] state_address=state==CLEAR ? clear_index : {stage,channel,op};
 wire comb_op=op>=3;
 wire [27:0] operand_b=comb_op ? ~state_q : state_q;
 wire [27:0] result=accumulator+operand_b+comb_op;
 wire state_write=enable && !fault && (state==CLEAR || state==APPLY);
 wire [27:0] state_data=state==CLEAR ? 28'b0 : comb_op ? accumulator : result;
 always @(posedge clk)begin
  state_q<=state_mem[state_address];
  if(state_write)state_mem[state_address]<=state_data;
 end
 reg [3:0] log;
 always @*begin
  case(stage)
   0:log=remaining_log>4 ? 4 : remaining_log;
   1:log=remaining_log>8 ? 4 : remaining_log>4 ? remaining_log-4 : 0;
   default:log=remaining_log>8 ? remaining_log-8 : 0;
  endcase
 end
 wire [3:0] last=(5'd1<<log)-1'b1;
 wire [3:0] shift=log+(log<<1);
 wire signed [27:0] normalized=$signed(result) >>> shift;
 wire [15:0] result_word=normalized[15:0]^16'h8000;
 wire signed [15:0] input_low=pair_data[15:0]^16'h8000;
 wire signed [15:0] input_high=pair_data[31:16]^16'h8000;
 reg [31:0] queue[0:3];
 reg [1:0] read_pointer=0,write_pointer=0;
 reg [2:0] queued=0;
 wire bypass=remaining_log==0;
 wire pop=enable && !fault && !bypass && state==IDLE && queued!=0;
 assign ready=enable && !fault && remaining_log<=12 && (bypass || queued<4 || pop);
 wire push=enable && valid && !bypass && ready;
 assign out_valid=enable && valid_q && !fault;
 integer i;
 initial for(i=0;i<3;i=i+1)begin count[i]=0;warm[i]=0;end
 always @(posedge clk)begin
  valid_q<=0;
  if(push)begin queue[write_pointer]<=data;write_pointer<=write_pointer+1'b1;end
  if(pop)begin pair_data<=queue[read_pointer];read_pointer<=read_pointer+1'b1;end
  case({push,pop})
   2'b10:queued<=queued+1'b1;
   2'b01:queued<=queued-1'b1;
  endcase
  if(!enable)begin
   state<=CLEAR;clear_index<=0;fault<=0;valid_q<=0;
   read_pointer<=0;write_pointer<=0;queued<=0;
   for(i=0;i<3;i=i+1)begin count[i]<=0;warm[i]<=0;end
  end else begin
   if(remaining_log>12 || (valid && !ready))fault<=1;
   if(bypass)begin q<=data;valid_q<=valid;end
   case(state)
    CLEAR:begin clear_index<=clear_index+1'b1;if(clear_index==63)state<=IDLE;end
    IDLE:if(pop)begin stage<=0;state<=BEGIN_STAGE;end
    BEGIN_STAGE:begin
     if(log==0)begin
      if(stage==2)begin q<=pair_data;valid_q<=1;state<=IDLE;end
      else stage<=stage+1'b1;
     end else begin
      emit_comb<=count[stage]==last;
      deliver<=count[stage]==last && warm[stage]==8;
      if(count[stage]==last)begin
       count[stage]<=0;if(warm[stage]<8)warm[stage]<=warm[stage]+1'b1;
      end else count[stage]<=count[stage]+1'b1;
      channel<=0;op<=0;accumulator<=input_low;state<=FETCH;
     end
    end
    FETCH:state<=APPLY;
    APPLY:begin
     accumulator<=comb_op ? result : state_q;
     if(op==5 || (op==2 && !emit_comb))begin
      if(op==5)begin
       if(channel)pair_data[31:16]<=result_word;else pair_data[15:0]<=result_word;
      end
      if(!channel)begin channel<=1;op<=0;accumulator<=input_high;state<=FETCH;end
      else if(deliver)begin
       if(stage==2)begin q<={result_word,pair_data[15:0]};valid_q<=1;state<=IDLE;end
       else begin stage<=stage+1'b1;state<=BEGIN_STAGE;end
      end else state<=IDLE;
     end else begin op<=op+1'b1;state<=FETCH;end
    end
    default:begin state<=CLEAR;clear_index<=0;fault<=1;end
   endcase
  end
 end
endmodule

// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Serializes one resampled bin's enabled channels through one accumulator.
// The state adapter must atomically write ALL fields of one channel/bin and
// acknowledge completion, preserve request order, and share reset. It must
// never alias retained raw SRAM. There is at most one outstanding operation.
// A fault poisons the stack until reset: earlier channel/bin writes cannot be
// rolled back. Reset also invalidates the logical stack and resets the adapter.
// TRLC-LINKS: REQ-SDS-141
module stack_bin_writer(
 input wire clk,reset,
 input wire valid,output wire ready,
 input wire [31:0] bin,input wire [1:0] mask,input wire odd,
 input wire [36:0] value0,value1,
 output wire request_valid,input wire request_ready,
 output wire request_write,output wire [31:0] request_bin,output wire request_channel,
 output wire [68:0] write_sum,write_sum_a,output wire [105:0] write_sum2,
 output wire [31:0] write_count,write_count_a,
 input wire response_valid,response_error,output wire response_ready,
 input wire [68:0] read_sum,read_sum_a,input wire [105:0] read_sum2,
 input wire [31:0] read_count,read_count_a,
 output reg done=0,fault=0
);
 localparam IDLE=0,READ=1,READ_WAIT=2,CALCULATE=3,WRITE=4,WRITE_WAIT=5;
 reg [2:0] state=IDLE;
 reg [31:0] bin_l=0;reg [1:0] mask_l=0;reg odd_l=0,channel=0;
 reg [36:0] value0_l=0,value1_l=0;
 wire acc_valid,acc_invalid,acc_busy;
 assign ready=state==IDLE && !fault && !reset;
 assign request_valid=(state==READ || state==WRITE) && !fault && !reset;
 assign request_write=state==WRITE;
 assign request_bin=bin_l;
 assign request_channel=channel;
 assign response_ready=(state==READ_WAIT || state==WRITE_WAIT) && !fault && !reset;
 wire acc_start=state==READ_WAIT && response_valid && !response_error && !fault && !reset;
 stack_accumulate accumulator(.clk(clk),.reset(reset),.start(acc_start),.enabled(1'b1),.odd(odd_l),
  .value(channel ? value1_l:value0_l),.sum(read_sum),.sum2(read_sum2),.sum_a(read_sum_a),
  .count(read_count),.count_a(read_count_a),.valid(acc_valid),
  .ready(state==WRITE_WAIT && response_valid && !response_error && !reset),
  .busy(acc_busy),.invalid(acc_invalid),.result_sum(write_sum),.result_sum2(write_sum2),
  .result_sum_a(write_sum_a),.result_count(write_count),.result_count_a(write_count_a));
 always @(posedge clk)begin
  done<=0;
  if(reset)begin state<=IDLE;fault<=0;end
  else if(!fault)case(state)
   IDLE:if(valid)begin
    bin_l<=bin;mask_l<=mask;odd_l<=odd;value0_l<=value0;value1_l<=value1;
    channel<=!mask[0];
    if(mask==0)done<=1;else state<=READ;
   end
   READ:if(request_ready)state<=READ_WAIT;
   READ_WAIT:if(response_valid)begin
    if(response_error)fault<=1;else state<=CALCULATE;
   end
   CALCULATE:if(acc_valid)begin
    if(acc_invalid)fault<=1;else state<=WRITE;
   end
   WRITE:if(request_ready)state<=WRITE_WAIT;
   WRITE_WAIT:if(response_valid)begin
    if(response_error)fault<=1;
    else if(!channel && mask_l[1])begin channel<=1;state<=READ;end
    else begin state<=IDLE;done<=1;end
   end
   default:fault<=1;
  endcase
 end
endmodule

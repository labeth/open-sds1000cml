// Frozen-record reader using the same host producer interface as stream_engine.
// Coordinates are transport-relative physical word addresses; record_start
// includes the capture origin. The caller keeps the record frozen and grants
// exclusive transport/host ownership until done. Reset is a coordinated epoch
// abort, not a request to erase SRAM. No write command is ever generated.
// Host first ordinals are relative to this requested range, starting at zero.
module sram_finite_recall #(parameter AW=19,BANK_WORDS=2560,READ_WARM=16,CONTINUE_READS=1)(
 input wire clk,reset,start,frozen,
 input wire [AW-1:0] record_start,read_bias,
 input wire [AW:0] record_words,offset,length,
 output wire start_ready,output reg active=0,done=0,request_error=0,fault=0,
 output reg [3:0] error_code=0,output wire [63:0] recalled,
 input wire host_core_fault,input wire [1:0] bank_release,
 output reg [1:0] bank_busy=0,bank_done=0,
 output wire word_valid,word_bank,output wire [31:0] word_data,
 output reg [11:0] word_index=0,
 output reg [63:0] first0=0,first1=0,
 output reg [AW:0] words0=0,words1=0,
 input wire transport_ready,transport_done,transport_read_valid,
 input wire [31:0] transport_read_data,input wire [AW-1:0] position,
 output reg command=0,command_discard=0,command_continue=0,
 output wire command_read,output wire [AW:0] command_count
);
 localparam [AW:0] DEPTH={1'b1,{AW{1'b0}}};
 localparam IDLE=0,CHECK=1,BASE=2,WAIT_BANK=3,PREP=4,TARGET=5,
  DISTANCE=6,SEEK=7,SEEK_WAIT=8,READ_COMMAND=9,READING=10,
  DRAIN=11,PUBLISH=12,FINISH=13,FAILED=14,SNAPSHOT=15;
 reg [3:0] state=IDLE;
 reg geometry_ok=0,bank=0,continued=0;
 reg [AW-1:0] base_stage=0,bias=0,base_address=0,target_pre=0,target=0,distance=0;
 reg [AW:0] requested=0,left=0;
 // Per-burst arithmetic is bounded by one host bank, not the SRAM depth.
 localparam CW=$clog2(BANK_WORDS+1);
 reg [CW-1:0] chunk=0,remaining=0;
 // Both operands settle before their command pulse. Select using the
 // registered command type instead of gating a wide load with distance==0.
 reg [AW:0] read_count=0;
 assign command_count=command_discard ? {1'b0,distance} : read_count;
 localparam WW=READ_WARM>0 ? $clog2(READ_WARM+1) : 1;
 reg [WW-1:0] warm_left=0;
 wire [AW+1:0] range_end={1'b0,offset}+{1'b0,length};
 // Snapshot every operand together, then compare on the next clock. The
 // SNAPSHOT state aligns CHECK with the settings accepted at start; later
 // input changes cannot replace an accepted request's geometry.
 reg [AW+1:0] range_end_q=0;
 reg [AW:0] record_words_q=0;
 reg frozen_q=0,record_size_ok_q=0;
 always @(posedge clk)begin
  range_end_q<=range_end;record_words_q<=record_words;
  frozen_q<=frozen;record_size_ok_q<=record_words<=DEPTH;
  geometry_ok<=frozen_q && record_size_ok_q && range_end_q<={1'b0,record_words_q};
 end
 wire [1:0] free_banks=~(bank_busy & ~bank_release);
 assign start_ready=state==IDLE && transport_ready && bank_busy==0 && !fault && !host_core_fault && !reset;
 assign command_read=1'b1;
 assign word_valid=state==READING && transport_read_valid && warm_left==0 && !fault && !reset;
 assign word_bank=bank;assign word_data=transport_read_data;
 reg word_event=0;
 always @(posedge clk)begin
  if(reset || state==CHECK)word_event<=0;else word_event<=word_valid;
 end
 sram_ordinal_counter progress(clk,reset || state==CHECK,word_event,recalled);
 always @(posedge clk)begin
  command<=0;bank_done<=0;request_error<=0;
  if(reset)begin
   state<=IDLE;active<=0;done<=0;fault<=0;error_code<=0;
   bank_busy<=0;continued<=0;word_index<=0;remaining<=0;warm_left<=0;
   command_discard<=0;command_continue<=0;read_count<=0;
  end else begin
   bank_busy<=bank_busy & ~bank_release;
   case(state)
    IDLE:if(start && start_ready)begin
     requested<=length;base_stage<=record_start+offset[AW-1:0];bias<=read_bias;
     active<=1;done<=0;continued<=0;state<=SNAPSHOT;
    end
    SNAPSHOT:state<=CHECK;
    CHECK:begin
     if(!geometry_ok)begin request_error<=1;active<=0;state<=IDLE;end
     else if(requested==0)begin active<=0;done<=1;state<=IDLE;end
     else begin left<=requested;state<=BASE;end
    end
    BASE:begin base_address<=base_stage+bias;state<=WAIT_BANK;end
    WAIT_BANK:if(|free_banks)begin
     bank<=!free_banks[0];
     bank_busy<=(bank_busy & ~bank_release) | (free_banks[0] ? 2'b01 : 2'b10);
     chunk<=left>BANK_WORDS ? BANK_WORDS : left;
     state<=PREP;
    end
    PREP:begin
     // The ordinal pipeline has settled during transport drain / host wait.
     if(bank)begin first1<=recalled;words1<=chunk;end
     else begin first0<=recalled;words0<=chunk;end
     remaining<=chunk;word_index<=0;
     warm_left<=CONTINUE_READS && continued ? 0 : READ_WARM;
     command_continue<=CONTINUE_READS && continued;
     read_count<=chunk+(CONTINUE_READS && continued ? 0 : READ_WARM);
     target_pre<=base_address+recalled[AW-1:0];
     if(CONTINUE_READS && continued)state<=READ_COMMAND;else state<=TARGET;
    end
    TARGET:begin target<=target_pre-READ_WARM;state<=DISTANCE;end
    DISTANCE:begin distance<=target-position;state<=SEEK;end
    SEEK:if(transport_ready)begin
     if(distance==0)state<=READ_COMMAND;
     else begin command<=1;command_discard<=1;command_continue<=0;state<=SEEK_WAIT;end
    end
    SEEK_WAIT:if(transport_done)state<=READ_COMMAND;
    READ_COMMAND:if(transport_ready)begin command<=1;command_discard<=0;state<=READING;end
    READING:begin
     if(transport_read_valid)begin
      if(warm_left!=0)warm_left<=warm_left-1'b1;
      else begin
       remaining<=remaining-1'b1;word_index<=word_index+1'b1;
       if(remaining==1)state<=DRAIN;
      end
     end
     if(transport_done)begin fault<=1;error_code<=1;state<=FAILED;end
    end
    DRAIN:begin
     if(transport_read_valid)begin fault<=1;error_code<=1;state<=FAILED;end
     else if(transport_done)state<=PUBLISH;
    end
    PUBLISH:begin
     bank_done<=bank ? 2'b10 : 2'b01;left<=left-chunk;continued<=1;
     if(left==chunk)state<=FINISH;else state<=WAIT_BANK;
    end
    FINISH:if(bank_busy==0 && transport_ready)begin active<=0;done<=1;state<=IDLE;end
    FAILED:if(transport_ready)active<=0;
   endcase
   if(active && state!=CHECK && state!=SNAPSHOT && state!=IDLE && !frozen)begin fault<=1;error_code<=2;state<=FAILED;command<=0;end
   if(active && host_core_fault)begin fault<=1;error_code<=3;state<=FAILED;command<=0;end
  end
 end
endmodule

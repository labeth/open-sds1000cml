// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Physical on-chip accumulator tile; does not drive acquisition SRAM pins.
// Ten little-endian 32-bit words per channel/bin store the 308 state bits.
// Layout low to high: sum69, sum2 106, count32, sumA69, countA32, zero12.
// One command at a time makes multiword writes atomically visible to readers.
// Reset clears every word before accepting work, including partial old writes.
// The eventual host loader and stack engine must exclusively own this port.
// TRLC-LINKS: REQ-SDS-141
module stack_state_tile #(parameter BINS=32)(
 input wire clk,reset,
 input wire request_valid,output wire request_ready,
 input wire request_write,input wire [31:0] request_bin,input wire request_channel,
 input wire [68:0] write_sum,write_sum_a,input wire [105:0] write_sum2,
 input wire [31:0] write_count,write_count_a,
 output wire response_valid,input wire response_ready,output reg response_error=0,
 output wire [68:0] read_sum,read_sum_a,output wire [105:0] read_sum2,
 output wire [31:0] read_count,read_count_a,
 output wire initialized
);
 localparam WORDS=BINS*20,AW=WORDS<=256 ? 8:$clog2(WORDS),SECTIONS=(WORDS+255)/256;
 localparam CLEAR=0,IDLE=1,WRITE=2,READ=3,COLLECT=4,RESPONSE=5,CHECK=6;
 reg [2:0] state=CLEAR;
 reg [AW-1:0] address=0;
 reg [3:0] word_index=0;
 reg write_l=0;
 reg [319:0] payload=0;
 wire [36:0] base=({5'd0,request_bin}<<4)+({5'd0,request_bin}<<2)+(request_channel ? 37'd10:37'd0);
 assign request_ready=state==IDLE && !reset;
 assign response_valid=state==RESPONSE && !reset;
 assign initialized=state!=CLEAR && !reset;
 assign {read_count_a,read_sum_a,read_count,read_sum2,read_sum}=payload[307:0];
 wire mem_write=(state==CLEAR || state==WRITE) && !reset;
 wire mem_read=state==READ && !reset;
 wire [31:0] mem_data=state==CLEAR ? 32'd0:payload[31:0];
 wire [31:0] bank_data[0:SECTIONS-1];
 wire [31:0] read_word=bank_data[address>>8];
 genvar bank;
 generate for(bank=0;bank<SECTIONS;bank=bank+1)begin: sections
  (* ramstyle = "M9K" *) reg [31:0] memory[0:255];
  reg [31:0] fetched=0;
  always @(posedge clk)begin
   if(mem_write && (address>>8)==bank)memory[address[7:0]]<=mem_data;
   if(mem_read && (address>>8)==bank)fetched<=memory[address[7:0]];
  end
  assign bank_data[bank]=fetched;
 end endgenerate
 always @(posedge clk)begin
  if(reset)begin state<=CLEAR;address<=0;word_index<=0;payload<=0;response_error<=0;end
  else case(state)
   CLEAR:if(address==WORDS-1)begin address<=0;state<=IDLE;end else address<=address+1'b1;
   IDLE:begin
    // Capture while idle, including the accepted request edge. Request and
    // write qualification must not drive the wide payload enable. Outputs
    // are meaningful only with response_valid; reads/invalid commands clear
    // this speculative payload in CHECK before any memory access.
    payload<={12'd0,write_count_a,write_sum_a,write_count,write_sum2,write_sum};
    if(request_valid)begin
     response_error<=request_bin>=BINS;word_index<=0;write_l<=request_write;
     address<=base[AW-1:0];state<=CHECK;
    end
   end
   CHECK:begin
    if(response_error)begin payload<=0;state<=RESPONSE;end
    else begin
     if(!write_l)payload<=0;
     state<=write_l ? WRITE:READ;
    end
   end
   WRITE:begin
    payload<=payload>>32;
    if(word_index==9)begin payload<=0;state<=RESPONSE;end
    else begin address<=address+1'b1;word_index<=word_index+1'b1;end
   end
   READ:state<=COLLECT;
   COLLECT:begin
    payload<={read_word,payload[319:32]};
    if(word_index==9)state<=RESPONSE;
    else begin address<=address+1'b1;word_index<=word_index+1'b1;state<=READ;end
   end
   RESPONSE:if(response_ready)state<=IDLE;
   default:begin state<=CLEAR;address<=0;response_error<=1;end
  endcase
 end
endmodule

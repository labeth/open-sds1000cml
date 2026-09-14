// Volatile SRAM bus owner. Fixed-length bursts allow the caller to reserve
// FIFO space before reading; read data cannot be backpressured within a burst.
// command_count is the payload count (1..2^AW), or zero for an unlimited
// write until write_stop. A fresh read emits one
// extra K2 pulse to flush the SRAM's two-stage synchronous output. A continued
// read consumes the prefetched word first and needs no extra pulse. Discard
// commands move the counter without publishing data or adding a flush. position
// counts EVERY enabled K2 pulse, including that flush, modulo 2^AW.
//
// clk=100 MHz, sample_clk=clk shifted +4 ns is the initial integration target.
// The 250 MHz benchmark is separate: this controller requires its own timing
// audit and board test before increasing frequency.
module sram_transport #(parameter AW=19,CONTINUOUS_ONLY=0,READ_DELAY=0)(
 input wire clk,sample_clk,reset,locked,
 input wire command,command_read,command_discard,command_continue,
 input wire [AW:0] command_count,
 output wire ready,
 input wire [31:0] write_data,input wire write_valid,write_stop,
 output wire write_ready,
 output reg [31:0] read_data=0,output reg read_valid=0,
 output reg done=0,output reg [AW-1:0] position=0,
 inout wire [31:0] dq,output wire k1,k2,g1
);
 localparam IDLE=0,SETUP=1,WRITE=2,HOLD=3,READ=4,FLUSH=5,DRAIN=6,VALIDATE=7;
 (* syn_encoding = "user" *) reg [7:0] state=8'b1;
 reg reading=1,discard=0,continuous=0,continuing=0,legal_command=0;
 reg [3:0] wait_count=0;reg wait_last=0,drain_last=0;
 reg [AW:0] remaining=0;reg remaining_last=0,stop_requested=0;
 reg [31:0] drive_data=0,captured=0;
 (* preserve *) reg write_queued=0;
 reg [31:0] queued_data=0;
 reg [2+READ_DELAY:0] read_tags=0;
 // In continuous mode this is exactly next state[WRITE] && !next stop.
 // Keep a dedicated register so transfer consumers do not re-decode state
 // and stop_requested before their own arithmetic and validity checks.
 (* preserve *) reg write_open=0;
 wire write_allowed=CONTINUOUS_ONLY ? write_open : state[WRITE] && !stop_requested;
 wire write_step=write_allowed && write_valid;
 wire read_step=state[READ] && !discard;
 // Dedicated pulse register removes the high-fanout state decode from the
 // external clock output path. This is the same next-cycle pulse sequence.
 (* preserve *) reg pulse=0;
 wire next_read_pulse=(state[SETUP] && wait_last && reading) ||
   (state[READ] && (!remaining_last || (!discard && !continuing)));

 assign ready=state[IDLE] && locked && !reset;
 assign write_ready=write_allowed && !reset;
 assign k1=reading;
 assign g1=locked && !reset;
 assign dq=(!reading && !state[IDLE]) ? drive_data : 32'bz;
 // Both DDR inputs latch on the positive clock edge. Data is registered on
 // that same edge and is stable for half a period before K2 rises.
 altddio_out #(.width(1),.power_up_high("OFF"),.intended_device_family("Cyclone IV E")) ckout
 (.outclock(clk),.datain_h(1'b0),.datain_l(pulse && !reset),.dataout(k2),
 .oe(1'b1),.aclr(!locked),.aset(1'b0),.sclr(1'b0),.sset(1'b0),.outclocken(1'b1));
 always @(posedge sample_clk) captured<=dq;
 always @(posedge clk) begin
  write_queued<=write_step;
  pulse<=write_step || next_read_pulse;
  queued_data<=write_data; // Validity travels separately in write_queued.
  drive_data<=queued_data;
  read_data<=captured;
  read_valid<=continuing ? read_tags[1+READ_DELAY] : read_tags[2+READ_DELAY];
  read_tags<={read_tags[1+READ_DELAY:0],read_step};
  done<=0;
  if(CONTINUOUS_ONLY)begin
   stop_requested<=write_stop;
   write_open<=((state[SETUP] && wait_last && !reading) ||
                (state[WRITE] && !stop_requested)) && !write_stop;
  end
  wait_last<=wait_count==14;drain_last<=wait_count==6;
  if(reset || !locked) begin
   state<=8'b1;reading<=1;position<=0;write_open<=0;write_queued<=0;pulse<=0;stop_requested<=0;read_tags<=0;read_valid<=0;
  end else begin
   if(pulse)position<=position+1'b1;
   state[IDLE]<=(state[IDLE] && !command) || (state[VALIDATE] && !legal_command) || (state[HOLD] && wait_last) || (state[DRAIN] && drain_last);
   state[VALIDATE]<=state[IDLE] && command;
   state[SETUP]<=(state[VALIDATE] && legal_command) || (state[SETUP] && !wait_last);
   state[WRITE]<=(state[SETUP] && wait_last && !reading) || (state[WRITE] && !stop_requested);
   state[HOLD]<=(state[WRITE] && stop_requested) || (state[HOLD] && !wait_last);
   state[READ]<=(state[SETUP] && wait_last && reading) || (state[READ] && !remaining_last);
   state[FLUSH]<=state[READ] && remaining_last && !discard && !continuing;
   state[DRAIN]<=state[FLUSH] || (state[READ] && remaining_last && (discard || continuing)) || (state[DRAIN] && !drain_last);
   if(state[IDLE] && command)begin
    legal_command<=(CONTINUOUS_ONLY && !command_read) || ((!command_read || command_count!=0) && command_count<={1'b1,{AW{1'b0}}});
    stop_requested<=0;reading<=command_read;discard<=command_discard;continuous<=CONTINUOUS_ONLY || command_count==0;
    continuing<=command_continue;
   end
   if(!CONTINUOUS_ONLY && state[WRITE])stop_requested<=write_stop || (!CONTINUOUS_ONLY && write_step && !continuous && remaining_last);
   if(state[HOLD] && wait_last)reading<=1;
   done<=(state[HOLD] && wait_last) || (state[DRAIN] && drain_last);
  end
  if(state[SETUP] || state[HOLD] || state[DRAIN])wait_count<=wait_count+1'b1;else wait_count<=0;
  if(state[IDLE])begin remaining<=command_count;end
  else if(state[VALIDATE])remaining_last<=remaining==1;
  else if(state[READ] || (!CONTINUOUS_ONLY && write_step && !continuous))begin
   remaining<=remaining-1'b1;remaining_last<=remaining==2;
  end
 end
endmodule

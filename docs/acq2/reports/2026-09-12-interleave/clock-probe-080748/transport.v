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
module sram_transport #(parameter AW=19)(
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
 reg [2:0] state=IDLE;
 reg reading=1,discard=0,continuous=0,continuing=0,legal_command=0;
 reg [3:0] wait_count=0;
 reg [AW:0] remaining=0;reg remaining_last=0,stop_requested=0;
 reg [31:0] drive_data=0,captured=0;
 (* preserve *) reg write_queued=0;
 reg [31:0] queued_data=0;
 reg [2:0] read_tags=0;
 wire write_step=state==WRITE && !stop_requested && write_valid;
 wire read_step=state==READ && !discard;
 wire pulse=write_queued || state==READ || state==FLUSH;
 assign ready=state==IDLE && locked && !reset;
 assign write_ready=state==WRITE && !stop_requested && !reset;
 assign k1=reading;
 assign g1=locked && !reset;
 assign dq=(!reading && state!=IDLE) ? drive_data : 32'bz;
 // Both DDR inputs latch on the positive clock edge. Data is registered on
 // that same edge and is stable for half a period before K2 rises.
 altddio_out #(.width(1),.power_up_high("OFF"),.intended_device_family("Cyclone IV E")) ckout
 (.outclock(clk),.datain_h(1'b0),.datain_l(pulse && !reset),.dataout(k2),
 .oe(1'b1),.aclr(!locked),.aset(1'b0),.sclr(1'b0),.sset(1'b0),.outclocken(1'b1));
 always @(posedge sample_clk) captured<=dq;
 always @(posedge clk) begin
  write_queued<=write_step;
  if(write_step)queued_data<=write_data;
  drive_data<=queued_data;
  read_data<=captured;
  read_valid<=continuing ? read_tags[1] : read_tags[2];
  read_tags<={read_tags[1:0],read_step};
  done<=0;
  if(reset || !locked) begin
   state<=IDLE;reading<=1;position<=0;write_queued<=0;stop_requested<=0;read_tags<=0;read_valid<=0;
  end else begin
   if(pulse) position<=position+1'b1;

   case(state)
    IDLE: if(command) begin
     legal_command<=(!command_read || command_count!=0) && command_count<={1'b1,{AW{1'b0}}};
     stop_requested<=0;reading<=command_read;discard<=command_discard;continuous<=command_count==0;
     continuing<=command_continue;
     remaining<=command_count;remaining_last<=command_count==1;wait_count<=0;state<=VALIDATE;
    end
    VALIDATE:state<=legal_command ? SETUP : IDLE;
    SETUP: begin
     wait_count<=wait_count+1'b1;
     if(wait_count==15) state<=reading ? READ : WRITE;
    end
    WRITE: begin
     if(write_step && !continuous) begin remaining<=remaining-1'b1;remaining_last<=remaining==2;end
     stop_requested<=write_stop || (write_step && !continuous && remaining_last);
     if(stop_requested) begin state<=HOLD;wait_count<=0;end
    end
    HOLD: begin
     wait_count<=wait_count+1'b1;
     if(wait_count==15) begin state<=IDLE;reading<=1;done<=1;end
    end
    READ: begin
     remaining<=remaining-1'b1;remaining_last<=remaining==2;
     if(remaining_last) begin
      state<=(discard || continuing) ? DRAIN : FLUSH;
      wait_count<=0;
     end
    end
    FLUSH: begin state<=DRAIN;wait_count<=0;end
    DRAIN: begin
     wait_count<=wait_count+1'b1;
     if(wait_count==7) begin state<=IDLE;done<=1;end
    end
   endcase
  end
 end
endmodule

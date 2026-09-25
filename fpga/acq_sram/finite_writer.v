// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-FINITE-WRITER
// Finite pre/post-trigger capture client for sram_board_capture_path.
// All signals are core-clock synchronous. capture_allowed must exclude an
// active backend, outstanding host banks and another accepted start.
// source_enable marks the acquisition interval; trigger accompanies its word.
// source_fault is a synchronous frontend error; it invalidates the capture
// through startup, final drain and frozen recall. Reset is the epoch boundary.
// Data is opaque (raw packed pairs or both Q8.8 channels). No sample RAM here.
// Prime writes are fully drained before recording the physical origin. The
// resulting origin convention needs board qualification before deployment.
// TRLC-LINKS: REQ-SDS-041, REQ-SDS-042, REQ-SDS-057, REQ-SDS-058
module sram_finite_writer #(parameter AW=19,PRIME_WORDS=16)(
 input wire clk,reset,start,capture_allowed,halt,
 input wire [AW:0] pre_count,post_count,
 input wire source_valid,source_fault,trigger,input wire [31:0] source_data,
 output wire start_ready,active,source_enable,source_ready,
 output reg frozen=0,fault=0,request_error=0,
 output wire triggered,output wire [AW-1:0] record_start,
 output wire [AW:0] record_words,trigger_index,
 output wire writer_request,writer_command,writer_valid,writer_stop,
 output wire [31:0] writer_data,
 input wire writer_ready,writer_write_ready,
 input wire [AW-1:0] position
);
 localparam IDLE=0,CLAIM=1,PRIME_START=2,PRIME=3,PRIME_DRAIN=4,
            CAPTURE_START=5,SETUP=6,ARM=7,RUN=8,DRAIN=9;
 reg [3:0] state=IDLE;
 (* preserve *) reg launch_pending=0;
 reg [AW:0] pre_l=0,post_l=1;
 // Speculative idle capture: the accepted-start edge captures these inputs,
 // and leaving IDLE holds them until ARM. Rejected starts may change these
 // private registers, but cannot change the separate frozen record state.
 always @(posedge clk)if(state==IDLE && !launch_pending)begin pre_l<=pre_count;post_l<=post_count;end
 reg halt_pending=0;
 localparam PW=PRIME_WORDS>1 ? $clog2(PRIME_WORDS+1) : 1;
 reg [PW-1:0] prime_left=0;
 reg [AW-1:0] origin=0;
 // Independent upper/lower sums keep the low carry off the upper adder.
 wire capture_geometry;
 generate if(AW>8)begin:geometry_split
  wire [8:0] low_sum={1'b0,pre_count[7:0]}+{1'b0,post_count[7:0]};
  wire [AW-7:0] high_sum={1'b0,pre_count[AW:8]}+{1'b0,post_count[AW:8]};
  localparam [AW-7:0] LIMIT=1<<(AW-8);
  wire low_zero=low_sum[7:0]==0;
  wire fits_no_carry=high_sum<LIMIT || (high_sum==LIMIT && low_zero);
  wire fits_carry=high_sum<LIMIT-1'b1 || (high_sum==LIMIT-1'b1 && low_zero);
  assign capture_geometry=post_count!=0 && (low_sum[8] ? fits_carry : fits_no_carry);
 end else begin:geometry_small
  wire [AW+1:0] total={1'b0,pre_count}+{1'b0,post_count};
  assign capture_geometry=post_count!=0 && total<=(1<<AW);
 end endgenerate
 wire geometry_ok=capture_geometry;
 wire running,record_done,config_error;
 wire [AW-1:0] logical_start,write_addr;
 wire [AW:0] filled;
 // Reservation is a single register on the arbitration path. State decode
 // remains local to the sequencer instead of feeding every client's readiness.
 (* preserve *) reg reserved=0;
 assign active=reserved;
 // synthesis translate_off
 always @(posedge clk)if(reserved !== (launch_pending || state!=IDLE))
  $fatal(1,"finite writer reservation disagrees with sequencer");
 // synthesis translate_on
 assign start_ready=!reset && !active && !fault && !source_fault && capture_allowed;
 wire accepted_start=start && start_ready && geometry_ok;
 // Private while idle: preload on every idle edge, including acceptance.
 // Once reserved, remember any halt through priming and final drain.
 // Geometry validation need not feed this sticky control register.
 always @(posedge clk)begin
  if(reset)halt_pending<=0;
  else if(!active)halt_pending<=halt;
  else if(halt)halt_pending<=1;
 end
 // Reserve immediately, but isolate the state machine from acceptance fan-in.
 always @(posedge clk)if(reset)launch_pending<=0;else launch_pending<=accepted_start;
 assign writer_request=active;
 assign writer_command=!source_fault && writer_ready && (state==PRIME_START || state==CAPTURE_START);
 assign source_enable=state==RUN && running && !fault && !halt_pending && !source_fault && !reset;
 assign source_ready=source_enable && writer_write_ready && !halt;
 wire lost_word=source_enable && source_valid && !writer_write_ready;
 wire abort_capture=halt || halt_pending || fault || source_fault || lost_word;
 assign writer_valid=!reset && !source_fault && ((state==PRIME && writer_write_ready) ||
                     (source_valid && source_ready));
 assign writer_data=state==PRIME ? 32'b0 : source_data;
 assign writer_stop=!(state==PRIME_START || state==PRIME ||
                      state==CAPTURE_START || state==SETUP || state==ARM ||
                      (state==RUN && running && !abort_capture));
 assign record_start=origin+logical_start;
 sram_record #(.AW(AW),.CONFIG_VALIDATED(1)) record(
  .clk(clk),.reset(reset),.arm(state==ARM),.halt(state==RUN && abort_capture),
  .pre_count(pre_l),.post_count(post_l),
  .step(source_valid && source_ready),.trigger(trigger),
  .running(running),.done(record_done),.triggered(triggered),.config_error(config_error),
  .write_addr(write_addr),.record_start(logical_start),.record_length(record_words),
  .trigger_index(trigger_index),.filled(filled));
 always @(posedge clk)begin
  request_error<=0;
  if(reset)begin state<=IDLE;reserved<=0;frozen<=0;fault<=0;request_error<=0;end
  else begin
   if(start && (!start_ready || !geometry_ok))request_error<=1;
   if(accepted_start)begin reserved<=1;frozen<=0;end
   if(lost_word || config_error || (source_fault && (active || frozen)))begin fault<=1;frozen<=0;end
   case(state)
    IDLE:if(launch_pending)state<=CLAIM;
    CLAIM:if(writer_ready)begin
     prime_left<=PRIME_WORDS;state<=PRIME_WORDS==0 ? CAPTURE_START : PRIME_START;
    end
    PRIME_START:if(writer_ready)state<=PRIME;
    PRIME:if(writer_write_ready)begin
     prime_left<=prime_left-1'b1;if(prime_left==1)state<=PRIME_DRAIN;
    end
    PRIME_DRAIN:if(writer_ready)state<=CAPTURE_START;
    CAPTURE_START:if(writer_ready)begin origin<=position;state<=SETUP;end
    SETUP:if(writer_write_ready)state<=ARM;
    ARM:state<=RUN;
    RUN:if(record_done || abort_capture)state<=DRAIN;
    DRAIN:if(writer_ready)begin reserved<=0;frozen<=!fault && !source_fault;state<=IDLE;end
    default:begin state<=IDLE;reserved<=0;fault<=1;frozen<=0;end
   endcase
   // Do not issue further priming/data writes after an upstream failure.
   // Retain the transport reservation until any in-flight operation drains.
   if(source_fault && active && state!=DRAIN)begin reserved<=1;state<=DRAIN;end
  end
 end
endmodule

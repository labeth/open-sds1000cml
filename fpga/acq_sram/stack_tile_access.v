// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Exclusive ownership of a physical state tile. Host commands win simultaneous
// idle acquisition; an accepted host command owns the tile through completion.
// Engine ownership spans the entire hit, including gaps between read and write.
// Release blocks new requests and waits for an outstanding response to be
// consumed. Shared reset clears the tile and invalidates the logical stack.
// GPMC mapping and host/core clock crossing remain external obligations.
// TRLC-LINKS: REQ-SDS-141
module stack_tile_access #(parameter BINS=32)(
 input wire clk,reset,
 input wire command_valid,output wire command_ready,input wire command_write,
 input wire [31:0] command_bin,input wire command_channel,
 input wire upload_valid,output wire upload_ready,input wire [15:0] upload_data,
 output wire download_valid,input wire download_ready,output wire [15:0] download_data,
 output wire completion_valid,input wire completion_ready,output wire completion_error,output wire busy,
 input wire engine_acquire,output wire engine_acquire_ready,input wire engine_release,output reg engine_owned=0,
 input wire engine_request_valid,output wire engine_request_ready,input wire engine_request_write,
 input wire [31:0] engine_request_bin,input wire engine_request_channel,
 output wire engine_response_valid,input wire engine_response_ready,output wire engine_response_error,
 output wire initialized,
 input wire [68:0] engine_write_sum,
 output wire [68:0] engine_read_sum,
 input wire [68:0] engine_write_sum_a,
 output wire [68:0] engine_read_sum_a,
 input wire [105:0] engine_write_sum2,
 output wire [105:0] engine_read_sum2,
 input wire [31:0] engine_write_count,
 output wire [31:0] engine_read_count,
 input wire [31:0] engine_write_count_a,
 output wire [31:0] engine_read_count_a
);
 wire host_command_ready,host_request_valid,host_request_ready,host_request_write,host_request_channel,host_response_ready;
 wire [31:0] host_request_bin;
 wire tile_request_valid,tile_request_ready,tile_response_valid,tile_response_ready,tile_response_error;
 reg pending=0,releasing=0;
 assign command_ready=host_command_ready && !engine_owned && initialized && !reset;
 assign engine_acquire_ready=!engine_owned && !busy && !command_valid && initialized && !reset;
 assign engine_request_ready=engine_owned && !pending && !releasing && !engine_release && tile_request_ready && !reset;
 assign engine_response_valid=engine_owned && pending && tile_response_valid && !reset;
 assign engine_response_error=tile_response_error;
 wire engine_offer=engine_owned && !pending && !releasing && !engine_release && engine_request_valid && !reset;
 assign tile_request_valid=engine_owned ? engine_offer:host_request_valid;
 assign host_request_ready=!engine_owned && tile_request_ready;
 assign tile_response_ready=engine_owned ? pending && engine_response_ready:host_response_ready;
 always @(posedge clk)begin
  if(reset)begin engine_owned<=0;pending<=0;releasing<=0;end
  else begin
   if(engine_acquire && engine_acquire_ready)begin engine_owned<=1;releasing<=0;end
   if(engine_owned && engine_release)releasing<=1;
   if(engine_request_valid && engine_request_ready)pending<=1;
   if(engine_response_valid && engine_response_ready)pending<=0;
   if(engine_owned && (releasing || engine_release) && (!pending || (engine_response_valid && engine_response_ready)))begin
    engine_owned<=0;releasing<=0;
   end
  end
 end
 wire [68:0] host_write_sum;
 wire [68:0] host_write_sum_a;
 wire [105:0] host_write_sum2;
 wire [31:0] host_write_count;
 wire [31:0] host_write_count_a;
 stack_tile_transfer host(.clk(clk),.reset(reset),.command_valid(command_valid && !engine_owned && initialized),
 .command_ready(host_command_ready),.command_write(command_write),.command_bin(command_bin),.command_channel(command_channel),
 .upload_valid(upload_valid),.upload_ready(upload_ready),.upload_data(upload_data),
 .download_valid(download_valid),.download_ready(download_ready),.download_data(download_data),
 .completion_valid(completion_valid),.completion_ready(completion_ready),.completion_error(completion_error),.busy(busy),
 .request_valid(host_request_valid),.request_ready(host_request_ready),.request_write(host_request_write),
 .request_bin(host_request_bin),.request_channel(host_request_channel),
 .response_valid(tile_response_valid && !engine_owned),.response_ready(host_response_ready),.response_error(tile_response_error),
 .write_sum(host_write_sum),.read_sum(engine_read_sum),
 .write_sum_a(host_write_sum_a),.read_sum_a(engine_read_sum_a),
 .write_sum2(host_write_sum2),.read_sum2(engine_read_sum2),
 .write_count(host_write_count),.read_count(engine_read_count),
 .write_count_a(host_write_count_a),.read_count_a(engine_read_count_a)
 );
 stack_state_tile #(.BINS(BINS)) tile(.clk(clk),.reset(reset),.initialized(initialized),
 .request_valid(tile_request_valid),.request_ready(tile_request_ready),
 .request_write(engine_owned ? engine_request_write:host_request_write),
 .request_bin(engine_owned ? engine_request_bin:host_request_bin),
 .request_channel(engine_owned ? engine_request_channel:host_request_channel),
 .response_valid(tile_response_valid),.response_ready(tile_response_ready),.response_error(tile_response_error),
 .write_sum(engine_owned ? engine_write_sum:host_write_sum),.read_sum(engine_read_sum),
 .write_sum_a(engine_owned ? engine_write_sum_a:host_write_sum_a),.read_sum_a(engine_read_sum_a),
 .write_sum2(engine_owned ? engine_write_sum2:host_write_sum2),.read_sum2(engine_read_sum2),
 .write_count(engine_owned ? engine_write_count:host_write_count),.read_count(engine_read_count),
 .write_count_a(engine_owned ? engine_write_count_a:host_write_count_a),.read_count_a(engine_read_count_a)
 );
endmodule

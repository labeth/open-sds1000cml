// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Read consecutive shifted windows from a retained record against reference[0:L).
// The memory adapter accepts paired reference/record addresses and returns one
// paired response no earlier than the next clock. Only one request is outstanding.
// start replaces a search, drains any old response and discards buffered data.
// reset MUST also reset the adapter response pipeline, as for host_read_window.
// This module neither owns SRAM nor authorizes overwriting the retained record.
// TRLC-LINKS: REQ-SDS-141
module stack_window_reader(
 input wire clk,reset,start,
 input wire [31:0] first_position,window_count,gate_length,record_samples,reference_samples,
 output wire request_valid,input wire request_ready,
 output wire [31:0] reference_address,candidate_address,
 input wire response_valid,response_error,
 input wire [7:0] reference_data,candidate_data,
 output wire response_ready,
 output wire pair_valid,input wire pair_ready,
 output wire [7:0] reference_sample,candidate_sample,
 output reg busy=0,done=0,fault=0
);
 reg pending=0,discard_pending=0,buffer_valid=0;
 reg [7:0] buffered_reference=0,buffered_candidate=0;
 reg [31:0] last_offset=0,windows_left=0,offset=0,window_position=0;
 wire [33:0] range_end={2'b0,first_position}+{2'b0,window_count}+{2'b0,gate_length}-34'd1;
 wire bad_config=window_count==0 || gate_length<4 || gate_length>reference_samples ||
  range_end>{2'b0,record_samples};
 assign request_valid=busy && !fault && !pending && !buffer_valid && !start && !reset;
 assign reference_address=offset;
 assign candidate_address=window_position+offset;
 assign response_ready=pending && !reset;
 assign pair_valid=busy && buffer_valid && !fault && !start && !reset;
 assign reference_sample=buffered_reference;
 assign candidate_sample=buffered_candidate;
 always @(posedge clk)begin
  done<=0;
  if(reset)begin
   pending<=0;discard_pending<=0;buffer_valid<=0;busy<=0;fault<=0;
  end else if(start)begin
   last_offset<=gate_length-1'b1;windows_left<=window_count;offset<=0;window_position<=first_position;
   buffer_valid<=0;fault<=bad_config;busy<=!bad_config;done<=bad_config;
   // No request can be accepted on start. A coincident old response is drained.
   pending<=pending && !response_valid;
   discard_pending<=pending && !response_valid;
  end else begin
   if(request_valid && request_ready)pending<=1;
   if(response_valid && pending)begin
    pending<=0;
    if(discard_pending)discard_pending<=0;
    else if(busy)begin
     if(response_error)begin fault<=1;busy<=0;buffer_valid<=0;done<=1;end
     else begin buffered_reference<=reference_data;buffered_candidate<=candidate_data;buffer_valid<=1;end
    end
   end
   if(pair_valid && pair_ready)begin
    buffer_valid<=0;
    if(offset==last_offset)begin
     offset<=0;windows_left<=windows_left-1'b1;
     if(windows_left==1)begin busy<=0;done<=1;end
     else window_position<=window_position+1'b1;
    end else offset<=offset+1'b1;
   end
  end
 end
endmodule

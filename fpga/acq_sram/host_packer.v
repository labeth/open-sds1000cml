// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-HOST-PACKER
// 250 MHz, non-backpressurable word input. Bank completion must arrive AFTER
// its final word, with stable first/count metadata. Ownership is external:
// no reuse until RAM publication and ARM release. Common epoch reset required.
// Output packets feed sram_host_fifo; FIFO overflow invalidates the epoch.
// ORDINAL_BITS (1..64) may bound retained-record metadata; upper bits fault.
// TRLC-LINKS: REQ-SDS-049
module sram_host_packer #(parameter ORDINAL_BITS=64)(
 input wire clk,reset,input wire word_valid,word_bank,
 input wire [31:0] word_data,input wire [11:0] word_index,
 input wire [1:0] bank_done,
 input wire [63:0] first0,first1,input wire [19:0] words0,words1,
 output reg push=0,output reg [79:0] packet=0,output wire fault
);
 reg [11:0] count[0:1];
 reg [31:0] half[0:1];
 reg [13:0] pair_address[0:1];
 reg [1:0] half_valid=0,pending=0;
 reg [ORDINAL_BITS-1:0] first[0:1];reg [13:0] words[0:1];
 reg valid_q=0,bank_q=0,index_legal_q=0;
 reg [31:0] data_q=0;reg [11:0] index_q=0;
 reg [1:0] done_q=0,count_legal_q=0;
 reg [ORDINAL_BITS-1:0] first0_q=0,first1_q=0;
 // Reject discarded upper bits and validate the retained descriptor count
 // in the input stage, alongside the associated completion pulse.
 reg [11:0] words0_q=0,words1_q=0;
 // Validate ordinals and descriptor counts in the input stage. Counts track
 // offered words before the delayed packing stage, so back-to-back words and
 // completion immediately after the final word use the correct predecessor.
 wire index_matches=word_bank ? word_index==count[1] : word_index==count[0];
 always @(posedge clk)begin
  if(reset)begin valid_q<=0;done_q<=0;index_legal_q<=0;count_legal_q<=0;end
  else begin
   valid_q<=word_valid;done_q<=bank_done;
   index_legal_q<=word_index<2560 && index_matches;
   count_legal_q[0]<=words0[19:12]==0 && (first0 >> ORDINAL_BITS)==0 && words0[11:0]!=0 && words0[11:0]==count[0];
   count_legal_q[1]<=words1[19:12]==0 && (first1 >> ORDINAL_BITS)==0 && words1[11:0]!=0 && words1[11:0]==count[1];
  end
  bank_q<=word_bank;data_q<=word_data;index_q<=word_index;
  first0_q<=first0;first1_q<=first1;words0_q<=words0[11:0];words1_q<=words1[11:0];
 end
 // Delayed packing only needs bank ownership/half-pair checks. Wide count
 // comparisons were registered with the associated word or completion.
 wire [1:0] descriptor_legal=count_legal_q;
 wire legal_word=index_legal_q && !pending[bank_q] && !done_q[bank_q];
 wire pair_arrival=valid_q && index_q[0];
 wire selected=pending[0] ? 1'b0 : 1'b1;
 // Expected next index is derived from the offered index, not a counter
 // feedback increment gated by the full validator. Invalid offers still fault
 // the epoch; this bookkeeping is never used to make invalid data valid.
 genvar bank_number;
 generate for(bank_number=0;bank_number<2;bank_number=bank_number+1)begin: indices
  always @(posedge clk)begin
   if(reset || bank_done[bank_number])count[bank_number]<=0;
   else if(word_valid && word_bank==bank_number)count[bank_number]<=word_index+1'b1;
  end
  // Payload capture is independent of the validator. The validity state
  // and packet offer still reject malformed input and invalidate its epoch.
  always @(posedge clk)begin
   // Unpublished payload need not reset; half_valid and offer do reset.
   if(valid_q && !index_q[0] && bank_q==bank_number)begin
    half[bank_number]<=data_q;
    pair_address[bank_number]<=(bank_number ? 14'd1280 : 14'd0)+{3'b0,index_q[11:1]};
   end
  end
  // Descriptor payload is speculative until pending is validated below.
  // An invalid descriptor faults the epoch; do not put count equality on
  // the wide metadata register enable path.
  always @(posedge clk)begin
   if(done_q[bank_number])begin
    first[bank_number]<=bank_number ? first1_q : first0_q;
    words[bank_number]<=bank_number ? {2'b0,words1_q} : {2'b0,words0_q};
   end
  end
 end endgenerate
 reg offer=0;
 reg [79:0] pair_candidate=0,tail_candidate=0,meta_candidate=0;
 reg pair_selected=0;
 reg packet_metadata=0;
 // Separate construction from selection; offer follows the same pipeline.
 always @(posedge clk)begin
  // Build both data alternatives before the validated selector arrives.
  // Validation still chooses the packet, with the same offer latency.
  pair_candidate<={1'b0,bank_q,pair_address[bank_q],data_q,half[bank_q]};
  tail_candidate<={1'b0,selected,pair_address[selected],32'b0,half[selected]};
  pair_selected<=pair_arrival;
  meta_candidate<={1'b1,selected,words[selected],{(64-ORDINAL_BITS){1'b0}},first[selected]};
  packet_metadata<=!pair_arrival && !half_valid[selected];
  if(reset)begin push<=0;packet<=0;end
  else begin
   push<=offer;
   packet<=packet_metadata ? meta_candidate : pair_selected ? pair_candidate : tail_candidate;
  end
 end
 // Independent sticky validators avoid priority selection between unrelated
 // word and descriptor errors. Their OR preserves same-edge fault reporting.
 reg [2:0] fault_flags=0;
 assign fault=|fault_flags;
 always @(posedge clk)begin
  if(reset)fault_flags<=0;
  else begin
   if(valid_q && (!legal_word || (index_q[0] && !half_valid[bank_q]) ||
      (!index_q[0] && half_valid[bank_q])))fault_flags[0]<=1;
   if(done_q[0] && (pending[0] || !descriptor_legal[0]))fault_flags[1]<=1;
   if(done_q[1] && (pending[1] || !descriptor_legal[1]))fault_flags[2]<=1;
  end
 end
 integer b;
 always @(posedge clk)begin
  offer<=0;
  if(reset)begin
   pending<=0;half_valid<=0;
  end else if(!fault)begin
   for(b=0;b<2;b=b+1)if(done_q[b])begin
    // Private bookkeeping may advance on malformed input; fault prevents
    // subsequent offers. No descriptor is made public by setting pending.
    pending[b]<=1;
   end
   if(valid_q)begin
    half_valid[bank_q]<=!index_q[0];
   end
   if(pair_arrival)begin
    offer<=legal_word;
   end else if(|pending)begin
    offer<=1;
    if(half_valid[selected])begin
     half_valid[selected]<=0;
    end else begin
     pending[selected]<=0;
    end
   end
  end
 end
endmodule

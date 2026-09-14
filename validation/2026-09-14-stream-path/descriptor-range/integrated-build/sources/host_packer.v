// 250 MHz, non-backpressurable word input. Bank completion must arrive AFTER
// its final word, with stable first/count metadata. Ownership is external:
// no reuse until RAM publication and ARM release. Common epoch reset required.
// Output packets feed sram_host_fifo; FIFO overflow invalidates the epoch.
module sram_host_packer(
 input wire clk,reset,input wire word_valid,word_bank,
 input wire [31:0] word_data,input wire [11:0] word_index,
 input wire [1:0] bank_done,
 input wire [63:0] first0,first1,input wire [19:0] words0,words1,
 output reg push=0,output reg [79:0] packet=0,output reg fault=0
);
 reg [11:0] count[0:1];
 reg [31:0] half[0:1];
 reg [13:0] pair_address[0:1];
 reg [1:0] half_valid=0,pending=0;
 reg [63:0] first[0:1];reg [13:0] words[0:1];
 reg valid_q=0,bank_q=0,index_legal_q=0;
 reg [31:0] data_q=0;reg [11:0] index_q=0;
 reg [1:0] done_q=0,count_legal_q=0;
 reg [63:0] first0_q=0,first1_q=0;
 // Reject discarded upper bits in the input stage; validate the retained
 // count in the packing stage alongside descriptor equality.
 reg [11:0] words0_q=0,words1_q=0;
 // Counts commit from the existing input stage. Index equality is checked
 // in that same stage, against the count before its current word is committed.
 wire [11:0] pending_count=index_q+1'b1;
 // Validate ordinal/count in parallel with a one-cycle input pipeline.
 // Pending/half-pair state belongs to the delayed packing stage.
 always @(posedge clk)begin
  if(reset)begin valid_q<=0;done_q<=0;index_legal_q<=0;count_legal_q<=0;end
  else begin
   valid_q<=word_valid;done_q<=bank_done;
   index_legal_q<=word_index<2560;
   count_legal_q[0]<=words0[19:12]==0;
   count_legal_q[1]<=words1[19:12]==0;
  end
  bank_q<=word_bank;data_q<=word_data;index_q<=word_index;
  first0_q<=first0;first1_q<=first1;words0_q<=words0[11:0];words1_q<=words1[11:0];
 end
 // While fault is clear, count is at most 2560: it is reset to zero or
 // comes from a valid index (<2560) plus one. An invalid index faults on
 // the same edge that commits its count. Equality therefore enforces the
 // descriptor upper bound without a second magnitude comparator.
 wire [1:0] descriptor_legal={count_legal_q[1] && words1_q!=0 && words1_q==count[1],
                              count_legal_q[0] && words0_q!=0 && words0_q==count[0]};
 wire legal_word=index_legal_q && index_q==count[bank_q] && !pending[bank_q] && !done_q[bank_q];
 wire complete_pair=valid_q && legal_word && index_q[0];
 wire selected=pending[0] ? 1'b0 : 1'b1;
 // Expected next index is derived from the offered index, not a counter
 // feedback increment gated by the full validator. Invalid offers still fault
 // the epoch; this bookkeeping is never used to make invalid data valid.
 genvar bank_number;
 generate for(bank_number=0;bank_number<2;bank_number=bank_number+1)begin: indices
  always @(posedge clk)begin
   if(reset || done_q[bank_number])count[bank_number]<=0;
   else if(valid_q && bank_q==bank_number)count[bank_number]<=pending_count;
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
 end endgenerate
 reg offer=0;
 reg [79:0] data_candidate=0,meta_candidate=0;
 reg packet_metadata=0;
 // Separate construction from selection; offer follows the same pipeline.
 always @(posedge clk)begin
  data_candidate<=complete_pair ? {1'b0,bank_q,pair_address[bank_q],data_q,half[bank_q]} :
                                 {1'b0,selected,pair_address[selected],32'b0,half[selected]};
  meta_candidate<={1'b1,selected,words[selected],first[selected]};
  packet_metadata<=!complete_pair && !half_valid[selected];
  if(reset)begin push<=0;packet<=0;end
  else begin
   push<=offer;
   packet<=packet_metadata ? meta_candidate : data_candidate;
  end
 end
 integer b;
 always @(posedge clk)begin
  offer<=0;
  if(reset)begin
   fault<=0;pending<=0;half_valid<=0;
   for(b=0;b<2;b=b+1)begin first[b]<=0;words[b]<=0;end
  end else if(!fault)begin
   for(b=0;b<2;b=b+1)if(done_q[b])begin
    if(pending[b] || !descriptor_legal[b])fault<=1;
    else begin pending[b]<=1;first[b]<=b ? first1_q : first0_q;words[b]<=b ? {2'b0,words1_q} : {2'b0,words0_q};end
   end
   if(valid_q)begin
    if(!legal_word || (index_q[0] && !half_valid[bank_q]) ||
       (!index_q[0] && half_valid[bank_q]))fault<=1;
    else begin
     if(!index_q[0])begin
      half_valid[bank_q]<=1;
     end else half_valid[bank_q]<=0;
    end
   end
   if(complete_pair)begin
    offer<=1;
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

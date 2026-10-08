// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
// Envelope recall (ADR-IMAGE-REGROUP-DECIMATION), on the 125 MHz side of the
// host buffer packer: each packet holds two recalled words (low word first).
// After skipping `skip` warm-up packets, every `bucket` packets reduce to one
// minimum and maximum per channel, written straight into the host buffer.
// Raw words hold CH1[n],CH2[n],CH1[n+1],CH2[n+1]; a bucket becomes one raw word
// {max2,max1,min2,min1}, i.e. the samples (min, max) per channel, and two
// buckets share a buffer entry. Q8.8 words hold CH1 low, CH2 high; a bucket
// becomes the entry {max2,max1,min2,min1} (minima in the low word). `words`
// counts the buffer words written. Settings are held by the host from before
// the recall until it completes; start clears the state. bucket >= 1.
// TRLC-LINKS: REQ-SDS-010, REQ-SDS-040
module envelope_reduce #(parameter AW=11)(
 input wire clk,start,q8,
 input wire [18:0] bucket,input wire [3:0] skip,
 input wire in_valid,input wire [63:0] in_data,
 output reg out_write=0,output reg [AW-1:0] out_addr=0,output reg [63:0] out_data=0,
 output reg [AW:0] words=0
);
 function [7:0] lo8(input [7:0] a,b);lo8=a<b?a:b;endfunction
 function [7:0] hi8(input [7:0] a,b);hi8=a>b?a:b;endfunction
 function [15:0] lo16(input [15:0] a,b);lo16=a<b?a:b;endfunction
 function [15:0] hi16(input [15:0] a,b);hi16=a>b?a:b;endfunction
 reg [3:0] skip_left=0;
 reg [18:0] left=0;
 reg stage_valid=0,stage_first=0,stage_last=0,done=0,have_pair=0;
 // Per-packet candidates: raw keeps four samples per channel, Q8.8 two.
 reg [15:0] c_min1=0,c_max1=0,c_min2=0,c_max2=0;
 reg [15:0] min1=0,max1=0,min2=0,max2=0;
 reg [31:0] pending=0;
 // Packets arrive from the 250 MHz packer; register them before any logic.
 reg packet_valid=0;reg [63:0] packet=0;
 wire accept=packet_valid && skip_left==0;
 wire [31:0] w0=packet[31:0],w1=packet[63:32];
 wire [7:0] r_min1=lo8(lo8(w0[7:0],w0[23:16]),lo8(w1[7:0],w1[23:16])),r_max1=hi8(hi8(w0[7:0],w0[23:16]),hi8(w1[7:0],w1[23:16]));
 wire [7:0] r_min2=lo8(lo8(w0[15:8],w0[31:24]),lo8(w1[15:8],w1[31:24])),r_max2=hi8(hi8(w0[15:8],w0[31:24]),hi8(w1[15:8],w1[31:24]));
 wire [15:0] n_min1=stage_first ? c_min1 : lo16(min1,c_min1),n_max1=stage_first ? c_max1 : hi16(max1,c_max1);
 wire [15:0] n_min2=stage_first ? c_min2 : lo16(min2,c_min2),n_max2=stage_first ? c_max2 : hi16(max2,c_max2);
 always @(posedge clk)begin
  out_write<=0;
  packet_valid<=in_valid;packet<=in_data;
  // Stage 1: packet candidates and bucket position.
  stage_valid<=accept;
  if(packet_valid && skip_left!=0)skip_left<=skip_left-1'b1;
  if(accept)begin
   stage_first<=left==bucket;stage_last<=left==1;
   left<=left==1 ? bucket : left-1'b1;
   if(q8)begin
    c_min1<=lo16(w0[15:0],w1[15:0]);c_max1<=hi16(w0[15:0],w1[15:0]);
    c_min2<=lo16(w0[31:16],w1[31:16]);c_max2<=hi16(w0[31:16],w1[31:16]);
   end else begin
    c_min1<={8'd0,r_min1};c_max1<={8'd0,r_max1};c_min2<={8'd0,r_min2};c_max2<={8'd0,r_max2};
   end
  end
  // Stage 2 accumulates; stage 3, a clock later, reads the completed bucket
  // (the next bucket's first packet replaces it only at the end of that clock).
  done<=stage_valid && stage_last;
  if(stage_valid)begin min1<=n_min1;max1<=n_max1;min2<=n_min2;max2<=n_max2;end
  if(done)begin
   if(q8)begin
    out_write<=1;out_data<={max2,max1,min2,min1};
   end else if(have_pair)begin
    have_pair<=0;out_write<=1;out_data<={max2[7:0],max1[7:0],min2[7:0],min1[7:0],pending};
   end else begin have_pair<=1;pending<={max2[7:0],max1[7:0],min2[7:0],min1[7:0]};end
  end
  if(out_write)begin out_addr<=out_addr+1'b1;words<=words+2'd2;end
  if(start)begin
   skip_left<=skip;left<=bucket;packet_valid<=0;stage_valid<=0;done<=0;have_pair<=0;out_write<=0;out_addr<=0;words<=0;
  end
 end
endmodule

// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-SRAM-TOP
`timescale 1ns/1ps
// ADR-STREAM-RING: the time of the record ring's newest word, on the
// stream's clock. While the stream runs on chip and the SRAM holds the
// record as a ring, this counts raw words (4 ns, two samples a channel) from
// the stream's start - the same origin the stream's own words count from
// (bit periods, edge windows) - and latches the count on every ring write.
// Once the ring is frozen (by its FPGA trigger and post-trigger count, or by
// the host's stop) the latch holds the newest ring word's raw-word time, so
// with the record's length and the decimation the host turns a stream
// ordinal into the ring word that holds it, with no search and no guess:
// ring word i from the end lies at time last - i*2^(n-1) raw words (minus
// the decimator's fixed delay). The count wraps after 17.2 s; the host
// unwraps it against its own stream time.
//
// Timing at 250 MHz: raw words may come every clock, so only the low byte
// counts every word; the high 24 bits step on a registered "low byte is all
// ones" flag, to a sum computed every clock ahead of need (a carry comes at
// most every 256 clocks, so the sum has settled by then), and the latch
// reads both parts of the same count.
// TRLC-LINKS: REQ-SDS-035
module ring_stamp(
 input clk,active,raw_valid,write,
 output reg [31:0] last=0
);
 (* preserve, dont_merge *) reg run=0;
 reg [7:0] lo=0;reg [23:0] hi=0,hi_next=1;reg lo_ones=0,v=0,w=0;
 always @(posedge clk)begin
  run<=active;
  v<=run && raw_valid;w<=write;
  hi_next<=hi+1'b1;
  if(!run)begin lo<=0;hi<=0;lo_ones<=0;end
  else if(v)begin
   lo<=lo+1'b1;lo_ones<=lo==8'hfe;
   if(lo_ones)hi<=hi_next;
  end
  if(w)last<={hi,lo};
 end
endmodule

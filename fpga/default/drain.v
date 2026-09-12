// drain.v -- the BURST pop port on the record read port (C2 domain): the windowed logical pointer
// over the wrapped record, BURST_REMAIN, OP_REWIND, DRAIN_STAT.POPS and POP_MON (06-TIERS s1.6 / s2
// on the v2.1 read port; shape after owned-fpga fpga/standard/drain.v: one pointer, one pop per
// nOE rise, saturating at the window end, stream mode chasing the write pointer with unconditional
// advance -- commit 6b36253's EDMA fix).
//
// WINDOW (triggered / halted record, RUN.STREAM = 0)
//   The drain covers the logical words [win_start, win_end) of the frozen record, win_start =
//   DRAIN_START.IDX and win_end = rec_len when DRAIN_LEN = 0, else min(rec_len, DRAIN_START +
//   DRAIN_LEN). Both are LATCHED from the live DRAIN_START/DRAIN_LEN registers at DONE (the rise of
//   the synchronized `valid`) and at OP_REWIND, which also puts the pointer back to win_start; a
//   write to DRAIN_START/DRAIN_LEN alone changes nothing until the next DONE or REWIND. The reset
//   window (0, 0) is the whole record = the v2.1 drain, word for word.
//   rd_addr = (rec_start + ptr) mod depth, clamped at the last word of the window once drained (a
//   flat tail: extra pops return the last word again, never re-wrap). REMAIN = win_end - ptr counts
//   down to 0; a window that starts at or beyond rec_len is empty (REMAIN 0 from the start).
//   READY follows the SYNCHRONIZED valid one clk late (valid_q), i.e. it rises on the very edge
//   that latches the window, so BURST_REMAIN never shows {READY=1, REMAIN=0} for the cycle between
//   DONE and the latch (v2.2 review finding). "at the window end" (at_end) and "the window's last
//   word" (last_l) are REGISTERS tracked exactly with ptr / win_end (recomputed at every edge that
//   changes either), so the M9K port-B address path is mux + adder + wrap from registers -- the
//   v2.1 shape -- and not compare + mux + adder + wrap (v2.2 flow 3: clk +0.212 ns on that path).
// STREAM (RUN.STREAM = 1): ptr is the physical read pointer chasing wr_ptr_s; REMAIN = (wr_ptr_s -
//   ptr) mod depth; READY always 1; every pop advances (the EDMA prefetch reads ahead). REWIND moves
//   the physical pointer to DRAIN_START mod depth.
// DRAIN_STAT.POPS: every pop strobe since GO / REWIND / RESET, 16-bit wrapping (the pointer-delta
//   ground truth of rung R1: pops that the host did not issue cannot exist, pops it issued are all
//   counted, underruns included).
// POP_MON: MIN_GAP = the shortest distance in clk cycles between two consecutive pop strobes since
//   GO (255 = none, saturating); UNDERRUN = pops that found no word since GO (window drained, no
//   record, or stream avail = 0 -- the last word is returned), saturating at 255. Cleared by GO and
//   RESET, not by REWIND.
// BURST_ALIAS (0x00) folds into `pop` in the generated regmux.vh, so nothing is done here.

`timescale 1ns/1ps
`include "regs.vh"

module drain (
    input  wire               clk,
    input  wire               arm,          // OP_GO accepted (pointer to 0, statistics cleared)
    input  wire               rst,          // OP_RESET
    input  wire               rewind,       // OP_REWIND
    input  wire               valid,        // synchronized: a frozen record is present
    input  wire               stream_on,
    input  wire [`ADDR_W-1:0] rec_start,    // frozen (stable while valid)
    input  wire [15:0]        rec_len,
    input  wire [`ADDR_W-1:0] wr_ptr_s,     // synchronized live write pointer (stream)
    input  wire [`ADDR_W-1:0] drain_start,  // DRAIN_START.IDX (live register)
    input  wire [15:0]        drain_len,    // DRAIN_LEN.LEN (live register)
    input  wire               pop,          // pop_BURST (alias folded in)
    output wire [`ADDR_W-1:0] rd_addr,
    output wire [`ADDR_W-1:0] rd_ptr,       // for the capture-side overflow check
    output wire [15:0]        remain_word,  // {READY, REMAIN[14:0]}
    output reg  [15:0]        pops,         // DRAIN_STAT.POPS
    output wire [15:0]        pop_mon       // {UNDERRUN, MIN_GAP}
);
    localparam [`ADDR_W-1:0] REC_LAST = `REC_DEPTH - 1;
    reg [`ADDR_W-1:0] ptr       = {`ADDR_W{1'b0}};
    reg [15:0]        win_end   = 16'd0;      // latched window end (logical, <= rec_len)
    reg [`ADDR_W-1:0] last_l    = {`ADDR_W{1'b0}};   // win_end - 1 (0 for an empty window), latched with it
    reg               at_end    = 1'b1;       // ptr >= win_end, tracked exactly (header)
    reg               valid_q   = 1'b0;

    // ---- window latch value (from the live registers and the frozen record length) ----
    wire [16:0] win_sum  = {2'b00, drain_start} + {1'b0, drain_len};
    wire [15:0] win_end_nx = (drain_len == 16'd0 || win_sum > {1'b0, rec_len}) ? rec_len : win_sum[15:0];
    wire [`ADDR_W-1:0] last_nx = (win_end_nx == 16'd0) ? {`ADDR_W{1'b0}} : (win_end_nx[`ADDR_W-1:0] - 1'b1);
    wire        done_evt = valid & ~valid_q;                 // DONE as seen on C2
    wire        latch    = rewind | done_evt;
    // the pointer after a latch: START (stream mode: clamped into the ring)
    wire [`ADDR_W-1:0] ptr_latch = (stream_on && drain_start > REC_LAST) ? REC_LAST : drain_start;

    // ---- address / remain (window mode) ----
    wire [15:0]        ptr16    = {1'b0, ptr};
    wire [`ADDR_W-1:0] ptr_inc  = ptr + 1'b1;                                  // window mode (ptr < win_end)
    wire [`ADDR_W-1:0] ptr_wrap = (ptr == REC_LAST) ? {`ADDR_W{1'b0}} : ptr_inc; // stream mode
    wire [`ADDR_W-1:0] lin    = at_end ? last_l : ptr;
    wire [`ADDR_W:0]   sum    = {1'b0, rec_start} + {1'b0, lin};
    wire [`ADDR_W-1:0] phys   = (sum >= `REC_DEPTH) ? (sum[`ADDR_W-1:0] - `REC_DEPTH) : sum[`ADDR_W-1:0];

    wire [`ADDR_W-1:0] avail_s = (wr_ptr_s >= ptr) ? (wr_ptr_s - ptr) : (`REC_DEPTH - ptr + wr_ptr_s);
    wire [15:0]        remain  = stream_on ? {1'b0, avail_s}
                               : (at_end ? 16'd0 : (win_end - ptr16));
    wire               ready   = stream_on | valid_q;        // valid_q: the edge that latched the window
    wire               empty   = stream_on ? (avail_s == {`ADDR_W{1'b0}}) : (~valid_q | at_end);

    assign rd_addr     = stream_on ? ptr : phys;
    assign rd_ptr      = ptr;
    assign remain_word = ready ? {1'b1, remain[14:0]} : 16'h0000;

    // ---- pointer + window (at_end / last_l recomputed wherever ptr or win_end changes) ----
    always @(posedge clk) begin
        valid_q <= valid;
        if (rst || arm) begin
            ptr <= {`ADDR_W{1'b0}};
            win_end <= 16'd0; last_l <= {`ADDR_W{1'b0}}; at_end <= 1'b1;
        end else if (latch) begin
            ptr     <= ptr_latch;
            win_end <= win_end_nx;
            last_l  <= last_nx;
            at_end  <= ({1'b0, ptr_latch} >= win_end_nx);
        end else if (stream_on) begin
            if (pop) begin ptr <= ptr_wrap; at_end <= ({1'b0, ptr_wrap} >= win_end); end
        end else if (valid_q && pop && !at_end) begin
            ptr <= ptr_inc; at_end <= ({1'b0, ptr_inc} >= win_end);
        end
    end

    // ---- statistics ----
    reg [7:0] underrun = 8'd0;
    reg [7:0] min_gap  = 8'hff;
    reg [7:0] gap_cnt  = 8'hff;      // cycles since the last pop, saturating
    reg       seen_pop = 1'b0;
    initial pops = 16'd0;
    always @(posedge clk) begin
        if (rst || arm) begin
            pops <= 16'd0; underrun <= 8'd0; min_gap <= 8'hff; gap_cnt <= 8'hff; seen_pop <= 1'b0;
        end else begin
            if (rewind) pops <= 16'd0;
            else if (pop) pops <= pops + 16'd1;
            if (pop) begin
                seen_pop <= 1'b1;
                gap_cnt  <= 8'd1;
                if (seen_pop && gap_cnt < min_gap) min_gap <= gap_cnt;
                if (empty && underrun != 8'hff) underrun <= underrun + 8'd1;
            end else if (gap_cnt != 8'hff)
                gap_cnt <= gap_cnt + 8'd1;
        end
    end
    assign pop_mon = {underrun, min_gap};
endmodule

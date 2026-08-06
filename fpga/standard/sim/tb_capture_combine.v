// tb_capture_combine.v — focused finalize proof for the COMBINE branch of capture.v
// Two runs:
//  A) combine_en=1: arm, dwell (no ticks), then stream a synthetic bin drain and prove
//     capture finalizes on the EXACT drain length: coherent=1, r_done=1, rec_len==3072,
//     and NOT one tick early / late.
//  B) combine_en=0: a normal NORM trigger + post fill, prove finalize timing is unchanged
//     (rec_len == pre+post, done at the exact post-count) — the byte-identical guard.
`timescale 1ns/1ps
`default_nettype none
`include "regs.vh"

module tb_capture_combine;
    localparam integer DRAINLEN = 3072;   // NBINS*WPB the fabric presents

    reg clk = 1'b0;
    always #5 clk = ~clk;                  // 100 MHz sim clock

    reg         arm=0, halt=0, rst=0, stream_on=0, combine_en=0;
    reg  [15:0] pre_work_w=16'd0, post_work_w=16'd0;
    reg  [15:0] cap_word=16'd0;
    reg         cap_tick=0;
    reg         mode_norm=0, trig_rise=0, decode_trig=0;
    reg  [7:0]  trig_level=8'd128;
    reg  [`ADDR_W-1:0] rd_addr=0;

    wire [15:0] rd_data;
    wire        filling, smp_valid;
    wire        r_valid, r_trig, r_done, coherent;
    wire [10:0] fill_out;
    wire [`ADDR_W-1:0] trig_idx;
    wire [15:0] trig_frac;
    wire [15:0] rec_len;
    wire        frame_done;
    wire [`ADDR_W-1:0] wr_ptr;

    capture dut (
        .clk(clk), .arm(arm), .halt(halt), .rst(rst), .stream_on(stream_on),
        .combine_en(combine_en),
        .pre_work_w(pre_work_w), .post_work_w(post_work_w),
        .cap_word(cap_word), .cap_tick(cap_tick),
        .mode_norm(mode_norm), .trig_rise(trig_rise), .trig_level(trig_level),
        .decode_trig(decode_trig),
        .rd_addr(rd_addr), .rd_data(rd_data),
        .filling(filling), .smp_valid(smp_valid),
        .r_valid(r_valid), .r_trig(r_trig), .r_done(r_done), .coherent(coherent),
        .fill_out(fill_out), .trig_idx(trig_idx), .trig_frac(trig_frac),
        .rec_len(rec_len), .frame_done(frame_done), .wr_ptr(wr_ptr)
    );

    integer errors = 0;
    integer i;
    integer done_at_tick;

    task pulse_arm; begin
        @(posedge clk); arm<=1'b1; @(posedge clk); arm<=1'b0;
    end endtask

    // one committed tick (word value = index+1 so mem is checkable), with a gap so it
    // mirrors the real drain's non-contiguous bin_tick (handshake gaps between bins).
    task one_tick(input [15:0] val); begin
        @(posedge clk); cap_word<=val; cap_tick<=1'b1;
        @(posedge clk); cap_tick<=1'b0;
        @(posedge clk);                 // idle gap (no tick) — must NOT advance counts
    end endtask

    initial begin
        // ---------- RUN A: combine finalize ----------
        combine_en = 1'b1; mode_norm = 1'b0;
        pre_work_w = 16'd100; post_work_w = 16'd99;   // deliberately != 3072; must be ignored
        pulse_arm;
        // DWELL: sit in FILL with no ticks for a while; nothing must finalize.
        repeat (40) @(posedge clk);
        if (!filling)  begin errors=errors+1; $display("FAIL A: not filling during dwell"); end
        if (coherent || r_done) begin errors=errors+1; $display("FAIL A: premature finalize in dwell"); end

        // stream DRAINLEN-1 words first; capture must NOT have finalized yet (not early).
        for (i=0; i<DRAINLEN-1; i=i+1) one_tick(i[15:0]+16'd1);
        if (coherent || r_done)
            begin errors=errors+1; $display("FAIL A: finalized EARLY after %0d ticks", DRAINLEN-1); end
        // the 3072nd (last) word must trip the finalize.
        one_tick(DRAINLEN[15:0]);
        repeat (4) @(posedge clk);      // let the post_full finalize edge latch

        if (!coherent)          begin errors=errors+1; $display("FAIL A: coherent never asserted"); end
        if (!r_done)            begin errors=errors+1; $display("FAIL A: r_done never asserted"); end
        if (rec_len !== DRAINLEN) begin errors=errors+1; $display("FAIL A: rec_len=%0d want %0d", rec_len, DRAINLEN); end
        // extra late ticks after freeze must not extend the record (static freeze).
        one_tick(16'hFFFF);
        if (rec_len !== DRAINLEN) begin errors=errors+1; $display("FAIL A: rec_len grew after freeze=%0d", rec_len); end
        $display("RUN A: coherent=%b r_done=%b rec_len=%0d (finalized on the exact 3072nd word)",
                 coherent, r_done, rec_len);

        // ---------- RUN B: combine_en=0 byte-identical finalize timing ----------
        rst<=1'b1; @(posedge clk); rst<=1'b0; @(posedge clk);
        combine_en = 1'b0; mode_norm = 1'b1;      // NORM so trig_rise anchors
        pre_work_w = 16'd4; post_work_w = 16'd6;   // small window: 4 pre + 6 post = 10
        pulse_arm;
        // fill 4 pre-trigger samples (no trig), then a trig tick, then 5 more post ticks.
        for (i=0; i<4; i=i+1) one_tick(16'hA000 + i[15:0]);
        // trigger tick: assert trig_rise coincident with a committed tick
        @(posedge clk); cap_word<=16'hB000; cap_tick<=1'b1; trig_rise<=1'b1;
        @(posedge clk); cap_tick<=1'b0; trig_rise<=1'b0;
        @(posedge clk);
        if (!r_trig) begin errors=errors+1; $display("FAIL B: trigger not accepted"); end
        // 5 more post ticks (trigger sample was post #1, need 5 more for post=6)
        for (i=0; i<5; i=i+1) one_tick(16'hC000 + i[15:0]);
        repeat (4) @(posedge clk);
        if (!coherent) begin errors=errors+1; $display("FAIL B: normal finalize failed"); end
        if (!r_done)   begin errors=errors+1; $display("FAIL B: r_done not set"); end
        // rec_len = pre(4) + post(6) = 10
        if (rec_len !== 16'd10) begin errors=errors+1; $display("FAIL B: rec_len=%0d want 10", rec_len); end
        $display("RUN B: coherent=%b r_done=%b r_trig=%b rec_len=%0d trig_idx=%0d",
                 coherent, r_done, r_trig, rec_len, trig_idx);

        if (errors==0) $display("ALL TESTS PASSED");
        else           $display("TESTS FAILED: %0d error(s)", errors);
        $finish;
    end

    initial begin
        #2000000; $display("TIMEOUT"); $finish;
    end
endmodule
`default_nettype wire

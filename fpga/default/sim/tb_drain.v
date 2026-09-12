// tb_drain.v -- pop + remain semantics on the drain window: the full record as in v2.1 (wrapped,
// end clamp, READY gating, READY and REMAIN rising on the same edge after DONE -- cycle-exact),
// DRAIN_START/DRAIN_LEN windows latched at DONE and at REWIND (inside
// the wrap, clipped at the record end, empty), stream mode chasing the write pointer, DRAIN_STAT
// pops, POP_MON min gap / underruns, arm / reset clearing.
`timescale 1ns/1ps
`include "regs.vh"
module tb_drain;
    reg clk = 0; always #6.25 clk = ~clk;
    reg arm = 0, rst = 0, rewind = 0, valid = 0, stream_on = 0, pop = 0;
    reg [14:0] rec_start = 0, wr_ptr_s = 0, dstart = 0; reg [15:0] rec_len = 0, dlen = 0;
    wire [14:0] rd_addr, rd_ptr; wire [15:0] remain, pops, mon;
    drain dut (.clk(clk), .arm(arm), .rst(rst), .rewind(rewind), .valid(valid), .stream_on(stream_on),
        .rec_start(rec_start), .rec_len(rec_len), .wr_ptr_s(wr_ptr_s), .drain_start(dstart), .drain_len(dlen),
        .pop(pop), .rd_addr(rd_addr), .rd_ptr(rd_ptr), .remain_word(remain), .pops(pops), .pop_mon(mon));
    integer errors = 0, i, n_bad;
    task check(input cond, input [8*56-1:0] msg); begin if (!cond) begin errors = errors + 1; $display("FAIL: %0s", msg); end end endtask
    task do_pop; begin @(posedge clk); pop <= 1; @(posedge clk); pop <= 0; @(posedge clk); #1; end endtask
    task do_arm; begin @(posedge clk); arm <= 1; @(posedge clk); arm <= 0; @(posedge clk); #1; end endtask
    task do_rewind; begin @(posedge clk); rewind <= 1; @(posedge clk); rewind <= 0; @(posedge clk); #1; end endtask
    task do_rst; begin @(posedge clk); rst <= 1; @(posedge clk); rst <= 0; @(posedge clk); #1; end endtask
    // DONE as the C2 side sees it: valid rises while the frozen record words are stable
    task done(input [14:0] start, input [15:0] len); begin @(posedge clk); valid <= 0; rec_start <= start; rec_len <= len; @(posedge clk); @(posedge clk); valid <= 1; @(posedge clk); @(posedge clk); #1; end endtask
    integer exp;
    function [14:0] paddr(input integer lin); integer p; begin p = rec_start + lin; if (p >= `REC_DEPTH) p = p - `REC_DEPTH; paddr = p[14:0]; end endfunction
    initial begin
        #50; check(remain == 16'h0000, "no record: READY 0");
        check(mon == 16'h00ff && pops == 0, "reset: MIN_GAP 255, UNDERRUN 0, POPS 0");
        do_pop; check(rd_ptr == 0, "no record: pop ignored");
        check(pops == 1 && mon[15:8] == 8'd1, "no record: pop counted, one underrun");
        // ---- (a) whole record (window 0/0 = v2.1): wrapped record start 20400, len 200 ----
        do_arm; check(pops == 0 && mon == 16'h00ff, "arm clears the statistics");
        // DONE cycle-exact: valid rises after edge E0; E1 latches the window. READY must not show
        // up before the window (the review finding: {READY 1, REMAIN 0} for one cycle after DONE).
        @(posedge clk); valid <= 0; rec_start <= 15'd20400; rec_len <= 16'd200;
        @(posedge clk); @(posedge clk); valid <= 1;                       // E0
        #1; check(remain == 16'h0000, "a: E0: valid up, window not latched: READY still 0");
        @(posedge clk); #1;                                                // E1
        check(remain == {1'b1, 15'd200}, "a: E1: READY and REMAIN 200 on the same edge");
        check(rd_addr == 15'd20400, "a: E1: first address with them");
        @(posedge clk); #1;
        check(remain == {1'b1, 15'd200}, "a: READY + REMAIN 200");
        check(rd_addr == 15'd20400, "a: first address");
        n_bad = 0;
        for (i = 0; i < 200; i = i + 1) begin
            if (rd_addr != paddr(i) || remain != {1'b1, 15'd200 - i[14:0]}) n_bad = n_bad + 1;
            do_pop;
        end
        check(n_bad == 0, "a: 200 pops walk the wrapped record with a live REMAIN");
        check(remain == 16'h8000, "a: drained: READY 1 REMAIN 0");
        check(rd_addr == 15'd119, "a: drained: address clamps at the last word");
        check(pops == 16'd200 && mon[15:8] == 8'd0, "a: POPS 200, no underrun");
        do_pop; check(rd_addr == 15'd119 && remain == 16'h8000, "a: extra pop is flat");
        check(pops == 16'd201 && mon[15:8] == 8'd1, "a: the flat pop is an underrun");
        check(mon[7:0] == 8'd3, "a: MIN_GAP 3 clk (the do_pop cadence)");
        @(posedge clk); valid <= 0; @(posedge clk); @(posedge clk); #1; check(remain == 16'h0000, "a: valid dropped: READY 0");
        // ---- (b) a window inside the wrapped record: start 150 len 30 -> logical 150..179 ----
        dstart = 15'd150; dlen = 16'd30;
        done(15'd20400, 16'd200);
        check(remain == {1'b1, 15'd30} && rd_addr == paddr(150), "b: window latched at DONE: REMAIN 30, first address");
        n_bad = 0;
        for (i = 0; i < 30; i = i + 1) begin if (rd_addr != paddr(150 + i) || remain != {1'b1, 15'd30 - i[14:0]}) n_bad = n_bad + 1; do_pop; end
        check(n_bad == 0, "b: 30 pops walk the window");
        check(remain == 16'h8000 && rd_addr == paddr(179), "b: drained: flat at the window's last word");
        // ---- (c) REWIND with a new window: registers written after DONE apply only at REWIND ----
        dstart = 15'd190; dlen = 16'd100;                                  // clipped at rec_len 200
        #50; check(remain == 16'h8000, "c: DRAIN_* writes alone change nothing");
        do_rewind;
        check(pops == 0, "c: REWIND clears POPS");
        check(remain == {1'b1, 15'd10} && rd_addr == paddr(190), "c: REWIND: window 190..199 (clipped)");
        for (i = 0; i < 10; i = i + 1) do_pop;
        check(remain == 16'h8000 && rd_addr == paddr(199) && pops == 10, "c: clipped window drained");
        dstart = 15'd0; dlen = 16'd0; do_rewind;
        check(remain == {1'b1, 15'd200} && rd_addr == paddr(0), "c: REWIND to the whole record");
        // ---- (d) empty window: start beyond the record ----
        dstart = 15'd200; dlen = 16'd5; do_rewind;
        check(remain == 16'h8000 && rd_addr == paddr(199), "d: start >= rec_len: REMAIN 0, last word");
        do_pop; check(mon[15:8] == 8'd2 && rd_ptr == 15'd200, "d: pop is an underrun, pointer stays");
        dstart = 15'd0; dlen = 16'd0;
        // ---- (e) UNDERRUN saturates at 255, MIN_GAP tracks the closest pair ----
        do_arm; done(15'd0, 16'd1);
        do_pop;                                                            // the one word
        for (i = 0; i < 300; i = i + 1) begin @(posedge clk); pop <= 1; @(posedge clk); pop <= 0; end
        @(posedge clk); #1;
        check(mon[15:8] == 8'hff, "e: UNDERRUN saturates at 255");
        check(mon[7:0] == 8'd2, "e: MIN_GAP 2 (pops every other clk)");
        check(pops == 16'd301, "e: POPS counts underruns too");
        for (i = 0; i < 4; i = i + 1) begin @(posedge clk); pop <= 1; end   // 4 consecutive pops: gap 1
        @(posedge clk); pop <= 0; @(posedge clk); #1; check(mon[7:0] == 8'd1, "e: MIN_GAP 1");
        do_arm; check(mon == 16'h00ff && pops == 0, "e: arm clears again");
        // ---- (f) stream ----
        @(posedge clk); valid <= 0; stream_on <= 1; @(posedge clk); do_arm; wr_ptr_s = 15'd5; #20;
        check(remain == {1'b1, 15'd5} && rd_addr == 0, "f: stream: avail 5");
        do_pop; do_pop; check(remain == {1'b1, 15'd3} && rd_addr == 2, "f: stream: pops advance");
        check(pops == 2 && mon[15:8] == 0, "f: stream: 2 pops, no underrun");
        wr_ptr_s = 15'd1; #20; check(remain == {1'b1, 15'd20479}, "f: stream: avail across the wrap");
        wr_ptr_s = 15'd2; #20; check(remain == 16'h8000, "f: stream: avail 0");
        do_pop; check(rd_ptr == 3 && mon[15:8] == 8'd1, "f: stream: an empty pop still advances (EDMA prefetch) and counts");
        dstart = 15'd100; do_rewind; check(rd_ptr == 15'd100 && pops == 0, "f: stream: REWIND moves the physical pointer");
        dstart = 15'd0;
        // wrap the read pointer: arm at 0 and pop past 20479
        do_arm; wr_ptr_s = 0;
        for (i = 0; i < 20480; i = i + 1) begin @(posedge clk); pop <= 1; @(posedge clk); pop <= 0; end
        @(posedge clk); #1; check(rd_ptr == 0, "f: stream: read pointer wraps at REC_DEPTH");
        check(pops == 16'd20480, "f: POPS 20480");
        // ---- (g) reset ----
        stream_on = 0; done(15'd0, 16'd10); do_pop; do_pop; do_rst;
        check(rd_ptr == 0 && pops == 0 && mon == 16'h00ff, "g: reset rewinds and clears");
        if (errors == 0) $display("PASS tb_drain"); else $display("FAIL tb_drain: %0d errors", errors);
        $finish;
    end
    initial begin #5000000; $display("FAIL tb_drain: timeout"); $finish; end
endmodule

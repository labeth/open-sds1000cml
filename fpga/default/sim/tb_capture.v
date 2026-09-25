// ENGMODEL-OWNER-UNIT: FU-RTL-DEFAULT-TESTS
// tb_capture.v -- pre/post window exactness, trigger position + Q16 fraction, wrapped records,
// decimation, single-channel packing, stream ring + overflow, HALT without trigger, hardware
// trigger, falling slope, RESET, and the TSRC test sources (ramp / column tag / glitch) bit-exact
// against the iface word models, in dual and single-channel mode, across the stream wrap, and
// restarting at k = 0 on a GO issued while the previous record is still filling.
`timescale 1ns/1ps
`include "regs.vh"
// TRLC-LINKS: REQ-SDS-195
module tb_capture;
    reg cap_clk = 0; always #2.5 cap_clk = ~cap_clk;
    reg rd_clk = 0;  always #6.25 rd_clk = ~rd_clk;
    reg [15:0] samp = 0; reg samp_tick = 0;
    reg arm = 0, halt = 0, rst = 0;
    reg [31:0] decim_m1 = 0; reg [14:0] pre_w = 100, post_w = 50;
    reg mode_norm = 0, stream_on = 0; reg [1:0] chmode = 0;
    reg trig_src = 0, trig_slope = 0; reg [7:0] level = 8'h80; reg [3:0] hyst = 4'd8; reg hw_sel = 0; reg trig_sense = 0; reg [1:0] tsrc = 0;
    reg [14:0] rd_ptr_s = 0, rd_addr = 0; wire [15:0] rd_data;
    wire filling, armed, triggered, done, valid, overflow, done_evt, trig_half; wire [1:0] st;
    wire [14:0] wr_ptr, rec_start, trig_idx; wire [15:0] wrote, rec_len, trig_frac;
    capture dut (.cap_clk(cap_clk), .samp(samp), .samp_tick(samp_tick), .arm(arm), .halt(halt), .rst(rst),
        .decim_m1(decim_m1), .pre_w(pre_w), .post_w(post_w), .mode_norm(mode_norm), .stream_on(stream_on),
        .chmode(chmode), .trig_src(trig_src), .trig_slope(trig_slope), .trig_level(level), .trig_hyst(hyst),
        .hw_sel(hw_sel), .trig_sense(trig_sense), .tsrc(tsrc), .rd_ptr_s(rd_ptr_s), .rd_clk(rd_clk), .rd_addr(rd_addr),
        .rd_data(rd_data), .filling(filling), .armed(armed), .triggered(triggered), .done(done), .valid(valid),
        .overflow(overflow), .wr_ptr(wr_ptr), .wrote_count(wrote), .rec_start(rec_start), .rec_len(rec_len),
        .trig_idx(trig_idx), .trig_half(trig_half), .trig_frac(trig_frac), .done_evt(done_evt), .state_dbg(st));
    integer errors = 0;
    task check(input cond, input [8*56-1:0] msg); begin if (!cond) begin errors = errors + 1; $display("FAIL: %0s", msg); end end endtask

    // sample source: CH1 ramp (step STEP), CH2 = ~CH1; one sample per cap_clk
    reg [7:0] ramp = 0; reg [7:0] step = 1; reg src_on = 1;
    always @(posedge cap_clk) begin
        if (src_on) begin samp <= {ramp, ~ramp}; samp_tick <= 1'b1; ramp <= ramp + step; end
        else samp_tick <= 1'b0;
    end
    task pulse(input integer which);   // 0 arm 1 halt 2 rst
        begin @(posedge cap_clk); case (which) 0: arm <= 1; 1: halt <= 1; 2: rst <= 1; endcase
              @(posedge cap_clk); arm <= 0; halt <= 0; rst <= 0; #1; end
    endtask
    task rd(input [14:0] a, output [15:0] v);
        begin @(posedge rd_clk); rd_addr <= a; @(posedge rd_clk); @(posedge rd_clk); #1; v = rd_data; end
    endtask
    // read the drained record (logical index i -> physical (rec_start+i) mod depth)
    task rd_log(input integer i, output [15:0] v);
        integer p; begin p = rec_start + i; if (p >= `REC_DEPTH) p = p - `REC_DEPTH; rd(p[14:0], v); end
    endtask
    reg [15:0] v, v2; integer i, n_bad;
    // the iface word models (codegen/emit/wordfmt: TsrcWord), k = the writer's word index since GO
    function [15:0] tsrc_word(input [1:0] mode, input integer k);
        integer col, row;
        begin
            col = k % `ROW_COLS; row = (k / `ROW_COLS) % 256;
            case (mode)
                `IL_CTRL_TSRC_RAMP:   tsrc_word = k[15:0];
                `IL_CTRL_TSRC_COLTAG: tsrc_word = {col[7:0], row[7:0]};
                `IL_CTRL_TSRC_GLITCH: tsrc_word = ((k % 512) == 0) ? 16'hffff : 16'h0000;
                default:              tsrc_word = 16'h0000;
            endcase
        end
    endfunction

    initial begin
        #100;
        // ---------------- (a) AUTO dual pre 100 post 50 ------------------------------
        pulse(0); wait (done); #20;
        check(rec_len == 150, "a: rec_len = pre+post");
        check(trig_idx == 100, "a: trig_idx = pre");
        check(wrote == 150, "a: wrote_count = 150 (finalize suppresses the extra write)");
        check(rec_start == 0, "a: rec_start 0");
        check(valid && done && triggered && !armed && !filling, "a: status flags");
        n_bad = 0; rd_log(0, v);
        for (i = 1; i < 150; i = i + 1) begin rd_log(i, v2); if (v2[15:8] != v[15:8] + 8'd1 || v2[7:0] != ~v2[15:8]) n_bad = n_bad + 1; v = v2; end
        check(n_bad == 0, "a: 150 consecutive ramp words");
        // ---------------- (b) NORM sw rising, wrap, fraction ------------------------
        mode_norm = 1; pre_w = 20000; post_w = 400; level = 8'h81; hyst = 4'd8; step = 2; ramp = 0;
        pulse(0); #100; check(armed && !triggered, "b: armed, not yet triggered");
        wait (done); #20;
        check(rec_len == 20400 && trig_idx == 20000, "b: rec_len / trig_idx");
        rd_log(20000, v); rd_log(19999, v2);
        check(v[15:8] >= 8'h81 && v2[15:8] < 8'h81, "b: trigger word crosses the level");
        check(v[15:8] == 8'h82 && v2[15:8] == 8'h80, "b: ramp step 2 straddles 0x81");
        check(trig_frac == 16'h8000, "b: Q16 fraction = 0.5");
        check(rec_start != 0, "b: record wrapped (rec_start != 0)");
        n_bad = 0; rd_log(0, v);
        for (i = 1; i < 20400; i = i + 1) begin rd_log(i, v2); if (v2[15:8] != v[15:8] + 8'd2) n_bad = n_bad + 1; v = v2; end
        check(n_bad == 0, "b: 20400 consecutive words across the wrap");
        // ---------------- (c) decimation by 4 -----------------------------------------
        mode_norm = 0; pre_w = 10; post_w = 10; decim_m1 = 3; step = 1;
        pulse(0); wait (done); #20;
        if (rec_len != 20) $display("DEBUG c: rec_len=%0d wrote=%0d trig_idx=%0d", rec_len, wrote, trig_idx);
        check(rec_len == 20, "c: rec_len 20");
        n_bad = 0; rd_log(0, v);
        for (i = 1; i < 20; i = i + 1) begin rd_log(i, v2); if (v2[15:8] != v[15:8] + 8'd4) n_bad = n_bad + 1; v = v2; end
        check(n_bad == 0, "c: every 4th sample recorded");
        decim_m1 = 0;
        // ---------------- (d) single-channel CH1, NORM trigger, packing ------------------
        chmode = 1; mode_norm = 1; pre_w = 5; post_w = 5; level = 8'h40; ramp = 0;
        pulse(0); wait (done); #20;
        if (!(rec_len == 10 && trig_idx == 5)) $display("DEBUG d: rec_len=%0d wrote=%0d trig_idx=%0d half=%0d", rec_len, wrote, trig_idx, trig_half);
        check(rec_len == 10 && trig_idx == 5, "d: single: rec_len 10 words, trig word 5");
        n_bad = 0; rd_log(0, v);
        if (v[7:0] != v[15:8] + 8'd1) n_bad = n_bad + 1;
        for (i = 1; i < 10; i = i + 1) begin rd_log(i, v2); if (v2[15:8] != v[7:0] + 8'd1 || v2[7:0] != v2[15:8] + 8'd1) n_bad = n_bad + 1; v = v2; end
        check(n_bad == 0, "d: packed consecutive CH1 samples");
        rd_log(5, v);
        check(trig_half ? (v[15:8] == 8'h40) : (v[7:0] == 8'h40), "d: trig_half marks the trigger sample in its word");
        chmode = 0; mode_norm = 0;
        // ---------------- (e) stream ring + overflow -------------------------------------
        stream_on = 1; rd_ptr_s = 15'd0; pre_w = 10; post_w = 10;
        pulse(0); #100; check(filling && !overflow, "e: streaming");
        wait (wr_ptr == 15'd20000); check(!overflow, "e: no overflow with the reader 20000 words behind");
        rd_ptr_s = 15'd20000;                                  // reader caught up
        wait (wr_ptr == 15'd0); #1; check(!overflow, "e: no overflow across the wrap");
        wait (wr_ptr == 15'd19999); #1; check(!overflow, "e: no overflow one word before the reader");
        wait (wr_ptr == 15'd20000); #1; check(overflow, "e: overflow flagged when the writer catches the reader");
        pulse(1); #50; check(!filling && done && !valid, "e: halt in stream: done, not valid");
        stream_on = 0;
        // ---------------- (f) HALT without trigger -------------------------------------------
        mode_norm = 1; hw_sel = 1; pre_w = 100; post_w = 100; ramp = 0;
        pulse(0); repeat (300) @(posedge cap_clk); pulse(1); #50;
        check(valid && done && !triggered, "f: untriggered halt record is valid");
        check(rec_len >= 300 && rec_len <= 302, "f: rec_len ~ samples written");
        check(trig_idx == rec_len[14:0], "f: trig_idx = rec_len (no trigger)");
        n_bad = 0; rd_log(0, v);
        for (i = 1; i < rec_len; i = i + 1) begin rd_log(i, v2); if (v2[15:8] != v[15:8] + 8'd1) n_bad = n_bad + 1; v = v2; end
        check(n_bad == 0, "f: consecutive words");
        // ---------------- (g) hardware trigger (A12) ---------------------------------------------
        pulse(0); repeat (200) @(posedge cap_clk); trig_sense = 1; #100; trig_sense = 0; wait (done); #20;
        check(triggered && rec_len == 200 && trig_idx == 100 && trig_frac == 0, "g: hw trigger, frac 0");
        hw_sel = 0;
        // ---------------- (h) falling slope software trigger --------------------------------
        trig_slope = 1; level = 8'h20; hyst = 4'd4; pre_w = 10; post_w = 10; step = 1;
        pulse(0); wait (done); #20;
        rd_log(10, v); rd_log(9, v2);
        check(v[15:8] <= 8'h20 && v2[15:8] > 8'h20, "h: falling trigger straddles the level");
        trig_slope = 0;
        // ---------------- (i) RESET ----------------------------------------------------------
        pulse(2); #50; check(!valid && !done && !triggered && !filling && rec_len == 0 && wrote == 0, "i: reset clears");
        // ---------------- (j) TSRC: ramp / column tag / glitch, dual, AUTO pre 700 post 900 ---------
        mode_norm = 0; pre_w = 700; post_w = 900; level = 8'h80; hyst = 4'd8; step = 1;
        tsrc = `IL_CTRL_TSRC_RAMP; #100; pulse(0); wait (done); #50;
        check(rec_len == 1600 && trig_idx == 700, "j: ramp: rec_len 1600");
        n_bad = 0; for (i = 0; i < 1600; i = i + 1) begin rd_log(i, v); if (v != tsrc_word(tsrc, i)) n_bad = n_bad + 1; end
        check(n_bad == 0, "j: ramp: 1600 words == k (writer index since GO)");
        tsrc = `IL_CTRL_TSRC_COLTAG; #100; pulse(0); wait (done); #50;
        n_bad = 0; for (i = 0; i < 1600; i = i + 1) begin rd_log(i, v); if (v != tsrc_word(tsrc, i)) n_bad = n_bad + 1; end
        check(n_bad == 0, "j: coltag: 1600 words == {k mod 5, (k/5) & 0xff}");
        rd_log(5 * 256 + 3, v); check(v == 16'h0300, "j: coltag: word 1283 = col 3 row 0 (row wraps at 256)");
        tsrc = `IL_CTRL_TSRC_GLITCH; #100; pulse(0); wait (done); #50;
        n_bad = 0; for (i = 0; i < 1600; i = i + 1) begin rd_log(i, v); if (v != tsrc_word(tsrc, i)) n_bad = n_bad + 1; end
        check(n_bad == 0, "j: glitch: 0xffff at k = 0, 512, 1024, else 0");
        // decimation does not change the pattern (k counts written words), single mode neither
        tsrc = `IL_CTRL_TSRC_RAMP; decim_m1 = 2; pre_w = 10; post_w = 10; #100; pulse(0); wait (done); #50;
        n_bad = 0; for (i = 0; i < 20; i = i + 1) begin rd_log(i, v); if (v != i[15:0]) n_bad = n_bad + 1; end
        check(n_bad == 0 && rec_len == 20, "j: ramp with decimation 3: still k per written word");
        decim_m1 = 0; chmode = 1; #100; pulse(0); wait (done); #50;
        n_bad = 0; for (i = 0; i < 20; i = i + 1) begin rd_log(i, v); if (v != i[15:0]) n_bad = n_bad + 1; end
        check(n_bad == 0 && rec_len == 20, "j: ramp in single-channel mode: substituted on the record word");
        chmode = 0;
        // stream: k continues across the ring wrap (word at address 0 after the wrap = 20480)
        stream_on = 1; rd_ptr_s = 15'd20000; #100; pulse(0);
        wait (wr_ptr == 15'd1000); wait (wr_ptr == 15'd0); wait (wr_ptr == 15'd100); pulse(1); #100;
        rd(15'd0, v); check(v == 16'd20480, "j: stream: ramp continues across the wrap (address 0 = 20480)");
        rd(15'd50, v); check(v == 16'd20530, "j: stream: address 50 = 20530");
        rd(15'd20479, v); check(v == 16'd20479, "j: stream: last address before the wrap = 20479");
        // ---------------- (k) GO while still FILLING (no HALT): the pattern restarts at k = 0 ----------
        //   the old record's last commit is still in wr_q on the cycle after the arm; it must not
        //   count (before the fix mem[0..2] read 1, 2, 3)
        rd_ptr_s = 15'd20000; #100; pulse(0);
        wait (wr_ptr == 15'd300); pulse(0);                        // re-arm mid-fill, ring at ~300
        check(filling && wr_ptr < 15'd10, "k: re-armed: pointer back at 0");
        wait (wr_ptr == 15'd50); pulse(1); #100;
        rd(15'd0, v); check(v == 16'd0, "k: GO while filling: word 0 = 0 (k restarts)");
        rd(15'd1, v); check(v == 16'd1, "k: word 1 = 1");
        rd(15'd2, v); check(v == 16'd2, "k: word 2 = 2");
        stream_on = 0; tsrc = 0; #100;
        // back to the ADC source: a plain record again
        pulse(0); wait (done); #50; rd_log(0, v); rd_log(1, v2);
        check(v2[15:8] == v[15:8] + 8'd1 && v2[7:0] == ~v2[15:8], "j: TSRC 0: the ADC word is back");
        if (errors == 0) $display("PASS tb_capture"); else $display("FAIL tb_capture: %0d errors", errors);
        $finish;
    end
    initial begin #20000000; $display("FAIL tb_capture: timeout"); $finish; end
endmodule

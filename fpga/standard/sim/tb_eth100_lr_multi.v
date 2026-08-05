// tb_eth100_lr_multi.v -- MULTI-FRAME back-to-back LINE-RATE testbench for the
// integrated 100BASE-TX PHY decoder (eth100_decode_lr.v).
//
// Feeds a SINGLE continuous 600 MSa/s sample stream carrying N back-to-back
// frames that share ONE scrambler LFSR + ONE MLT-3 state (idle fill between
// frames) into the gearbox WRITE side at WR_SAMP(=3) samples per 200 MHz tick,
// CONTINUOUSLY (wr_valid every tick = live line rate, NO pacing), with the read/
// fabric chain on a SEPARATE async 80 MHz clock.  It then, per frame:
//   * segments emitted MAC octets on frame_done,
//   * checks the frame body BYTE-EXACT vs golden (frame||FCS),
//   * checks the FCS verdict (OK for clean frames, ERR for the corrupted one),
// and across the WHOLE stream requires:
//   * NO gearbox/CDR/4b5b overflow ever asserted,
//   * descr_locked, once asserted, NEVER deasserts (stays locked across frames),
//   * sfd_cnt == frame_done_cnt == N.
//
// Plusargs: +SAMP=<multi.samples> +EXPECT=<multi.expect> +NAME=<label>
//   multi.expect: line1 = N; then per frame "<nbody> <fcsok 1|0>" + nbody hex.

`timescale 1ns/1ps
module tb_eth100_lr_multi;
    localparam integer SAMPLE_W = 12;
    localparam integer LANES    = 8;
    localparam integer WR_SAMP  = 3;
    localparam integer MAXF     = 32;
    localparam integer MAXB     = 2048;

    integer i, j, fd, r, nsamp, nframes, f;
    reg [8*160-1:0] samp_file, expect_file;
    reg [8*32-1:0]  name;

    integer samp_mem [0:65535];

    // expected, flattened: exp_body[frame][octet], exp_nbody[frame], exp_ok[frame]
    reg [7:0] exp_body  [0:MAXF*MAXB-1];
    integer   exp_nbody [0:MAXF-1];
    integer   exp_ok    [0:MAXF-1];

    // captured, per frame
    reg [7:0] got_body  [0:MAXF*MAXB-1];
    integer   got_nbody [0:MAXF-1];
    integer   got_ok    [0:MAXF-1];

    integer cur_frame, cur_n;
    integer frame_done_cnt, sfd_cnt;
    reg [7:0] tmp_oct;

    // descrambler-lock watchdog
    reg lock_ever, lock_dropped;

    // sticky overflow observation
    reg any_gb_ovf, any_cdr_ovf, any_fb_ovf;

    // ---- two asynchronous clocks ----
    reg wr_clk, clk;
    always #2.5  wr_clk = ~wr_clk;   // 200 MHz interleave (5.0 ns period)
    always #6.25 clk    = ~clk;      //  80 MHz fabric     (12.5 ns period)

    // ---- DUT ports ----
    reg                        rst, wr_rst, en, wr_valid, flush;
    reg  signed [SAMPLE_W-1:0] thr_hi, thr_lo;
    reg  [WR_SAMP*SAMPLE_W-1:0] wr_samp;

    wire        emit_stb, sfd_seen, frame_done, fcs_ok_o;
    wire [7:0]  emit_byte, emit_flags;
    wire [23:0] emit_idx;
    wire        descr_locked, cg_locked, gb_overflow, cdr_overflow, fb_ovf;

    eth100_decode_lr #(.SAMPLE_W(SAMPLE_W), .LANES(LANES), .WR_SAMP(WR_SAMP)) dut (
        .clk(clk), .rst(rst), .en(en),
        .thr_hi(thr_hi), .thr_lo(thr_lo),
        .wr_clk(wr_clk), .wr_rst(wr_rst), .wr_valid(wr_valid), .wr_samp(wr_samp),
        .flush(flush),
        .emit_stb(emit_stb), .emit_byte(emit_byte),
        .emit_idx(emit_idx), .emit_flags(emit_flags),
        .sfd_seen(sfd_seen), .frame_done(frame_done), .fcs_ok_o(fcs_ok_o),
        .descr_locked(descr_locked), .cg_locked(cg_locked),
        .gb_overflow(gb_overflow), .cdr_overflow(cdr_overflow), .fb_ovf(fb_ovf)
    );

    // ---- capture + monitors (rd/fabric domain) ----
    always @(posedge clk) begin
        if (!rst && en) begin
            // overflow stickies
            if (gb_overflow)  any_gb_ovf  = 1'b1;
            if (cdr_overflow) any_cdr_ovf = 1'b1;
            if (fb_ovf)       any_fb_ovf  = 1'b1;

            // descrambler lock watchdog: once locked, must never drop
            if (descr_locked) lock_ever = 1'b1;
            if (lock_ever && !descr_locked) lock_dropped = 1'b1;

            // per-frame octet capture
            if (emit_stb) begin
                if (cur_frame < MAXF && cur_n < MAXB) begin
                    got_body[cur_frame*MAXB + cur_n] = emit_byte;
                    cur_n = cur_n + 1;
                end
            end
            if (sfd_seen) sfd_cnt = sfd_cnt + 1;
            if (frame_done) begin
                frame_done_cnt = frame_done_cnt + 1;
                if (cur_frame < MAXF) begin
                    got_nbody[cur_frame] = cur_n;
                    got_ok[cur_frame]    = fcs_ok_o ? 1 : 0;
                end
                cur_frame = cur_frame + 1;
                cur_n = 0;
            end
        end
    end

    initial begin
        wr_clk = 0; clk = 0;
        rst = 1; wr_rst = 1; en = 0; wr_valid = 0; flush = 0;
        wr_samp = 0;
        cur_frame = 0; cur_n = 0; frame_done_cnt = 0; sfd_cnt = 0;
        lock_ever = 0; lock_dropped = 0;
        any_gb_ovf = 0; any_cdr_ovf = 0; any_fb_ovf = 0;
        thr_hi = 500; thr_lo = -500;

        if (!$value$plusargs("SAMP=%s",   samp_file))   begin $display("need +SAMP=");   $finish; end
        if (!$value$plusargs("EXPECT=%s", expect_file)) begin $display("need +EXPECT="); $finish; end
        if (!$value$plusargs("NAME=%s",   name)) name = "multi";

        // ---- load expected: N; then per frame "<nbody> <ok>" + nbody hex ----
        fd = $fopen(expect_file, "r");
        if (fd == 0) begin $display("FAIL[%0s]: cannot open %0s", name, expect_file); $finish; end
        r = $fscanf(fd, "%d", nframes);
        if (r != 1) begin $display("FAIL[%0s]: bad expect header", name); $finish; end
        for (f = 0; f < nframes; f = f + 1) begin
            r = $fscanf(fd, "%d", exp_nbody[f]);
            if (r != 1) begin $display("FAIL[%0s]: bad expect frame %0d nbody", name, f); $finish; end
            r = $fscanf(fd, "%d", exp_ok[f]);
            if (r != 1) begin $display("FAIL[%0s]: bad expect frame %0d ok", name, f); $finish; end
            for (i = 0; i < exp_nbody[f]; i = i + 1) begin
                r = $fscanf(fd, "%h", tmp_oct);
                if (r != 1) begin $display("FAIL[%0s]: bad expect octet f%0d i%0d", name, f, i); $finish; end
                exp_body[f*MAXB + i] = tmp_oct;
            end
        end
        $fclose(fd);

        // ---- load samples (signed decimal, one per line) ----
        fd = $fopen(samp_file, "r");
        if (fd == 0) begin $display("FAIL[%0s]: cannot open %0s", name, samp_file); $finish; end
        nsamp = 0;
        r = $fscanf(fd, "%d", samp_mem[nsamp]);
        while (r == 1) begin
            nsamp = nsamp + 1;
            r = $fscanf(fd, "%d", samp_mem[nsamp]);
        end
        $fclose(fd);

        // release resets, engine on
        en = 1;
        repeat (4) @(posedge wr_clk);
        @(negedge wr_clk); wr_rst = 0;
        @(negedge clk);    rst    = 0;

        // ---- feed WR_SAMP samples/200MHz-tick CONTINUOUSLY (live line rate) ----
        i = 0;
        while (i + WR_SAMP <= nsamp) begin
            @(negedge wr_clk);
            for (j = 0; j < WR_SAMP; j = j + 1)
                wr_samp[j*SAMPLE_W +: SAMPLE_W] = samp_mem[i+j][SAMPLE_W-1:0];
            wr_valid = 1'b1;
            i = i + WR_SAMP;
        end
        @(negedge wr_clk); wr_valid = 1'b0;

        // drain read side + tail, then flush partial gearbox word
        repeat (120) @(negedge clk);
        @(negedge clk); flush = 1'b1;
        repeat (4)      @(negedge clk);
        @(negedge clk); flush = 1'b0;
        repeat (120) @(negedge clk);

        // ---- checks ------------------------------------------------------
        if (any_gb_ovf)  begin $display("FAIL[%0s]: gearbox overflow asserted", name);  $finish; end
        if (any_cdr_ovf) begin $display("FAIL[%0s]: cdr_overflow asserted", name);       $finish; end
        if (any_fb_ovf)  begin $display("FAIL[%0s]: 4b5b2 emit-FIFO overflow asserted", name); $finish; end
        if (!lock_ever)  begin $display("FAIL[%0s]: descrambler never locked", name);    $finish; end
        if (lock_dropped) begin $display("FAIL[%0s]: descr_locked DROPPED across frames", name); $finish; end
        if (sfd_cnt != nframes) begin
            $display("FAIL[%0s]: sfd_cnt=%0d exp %0d", name, sfd_cnt, nframes); $finish; end
        if (frame_done_cnt != nframes) begin
            $display("FAIL[%0s]: frame_done_cnt=%0d exp %0d", name, frame_done_cnt, nframes); $finish; end

        for (f = 0; f < nframes; f = f + 1) begin
            if (got_nbody[f] != exp_nbody[f]) begin
                $display("FAIL[%0s]: frame %0d octet count got=%0d exp=%0d",
                         name, f, got_nbody[f], exp_nbody[f]);
                for (i = 0; i < got_nbody[f] && i < 40; i = i + 1)
                    $display("   f%0d body[%0d] got=%02h", f, i, got_body[f*MAXB+i]);
                $finish;
            end
            for (i = 0; i < exp_nbody[f]; i = i + 1) begin
                if (got_body[f*MAXB+i] !== exp_body[f*MAXB+i]) begin
                    $display("FAIL[%0s]: frame %0d body[%0d] got=%02h exp=%02h",
                             name, f, i, got_body[f*MAXB+i], exp_body[f*MAXB+i]);
                    $finish;
                end
            end
            if (got_ok[f] != exp_ok[f]) begin
                $display("FAIL[%0s]: frame %0d FCS verdict got=%0d exp=%0d",
                         name, f, got_ok[f], exp_ok[f]);
                $finish;
            end
            $display("PASS[%0s] frame %0d: %0d octets BYTE-EXACT, FCS %0s (exp %0s)",
                     name, f, got_nbody[f],
                     got_ok[f] ? "OK" : "ERR", exp_ok[f] ? "OK" : "ERR");
        end

        $display("PASS[%0s]: %0d samples -(continuous 200->80 CDC + 2b/clk tail)-> %0d back-to-back frames, all bodies BYTE-EXACT, FCS verdicts correct, NO overflow, descr stayed LOCKED (sfd=%0d frame_done=%0d)",
                 name, nsamp, nframes, sfd_cnt, frame_done_cnt);
        $finish;
    end

    // safety timeout
    initial begin
        #12000000;
        $display("FAIL: timeout");
        $finish;
    end
endmodule

// tb_eth100_lr.v -- END-TO-END iverilog testbench for the LINE-RATE integrated
// 100BASE-TX PHY decoder (eth100_decode_lr.v: gearbox -> CDR -> descramble2 ->
// 4b5b2 -> framer).
//
// Feeds the golden model's 600 MSa/s ternary sample codes (<case>.samples) into
// the gearbox WRITE side as WR_SAMP(=3) samples per 200 MHz wr_clk -- CONTINUOUS
// (wr_valid every tick), i.e. the LIVE interleave rate -- with the read/fabric
// side (gearbox rd + whole tail) on a SEPARATE, ASYNCHRONOUS 80 MHz clock.  This
// exercises the real 200->80 CDC AND the 2-bit/clk unroll at full line rate (NO
// pacing, unlike tb_eth100_decode.v).  It then checks the emitted MAC-octet
// stream BYTE-EXACT vs the golden frame body (<case>.body = frame octets + 4 FCS
// octets), the FCS verdict, and the frame delimiters.
//
//   samples --(gearbox 200->80 async CDC)--[slice+CDR+MLT3+descr2+4b5b2+framer]-->
//        MAC octets + FCS OK  ==  golden eth100tx DecodeSamples() body + FCSOK.
//
// Plusargs: +SAMP=<file> +BODY=<file> +NBODY=<n> +NAME=<label>

`timescale 1ns/1ps
module tb_eth100_lr;
    localparam integer SAMPLE_W = 12;
    localparam integer LANES    = 8;
    localparam integer WR_SAMP  = 3;

    integer i, j, fd, r, nsamp, nbody;
    reg [8*160-1:0] samp_file, body_file;
    reg [8*32-1:0]  name;

    integer samp_mem [0:16383];   // signed sample codes
    reg [7:0] exp_body [0:2047];  // expected body octets (frame||FCS)

    reg [7:0] got_body [0:2047];
    integer   got_n;
    integer   frame_done_cnt, sfd_cnt;
    reg       fcs_ok_latched;

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

    // collect emitted octets + status (rd/fabric domain)
    always @(posedge clk) begin
        if (!rst && en) begin
            if (emit_stb) begin
                got_body[got_n] = emit_byte;
                got_n = got_n + 1;
            end
            if (sfd_seen)   sfd_cnt = sfd_cnt + 1;
            if (frame_done) begin
                frame_done_cnt = frame_done_cnt + 1;
                fcs_ok_latched = fcs_ok_o;
            end
        end
    end

    initial begin
        wr_clk = 0; clk = 0;
        rst = 1; wr_rst = 1; en = 0; wr_valid = 0; flush = 0;
        wr_samp = 0; got_n = 0;
        frame_done_cnt = 0; sfd_cnt = 0; fcs_ok_latched = 0;
        thr_hi = 500; thr_lo = -500;

        if (!$value$plusargs("SAMP=%s",  samp_file)) begin $display("need +SAMP=");  $finish; end
        if (!$value$plusargs("BODY=%s",  body_file)) begin $display("need +BODY=");  $finish; end
        if (!$value$plusargs("NBODY=%d", nbody))     begin $display("need +NBODY="); $finish; end
        if (!$value$plusargs("NAME=%s",  name)) name = "case";

        // read signed decimal samples
        fd = $fopen(samp_file, "r");
        if (fd == 0) begin $display("FAIL[%0s]: cannot open %0s", name, samp_file); $finish; end
        nsamp = 0;
        r = $fscanf(fd, "%d", samp_mem[nsamp]);
        while (r == 1) begin
            nsamp = nsamp + 1;
            r = $fscanf(fd, "%d", samp_mem[nsamp]);
        end
        $fclose(fd);
        $readmemh(body_file, exp_body);

        // release both resets, engine on
        en = 1;
        repeat (4) @(posedge wr_clk);
        @(negedge wr_clk); wr_rst = 0;
        @(negedge clk);    rst    = 0;

        // ---- feed WR_SAMP samples per 200 MHz tick CONTINUOUSLY (live rate) ----
        i = 0;
        while (i + WR_SAMP <= nsamp) begin
            @(negedge wr_clk);
            for (j = 0; j < WR_SAMP; j = j + 1)
                wr_samp[j*SAMPLE_W +: SAMPLE_W] = samp_mem[i+j][SAMPLE_W-1:0];
            wr_valid = 1'b1;
            i = i + WR_SAMP;
        end
        @(negedge wr_clk); wr_valid = 1'b0;

        // let the read side drain the banks + tail, then flush the partial word
        repeat (80) @(negedge clk);
        @(negedge clk); flush = 1'b1;
        repeat (4)      @(negedge clk);
        @(negedge clk); flush = 1'b0;
        repeat (80) @(negedge clk);

        // ---- checks ------------------------------------------------------
        if (gb_overflow)  begin $display("FAIL[%0s]: gearbox overflow", name);  $finish; end
        if (cdr_overflow) begin $display("FAIL[%0s]: cdr_overflow", name);       $finish; end
        if (fb_ovf)       begin $display("FAIL[%0s]: 4b5b2 emit-FIFO overflow", name); $finish; end
        if (sfd_cnt != 1) begin
            $display("FAIL[%0s]: sfd_cnt=%0d exp 1", name, sfd_cnt); $finish; end
        if (frame_done_cnt != 1) begin
            $display("FAIL[%0s]: frame_done_cnt=%0d exp 1", name, frame_done_cnt); $finish; end
        if (got_n != nbody) begin
            $display("FAIL[%0s]: body octet count got=%0d exp=%0d", name, got_n, nbody);
            for (i = 0; i < got_n && i < 40; i = i + 1)
                $display("   body[%0d] got=%02h", i, got_body[i]);
            $finish;
        end
        for (i = 0; i < nbody; i = i + 1) begin
            if (got_body[i] !== exp_body[i]) begin
                $display("FAIL[%0s]: body[%0d] got=%02h exp=%02h",
                         name, i, got_body[i], exp_body[i]);
                $finish;
            end
        end
        if (!fcs_ok_latched) begin
            $display("FAIL[%0s]: FCS verdict NOT ok at frame_done", name); $finish; end

        $display("PASS[%0s]: %0d samples -(gearbox 200->80 async CDC + 2b/clk tail)-> %0d MAC octets (frame||FCS) BYTE-EXACT vs golden, FCS OK, sfd=1 frame_done=1",
                 name, nsamp, nbody);
        $finish;
    end

    // safety timeout
    initial begin
        #4000000;
        $display("FAIL: timeout");
        $finish;
    end
endmodule

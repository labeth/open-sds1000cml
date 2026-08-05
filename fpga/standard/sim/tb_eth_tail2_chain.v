// tb_eth_tail2_chain.v -- iverilog testbench for the LINE-RATE 2-wide tail:
//   eth_slicer_cdr -> eth_descramble2 -> eth_4b5b2
// fed the golden 600 MSa/s ternary sample codes (<case>.samples) LANES(=8) per
// word CONTINUOUSLY (in_valid every clock, NO bubble pacing) — i.e. the live
// interleave rate.  Proves the exact 2-wide handshake (CDR out_bits/out_cnt ->
// descramble2 in_bits/in_nbits -> 4b5b2 in_bits/in_nbits) sustains the stream
// without underrun/overflow, and that the recovered nibble stream is nibble-
// EXACT vs the golden <case>.mii_nibbles.
//
// Plusargs: +SAMP=<file> +NIBS=<file> +NSAMP=<n> +NNIB=<n> +NAME=<label>

`timescale 1ns/1ps
module tb_eth_tail2_chain;
    localparam integer SAMPLE_W = 12;
    localparam integer LANES    = 8;
    localparam integer OBITS_W  = 8;

    integer i, j, nsamp, nnib, remain, nv;
    reg [8*160-1:0] samp_file, nib_file;
    reg [8*32-1:0]  name;

    integer   samp_mem [0:16383];
    reg [3:0] exp_nib  [0:511];
    reg [3:0] got_nib  [0:511];
    integer   got_nib_n, sof_cnt, eof_cnt;
    integer   max_cdr_cnt;

    // DUT nets
    reg                        clk, rst, en, in_valid, flush;
    reg  signed [SAMPLE_W-1:0] thr_hi, thr_lo;
    reg  [LANES*SAMPLE_W-1:0]  codes;
    reg  [3:0]                 nvalid;

    wire [OBITS_W-1:0] cdr_bits;
    wire [3:0]         cdr_cnt;
    wire               cdr_valid, cdr_overflow;

    wire        de_valid, de_locked;
    wire [1:0]  de_nbits, de_bits;

    wire        cg_stb, cg_ctrl, cg_err, nibble_stb, sof, eof, cg_locked, fb_ovf;
    wire [4:0]  cg_code;
    wire [2:0]  cg_sym;
    wire [3:0]  nibble;

    eth_slicer_cdr #(.SAMPLE_W(SAMPLE_W), .LANES(LANES)) u_cdr (
        .clk(clk), .rst(rst), .en(en),
        .thr_hi(thr_hi), .thr_lo(thr_lo),
        .codes(codes), .nvalid(nvalid), .in_valid(in_valid), .flush(flush),
        .out_bits(cdr_bits), .out_cnt(cdr_cnt),
        .out_valid(cdr_valid), .overflow(cdr_overflow)
    );

    eth_descramble2 u_descr (
        .clk(clk), .rst(rst), .en(en),
        .in_valid(cdr_valid), .in_nbits(cdr_cnt[1:0]), .in_bits(cdr_bits[1:0]),
        .out_valid(de_valid), .out_nbits(de_nbits), .out_bits(de_bits),
        .locked(de_locked)
    );

    eth_4b5b2 u_4b5b (
        .clk(clk), .rst(rst), .en(en),
        .in_valid(de_valid), .in_nbits(de_nbits), .in_bits(de_bits),
        .cg_stb(cg_stb), .cg_code(cg_code), .cg_ctrl(cg_ctrl),
        .cg_sym(cg_sym), .cg_err(cg_err),
        .nibble(nibble), .nibble_stb(nibble_stb),
        .sof(sof), .eof(eof), .locked(cg_locked), .ovf(fb_ovf)
    );

    always #5 clk = ~clk;

    always @(posedge clk) begin
        if (!rst && en) begin
            if (cdr_valid && (cdr_cnt > max_cdr_cnt)) max_cdr_cnt = cdr_cnt;
            if (cdr_valid && (cdr_cnt > 2)) begin
                $display("FAIL[%0s]: CDR emitted %0d bits/clk (>2, tail truncates)",
                         name, cdr_cnt); $finish;
            end
            if (nibble_stb) begin got_nib[got_nib_n] = nibble; got_nib_n = got_nib_n + 1; end
            if (cg_err)  begin $display("FAIL[%0s]: invalid code group", name); $finish; end
            if (fb_ovf)  begin $display("FAIL[%0s]: 4b5b2 emit-FIFO overflow", name); $finish; end
            if (cdr_overflow) begin $display("FAIL[%0s]: cdr_overflow", name); $finish; end
            if (sof) sof_cnt = sof_cnt + 1;
            if (eof) eof_cnt = eof_cnt + 1;
        end
    end

    initial begin
        clk = 0; rst = 1; en = 0; in_valid = 0; flush = 0;
        codes = 0; nvalid = 0; got_nib_n = 0; sof_cnt = 0; eof_cnt = 0;
        max_cdr_cnt = 0; thr_hi = 500; thr_lo = -500;

        if (!$value$plusargs("SAMP=%s",  samp_file)) begin $display("need +SAMP="); $finish; end
        if (!$value$plusargs("NIBS=%s",  nib_file))  begin $display("need +NIBS="); $finish; end
        if (!$value$plusargs("NNIB=%d",  nnib))      begin $display("need +NNIB="); $finish; end
        if (!$value$plusargs("NAME=%s",  name))      name = "case";

        // read signed decimal samples
        i = $fopen(samp_file, "r");
        if (i == 0) begin $display("FAIL[%0s]: cannot open %0s", name, samp_file); $finish; end
        nsamp = 0;
        j = $fscanf(i, "%d", samp_mem[nsamp]);
        while (j == 1) begin
            nsamp = nsamp + 1;
            j = $fscanf(i, "%d", samp_mem[nsamp]);
        end
        $fclose(i);

        $readmemh(nib_file, exp_nib);

        @(negedge clk); rst = 1; en = 1;
        @(negedge clk); rst = 0;

        // feed LANES samples per word CONTINUOUSLY (no bubble) = live line rate
        i = 0;
        while (i < nsamp) begin
            @(negedge clk);
            remain = nsamp - i;
            nv = (remain >= LANES) ? LANES : remain;
            codes = 0;
            for (j = 0; j < LANES; j = j + 1)
                if (j < nv) codes[j*SAMPLE_W +: SAMPLE_W] = samp_mem[i+j][SAMPLE_W-1:0];
            nvalid = nv[3:0]; in_valid = 1;
            i = i + nv;
        end
        @(negedge clk); in_valid = 0; nvalid = 0; flush = 1;
        @(negedge clk); flush = 0;
        repeat (64) @(negedge clk);

        // ---- checks ------------------------------------------------------
        if (got_nib_n != nnib) begin
            $display("FAIL[%0s]: nibble count got=%0d exp=%0d", name, got_nib_n, nnib);
            for (i = 0; i < got_nib_n && i < 24; i = i + 1)
                $display("   nib[%0d]=%h", i, got_nib[i]);
            $finish;
        end
        for (i = 0; i < nnib; i = i + 1) begin
            if (got_nib[i] !== exp_nib[i]) begin
                $display("FAIL[%0s]: nibble[%0d] got=%h exp=%h", name, i, got_nib[i], exp_nib[i]);
                $finish;
            end
        end
        if (sof_cnt != 1) begin $display("FAIL[%0s]: sof_cnt=%0d exp 1", name, sof_cnt); $finish; end
        if (eof_cnt != 1) begin $display("FAIL[%0s]: eof_cnt=%0d exp 1", name, eof_cnt); $finish; end

        $display("PASS[%0s]: %0d samples CONTINUOUS-fed -> %0d nibbles EXACT vs golden (line-rate tail, max %0d bits/clk, sof=1 eof=1)",
                 name, nsamp, nnib, max_cdr_cnt);
        $finish;
    end
endmodule

// tb_eth_slicer_cdr.v -- iverilog testbench: eth_slicer_cdr vs golden vectors.
//
// Feeds <case>.samples (600 MSa/s signed amplitude codes) LANES(=8) per 80 MHz
// clock into the slicer+CDR, concatenates the recovered NRZI bit words, and
// checks them BIT-EXACT against <case>.scrambled_bits (the golden MLT-3-decode
// output).  This is the {codes -> recovered NRZI bits} oracle comparison.
//
// Plusargs: +SAMP=<file> +BITS=<file> +NBITS=<n> +NAME=<label>

`timescale 1ns/1ps
module tb_eth_slicer_cdr;
    localparam integer SAMPLE_W = 12;
    localparam integer LANES    = 8;
    localparam integer OBITS_W  = 32;

    integer i, j, fd, r, nsamp, nbits, remain, nv, k;
    reg [8*160-1:0] samp_file, bits_file;
    reg [8*32-1:0]  name;

    // vector storage
    integer samp_mem [0:8191];    // signed sample codes
    reg     exp_bit  [0:2047];    // expected scrambled/NRZI bits

    // collected output bits
    reg     got_bit  [0:2047];
    integer got_n;

    // DUT signals
    reg                     clk, rst, en, in_valid, flush;
    reg  signed [SAMPLE_W-1:0] thr_hi, thr_lo;
    reg  [LANES*SAMPLE_W-1:0]  codes;
    reg  [3:0]              nvalid;
    wire [OBITS_W-1:0]      out_bits;
    wire [3:0]              out_cnt;
    wire                    out_valid, overflow;

    eth_slicer_cdr #(.SAMPLE_W(SAMPLE_W), .LANES(LANES), .OBITS_W(OBITS_W)) dut (
        .clk(clk), .rst(rst), .en(en),
        .thr_hi(thr_hi), .thr_lo(thr_lo),
        .codes(codes), .nvalid(nvalid), .in_valid(in_valid), .flush(flush),
        .out_bits(out_bits), .out_cnt(out_cnt),
        .out_valid(out_valid), .overflow(overflow)
    );

    always #5 clk = ~clk;   // 100 MHz sim clock (period irrelevant to logic)

    // collect emitted bits on each rising edge (registered output)
    always @(posedge clk) begin
        if (out_valid && !rst && en) begin
            for (k = 0; k < out_cnt; k = k + 1) begin
                got_bit[got_n] = out_bits[k];
                got_n = got_n + 1;
            end
        end
    end

    initial begin
        clk = 0; rst = 1; en = 0; in_valid = 0; flush = 0;
        codes = 0; nvalid = 0; got_n = 0;
        thr_hi = 500; thr_lo = -500;

        if (!$value$plusargs("SAMP=%s",  samp_file)) begin $display("need +SAMP=");  $finish; end
        if (!$value$plusargs("BITS=%s",  bits_file)) begin $display("need +BITS=");  $finish; end
        if (!$value$plusargs("NBITS=%d", nbits))     begin $display("need +NBITS="); $finish; end
        if (!$value$plusargs("NAME=%s",  name)) name = "case";

        // read signed decimal samples via $fscanf
        fd = $fopen(samp_file, "r");
        if (fd == 0) begin $display("FAIL[%0s]: cannot open %0s", name, samp_file); $finish; end
        nsamp = 0;
        r = $fscanf(fd, "%d", samp_mem[nsamp]);
        while (r == 1) begin
            nsamp = nsamp + 1;
            r = $fscanf(fd, "%d", samp_mem[nsamp]);
        end
        $fclose(fd);

        // expected bits
        $readmemb(bits_file, exp_bit);

        // reset
        @(negedge clk); rst = 1; en = 1;
        @(negedge clk); rst = 0;

        // feed LANES samples per clock
        i = 0;
        while (i < nsamp) begin
            @(negedge clk);
            remain = nsamp - i;
            nv = (remain >= LANES) ? LANES : remain;
            codes = 0;
            for (j = 0; j < LANES; j = j + 1) begin
                if (j < nv)
                    codes[j*SAMPLE_W +: SAMPLE_W] = samp_mem[i+j][SAMPLE_W-1:0];
            end
            nvalid   = nv[3:0];
            in_valid = 1;
            i = i + nv;
        end
        // stop feeding, assert flush to close the final run
        @(negedge clk); in_valid = 0; nvalid = 0; flush = 1;
        @(negedge clk); flush = 0;
        // drain registered output
        repeat (4) @(negedge clk);

        // ---- checks ------------------------------------------------------
        if (overflow) begin
            $display("FAIL[%0s]: overflow asserted (OBITS_W=%0d too small)", name, OBITS_W);
            $finish;
        end
        if (got_n != nbits) begin
            $display("FAIL[%0s]: NRZI bit count got=%0d exp=%0d", name, got_n, nbits);
            // dump a little context
            for (i = 0; i < (got_n<nbits?got_n:nbits) && i < 24; i = i + 1)
                $display("   bit[%0d] got=%b exp=%b", i, got_bit[i], exp_bit[i]);
            $finish;
        end
        for (i = 0; i < nbits; i = i + 1) begin
            if (got_bit[i] !== exp_bit[i]) begin
                $display("FAIL[%0s]: NRZI bit[%0d] got=%b exp=%b", name, i, got_bit[i], exp_bit[i]);
                $finish;
            end
        end

        $display("PASS[%0s]: %0d samples -> %0d NRZI bits, BIT-EXACT vs golden scrambled_bits",
                 name, nsamp, nbits);
        $finish;
    end
endmodule

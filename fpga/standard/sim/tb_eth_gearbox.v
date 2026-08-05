// tb_eth_gearbox.v -- iverilog testbench: eth_gearbox -> eth_slicer_cdr vs golden.
//
// Proves the 200->80 gearbox is TRANSPARENT: it re-packs the golden 600 MSa/s
// ternary sample stream (fed 3 samples per 200 MHz wr_clk, as the interleave
// delivers c1a_p/c1b_p/c1c_p) into 8-lane words at the 80 MHz rd_clk, and the
// existing eth_slicer_cdr recovers the SAME NRZI (scrambled) bits as the direct
// feed (tb_eth_slicer_cdr.v) -- BIT-EXACT vs <case>.scrambled_bits.
//
// The two clocks are driven ASYNCHRONOUS (200 MHz vs 80 MHz, unrelated edges) to
// exercise the real CDC. No sample is dropped (overflow must stay 0).
//
// Plusargs: +SAMP=<file> +BITS=<file> +NBITS=<n> +NAME=<label>

`timescale 1ns/1ps
module tb_eth_gearbox;
    localparam integer SAMPLE_W = 12;
    localparam integer LANES    = 8;
    localparam integer WR_SAMP  = 3;
    localparam integer OBITS_W  = 32;

    integer i, j, fd, r, nsamp, nbits, k;
    reg [8*160-1:0] samp_file, bits_file;
    reg [8*32-1:0]  name;

    integer samp_mem [0:8191];
    reg     exp_bit  [0:2047];
    reg     got_bit  [0:2047];
    integer got_n;

    // ---- clocks (asynchronous) ----
    reg wr_clk, rd_clk;
    always #2.5 wr_clk = ~wr_clk;   // 200 MHz (5.0 ns period)
    always #6.25 rd_clk = ~rd_clk;  //  80 MHz (12.5 ns period)

    // ---- gearbox ports ----
    reg                        wr_rst, rd_rst, en, wr_valid, flush, rd_ready;
    reg  [WR_SAMP*SAMPLE_W-1:0] wr_samp;
    wire [LANES*SAMPLE_W-1:0]  codes;
    wire [3:0]                 nvalid;
    wire                       in_valid, gb_ovf;

    eth_gearbox #(.SAMPLE_W(SAMPLE_W), .LANES(LANES), .WR_SAMP(WR_SAMP), .DEPTHW(4)) gb (
        .wr_clk(wr_clk), .wr_rst(wr_rst), .wr_valid(wr_valid), .wr_samp(wr_samp),
        .rd_clk(rd_clk), .rd_rst(rd_rst), .rd_ready(rd_ready), .flush(flush),
        .en(en),
        .codes(codes), .nvalid(nvalid), .in_valid(in_valid), .overflow(gb_ovf)
    );

    // ---- CDR fed by the gearbox output ----
    reg  signed [SAMPLE_W-1:0] thr_hi, thr_lo;
    wire [OBITS_W-1:0] out_bits;
    wire [3:0]         out_cnt;
    wire               cdr_valid, cdr_ovf;

    eth_slicer_cdr #(.SAMPLE_W(SAMPLE_W), .LANES(LANES), .OBITS_W(OBITS_W)) cdr (
        .clk(rd_clk), .rst(rd_rst), .en(en),
        .thr_hi(thr_hi), .thr_lo(thr_lo),
        .codes(codes), .nvalid(nvalid), .in_valid(in_valid), .flush(flush),
        .out_bits(out_bits), .out_cnt(out_cnt),
        .out_valid(cdr_valid), .overflow(cdr_ovf)
    );

    // collect recovered bits (rd domain)
    always @(posedge rd_clk) begin
        if (cdr_valid && !rd_rst && en) begin
            for (k = 0; k < out_cnt; k = k + 1) begin
                got_bit[got_n] = out_bits[k];
                got_n = got_n + 1;
            end
        end
    end

    initial begin
        wr_clk = 0; rd_clk = 0;
        wr_rst = 1; rd_rst = 1; en = 0; wr_valid = 0; flush = 0; rd_ready = 1;
        wr_samp = 0; got_n = 0;
        thr_hi = 500; thr_lo = -500;

        if (!$value$plusargs("SAMP=%s",  samp_file)) begin $display("need +SAMP=");  $finish; end
        if (!$value$plusargs("BITS=%s",  bits_file)) begin $display("need +BITS=");  $finish; end
        if (!$value$plusargs("NBITS=%d", nbits))     begin $display("need +NBITS="); $finish; end
        if (!$value$plusargs("NAME=%s",  name)) name = "case";

        // load signed decimal samples
        fd = $fopen(samp_file, "r");
        if (fd == 0) begin $display("FAIL[%0s]: cannot open %0s", name, samp_file); $finish; end
        nsamp = 0;
        r = $fscanf(fd, "%d", samp_mem[nsamp]);
        while (r == 1) begin
            nsamp = nsamp + 1;
            r = $fscanf(fd, "%d", samp_mem[nsamp]);
        end
        $fclose(fd);
        $readmemb(bits_file, exp_bit);

        if (nsamp % WR_SAMP != 0)
            $display("NOTE[%0s]: nsamp=%0d not a multiple of WR_SAMP=%0d (tail via flush)",
                     name, nsamp, WR_SAMP);

        // release resets (both domains), engine on
        en = 1;
        repeat (4) @(posedge wr_clk);
        @(negedge wr_clk); wr_rst = 0;
        @(negedge rd_clk); rd_rst = 0;

        // ---- feed WR_SAMP samples per 200 MHz tick ----
        i = 0;
        while (i + WR_SAMP <= nsamp) begin
            @(negedge wr_clk);
            for (j = 0; j < WR_SAMP; j = j + 1)
                wr_samp[j*SAMPLE_W +: SAMPLE_W] = samp_mem[i+j][SAMPLE_W-1:0];
            wr_valid = 1'b1;
            i = i + WR_SAMP;
        end
        @(negedge wr_clk); wr_valid = 1'b0;

        // let the read side drain the banks
        repeat (60) @(negedge rd_clk);
        // flush any tail partial word + close the final run
        @(negedge rd_clk); flush = 1'b1;
        repeat (4) @(negedge rd_clk);
        @(negedge rd_clk); flush = 1'b0;
        repeat (6) @(negedge rd_clk);

        // ---- checks ------------------------------------------------------
        if (gb_ovf) begin
            $display("FAIL[%0s]: gearbox overflow asserted (bank depth too small)", name);
            $finish;
        end
        if (cdr_ovf) begin
            $display("FAIL[%0s]: CDR overflow asserted", name);
            $finish;
        end
        if (got_n != nbits) begin
            $display("FAIL[%0s]: NRZI bit count got=%0d exp=%0d", name, got_n, nbits);
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

        $display("PASS[%0s]: %0d samples -(gearbox 200->80)-> %0d NRZI bits, BIT-EXACT vs golden (async CDC, overflow=0)",
                 name, nsamp, nbits);
        $finish;
    end

    // safety timeout
    initial begin
        #2000000;
        $display("FAIL: timeout");
        $finish;
    end
endmodule

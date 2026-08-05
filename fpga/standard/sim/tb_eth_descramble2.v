// tb_eth_descramble2.v -- iverilog testbench: eth_descramble2 (2-bit/clk unroll)
// vs golden-model vectors.  Feeds <case>.scrambled_bits at a VARIABLE 0..2 bits
// per clock, collects the descrambled burst output, and checks it BIT-EXACT
// against <case>.plain_bits, plus idle-LOCK TIMING (locked rises after exactly
// SEED_LEN+VERIFY_LEN = 44 processed bits, matching the 1-bit engine / golden).
//
// +MODE=0 : pure 2 bits/clk  -> proves the tail sustains 160 Mbit/s (>=125).
// +MODE=1 : mixed 0/1/2 pattern -> exercises every valid burst count incl gaps.
//
// Plusargs: +SCR=<file> +PLAIN=<file> +NBITS=<n> +MODE=<0|1> +NAME=<label>

`timescale 1ns/1ps
module tb_eth_descramble2;
    integer i, k;
    reg [8*160-1:0] scr_file, plain_file;
    reg [8*32-1:0]  name;
    integer nbits, mode;

    reg        scr_mem   [0:2047];   // scrambled input bits
    reg        plain_mem [0:2047];   // expected descrambled bits
    reg        got       [0:2047];   // collected descrambled bits
    integer    got_n;
    integer    fed;                  // cumulative processed input bits
    integer    lock_at;             // fed count when `locked` first rose
    reg        locked_prev;

    // burst pattern (mixed): counts per clock
    integer patt [0:5];
    integer pidx, nb, rem;

    // DUT
    reg        clk, rst, en, in_valid;
    reg [1:0]  in_nbits, in_bits;
    wire       out_valid, locked;
    wire [1:0] out_nbits, out_bits;

    eth_descramble2 dut(
        .clk(clk), .rst(rst), .en(en),
        .in_valid(in_valid), .in_nbits(in_nbits), .in_bits(in_bits),
        .out_valid(out_valid), .out_nbits(out_nbits), .out_bits(out_bits),
        .locked(locked)
    );

    always #5 clk = ~clk;

    // collect descrambled bits + capture lock timing
    always @(posedge clk) begin
        if (!rst && en) begin
            if (out_valid) begin
                for (k = 0; k < out_nbits; k = k + 1) begin
                    got[got_n] = out_bits[k];
                    got_n = got_n + 1;
                end
            end
            // `locked` and out_bits are registered in lockstep, so the count of
            // descrambled bits collected when `locked` first rises == the number
            // of bits PROCESSED to reach lock (SEED_LEN+VERIFY_LEN = 44).
            if (locked && !locked_prev) lock_at = got_n;
            locked_prev = locked;
        end
    end

    initial begin
        clk = 0; rst = 1; en = 0; in_valid = 0; in_nbits = 0; in_bits = 0;
        got_n = 0; fed = 0; lock_at = -1; locked_prev = 0;
        // mixed pattern: includes a 0 (gap), 1s and 2s
        patt[0]=2; patt[1]=2; patt[2]=1; patt[3]=2; patt[4]=0; patt[5]=2;

        if (!$value$plusargs("SCR=%s",    scr_file))   begin $display("need +SCR=");   $finish; end
        if (!$value$plusargs("PLAIN=%s",  plain_file)) begin $display("need +PLAIN="); $finish; end
        if (!$value$plusargs("NBITS=%d",  nbits))      begin $display("need +NBITS="); $finish; end
        if (!$value$plusargs("MODE=%d",   mode))       mode = 0;
        if (!$value$plusargs("NAME=%s",   name))       name = "case";

        $readmemb(scr_file,   scr_mem);
        $readmemb(plain_file, plain_mem);

        @(negedge clk); rst = 1; en = 1;
        @(negedge clk); rst = 0;

        // feed scrambled bits up to 2/clk
        i = 0; pidx = 0;
        while (i < nbits) begin
            @(negedge clk);
            rem = nbits - i;
            if (mode == 0) nb = (rem >= 2) ? 2 : rem;     // pure 2/clk
            else begin                                     // mixed 0/1/2
                nb = patt[pidx];
                pidx = (pidx == 5) ? 0 : pidx + 1;
                if (nb > rem) nb = rem;
            end
            if (nb == 0) begin
                in_valid = 0; in_nbits = 0; in_bits = 0;
            end else begin
                in_valid = 1; in_nbits = nb[1:0];
                in_bits[0] = scr_mem[i];
                in_bits[1] = (nb >= 2) ? scr_mem[i+1] : 1'b0;
                i   = i + nb;
                fed = fed + nb;
            end
        end
        @(negedge clk); in_valid = 0; in_nbits = 0;
        repeat (8) @(negedge clk);

        // ---- checks ------------------------------------------------------
        if (got_n != nbits) begin
            $display("FAIL[%0s]: descrambled bit count got=%0d exp=%0d",
                     name, got_n, nbits); $finish;
        end
        for (i = 0; i < nbits; i = i + 1) begin
            if (got[i] !== plain_mem[i]) begin
                $display("FAIL[%0s]: plain bit[%0d] got=%b exp=%b",
                         name, i, got[i], plain_mem[i]); $finish;
            end
        end
        if (lock_at != 44 && lock_at != 45) begin
            $display("FAIL[%0s]: idle-lock at %0d processed bits, exp 44/45",
                     name, lock_at); $finish;
        end
        $display("PASS[%0s]: mode=%0d %0d scrambled bits -> %0d plain bits BIT-EXACT vs golden, idle-lock at bit %0d",
                 name, mode, nbits, got_n, lock_at);
        $finish;
    end
endmodule

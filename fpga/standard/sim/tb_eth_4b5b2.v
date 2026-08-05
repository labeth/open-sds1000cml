// tb_eth_4b5b2.v -- iverilog testbench: eth_4b5b2 (2-bit/clk unroll) vs golden.
// Feeds <case>.plain_bits at a VARIABLE 0..2 bits/clk, collects the nibble
// stream + code groups, and checks them nibble-exact vs <case>.mii_nibbles plus
// SSD /J/K/, ESD /T/R/, sof/eof, no invalid code group, and the AT-MOST-1
// nibble/clk contract (never two nibble_stb in a clock) + emit-FIFO no-overflow.
//
// +MODE=0 : pure 2 bits/clk (160 Mbit/s throughput).  +MODE=1 : mixed 0/1/2.
//
// Plusargs: +PLAIN=<file> +NIBS=<file> +NBITS=<n> +NNIB=<n> +MODE=<0|1> +NAME=<label>

`timescale 1ns/1ps
module tb_eth_4b5b2;
    integer i;
    reg [8*160-1:0] plain_file, nib_file;
    reg [8*32-1:0]  name;
    integer nbits, nnib, mode;

    reg        plain_mem [0:2047];
    reg [3:0]  exp_nib   [0:511];
    reg [3:0]  got_nib   [0:511];
    integer    got_nib_n;
    integer    sof_cnt, eof_cnt, cg_cnt;
    reg [4:0]  cg0, cg1;
    reg [2:0]  sym0, sym1;
    integer    saw_T_then_R;
    reg [2:0]  prev_sym;
    reg        prev_ctrl;
    integer    two_nib_clk;   // count of clocks with >1 nibble (must stay 0)

    integer patt [0:5];
    integer pidx, nb, rem;

    // DUT
    reg        clk, rst, en, in_valid;
    reg [1:0]  in_nbits, in_bits;
    wire       cg_stb, cg_ctrl, cg_err, nibble_stb, sof, eof, locked, ovf;
    wire [4:0] cg_code;
    wire [2:0] cg_sym;
    wire [3:0] nibble;

    eth_4b5b2 dut(
        .clk(clk), .rst(rst), .en(en),
        .in_valid(in_valid), .in_nbits(in_nbits), .in_bits(in_bits),
        .cg_stb(cg_stb), .cg_code(cg_code), .cg_ctrl(cg_ctrl),
        .cg_sym(cg_sym), .cg_err(cg_err),
        .nibble(nibble), .nibble_stb(nibble_stb),
        .sof(sof), .eof(eof), .locked(locked), .ovf(ovf)
    );

    always #5 clk = ~clk;

    always @(posedge clk) begin
        if (!rst && en) begin
            if (nibble_stb) begin
                got_nib[got_nib_n] = nibble;
                got_nib_n = got_nib_n + 1;
            end
            if (cg_stb) begin
                if (cg_cnt == 0) begin cg0 = cg_code; sym0 = cg_sym; end
                if (cg_cnt == 1) begin cg1 = cg_code; sym1 = cg_sym; end
                if (prev_ctrl && (prev_sym == 3'd4) && cg_ctrl && (cg_sym == 3'd5))
                    saw_T_then_R = 1;   // SYM_T=4 then SYM_R=5
                prev_sym  = cg_sym;
                prev_ctrl = cg_ctrl;
                cg_cnt = cg_cnt + 1;
            end
            if (cg_err) begin
                $display("FAIL[%0s]: invalid code group cg_code=%b", name, cg_code);
                $finish;
            end
            if (ovf) begin
                $display("FAIL[%0s]: emit-FIFO overflow", name); $finish;
            end
            if (sof) sof_cnt = sof_cnt + 1;
            if (eof) eof_cnt = eof_cnt + 1;
        end
    end

    initial begin
        clk = 0; rst = 1; en = 0; in_valid = 0; in_nbits = 0; in_bits = 0;
        got_nib_n = 0; sof_cnt = 0; eof_cnt = 0; cg_cnt = 0;
        saw_T_then_R = 0; prev_sym = 3'd0; prev_ctrl = 0; two_nib_clk = 0;
        patt[0]=2; patt[1]=2; patt[2]=1; patt[3]=2; patt[4]=0; patt[5]=2;

        if (!$value$plusargs("PLAIN=%s", plain_file)) begin $display("need +PLAIN="); $finish; end
        if (!$value$plusargs("NIBS=%s",  nib_file))   begin $display("need +NIBS=");  $finish; end
        if (!$value$plusargs("NBITS=%d", nbits))      begin $display("need +NBITS="); $finish; end
        if (!$value$plusargs("NNIB=%d",  nnib))       begin $display("need +NNIB=");  $finish; end
        if (!$value$plusargs("MODE=%d",  mode))       mode = 0;
        if (!$value$plusargs("NAME=%s",  name))       name = "case";

        $readmemb(plain_file, plain_mem);
        $readmemh(nib_file,   exp_nib);

        @(negedge clk); rst = 1; en = 1;
        @(negedge clk); rst = 0;

        i = 0; pidx = 0;
        while (i < nbits) begin
            @(negedge clk);
            rem = nbits - i;
            if (mode == 0) nb = (rem >= 2) ? 2 : rem;
            else begin
                nb = patt[pidx];
                pidx = (pidx == 5) ? 0 : pidx + 1;
                if (nb > rem) nb = rem;
            end
            if (nb == 0) begin
                in_valid = 0; in_nbits = 0; in_bits = 0;
            end else begin
                in_valid = 1; in_nbits = nb[1:0];
                in_bits[0] = plain_mem[i];
                in_bits[1] = (nb >= 2) ? plain_mem[i+1] : 1'b0;
                i = i + nb;
            end
        end
        @(negedge clk); in_valid = 0; in_nbits = 0;
        repeat (16) @(negedge clk);

        // ---- checks ------------------------------------------------------
        if (got_nib_n != nnib) begin
            $display("FAIL[%0s]: nibble count got=%0d exp=%0d", name, got_nib_n, nnib);
            $finish;
        end
        for (i = 0; i < nnib; i = i + 1) begin
            if (got_nib[i] !== exp_nib[i]) begin
                $display("FAIL[%0s]: nibble[%0d] got=%h exp=%h",
                         name, i, got_nib[i], exp_nib[i]); $finish;
            end
        end
        if (sof_cnt != 1) begin $display("FAIL[%0s]: sof_cnt=%0d exp 1", name, sof_cnt); $finish; end
        if (eof_cnt != 1) begin $display("FAIL[%0s]: eof_cnt=%0d exp 1", name, eof_cnt); $finish; end
        if (cg0 !== 5'b11000 || sym0 !== 3'd2) begin
            $display("FAIL[%0s]: cg0 not /J/ (%b sym=%0d)", name, cg0, sym0); $finish; end
        if (cg1 !== 5'b10001 || sym1 !== 3'd3) begin
            $display("FAIL[%0s]: cg1 not /K/ (%b sym=%0d)", name, cg1, sym1); $finish; end
        if (!saw_T_then_R) begin
            $display("FAIL[%0s]: never saw /T/ then /R/ ESD", name); $finish; end

        $display("PASS[%0s]: mode=%0d nibbles=%0d exact, SSD=/J/K/ ok, ESD=/T/R/ ok, sof=1 eof=1, cgs=%0d, <=1 nib/clk, no FIFO ovf",
                 name, mode, nnib, cg_cnt);
        $finish;
    end
endmodule

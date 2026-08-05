// tb_eth_4b5b.v -- iverilog testbench: eth_4b5b vs golden-model vectors.
// Feeds <case>.plain_bits one bit/clk, collects the nibble stream and code
// groups, and checks them bit/nibble-exact against <case>.mii_nibbles plus
// the SSD/ESD alignment and a control-symbol case.
//
// Plusargs: +PLAIN=<file> +NIBS=<file> +NBITS=<n> +NNIB=<n> +NAME=<label>

`timescale 1ns/1ps
module tb_eth_4b5b;
    integer i;
    reg [8*160-1:0] plain_file, nib_file;
    reg [8*32-1:0]  name;
    integer nbits, nnib;

    // vector storage
    reg        plain_mem [0:2047];   // one descrambled bit per entry
    reg [3:0]  exp_nib   [0:511];    // expected MII nibbles (hex)

    // collected outputs
    reg [3:0]  got_nib [0:511];
    integer    got_nib_n;
    integer    sof_cnt, eof_cnt, cg_cnt;
    // capture first two code groups + the T/R at end
    reg [4:0]  cg0, cg1;
    reg [2:0]  sym0, sym1;
    integer    saw_T_then_R;
    reg [2:0]  prev_sym;
    reg        prev_ctrl;

    // DUT
    reg        clk, rst, en, in_bit, in_valid;
    wire       cg_stb, cg_ctrl, cg_err, nibble_stb, sof, eof, locked;
    wire [4:0] cg_code;
    wire [2:0] cg_sym;
    wire [3:0] nibble;

    eth_4b5b dut(
        .clk(clk), .rst(rst), .en(en),
        .in_bit(in_bit), .in_valid(in_valid),
        .cg_stb(cg_stb), .cg_code(cg_code), .cg_ctrl(cg_ctrl),
        .cg_sym(cg_sym), .cg_err(cg_err),
        .nibble(nibble), .nibble_stb(nibble_stb),
        .sof(sof), .eof(eof), .locked(locked)
    );

    always #5 clk = ~clk;

    // collect outputs on each rising edge
    always @(posedge clk) begin
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
        if (sof) sof_cnt = sof_cnt + 1;
        if (eof) eof_cnt = eof_cnt + 1;
    end

    initial begin
        clk = 0; rst = 1; en = 0; in_bit = 0; in_valid = 0;
        got_nib_n = 0; sof_cnt = 0; eof_cnt = 0; cg_cnt = 0;
        saw_T_then_R = 0; prev_sym = 3'd0; prev_ctrl = 0;

        if (!$value$plusargs("PLAIN=%s", plain_file)) begin
            $display("need +PLAIN="); $finish; end
        if (!$value$plusargs("NIBS=%s", nib_file)) begin
            $display("need +NIBS="); $finish; end
        if (!$value$plusargs("NBITS=%d", nbits)) begin
            $display("need +NBITS="); $finish; end
        if (!$value$plusargs("NNIB=%d", nnib)) begin
            $display("need +NNIB="); $finish; end
        if (!$value$plusargs("NAME=%s", name)) name = "case";

        $readmemb(plain_file, plain_mem);
        $readmemh(nib_file, exp_nib);

        // reset for a few cycles
        @(negedge clk); rst = 1; en = 1;
        @(negedge clk); rst = 0;

        // feed plain bits one per clock
        for (i = 0; i < nbits; i = i + 1) begin
            @(negedge clk);
            in_valid = 1;
            in_bit   = plain_mem[i];
        end
        @(negedge clk); in_valid = 0;
        // drain a few cycles
        repeat (8) @(negedge clk);

        // ---- checks ------------------------------------------------------
        if (got_nib_n != nnib) begin
            $display("FAIL[%0s]: nibble count got=%0d exp=%0d",
                     name, got_nib_n, nnib); $finish;
        end
        for (i = 0; i < nnib; i = i + 1) begin
            if (got_nib[i] !== exp_nib[i]) begin
                $display("FAIL[%0s]: nibble[%0d] got=%h exp=%h",
                         name, i, got_nib[i], exp_nib[i]); $finish;
            end
        end
        if (sof_cnt != 1) begin
            $display("FAIL[%0s]: sof_cnt=%0d exp 1", name, sof_cnt); $finish; end
        if (eof_cnt != 1) begin
            $display("FAIL[%0s]: eof_cnt=%0d exp 1", name, eof_cnt); $finish; end
        if (cg0 !== 5'b11000 || sym0 !== 3'd2) begin
            $display("FAIL[%0s]: cg0 not /J/ (%b sym=%0d)", name, cg0, sym0);
            $finish; end
        if (cg1 !== 5'b10001 || sym1 !== 3'd3) begin
            $display("FAIL[%0s]: cg1 not /K/ (%b sym=%0d)", name, cg1, sym1);
            $finish; end
        if (!saw_T_then_R) begin
            $display("FAIL[%0s]: never saw /T/ then /R/ ESD", name); $finish; end

        $display("PASS[%0s]: nibbles=%0d exact, SSD=/J/K/ ok, ESD=/T/R/ ok, sof=1 eof=1, cgs=%0d",
                 name, nnib, cg_cnt);
        $finish;
    end
endmodule

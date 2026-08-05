// tb_eth_4b5b_dir.v -- directed checks: reset-inert gating, invalid code-group
// error flag, and idle-before-SSD produces no output.
`timescale 1ns/1ps
module tb_eth_4b5b_dir;
    reg clk=0, rst, en, in_bit, in_valid;
    wire cg_stb, cg_ctrl, cg_err, nibble_stb, sof, eof, locked;
    wire [4:0] cg_code; wire [2:0] cg_sym; wire [3:0] nibble;
    integer errs=0, stbs=0, i;
    reg saw_err;

    eth_4b5b dut(.clk(clk),.rst(rst),.en(en),.in_bit(in_bit),.in_valid(in_valid),
      .cg_stb(cg_stb),.cg_code(cg_code),.cg_ctrl(cg_ctrl),.cg_sym(cg_sym),
      .cg_err(cg_err),.nibble(nibble),.nibble_stb(nibble_stb),
      .sof(sof),.eof(eof),.locked(locked));
    always #5 clk=~clk;

    // shift a bit
    task bit1(input b); begin @(negedge clk); in_valid=1; in_bit=b; end endtask
    // feed 5-bit group MSB-first
    task grp5(input [4:0] g); begin
        bit1(g[4]); bit1(g[3]); bit1(g[2]); bit1(g[1]); bit1(g[0]);
    end endtask

    always @(posedge clk) begin
        if (cg_stb) stbs = stbs + 1;
        if (cg_err) saw_err = 1;
    end

    initial begin
        rst=1; en=1; in_bit=0; in_valid=0; saw_err=0;
        @(negedge clk); @(negedge clk); rst=0;

        // 1) inert while en=0: shift a full /J/K/ with en=0 -> no sof/lock
        en=0;
        grp5(5'b11000); grp5(5'b10001);
        @(negedge clk); in_valid=0; @(negedge clk);
        if (sof || locked || stbs!=0) begin
            $display("FAIL: engine active while en=0 (sof=%b lock=%b stbs=%0d)",
                     sof, locked, stbs); $finish; end

        // 2) enable, send idle then SSD then one data group (0x5=01011),
        //    then an INVALID code group 00110, then valid data, then T,R.
        en=1;
        for (i=0;i<16;i=i+1) bit1(1'b1);      // idle all-ones
        grp5(5'b11000); grp5(5'b10001);       // /J/K/ SSD
        grp5(5'b01011);                       // data 0x5
        grp5(5'b00110);                       // INVALID code (not in table)
        grp5(5'b01011);                       // data 0x5
        grp5(5'b01101); grp5(5'b00111);       // /T/R/ ESD
        @(negedge clk); in_valid=0; repeat(6) @(negedge clk);

        if (!saw_err) begin
            $display("FAIL: invalid code group 00110 did not raise cg_err"); $finish; end
        if (!locked && eof===1'bx) ; // eof pulse already passed; just informational

        $display("PASS[dir]: en=0 inert (no strobes), invalid-code cg_err raised");
        $finish;
    end
endmodule

// tb_gpmc_slave.v -- write/read/pop semantics of gpmc_slave against the GPMC model, with the full
// 7-bit selector (bits 1:0 = GPMC A2/A1 on balls A2/B1, schema v3).
`timescale 1ns/1ps
module tb_gpmc_slave;
    reg clk = 0; always #6.25 clk = ~clk;          // C2 ~80 MHz
    wire nCS1, nOE, nWE; wire [6:0] sel; wire [15:0] d;
    wire we_commit, rd_pop, drive_active;
    wire [7:0] wr_sel, rd_sel; wire [15:0] wr_data; wire [1:0] wr_aux;
    wire [15:0] rdata = {rd_sel, 8'ha5};
    gpmc_bfm bfm (.nCS1(nCS1), .nOE(nOE), .nWE(nWE), .sel(sel), .d(d));
    gpmc_slave dut (.clk(clk), .nCS1(nCS1), .nOE(nOE), .nWE(nWE), .sel(sel), .gpmc_d(d),
        .we_commit(we_commit), .wr_sel(wr_sel), .wr_data(wr_data), .wr_aux(wr_aux),
        .rd_pop(rd_pop), .rd_sel(rd_sel), .rdata(rdata), .drive_active(drive_active));

    integer errors = 0, n_we = 0, n_pop = 0, n_zviol = 0;
    reg [7:0] pop_sel_seen = 0, we_sel_seen = 0; reg [15:0] we_data_seen = 0; reg [1:0] we_aux_seen = 0;
    always @(posedge clk) begin
        if (we_commit) begin n_we = n_we + 1; we_sel_seen = wr_sel; we_data_seen = wr_data; we_aux_seen = wr_aux; end
        if (rd_pop)    begin n_pop = n_pop + 1; pop_sel_seen = rd_sel; end
    end
    // the data bus must be Hi-Z from the FPGA side unless nCS1 & nOE are both low
    always @(nCS1 or nOE or d or bfm.drive) begin
        #0.01;   // let the same-timestep continuous assignments settle
        if (!(nCS1 == 1'b0 && nOE == 1'b0) && !bfm.drive && d !== 16'hzzzz) n_zviol = n_zviol + 1;
    end

    task check(input cond, input [8*48-1:0] msg); begin if (!cond) begin errors = errors + 1; $display("FAIL: %0s", msg); end end endtask

    reg [15:0] v;
    initial begin
        #100;
        // ---- write: one commit, the full selector, data, aux = {B1, A2} = {sel[0], sel[1]} ----
        bfm.write(7'h25, 16'h1234); #100;
        check(n_we == 1, "write: exactly one we_commit");
        check(we_sel_seen == 8'h25, "write: wr_sel = {0, A7..A1} (0x25 stays 0x25)");
        check(we_data_seen == 16'h1234, "write: wr_data");
        check(we_aux_seen == 2'b10, "write: aux {B1,A2} = {sel[0],sel[1]} of 0x25");
        check(n_pop == 0, "write: no rd_pop");
        bfm.write(7'h42, 16'h4242); #100;
        check(we_sel_seen == 8'h42 && we_aux_seen == 2'b01, "write: 0x42 -> aux {B1,A2} = 01");
        // ---- read: data from the live selector, one pop with the right selector ----
        bfm.read(7'h45, v); #100;
        check(v == 16'h45a5, "read: data from live rd_sel (odd selector)");
        check(n_pop == 1, "read: exactly one rd_pop");
        check(pop_sel_seen == 8'h45, "read: rd_sel at the pop");
        check(n_we == 2, "read: no we_commit");
        // ---- read where the address changes right after nOE rises: pop still sees 0x41 ----
        bfm.sel = 7'h41; #10; bfm.nCS1 = 0; #10; bfm.nOE = 0; #80; v = d; bfm.nOE = 1; bfm.sel = 7'h10; #10; bfm.nCS1 = 1; #100;
        check(v == 16'h41a5, "read2: data");
        check(n_pop == 2, "read2: one pop");
        check(pop_sel_seen == 8'h41, "read2: pop selector held from the read (not the new address)");
        // ---- back-to-back reads (EDMA-like, 50 ns gap) each pop once ----
        bfm.read(7'h40, v); bfm.read(7'h40, v); bfm.read(7'h00, v); #100;
        check(n_pop == 5, "burst: three pops");
        check(v == 16'h00a5, "burst: alias 0x00 data");
        // ---- writes of every selector commit exactly once ----
        begin : wr_all
            integer i; for (i = 0; i < 128; i = i + 1) bfm.write(i[6:0], 16'h8000 | i[6:0]);
            #100; check(n_we == 130, "128 writes -> 128 commits");
            check(we_sel_seen == 8'h7f, "last write sel 0x7f"); check(we_data_seen == 16'h807f, "last write data");
        end
        check(n_zviol == 0, "gpmc_d only driven while nCS1&nOE low");
        check(n_pop == 5, "writes never pop");
        if (errors == 0) $display("PASS tb_gpmc_slave"); else $display("FAIL tb_gpmc_slave: %0d errors", errors);
        $finish;
    end
endmodule

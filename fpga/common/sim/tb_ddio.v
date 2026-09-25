// ENGMODEL-OWNER-UNIT: FU-RTL-COMMON-TESTS
// tb_ddio.v -- the ddio_out1 / ddio_pair SIM model: clock forward, static, SDR, complementary.
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-189
module tb_ddio;
    reg clk = 0; always #2.5 clk = ~clk;
    reg dh = 0, dl = 0; wire p, n;
    ddio_pair dut (.outclock(clk), .dh(dh), .dl(dl), .pad_p(p), .pad_n(n));
    integer errors = 0, mism = 0, comp = 0;
    task check(input cond, input [8*48-1:0] msg); begin if (!cond) begin errors = errors + 1; $display("FAIL: %0s", msg); end end endtask
    reg d = 0;
    initial begin
        #20;
        dh = 1; dl = 0; #20.3;                      // clock forward: p == clk, n == ~clk (sample off the edges)
        repeat (40) begin #1; if (p !== clk) mism = mism + 1; if (n !== ~clk) comp = comp + 1; end
        check(mism == 0, "clock forward p == outclock");
        check(comp == 0, "pair n == ~p");
        dh = 0; dl = 0; #20;                         // static 0
        mism = 0; repeat (40) begin #1; if (p !== 1'b0 || n !== 1'b1) mism = mism + 1; end
        check(mism == 0, "static {0,0} -> p=0 n=1");
        // SDR data: p follows d (registered), n complementary
        mism = 0;
        repeat (10) begin d = ~d; dh = d; dl = d; #20; if (p !== d || n !== ~d) mism = mism + 1; end
        check(mism == 0, "SDR {d,d} -> p=d n=~d");
        if (errors == 0) $display("PASS tb_ddio"); else $display("FAIL tb_ddio: %0d errors", errors);
        $finish;
    end
endmodule

// ENGMODEL-OWNER-UNIT: FU-RTL-COMMON-TESTS
// tb_lane_in.v -- lane_in registers, lane_mon ever-flags with gate reset, toggle_ctr window count
// and saturation.
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-190
module tb_lane_in;
    reg clk = 0; always #10 clk = ~clk;      // 50 MHz
    integer errors = 0;
    task check(input cond, input [8*48-1:0] msg); begin if (!cond) begin errors = errors + 1; $display("FAIL: %0s", msg); end end endtask

    reg [7:0] pad = 0; wire [7:0] q;
    lane_in #(.N(8)) u_in (.clk(clk), .pad(pad), .q(q));

    reg [3:0] d = 0; reg gate = 0; wire [3:0] e1, e0;
    lane_mon #(.N(4)) u_mon (.clk(clk), .d(d), .gate_rst(gate), .ever1(e1), .ever0(e0));

    reg t = 0; wire [15:0] cnt; wire wd; integer nwd = 0;
    toggle_ctr u_tc (.clk(clk), .d(t), .count(cnt), .window_done(wd));
    always @(posedge clk) if (wd) nwd = nwd + 1;

    // stimulus modes for the toggle counter: 0 idle, 1 square wave (512-cycle half period), 2 every cycle
    integer mode = 0, hp = 0;
    always @(posedge clk) begin
        if (mode == 1) begin hp = hp + 1; if (hp == 512) begin hp = 0; t <= ~t; end end
        else if (mode == 2) t <= ~t;
    end
    integer i;
    initial begin
        // lane_in: two-stage register
        @(posedge clk); pad <= 8'h5a; @(posedge clk); @(posedge clk); #1; check(q == 8'h5a, "lane_in q after 2 clocks");
        // lane_mon (gate first so the power-up level does not pre-set ever-0)
        gate = 1; repeat (2) @(posedge clk); d = 4'b0101; @(posedge clk); gate = 0; repeat (3) @(posedge clk); #1;
        check(e1 == 4'b0101 && e0 == 4'b1010, "ever flags follow the level");
        d = 4'b1010; repeat (3) @(posedge clk); #1;
        check(e1 == 4'b1111 && e0 == 4'b1111, "ever flags accumulate");
        gate = 1; repeat (2) @(posedge clk); gate = 0; #1;
        check(e1 == 0 && e0 == 0, "gate reset clears");
        repeat (2) @(posedge clk); #1; check(e1 == 4'b1010 && e0 == 4'b0101, "flags restart after gate");
        // toggle_ctr: square wave, 512-cycle half period -> 128 transitions per 65536-cycle window
        mode = 1;
        wait (nwd == 2); #1;
        check(cnt == 16'd128, "toggle count 128 per window");
        // saturation: toggle every cycle -> 65535
        mode = 2;
        wait (nwd == 5); #1; check(cnt == 16'hffff, "toggle count saturates at 0xFFFF");
        mode = 0;
        wait (nwd == 7); #1; check(cnt == 16'd0 || cnt == 16'd1, "idle window counts 0 (or the last edge)");
        if (errors == 0) $display("PASS tb_lane_in"); else $display("FAIL tb_lane_in: %0d errors", errors);
        $finish;
    end
endmodule

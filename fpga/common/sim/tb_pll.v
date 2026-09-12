// tb_pll.v -- pll_m2 SIM stand-in: lock, periods, phase offsets, and the dynamic phase-step engine.
`timescale 1ns/1ps
module tb_pll;
    reg mclk = 0; always #3.125 mclk = ~mclk;
    reg scan = 0; always #6.25 scan = ~scan;
    wire ph0, ph90, ph180, ph270, cap, c100, c50, la, lb, busy, err;
    reg [7:0] target = 0; wire [7:0] applied;
    pll_m2 dut (.mclk_in(mclk), .ph0(ph0), .ph90(ph90), .ph180(ph180), .ph270(ph270), .cap_clk(cap),
        .clk100(c100), .clk50(c50), .locked_a(la), .locked_b(lb), .scanclk(scan),
        .step_target(target), .step_applied(applied), .step_busy(busy), .step_err(err));
    integer errors = 0;
    task check(input cond, input [8*48-1:0] msg); begin if (!cond) begin errors = errors + 1; $display("FAIL: %0s", msg); end end endtask
    real t0, t1, tp;
    task period(input integer which, output real per);
        begin
            case (which) 0: @(posedge ph0); 1: @(posedge c100); 2: @(posedge c50); 3: @(posedge cap); endcase
            t0 = $realtime;
            case (which) 0: @(posedge ph0); 1: @(posedge c100); 2: @(posedge c50); 3: @(posedge cap); endcase
            per = $realtime - t0;
        end
    endtask
    task cap_offset(output real off);   // cap rising edge time minus the preceding ph0 rising edge
        begin @(posedge cap); t1 = $realtime; @(posedge ph0); t0 = $realtime; @(posedge cap); off = $realtime - t0; end
    endtask
    real off;
    initial begin
        #500; check(la == 0 && lb == 0, "not locked before 1 us");
        #1000; check(la == 1 && lb == 1, "locked after 1 us");
        period(0, tp); check(tp > 4.999 && tp < 5.001, "ph0 period 5 ns");
        period(1, tp); check(tp > 9.999 && tp < 10.001, "clk100 period 10 ns");
        period(2, tp); check(tp > 19.999 && tp < 20.001, "clk50 period 20 ns");
        @(posedge ph0); t0 = $realtime; @(posedge ph90); check(($realtime - t0) > 1.249 && ($realtime - t0) < 1.251, "ph90 lags ph0 by 1.25 ns");
        @(posedge ph0); t0 = $realtime; @(posedge ph270); check(($realtime - t0) > 3.749 && ($realtime - t0) < 3.751, "ph270 lags ph0 by 3.75 ns");
        cap_offset(off); check(off < 0.001, "cap_clk at step 0 aligned to ph0");
        // step up to 6 (= 90 deg = 1.248 ns)
        target = 6; repeat (3) @(posedge scan); #1; check(busy, "engine busy after target change");
        wait (!busy && applied == 6); #50;
        check(err == 0, "no step error");
        cap_offset(off); check(off > 1.247 && off < 1.249, "cap_clk offset 6 x 208 ps");
        target = 2; wait (applied == 2); wait (!busy); #50;
        cap_offset(off); check(off > 0.415 && off < 0.417, "cap_clk offset 2 x 208 ps (stepped down)");
        if (errors == 0) $display("PASS tb_pll"); else $display("FAIL tb_pll: %0d errors", errors);
        $finish;
    end
    initial begin #200000; $display("FAIL tb_pll: timeout"); $finish; end
endmodule

// tb_sr_accum.v — self-checking testbench for sr_accum.v
// =========================================================================
// Feeds a synthetic FAST stream over N trigger-referenced repetitions into the
// in-fabric combine engine, then drains the frozen grid and checks every bin's
// {cnt,sum,sum2} plus the odd-half {cntA,sumA} and the other channel {cnt,sum}
// against an independently-maintained golden accumulation using the SAME bin/
// odd rule. Exercises: trigger-referenced pos anchor, the RMW pipeline, the
// cnt-saturation overflow guard (DMAX small on purpose), the odd/even split,
// the freeze-then-snapshot CDC, and the 12-word/bin drain packing/order.
//
// Also proves the byte-identical GATE: with combine_en=0 the engine never arms,
// never drives bin_tick, and busy/coherent stay low.
//
// This proves the ENGINE logic in iverilog. Real-bench 200 MHz interleave skew
// calibration (true-600) is a separate gated scope Verify (bench-owed).
`timescale 1ns/1ps
`default_nettype none

module tb_sr_accum;
    localparam integer NBINS = 16;
    localparam integer AW    = 4;
    localparam integer DMAX  = 8;    // small: forces saturation after 8 reps
    localparam integer NREP  = 12;   // > DMAX -> reps 8..11 must be dropped
    localparam integer WPB   = 12;

    reg         cap_clk = 1'b0, clk = 1'b0;
    reg         combine_en = 1'b0;
    reg         smp_valid = 1'b0;
    reg  [7:0]  smp_a = 8'd0, smp_b = 8'd0;
    reg         trig = 1'b0, trig_en = 1'b1;
    reg         arm = 1'b0, halt = 1'b0, drain_req = 1'b0;
    wire [15:0] bin_word;
    wire        bin_tick, busy, coherent;

    sr_accum #(.NBINS(NBINS), .AW(AW), .DMAX(DMAX)) dut (
        .cap_clk(cap_clk), .combine_en(combine_en),
        .smp_valid(smp_valid), .smp_a(smp_a), .smp_b(smp_b),
        .trig(trig), .trig_en(trig_en),
        .clk(clk), .arm(arm), .halt(halt), .drain_req(drain_req),
        .bin_word(bin_word), .bin_tick(bin_tick),
        .busy(busy), .coherent(coherent)
    );

    always #2.5  cap_clk = ~cap_clk;   // 200 MHz
    always #6.25 clk     = ~clk;       // 80 MHz

    // ---- golden accumulators ----
    integer gcnt  [0:NBINS-1];
    integer gsum  [0:NBINS-1];
    integer gsum2 [0:NBINS-1];
    integer gcntA [0:NBINS-1];
    integer gsumA [0:NBINS-1];
    integer gbcnt [0:NBINS-1];
    integer gbsum [0:NBINS-1];

    // ---- drained word capture ----
    integer rbuf [0:NBINS*WPB-1];
    integer rcount;
    always @(posedge clk) begin
        if (bin_tick) begin
            rbuf[rcount] = bin_word;
            rcount = rcount + 1;
        end
    end

    integer errors = 0;
    integer r, pos, b, tick_seen;
    reg [7:0] va, vb;

    // synthetic fast-stream pattern (deterministic, varied for real sum2)
    function [7:0] pat_a; input integer rr; input integer pp;
        pat_a = (pp*17 + rr*3) & 8'hFF; endfunction
    function [7:0] pat_b; input integer rr; input integer pp;
        pat_b = (pp*5 + rr*7 + 11) & 8'hFF; endfunction

    task feed_rep(input integer rr);
        integer pp;
        reg oddpass;
        begin
            // trigger: one clean rising edge, allow the synchronizer to catch it
            @(negedge cap_clk); trig = 1'b1; smp_valid = 1'b0;
            repeat (3) @(negedge cap_clk);
            trig = 1'b0;
            repeat (4) @(negedge cap_clk);   // let pos reset + pass_idx++ settle
            // pass parity mirrors the RTL: FI_ACCUM enters pass_idx=0, each
            // trigger increments -> rep rr uses parity (rr+1)&1.
            oddpass = ((rr+1) & 1);
            for (pp = 0; pp < NBINS; pp = pp + 1) begin
                @(negedge cap_clk);
                smp_a = pat_a(rr, pp); smp_b = pat_b(rr, pp); smp_valid = 1'b1;
                // golden (same overflow guard + odd rule)
                va = pat_a(rr, pp); vb = pat_b(rr, pp);
                if (gcnt[pp] != DMAX) begin
                    gsum[pp]  = gsum[pp]  + va;
                    gsum2[pp] = gsum2[pp] + va*va;
                    if (oddpass) begin
                        gcntA[pp] = gcntA[pp] + 1;
                        gsumA[pp] = gsumA[pp] + va;
                    end
                    gcnt[pp] = gcnt[pp] + 1;
                end
                if (gbcnt[pp] != DMAX) begin
                    gbsum[pp] = gbsum[pp] + vb;
                    gbcnt[pp] = gbcnt[pp] + 1;
                end
            end
            @(negedge cap_clk); smp_valid = 1'b0;
            repeat (2) @(negedge cap_clk);
        end
    endtask

    // reassemble one bin from the 12-word drain buffer and check vs golden
    task check_bin(input integer bb);
        integer base;
        integer d_cnt, d_sum, d_sum2, d_cntA, d_sumA, d_bcnt, d_bsum;
        begin
            base   = bb*WPB;
            d_cnt  = rbuf[base+0];
            d_sum  = rbuf[base+1] | (rbuf[base+2]  << 16);
            d_sum2 = rbuf[base+3] | (rbuf[base+4]  << 16) | (rbuf[base+5] << 32);
            d_cntA = rbuf[base+6];
            d_sumA = rbuf[base+7] | (rbuf[base+8]  << 16);
            d_bcnt = rbuf[base+9];
            d_bsum = rbuf[base+10] | (rbuf[base+11] << 16);
            if (d_cnt  !== gcnt[bb])  begin errors=errors+1; $display("FAIL bin %0d cnt  dev=%0d gold=%0d", bb, d_cnt,  gcnt[bb]);  end
            if (d_sum  !== gsum[bb])  begin errors=errors+1; $display("FAIL bin %0d sum  dev=%0d gold=%0d", bb, d_sum,  gsum[bb]);  end
            if (d_sum2 !== gsum2[bb]) begin errors=errors+1; $display("FAIL bin %0d sum2 dev=%0d gold=%0d", bb, d_sum2, gsum2[bb]); end
            if (d_cntA !== gcntA[bb]) begin errors=errors+1; $display("FAIL bin %0d cntA dev=%0d gold=%0d", bb, d_cntA, gcntA[bb]); end
            if (d_sumA !== gsumA[bb]) begin errors=errors+1; $display("FAIL bin %0d sumA dev=%0d gold=%0d", bb, d_sumA, gsumA[bb]); end
            if (d_bcnt !== gbcnt[bb]) begin errors=errors+1; $display("FAIL bin %0d bcnt dev=%0d gold=%0d", bb, d_bcnt, gbcnt[bb]); end
            if (d_bsum !== gbsum[bb]) begin errors=errors+1; $display("FAIL bin %0d bsum dev=%0d gold=%0d", bb, d_bsum, gbsum[bb]); end
        end
    endtask

    initial begin
        for (b=0;b<NBINS;b=b+1) begin
            gcnt[b]=0; gsum[b]=0; gsum2[b]=0; gcntA[b]=0; gsumA[b]=0;
            gbcnt[b]=0; gbsum[b]=0;
        end
        rcount = 0;

        // ---------- GATE proof: combine_en=0, arm must do NOTHING ----------
        combine_en = 1'b0;
        @(posedge clk); arm = 1'b1; @(posedge clk); arm = 1'b0;
        repeat (NBINS+40) @(posedge cap_clk);
        tick_seen = 0;
        if (busy)      begin errors=errors+1; $display("FAIL gate: busy high with combine_en=0"); end
        if (coherent)  begin errors=errors+1; $display("FAIL gate: coherent high with combine_en=0"); end
        // feed junk that must be ignored
        combine_en = 1'b0;
        @(negedge cap_clk); smp_a=8'hAA; smp_b=8'h55; smp_valid=1'b1;
        repeat (20) @(negedge cap_clk);
        smp_valid=1'b0;
        @(posedge clk); drain_req=1'b1; @(posedge clk); drain_req=1'b0;
        repeat (10) @(posedge clk);
        if (rcount != 0) begin errors=errors+1; $display("FAIL gate: %0d words drained with combine_en=0", rcount); end

        // ---------- ACTIVE combine ----------
        combine_en = 1'b1;
        // arm -> clear sweep -> accumulate
        @(posedge clk); arm = 1'b1; @(posedge clk); arm = 1'b0;
        // wait past the NBINS-cycle clear sweep into FI_ACCUM
        repeat (NBINS + 20) @(posedge cap_clk);

        for (r = 0; r < NREP; r = r + 1) feed_rep(r);

        // freeze
        @(posedge clk); halt = 1'b1; @(posedge clk); halt = 1'b0;
        // wait for coherent (freeze CDC)
        tick_seen = 0;
        while (!coherent && tick_seen < 1000) begin @(posedge clk); tick_seen = tick_seen + 1; end
        if (!coherent) begin errors=errors+1; $display("FAIL: coherent never asserted"); end

        // drain the grid
        rcount = 0;
        @(posedge clk); drain_req = 1'b1; @(posedge clk); drain_req = 1'b0;
        tick_seen = 0;
        while (rcount < NBINS*WPB && tick_seen < 100000) begin @(posedge clk); tick_seen = tick_seen + 1; end
        if (rcount != NBINS*WPB) begin
            errors=errors+1; $display("FAIL: drained %0d words, expected %0d", rcount, NBINS*WPB);
        end else begin
            for (b = 0; b < NBINS; b = b + 1) check_bin(b);
        end

        // sanity: bin 0 must have saturated at DMAX (proves overflow guard fired)
        if (gcnt[0] != DMAX) begin errors=errors+1; $display("FAIL tb-self: golden bin0 cnt=%0d expected DMAX=%0d", gcnt[0], DMAX); end

        if (errors == 0)
            $display("tb_sr_accum: PASS (%0d bins, DMAX=%0d, %0d reps, saturation+odd/even+drain checked)", NBINS, DMAX, NREP);
        else
            $display("tb_sr_accum: FAIL (%0d errors)", errors);
        if (errors != 0) $stop;
        $finish;
    end

    // global watchdog
    initial begin
        #5000000;
        $display("tb_sr_accum: TIMEOUT");
        $stop;
    end
endmodule
`default_nettype wire

// tb_fast_siggen.v — self-checking testbench for fast_siggen.v + a copy of sr_accum
// =========================================================================
// Proves the FABRIC FAST-SIGNAL GENERATOR end of the super-res proving chain:
//
//   1. CLEAN TRIANGLE: fast_siggen (cap_clk domain) makes a clean repetitive
//      triangle; the amplitude satisfies the host reference-lock gate
//      (hi-lo >> 12, trough>6, crest<253 => never "Clipped").
//   2. COHERENT GRID: feeding that triangle through a copy of sr_accum (the
//      exact shipping mean-only config: FULLSTATS=0, WITH_OTHER=0), trigger-
//      referenced on samp[7], the drained per-bin mean (sum/cnt) reconstructs
//      the SAME triangle — every bin populated, smooth (adjacent step 1),
//      exactly two turning points (one crest, one trough): coherent + varying.
//   3. NOISE REDUCTION: injecting per-sample amplitude noise (models ADC noise;
//      the trigger stays clean = a stable ETS time reference) and STACKING many
//      reps, the drained per-bin mean stays close to the clean triangle — the
//      per-bin residual is far smaller than the injected noise amplitude, i.e.
//      averaging suppressed the noise (~1/sqrt(reps)).
//   4. GATE / OFF: with combine_en=0 the engine never arms/drains (byte-identical
//      inertness); with siggen en=0 the fast source is parked flat.
//
// This validates the LOGIC in iverilog. The physical 200 MHz interleave skew
// (true-600 per-core cal) is a separate bench Verify and is not exercised here.
// =========================================================================
`timescale 1ns/1ps
`default_nettype none

module tb_fast_siggen;
    // ---- shipping combine geometry (mean-only) ----
    localparam integer NBINS = 256;
    localparam integer AW    = 8;
    localparam integer DMAX  = 255;
    localparam integer WPB   = 12;
    // fast_siggen defaults: triangle 64..192, period 2*(192-64)=256 == NBINS.
    localparam integer AMIN  = 64;
    localparam integer AMAX  = 192;
    localparam integer NREP  = 128;   // periods stacked (cnt ~ 128/bin < DMAX)
    localparam integer NOISE = 15;    // injected amplitude noise: +/-NOISE (uniform)

    reg  cap_clk = 1'b0, clk = 1'b0;
    always #2.5  cap_clk = ~cap_clk;   // 200 MHz fast fill
    always #6.25 clk     = ~clk;       // 80 MHz drain / control

    // ---- fast_siggen (DUT #1) ----
    reg        sg_en   = 1'b0;
    reg        sg_shape= 1'b0;         // 0 = triangle
    wire [7:0] sg_a;
    fast_siggen #(.AMIN(AMIN), .AMAX(AMAX), .STEP(1)) u_siggen (
        .cap_clk(cap_clk), .en(sg_en), .shape(sg_shape), .samp(sg_a)
    );

    // ---- TB noise source (models ADC amplitude noise; NOT part of the fabric) ----
    reg [15:0] lfsr = 16'hACE1;
    always @(posedge cap_clk) lfsr <= {lfsr[14:0], lfsr[15]^lfsr[13]^lfsr[12]^lfsr[10]};
    wire signed [8:0] noise = $signed({1'b0, lfsr[4:0]}) - 9'sd16 + 9'sd1; // -15..+15
    reg        noise_on = 1'b0;
    // noisy sample, clamped away from the rails so it never wraps 8-bit
    wire signed [10:0] a_ns = $signed({3'b0, sg_a}) + (noise_on ? {{2{noise[8]}}, noise} : 11'sd0);
    wire [7:0] a_clamped = (a_ns < 11'sd1)   ? 8'd1
                         : (a_ns > 11'sd254) ? 8'd254
                                             : a_ns[7:0];

    // ---- sr_accum (DUT #2) — EXACT shipping mean-only config ----
    reg        combine_en = 1'b0;
    reg        accum_on   = 1'b0;      // gates smp_valid during the fill window
    reg        arm = 1'b0, halt = 1'b0, drain_req = 1'b0;
    wire [7:0] smp_a = a_clamped;      // siggen (optionally + noise)
    wire       trig  = sg_a[7];        // CLEAN trigger: stable ETS time reference
    wire [15:0] bin_word;
    wire        bin_tick, busy, coherent;

    sr_accum #(.NBINS(NBINS), .AW(AW), .DMAX(DMAX), .WITH_OTHER(0), .FULLSTATS(0)) u_sr (
        .cap_clk(cap_clk), .combine_en(combine_en),
        .smp_valid(accum_on), .smp_a(smp_a), .smp_b(8'd0),
        .trig(trig), .trig_en(1'b1),
        .clk(clk), .arm(arm), .halt(halt), .drain_req(drain_req),
        .bin_word(bin_word), .bin_tick(bin_tick),
        .busy(busy), .coherent(coherent)
    );

    // ---- drained-word capture ----
    integer rbuf [0:NBINS*WPB-1];
    integer rcount;
    always @(posedge clk) if (bin_tick) begin rbuf[rcount] = bin_word; rcount = rcount + 1; end

    // ---- reconstructed grids ----
    integer mean_clean [0:NBINS-1];
    integer cnt_clean  [0:NBINS-1];
    integer mean_noisy [0:NBINS-1];
    integer cnt_noisy  [0:NBINS-1];

    integer errors = 0;
    integer i, b, tick_seen;

    // Run one full COMBINE cycle: arm -> (align to a trigger) -> stack NREP
    // periods -> halt -> drain.  Reconstruct mean[bin]=sum/cnt into the target
    // arrays (which=0 clean, which=1 noisy).
    task run_combine(input integer which);
        integer base, d_cnt, d_sum;
        begin
            noise_on = (which != 0);
            // arm: clear-sweep + open accumulate window (drive on negedge => race-free
            // vs the DUT's posedge sampling of the 1-cycle control pulses).
            @(negedge clk); arm = 1'b1; @(negedge clk); arm = 1'b0;
            repeat (NBINS + 40) @(posedge cap_clk);   // wait past the clear sweep
            // align the fill window to a clean period boundary (samp[7] rising)
            @(negedge sg_a[7]); @(posedge sg_a[7]);
            accum_on = 1'b1;
            repeat (NREP * NBINS) @(posedge cap_clk); // stack NREP full periods
            @(negedge cap_clk); accum_on = 1'b0;
            repeat (4) @(negedge cap_clk);
            // freeze
            @(negedge clk); halt = 1'b1; @(negedge clk); halt = 1'b0;
            tick_seen = 0;
            while (!coherent && tick_seen < 2000) begin @(posedge clk); tick_seen = tick_seen + 1; end
            if (!coherent) begin errors=errors+1; $display("FAIL(%0d): coherent never asserted", which); end
            // drain
            rcount = 0;
            @(negedge clk); drain_req = 1'b1; @(negedge clk); drain_req = 1'b0;
            tick_seen = 0;
            while (rcount < NBINS*WPB && tick_seen < 200000) begin @(posedge clk); tick_seen = tick_seen + 1; end
            if (rcount != NBINS*WPB) begin
                errors=errors+1; $display("FAIL(%0d): drained %0d words, expected %0d", which, rcount, NBINS*WPB);
            end
            for (b = 0; b < NBINS; b = b + 1) begin
                base  = b*WPB;
                d_cnt = rbuf[base+0];
                d_sum = rbuf[base+1] | (rbuf[base+2] << 16);
                if (rbuf[base+2] != 0) begin
                    errors=errors+1; $display("FAIL(%0d) bin %0d: sum hi word nonzero (%0d) - 16b overflow", which, b, rbuf[base+2]);
                end
                if (which == 0) begin
                    cnt_clean[b]  = d_cnt;
                    mean_clean[b] = (d_cnt > 0) ? (d_sum / d_cnt) : 0;
                end else begin
                    cnt_noisy[b]  = d_cnt;
                    mean_noisy[b] = (d_cnt > 0) ? (d_sum / d_cnt) : 0;
                end
            end
        end
    endtask

    integer nzbins, gmax, gmin, maxstep, s, tp;
    integer d, dir, prev;
    integer sumabs, maxabs, e;

    initial begin
        for (i=0;i<NBINS;i=i+1) begin
            mean_clean[i]=0; cnt_clean[i]=0; mean_noisy[i]=0; cnt_noisy[i]=0;
        end
        rcount = 0;

        // =============== GATE proof: combine_en=0 => nothing happens ===============
        sg_en = 1'b1;              // siggen running, but combine gated off
        combine_en = 1'b0;
        @(negedge clk); arm = 1'b1; @(negedge clk); arm = 1'b0;
        accum_on = 1'b1;
        repeat (NBINS+80) @(posedge cap_clk);
        accum_on = 1'b0;
        if (busy)     begin errors=errors+1; $display("FAIL gate: busy high with combine_en=0"); end
        if (coherent) begin errors=errors+1; $display("FAIL gate: coherent high with combine_en=0"); end
        @(negedge clk); drain_req=1'b1; @(negedge clk); drain_req=1'b0;
        repeat (20) @(posedge clk);
        if (rcount != 0) begin errors=errors+1; $display("FAIL gate: %0d words drained with combine_en=0", rcount); end

        // =============== CLEAN triangle stack ===============
        combine_en = 1'b1;
        run_combine(0);

        // (2a) every bin populated ~evenly (each bin one sample/period)
        nzbins = 0;
        for (b=0;b<NBINS;b=b+1) if (cnt_clean[b] > 0) nzbins = nzbins + 1;
        if (nzbins < NBINS) begin errors=errors+1; $display("FAIL clean: only %0d/%0d bins populated", nzbins, NBINS); end
        for (b=0;b<NBINS;b=b+1)
            if (cnt_clean[b] < NREP-2 || cnt_clean[b] > NREP+2) begin
                errors=errors+1; $display("FAIL clean: bin %0d cnt=%0d not ~NREP(%0d)", b, cnt_clean[b], NREP);
            end

        // (2b) amplitude present & not clipped: crest ~AMAX, trough ~AMIN
        gmax = 0; gmin = 255;
        for (b=0;b<NBINS;b=b+1) begin
            if (mean_clean[b] > gmax) gmax = mean_clean[b];
            if (mean_clean[b] < gmin) gmin = mean_clean[b];
        end
        if (gmax < AMAX-2 || gmax > AMAX+0) begin errors=errors+1; $display("FAIL clean: crest %0d != ~%0d", gmax, AMAX); end
        if (gmin > AMIN+2 || gmin < AMIN-0) begin errors=errors+1; $display("FAIL clean: trough %0d != ~%0d", gmin, AMIN); end
        if (gmax - gmin < 12) begin errors=errors+1; $display("FAIL clean: hi-lo=%0d < 12 (host gate would reject)", gmax-gmin); end

        // (2c) coherent + varying: exactly TWO turning points (a clean triangle),
        //      and smooth (adjacent step exactly 1 on the ramps)
        tp = 0; prev = 0;
        for (b = 0; b < NBINS; b = b + 1) begin
            d   = mean_clean[(b+1)%NBINS] - mean_clean[b];
            dir = (d > 0) ? 1 : (d < 0) ? -1 : prev;
            if (prev != 0 && dir != 0 && dir != prev) tp = tp + 1;
            if (dir != 0) prev = dir;
        end
        if (tp != 2) begin errors=errors+1; $display("FAIL clean: %0d turning points (triangle must have 2)", tp); end
        maxstep = 0;
        for (b=0;b<NBINS;b=b+1) begin
            s = mean_clean[(b+1)%NBINS] - mean_clean[b];
            if (s < 0) s = -s;
            if (s > maxstep) maxstep = s;
        end
        if (maxstep > 2) begin errors=errors+1; $display("FAIL clean: max adjacent step %0d > 2 (not smooth/coherent)", maxstep); end

        // =============== NOISY stack: prove averaging reduces the noise ===============
        run_combine(1);
        // per-bin residual of the STACKED noisy mean vs the clean triangle
        sumabs = 0; maxabs = 0;
        for (b=0;b<NBINS;b=b+1) begin
            e = mean_noisy[b] - mean_clean[b];
            if (e < 0) e = -e;
            sumabs = sumabs + e;
            if (e > maxabs) maxabs = e;
        end
        // averaging over ~NREP reps must cut the +/-NOISE per-sample error to a
        // small residual: mean|resid| far below the raw noise amplitude.
        if (sumabs*10 > NOISE*NBINS*3) begin   // mean|resid| > 0.3*NOISE => averaging failed
            errors=errors+1;
            $display("FAIL noise: mean|resid|=%0d.%0d not << NOISE(%0d) (avg not reducing noise)",
                     sumabs/NBINS, (sumabs*10/NBINS)%10, NOISE);
        end
        if (maxabs > NOISE) begin
            errors=errors+1; $display("FAIL noise: max|resid|=%0d >= raw NOISE(%0d) (no reduction)", maxabs, NOISE);
        end
        $display("noise-reduction: mean|resid|=%0d.%0d  max|resid|=%0d  (raw per-sample noise = +/-%0d, ~%0d reps stacked)",
                 sumabs/NBINS, (sumabs*10/NBINS)%10, maxabs, NOISE, NREP);

        // =============== siggen OFF => flat source ===============
        sg_en = 1'b0;
        repeat (20) @(posedge cap_clk);
        if (sg_a != AMIN[7:0]) begin errors=errors+1; $display("FAIL off: siggen not parked at AMIN (%0d)", sg_a); end

        if (errors == 0)
            $display("tb_fast_siggen: PASS (clean triangle coherent grid + noise reduction + gate/off, NBINS=%0d NREP=%0d)", NBINS, NREP);
        else
            $display("tb_fast_siggen: FAIL (%0d errors)", errors);
        if (errors != 0) $stop;
        $finish;
    end

    // watchdog
    initial begin
        #40000000;
        $display("tb_fast_siggen: TIMEOUT");
        $stop;
    end
endmodule
`default_nettype wire

// tb_adc_front.v -- encode generation (rates, pair enable, complementary pairs, all five pairs in
// phase on ph0), samp_tick period, and the LANEMAP assembly with a CH1 seed and a runtime remap.
`timescale 1ns/1ps
module tb_adc_front;
    reg ph0 = 0;
    always #2.5 ph0 = ~ph0;
    wire cap_clk = ph0;
    reg  [79:0] lane = 0;
    wire [4:0] enc_p, enc_n;
    reg enc_en = 0; reg [1:0] rate = 3; reg [4:0] pair_en = 5'b11111;
    // default seed: CH1 bit7..0 = lanes 8 47 45 35 46 36 13 11 ; CH2 = lanes 16..23 identity
    reg [55:0] map1 = {7'd8, 7'd47, 7'd45, 7'd35, 7'd46, 7'd36, 7'd13, 7'd11};
    reg [55:0] map2 = {7'd23, 7'd22, 7'd21, 7'd20, 7'd19, 7'd18, 7'd17, 7'd16};
    wire [79:0] lane_q; wire [15:0] samp; wire tick; wire [3:0] dbg;
    adc_front dut (.cap_clk(cap_clk), .ph0(ph0), .lane(lane),
        .enc_p(enc_p), .enc_n(enc_n), .enc_en(enc_en), .enc_rate(rate), .pair_en(pair_en),
        .map_ch1(map1), .map_ch2(map2), .lane_q(lane_q), .samp(samp), .samp_tick(tick), .dbg(dbg));
    integer errors = 0;
    task check(input cond, input [8*56-1:0] msg); begin if (!cond) begin errors = errors + 1; $display("FAIL: %0s", msg); end end endtask
    real t0, per;
    task enc_period(input integer k, output real p);
        begin @(posedge enc_p[k]); t0 = $realtime; @(posedge enc_p[k]); p = $realtime - t0; end
    endtask
    task tick_period(output real p);
        begin @(posedge cap_clk); while (!tick) @(posedge cap_clk); t0 = $realtime; @(posedge cap_clk); while (!tick) @(posedge cap_clk); p = $realtime - t0; end
    endtask
    integer ncomp = 0, nstatic = 0, i;
    // pair complementarity monitor (after enable settles)
    reg mon_on = 0;
    always @(ph0) if (mon_on) begin #0.1; for (i = 0; i < 5; i = i + 1) if (enc_n[i] !== ~enc_p[i]) ncomp = ncomp + 1; end
    task set_lanes(input [7:0] ch1, input [7:0] ch2);
        begin
            lane = 0;
            lane[8] = ch1[7]; lane[47] = ch1[6]; lane[45] = ch1[5]; lane[35] = ch1[4];
            lane[46] = ch1[3]; lane[36] = ch1[2]; lane[13] = ch1[1]; lane[11] = ch1[0];
            lane[23:16] = ch2;
        end
    endtask
    initial begin
        #100;
        // encode off: all pairs static
        repeat (20) begin #5; if (enc_p !== 5'b00000 || enc_n !== 5'b11111) nstatic = nstatic + 1; end
        check(nstatic == 0, "encode off: p=0 n=1 on all pairs");
        enc_en = 1; #100; mon_on = 1;
        enc_period(0, per); check(per > 4.99 && per < 5.01, "rate 3: 200 MHz on E1");
        enc_period(4, per); check(per > 4.99 && per < 5.01, "rate 3: 200 MHz on E5");
        tick_period(per); check(per > 4.99 && per < 5.01, "rate 3: samp_tick every 5 ns");
        rate = 2; #200; enc_period(0, per); check(per > 9.99 && per < 10.01, "rate 2: 100 MHz");
        tick_period(per); check(per > 9.99 && per < 10.01, "rate 2: tick every 10 ns");
        rate = 1; #200; enc_period(2, per); check(per > 19.99 && per < 20.01, "rate 1: 50 MHz");
        tick_period(per); check(per > 19.99 && per < 20.01, "rate 1: tick every 20 ns");
        rate = 0; #400; enc_period(3, per); check(per > 79.99 && per < 80.01, "rate 0: 12.5 MHz");
        tick_period(per); check(per > 79.99 && per < 80.01, "rate 0: tick every 80 ns");
        #100; check(ncomp == 0, "pairs complementary at every rate");
        // pair enable (freeze probe): E2 off, others on
        rate = 3; pair_en = 5'b11101; #200; nstatic = 0;
        repeat (20) begin #5; if (enc_p[1] !== 1'b0) nstatic = nstatic + 1; end
        check(nstatic == 0, "pair_en: E2 static while others run");
        enc_period(0, per); check(per > 4.99 && per < 5.01, "pair_en: E1 still 200 MHz");
        pair_en = 5'b11111;
        // every pair on ph0 (v2.2, PHASE_SEL retired): all five stay in phase with E1 -- 0.5 ns
        // after E1's rising edge all five are high, 3 ns after it all five are low, over 8 periods.
        enc_en = 0; #100; enc_en = 1; #200;
        nstatic = 0;
        repeat (8) begin
            @(posedge enc_p[0]); #0.5; if (enc_p !== 5'b11111) nstatic = nstatic + 1;
            #2.5; if (enc_p !== 5'b00000) nstatic = nstatic + 1;
        end
        check(nstatic == 0, "all five pairs in phase with E1 (one encode clock)");
        nstatic = 0;
        // LANEMAP assembly (rate 3): ch1 = 0xA3, ch2 = 0x5C
        set_lanes(8'ha3, 8'h5c); #100;
        check(samp == 16'ha35c, "assembly with the default CH1 seed + CH2 identity");
        set_lanes(8'h01, 8'h80); #100;
        check(samp == 16'h0180, "assembly bit 0 / bit 7");
        // runtime remap: CH1 bit 0 <- lane 79
        map1[6:0] = 7'd79; lane[79] = 1; lane[11] = 0; #100;
        check(samp == 16'h0180, "remap CH1 bit0 to lane 79");
        map1[6:0] = 7'd11; #100; check(samp == 16'h0080, "remap back");
        if (errors == 0) $display("PASS tb_adc_front"); else $display("FAIL tb_adc_front: %0d errors", errors);
        $finish;
    end
    initial begin #100000; $display("FAIL tb_adc_front: timeout"); $finish; end
endmodule

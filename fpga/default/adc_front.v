// adc_front.v -- ADC front end of the default image: encode generation on the five DDIO pairs,
// the 80 registered lanes, and the LANEMAP-driven assembly of the CH1/CH2 sample word.
//
// CLOCKING (capture domain = PLL A c4 "cap_clk", 200 MHz; encode = PLL A c0 "ph0", 200 MHz)
//   * The 80 lanes are registered at the pad on cap_clk (lane_in). One capture clock for all
//     lanes: registering each pair on its own phase clock and then re-timing into cap_clk would
//     add a related-clock sub-cycle transfer whose phase changes at runtime -- STA cannot close
//     it and it is why the v2.0 image does not do it (05-WORKPLAN s3 "lane registering per pair
//     clock" -- see README).
//   * Encode: EVERY pair is clocked by ph0 itself -- the same PLL output net on its global
//     network, no per-pair logic in the clock path, so all five encode edges sit at the same
//     place and shift together (the v3.0 shape, where per-pair PLL counters take over). Rate 3
//     (200 MHz) forwards ph0 through the DDIO cell ({1,0}); rates 0..2 (12.5/50/100 MHz) output
//     an SDR divided bit `enc_sdr` generated on cap_clk and re-timed into ph0 with a 2-flop sync
//     (the encode edges are then locked to the PLL phase, the lane capture to cap_clk).
//     History: v2.0-v2.1 had a 4:1 LUT mux of ph0/ph90/ph180/ph270 per pair (DIAG PHASE_SEL) and
//     a runtime-shiftable capture clock (CAP_STEP); schema v3 retired both (06-TIERS s1.7) and
//     v2.2 flow 3 showed why the mux must not merely be left to synthesis with a constant select:
//     the fitter folded only E1's mux onto the PLL global and kept E2..E5 as LUT clock cells, two
//     different encode-clock structures with a placement-dependent skew that STA never reports
//     (enc_p is a false path). The mux and its select are therefore gone from the RTL.
//   * Encode-to-capture relationship of v2.2: E1's encode edge = ph0 (c0), the lanes are captured
//     on cap_clk (c4) at its reset phase (CAP_STEP 0). The v2.1 lanecal image had one LUT + local
//     route in the encode path, so the encode edge now sits earlier relative to cap_clk (order
//     0.5-1 ns, unmeasured); the lane map is unchanged, the capture-window margin is re-checked
//     on the hardware by the DC-spread rung (build report 2026-09-05-v22-build.md s4).
//   * samp_tick marks the cap_clk cycle that carries a new sample at the encode rate: every cycle
//     at rate 3, every 2nd/4th/16th at rates 2/1/0, delayed by the assembly pipeline depth.
//
// LANEMAP (DIAG 0x10..0x5f, 80 entries, 7-bit lane numbers): entry i feeds core i>>4, bit i&15;
// core k is encode pair E(k+1), bits 7..0 its CH1 byte and bits 15..8 its CH2 byte (entry 16k+b =
// CH1 bit b, 16k+8+b = CH2 bit b). Dual-E1 mode assembles CH1 from entries 0..7 and CH2 from
// entries 8..15 (pair E1 only; the other pairs are the interleave tier's). The two 8-bit assemblers
// are 80:1 muxes pipelined in two registered stages (16:1 within a cluster, then 5:1 across
// clusters) so nothing wider than a 16:1 mux sits between registers.
// From v2.2 the map is BAKED: map_ch1/map_ch2 are constants (diag.v takes cores 0/1 of
// fpga/default/lanemap_seed.vh, the calibrated map of lanecal-2026-09-05), so synthesis folds the
// re-registration and both mux stages into fixed wiring (06-TIERS s1.7); the module keeps the
// port so tb_adc_front can still prove the assembler on an arbitrary map.
//
// Pair enables: pair k runs when enc_en & pair_en[k] (freeze probe, owned-fpga 094f05f).

`timescale 1ns/1ps

module adc_front (
    input  wire        cap_clk,
    input  wire        ph0,           // the encode clock of every pair (PLL A c0)
    input  wire [79:0] lane,          // pads
    output wire [4:0]  enc_p,         // E1..E5 positive balls (K8 C14 K9 L8 K10)
    output wire [4:0]  enc_n,         // E1..E5 negative balls (M8 D14 L10 M7 L9)

    // control (C2-domain registers, quasi-static; re-registered on cap_clk here)
    input  wire        enc_en,
    input  wire [1:0]  enc_rate,      // 0=12.5 MHz 1=50 2=100 3=200
    input  wire [4:0]  pair_en,
    input  wire [55:0] map_ch1,       // 8 x 7-bit lane numbers, bit b at [7b+6:7b] (constants since v2.2)
    input  wire [55:0] map_ch2,

    output wire [79:0] lane_q,        // cap_clk-registered lanes (diag / snapshot source)
    output reg  [15:0] samp,          // {CH1, CH2}
    output reg         samp_tick,
    output wire [3:0]  dbg            // {rate3, enc_sdr, en_q, tick0}
);
    // ---- control re-registration (cap_clk) --------------------------------
    reg        en_q   = 1'b0;
    reg [1:0]  rate_q = 2'd0;
    reg [4:0]  pen_q  = 5'd0;
    reg [55:0] m1_q   = 56'd0;
    reg [55:0] m2_q   = 56'd0;
    always @(posedge cap_clk) begin
        en_q <= enc_en; rate_q <= enc_rate; pen_q <= pair_en;
        m1_q <= map_ch1; m2_q <= map_ch2;
    end

    // ---- encode divider + sample tick (cap_clk) ---------------------------
    //   period P cycles: rate 0 -> 16, 1 -> 4, 2 -> 2, 3 -> 1
    reg [3:0] enc_cnt = 4'd0;
    reg       enc_sdr = 1'b0;
    reg       tick0   = 1'b0;
    wire      rate3   = (rate_q == 2'd3);
    wire [3:0] p_last = (rate_q == 2'd0) ? 4'd15 : (rate_q == 2'd1) ? 4'd3 : (rate_q == 2'd2) ? 4'd1 : 4'd0;
    wire [3:0] p_half = (rate_q == 2'd0) ? 4'd8  : (rate_q == 2'd1) ? 4'd2 : 4'd1;
    always @(posedge cap_clk) begin
        if (!en_q) begin
            enc_cnt <= 4'd0; enc_sdr <= 1'b0; tick0 <= 1'b0;
        end else begin
            enc_cnt <= (enc_cnt == p_last) ? 4'd0 : (enc_cnt + 4'd1);
            enc_sdr <= rate3 ? 1'b0 : ((enc_cnt == p_last) ? 1'b1 : ((enc_cnt + 4'd1) < p_half));
            tick0   <= rate3 ? 1'b1 : (enc_cnt == p_last);      // next cycle starts a period
        end
    end
    assign dbg = {rate3, enc_sdr, en_q, tick0};

    // ---- five encode pairs, all clocked by ph0 (see header) -------------------
    genvar k;
    generate
        for (k = 0; k < 5; k = k + 1) begin : g_pair
            wire en_k = en_q & pen_q[k];
            reg [2:0] s1 = 3'b000, s2 = 3'b000;     // {rate3, en_k, enc_sdr} into the encode clock
            always @(posedge ph0) begin
                s1 <= {rate3, en_k, enc_sdr};
                s2 <= s1;
            end
            wire dh = s2[2] ? s2[1] : (s2[1] & s2[0]);
            wire dl = s2[2] ? 1'b0  : (s2[1] & s2[0]);
            ddio_pair u_ddio (.outclock(ph0), .dh(dh), .dl(dl), .pad_p(enc_p[k]), .pad_n(enc_n[k]));
        end
    endgenerate

    // ---- lane pad registers --------------------------------------------------
    lane_in #(.N(80)) u_lanes (.clk(cap_clk), .pad(lane), .q(lane_q));

    // ---- LANEMAP assembly: stage 1 = 16:1 per cluster, stage 2 = 5:1 across clusters ----
    (* ramstyle = "logic" *) reg [4:0] cand [0:15];   // cand[b][g] = lane_q[16g + map[b][3:0]]
    (* ramstyle = "logic" *) reg [2:0] gsel [0:15];   // map[b][6:4] delayed with stage 1
    reg        tick1 = 1'b0;
    reg [15:0] clus;                                   // one 16-lane cluster (4-bit index, no width doubt)
    integer b, g;
    wire [6:0] map [0:15];
    generate
        for (k = 0; k < 8; k = k + 1) begin : g_map
            assign map[k]     = m1_q[7*k +: 7];   // CH1 bit k
            assign map[8 + k] = m2_q[7*k +: 7];   // CH2 bit k
        end
    endgenerate
    always @(posedge cap_clk) begin
        for (b = 0; b < 16; b = b + 1) begin
            for (g = 0; g < 5; g = g + 1) begin
                clus = lane_q[16*g +: 16];
                cand[b][g] <= clus[map[b][3:0]];
            end
            gsel[b] <= map[b][6:4];
        end
        tick1 <= tick0;
        for (b = 0; b < 16; b = b + 1) begin
            // b 0..7 -> CH1 = samp[15:8], b 8..15 -> CH2 = samp[7:0]; cluster 5..7 selects read 0
            samp[(b < 8) ? (8 + b) : (b - 8)] <= (gsel[b] < 3'd5) ? cand[b][gsel[b]] : 1'b0;
        end
        samp_tick <= tick1;
    end
    initial begin samp = 16'd0; samp_tick = 1'b0; end
endmodule

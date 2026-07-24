// adcif.v — ADC front-end for the standard (owned) acquisition bitstream.
//
// This module replaces the proving-ground's IMAGINARY "an aux FPGA drives a 16-bit
// inter-FPGA sample bus" model with the HARDWARE-VERIFIED path recovered by JTAG
// boundary-scan (open-sds1000cml-fpga/docs/aux-bus-re.md) and confirmed by a physical
// board trace of the ADC->FPGA fan-out:
//
//   * The AD9288-class 8-bit ADCs feed the Cyclone DIRECTLY — no aux FPGA in the data
//     path (the MAX V is a config/DAC side-car on CS3, not here).
//   * The data bus is 40 lanes = FIVE 8-bit converter cores across 3 chips, counted
//     on the board: TOP ADC = 8 lanes (1 core), MIDDLE = 16 (2 cores), BOTTOM = 16
//     (2 cores). Split by channel: CH1 = 3 cores (24 bits), CH2 = 2 cores (16 bits).
//   * The Cyclone DRIVES the ADC ENCODE clock. This is WHY every passive monitor
//     fabric saw a static ADC: with no ENCODE driven, the converters never convert.
//     The fabric reference clock is ball C2 (verified ~80 MHz free-running input).
//
// What this module does:
//   1. registers the 40 raw lanes into the clk (C2) domain (source-synchronous);
//   2. de-interleaves them into ONE 8-bit sample per channel via an EDITABLE
//      lane->bit table (§ "DE-INTERLEAVE TABLE" below);
//   3. packs {CH1[7:0], CH2[7:0]} into the canonical `samp` word the spine consumes
//      (spine.v contract: hi byte = CH1, lo byte = CH2);
//   4. generates the ADC ENCODE clock driven back out to the converters.
//
// MAP STATUS (what is verified vs pending a board trace):
//   * 36 of the 40 lane balls are JTAG-VERIFIED (see acq.qsf): CH1 lanes [20:0],
//     CH2 lanes [14:0]. Correlation only promotes a lane that TOGGLES, so 4 lanes —
//     the constant-MSB / via-hidden ones — are still CANDIDATE balls in acq.qsf
//     (ch1[23:21], ch2[15]). Replace those 4 with the real balls from the trace.
//
// THREE THINGS ARE BENCH-TUNE (functional: flash, feed a known ramp/triangle, compare
// the reconstructed waveform — NOT resolvable by more boundary-scan):
//   [TUNE-1] the ENCODE ball + rate. acq.qsf `adc_encode` is a CANDIDATE ball
//            (the boundary-scan "CLK2" candidate was ball M2 = GPMC A1, a confounder,
//            ruled out). ENCODE_DIV sets the rate. RATE TARGET: the scope's 1 GSa/s
//            = 5 cores * ~200 MSPS, so the real ENCODE is ~200 MHz — likely a PLL
//            multiply of the ~80 MHz C2 reference (or a 100 MHz ENCODE with DDR
//            data). The forwarded-C2 default below proves the path at ~80 MSPS/core;
//            swap in an altpll + DDR capture once the bench confirms the clocking.
//   [TUNE-2] the lane->bit table below: which raw lane is bit k of the channel byte.
//            FIRST CUT reconstructs ONE core per channel (correct waveform SHAPE at a
//            sub-maximal rate); the DEFAULT is a straight low-8-lanes identity map.
//   [TUNE-3] full 5-core interleave for the MAX sample rate: which cores belong to a
//            channel and in what phase order. Not needed for a correct-shape first cut;
//            wire it once the per-core grouping is known from the board.
//
// SAFETY: the 40 lanes + clk are INPUTS ONLY (never driven). The single driven ball
// here is `adc_encode`, capped to MINIMUM CURRENT in acq.qsf like every driven ball.
//
// Clean-room: design/spec-derived, never vendor RTL. Synthesizable Verilog-2001, EP4CE10.

module adcif #(
    // clk cycles per ENCODE half-period. 1 => ENCODE is the forwarded reference clock
    // (converters run at the C2 rate, data returns aligned to clk). >1 => a registered
    // clk/(2*ENCODE_DIV) ENCODE. [TUNE-1] pick the rate the real converters expect.
    parameter integer ENCODE_DIV = 1
)(
    input  wire        clk,        // C2 domain: verified ~80 MHz free-running reference
    input  wire [23:0] adc_ch1,    // CH1 data lanes = 3 cores * 8 bits (21 verified + 3 cand)
    input  wire [15:0] adc_ch2,    // CH2 data lanes = 2 cores * 8 bits (15 verified + 1 cand)

    output wire        adc_encode, // ENCODE clock driven to the ADCs (only driven ball)
    output reg  [15:0] samp        // canonical sample {CH1[7:0], CH2[7:0]} (clk domain)
);

    // -----------------------------------------------------------------------
    // Stage 1 — register the raw lanes into the clk domain (source-synchronous
    //   capture; the ADCs are clocked by our ENCODE, so their output is aligned
    //   to clk). One capture register; `samp` below adds a second pipeline stage.
    // -----------------------------------------------------------------------
    reg [23:0] ch1_r = 24'd0;
    reg [15:0] ch2_r = 16'd0;
    always @(posedge clk) begin
        ch1_r <= adc_ch1;
        ch2_r <= adc_ch2;
    end

    // =======================================================================
    // DE-INTERLEAVE TABLE  [TUNE-2] — the ONLY place the lane->bit mapping lives.
    //   ch1_byte[b] = ch1_r[ C1B<b> ];  ch2_byte[b] = ch2_r[ C2B<b> ].
    //   DEFAULT = identity low-8 (lanes 0..7 of each channel, in discovery order) —
    //   i.e. reconstruct ONE core per channel. This is a STARTING POINT: it makes the
    //   whole readout path build and stream a correct-SHAPE waveform, but the exact
    //   amplitude/bit-order is right only once these 16 indices are set from the ramp
    //   test. Full 5-core interleave (max rate) is a later refinement [TUNE-3].
    // =======================================================================
    localparam integer C1B0 = 0, C1B1 = 1, C1B2 = 2, C1B3 = 3,
                       C1B4 = 4, C1B5 = 5, C1B6 = 6, C1B7 = 7;
    localparam integer C2B0 = 0, C2B1 = 1, C2B2 = 2, C2B3 = 3,
                       C2B4 = 4, C2B5 = 5, C2B6 = 6, C2B7 = 7;

    wire [7:0] ch1_byte = { ch1_r[C1B7], ch1_r[C1B6], ch1_r[C1B5], ch1_r[C1B4],
                            ch1_r[C1B3], ch1_r[C1B2], ch1_r[C1B1], ch1_r[C1B0] };
    wire [7:0] ch2_byte = { ch2_r[C2B7], ch2_r[C2B6], ch2_r[C2B5], ch2_r[C2B4],
                            ch2_r[C2B3], ch2_r[C2B2], ch2_r[C2B1], ch2_r[C2B0] };

    // Stage 2 — the canonical sample the spine consumes (hi=CH1, lo=CH2).
    always @(posedge clk)
        samp <= { ch1_byte, ch2_byte };

    // =======================================================================
    // ENCODE clock generation  [TUNE-1] — driven to the ADC ENCODE input(s).
    //   ENCODE_DIV==1: forward the reference clock (source-synchronous capture).
    //   ENCODE_DIV >1: a registered clk/(2*ENCODE_DIV) square wave (slower ENCODE).
    // =======================================================================
    generate
        if (ENCODE_DIV <= 1) begin : gen_enc_fwd
            assign adc_encode = clk;                 // forwarded reference clock
        end else begin : gen_enc_div
            reg                 enc_r = 1'b0;
            reg [15:0]          enc_c = 16'd0;
            always @(posedge clk) begin
                if (enc_c >= (ENCODE_DIV[15:0] - 16'd1)) begin
                    enc_c <= 16'd0;
                    enc_r <= ~enc_r;
                end else begin
                    enc_c <= enc_c + 16'd1;
                end
            end
            assign adc_encode = enc_r;
        end
    endgenerate

endmodule

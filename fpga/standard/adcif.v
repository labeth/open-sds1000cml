// adcif.v — ADC front-end for the standard (owned) acquisition bitstream.
//
// This drives and reads the three AD9288 dual-8-bit ADCs DIRECTLY from the Cyclone,
// using the drive recipe cracked entirely FPGA-side (JTAG boundary-scan of the running
// factory + a shotgun-replicate diagnostic, docs/aux-bus-re.md "ADC-DRIVE RECIPE"):
//
//   * DRIVE the ADC ENCODE clock on the 8 balls K8 K9 K10 L8 L9 L10 M7 M8 (a common
//     clock converts fully; these 8 alone give the full data set). The converters do
//     NOT free-run — the Cyclone must drive these (proven: no clock -> no conversion).
//   * HOLD the AD9288 mode/power controls at their factory-static values so the parts
//     leave power-down: enc-adjacent bottom-cluster F1/L4/T2/T7 = 1, G1/G2/K1 = 0.
//   * READ the ADC data on the 27 verified data lanes (+ headroom), de-interleave into
//     one 8-bit sample per channel, and pack the canonical {CH1,CH2} word for the spine.
//
// Reference clock = ball C2 (~80 MHz, verified). ENCODE rate: ENCODE_DIV sets clk/(2*DIV)
// (>= ~1 MHz for the AD9288 1 MSPS floor; the interleave PHASE and the exact bit-order/
// channel-split de-interleave are functional bench-tune, tightened against a known input).
//
// SAFETY: the only driven balls are the 8 ENCODE clocks + the 7 controls (all Cyclone
// outputs the factory itself drives — no board contention), capped to MINIMUM CURRENT in
// acq.qsf. The 33 data lanes + clk are inputs only.
//
// Clean-room: design/spec-derived, never vendor RTL. Synthesizable Verilog-2001, EP4CE10.

module adcif #(
    parameter integer ENCODE_DIV = 4          // clk/(2*4) ~ 10 MHz ENCODE (>= 1 MSPS)
)(
    input  wire        clk,                    // C2 ~80 MHz reference
    input  wire [32:0] adc_lane,               // ADC data lanes (27 live + headroom)

    output wire [7:0]  adc_enc,                // 8 ENCODE clock outputs (common clock)
    output wire [3:0]  adc_ctl_hi,             // held-HIGH mode controls (F1 L4 T2 T7)
    output wire [2:0]  adc_ctl_lo,             // held-LOW  mode controls (G1 G2 K1)

    output reg  [15:0] samp                    // canonical {CH1[7:0], CH2[7:0]} (clk domain)
);
    // ---- ENCODE clock + static controls (bring the AD9288s out of power-down) ----
    reg        enc_clk = 1'b0;
    reg [15:0] dv = 16'd0;
    always @(posedge clk) begin
        if (dv >= (ENCODE_DIV[15:0] - 16'd1)) begin dv <= 16'd0; enc_clk <= ~enc_clk; end
        else                                       dv <= dv + 16'd1;
    end
    assign adc_enc    = {8{enc_clk}};          // common ENCODE on all 8 balls
    assign adc_ctl_hi = 4'b1111;
    assign adc_ctl_lo = 3'b000;

    // ---- capture the data lanes (source-synchronous to our ENCODE domain) ----
    reg [32:0] lane_r = 33'd0;
    always @(posedge clk) lane_r <= adc_lane;

    // =======================================================================
    // DE-INTERLEAVE TABLE  [TUNE] — the ONLY place the lane->bit mapping lives.
    //   samp = {CH1[7:0], CH2[7:0]}. Default = the first 8 verified lanes as CH1, the
    //   next 8 as CH2 (straight identity in adc_lane index order — see acq.qsf). This
    //   builds and streams a real waveform; correct the 16 indices to the true bit-order
    //   / channel-split once the streamed data is compared to a known ramp/triangle.
    // =======================================================================
    localparam integer C1B0=0,  C1B1=1,  C1B2=2,  C1B3=3,  C1B4=4,  C1B5=5,  C1B6=6,  C1B7=7;
    localparam integer C2B0=8,  C2B1=9,  C2B2=10, C2B3=11, C2B4=12, C2B5=13, C2B6=14, C2B7=15;

    wire [7:0] ch1_byte = { lane_r[C1B7], lane_r[C1B6], lane_r[C1B5], lane_r[C1B4],
                            lane_r[C1B3], lane_r[C1B2], lane_r[C1B1], lane_r[C1B0] };
    wire [7:0] ch2_byte = { lane_r[C2B7], lane_r[C2B6], lane_r[C2B5], lane_r[C2B4],
                            lane_r[C2B3], lane_r[C2B2], lane_r[C2B1], lane_r[C2B0] };

    always @(posedge clk)
        samp <= { ch1_byte, ch2_byte };        // spine contract: hi=CH1, lo=CH2

endmodule

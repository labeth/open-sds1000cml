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
    output wire [1:0]  adc_enc2,               // C14/D14 differential ADC sample clock (JTAG: factory drives these; own fabric left them floating -> ADC dead)
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
    assign adc_enc2   = {~enc_clk, enc_clk};   // D14=~enc, C14=enc — differential ADC sample clock
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
    // DE-INTERLEAVE derived on the bench via the rawcap diagnostic (fpga/rawcap): a
    // decimated raw-lane TIME-SERIES was captured while a triangle swept each channel's
    // converting window (C1 offset 0x29 / C2 offset 0x28). Lanes were clustered by the
    // common-ENCODE core-redundancy and ordered by toggle rate; CH1 was optimised to a
    // clean reconstructed waveform (mean|Δ|~4, hits the 0/255 rails). CH1 is PROVEN;
    // CH2's bench signal was weak (thin/low-amplitude window) so its order is best-effort
    // toggle-ranked over the rail-verified CH2 lanes — refine with a stronger CH2 input.
    //   CH1 bit7..bit0 = adc_lane[3,0,1,11,9,12,6,5]  (PROVEN, clean sweep)
    //   CH2 bit7..bit0 = adc_lane[18,23,28,24,30,27,20,22]  (data-derived, PARTIAL)
    // CH2 update: with CH2 on a 1x probe (strong signal) + gain set to ~200 mV/div so the
    // triangle fills the ADC range, rawcap clustered CH2's 11 mapped lanes to only ~6 DISTINCT
    // bits (toggle-rate gaps show bit6/bit0 lanes are NOT among the mapped set). So CH2 is a
    // partial map pending the "find the missing lanes" hunt (full 8*5=40 core map). Order here
    // is the best data-derived assignment (mean|d|~10); finalise once the missing lanes land.
    localparam integer C1B7=3,  C1B6=0,  C1B5=1,  C1B4=11, C1B3=9,  C1B2=12, C1B1=6,  C1B0=5;   // CH1 (proven)
    localparam integer C2B7=18, C2B6=23, C2B5=28, C2B4=24, C2B3=30, C2B2=27, C2B1=20, C2B0=22;  // CH2 (partial)

    wire [7:0] ch1_byte = { lane_r[C1B7], lane_r[C1B6], lane_r[C1B5], lane_r[C1B4],
                            lane_r[C1B3], lane_r[C1B2], lane_r[C1B1], lane_r[C1B0] };
    wire [7:0] ch2_byte = { lane_r[C2B7], lane_r[C2B6], lane_r[C2B5], lane_r[C2B4],
                            lane_r[C2B3], lane_r[C2B2], lane_r[C2B1], lane_r[C2B0] };

    always @(posedge clk)
        samp <= { ch1_byte, ch2_byte };        // spine contract: hi=CH1, lo=CH2

endmodule

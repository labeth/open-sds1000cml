// spine.v — canonical streaming spine for the standard acquisition fabric.
//
// The spine carries the canonical sample stream {ch1, ch2, valid, idx, trig_mark}
// on a >=18-bit lane (contract C1). This module owns the two pieces of the spine
// that live BEFORE the capture writer:
//
//   * transform STAGE 0 = a real, programmable DECIMATOR. The aux ADC is a
//     fixed-rate source we do not touch, so slow timebases (ms-s/div, roll /
//     envelope bands) are produced by dropping input samples in fabric: the
//     canonical stream asserts `cap_tick` (a `valid`) once per DECIM input
//     samples. Native-fast bands set DECIM=1 -> a tick every clock. This both
//     delivers the timebase ladder and proves the stage-insertion contract is
//     LIVE in the shipped fabric (DESIGN.md sec.3), not merely reserved-empty.
//
//   * transform STAGE 1 = a reserved, bypassable in-line identity slot for a
//     future deglitch / ERES / math stage (v3). It is a passthrough wire today,
//     with the bypass mux (the "no dead-end" insertion contract) already present.
//
// Both data transform slots are BYPASSED at reset (XFORM_CTRL = 0x0003 in the top),
// so the reset stream is raw. `idx` and `trig_mark` are capture concerns (the write
// pointer + the accepted-crossing sample), so they are owned by capture.v; the spine
// hands capture/envelope a decimated {cap_word, cap_tick} pair.
//
// Clean-room: design/spec-derived. Synthesizable Verilog-2001, EP4CE10.

module spine (
    input  wire        clk,

    // filling gate (from capture FSM): the decimator only ticks while a frame fills.
    input  wire        filling,

    // synchronized 16-bit sample word (hi byte = CH1, lo byte = CH2).
    input  wire [15:0] samp,

    // programmable decimation factor (transform stage 0). DECIM=1 => tick every clk.
    input  wire [31:0] decim,

    // reserved transform-stage bypass controls (both = 1 at reset -> raw stream).
    input  wire        bypass0,   // stage-0 data transform bypass (decimator always live)
    input  wire        bypass1,   // stage-1 data transform bypass

    // canonical (decimated) stream out to the capture writer + live envelope.
    output wire [15:0] cap_word,  // low 16 bits of the lane (hi=CH1, lo=CH2)
    output wire        cap_tick   // spine `valid`: one asserted per DECIM input samples
);

    // -----------------------------------------------------------------------
    // Transform STAGE 0 — the decimator (drives `cap_tick`).
    //   A free-running down-counter reloaded with DECIM-1. While filling, a tick
    //   fires whenever the counter is 0; otherwise the counter is held reset so
    //   the FIRST filling cycle always ticks (sample 0 is captured). DECIM=0 is
    //   treated as DECIM=1 (reload 0 -> tick every clock).
    // -----------------------------------------------------------------------
    wire [31:0] decim_m1 = (decim == 32'd0) ? 32'd0 : (decim - 32'd1);

    reg  [31:0] dcnt = 32'd0;
    always @(posedge clk) begin
        if (!filling)
            dcnt <= 32'd0;               // hold reset while idle -> first fill tick fires
        else if (dcnt == 32'd0)
            dcnt <= decim_m1;            // reload the decimation period
        else
            dcnt <= dcnt - 32'd1;
    end

    assign cap_tick = filling && (dcnt == 32'd0);

    // -----------------------------------------------------------------------
    // Canonical >=18-bit lane + the two reserved bypassable transform slots.
    //   The 16-bit sample is zero-extended to 18 bits (v3 ENOB/dither/math
    //   headroom) so a future transform never has to widen or fork the stream.
    //   Each slot is an in-line mux: xfN_out = bypassN ? in : xfN_transformed.
    //   Today both transforms are identity wires; v3 replaces them behind the
    //   same bypass bit, never as a spine rewrite.
    // -----------------------------------------------------------------------
    wire [17:0] s0_lane         = {2'b00, samp};        // 16 -> 18 zero-extend (C1 headroom)

    wire [17:0] xf0_transformed = s0_lane;              // v0 IDENTITY (v3: deglitch / noise-reject)
    wire [17:0] xf0_out         = bypass0 ? s0_lane : xf0_transformed;

    wire [17:0] xf1_transformed = xf0_out;              // v0 IDENTITY (v3: ERES / dither / math)
    wire [17:0] xf1_out         = bypass1 ? xf0_out : xf1_transformed;

    // capture consumes the transformed stream, keeping the low 16 bits.
    assign cap_word = xf1_out[15:0];

endmodule

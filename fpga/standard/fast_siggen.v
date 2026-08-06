// fast_siggen.v — synthetic FAST-domain repetitive waveform generator
// =========================================================================
// Part of the owned Cyclone IV E (EP4CE10F17C8) acquisition bitstream.
// Branch owned-fpga, TOP = acq.v.  ADDITIVE / LOGIC-ONLY / GATED.
//
// WHAT IT DOES
//   In the FAST interleave clock domain (cap_clk = cap_clk200, the phased
//   200 MHz fill clock), it produces a clean, deterministic, REPETITIVE 8-bit
//   ADC-code sample every tick — a triangle (up/down) or a ramp (sawtooth).
//   It is a pure logic accumulator/counter: ONE 8-bit register + a direction
//   flag + comparators. NO M9K, NO DSP, NO PLL (it consumes an existing clock).
//   The 46/46-full device stays 46/46.
//
// WHY
//   To prove the whole in-fabric super-res chain LIVE and REMOTELY with NO
//   bench signal: this synthetic source drives BOTH
//     (a) the normal capture record (via acq.v's samp_eff mux) so the host
//         reference-lock (superres.Stack.SeedRefGate: needs hi-lo>=12, not
//         Clipped, a detectable period) ENGAGES on a real repetitive trace, and
//     (b) sr_accum's align input (via acq.v's smp_a/trig mux) so the in-fabric
//         ETS COMBINE engine stacks the SAME signal into a coherent bin grid.
//
// DEFAULT WAVEFORM (parameters, so period/amplitude are build-configurable)
//   AMIN=64, AMAX=192, STEP=1:
//     * amplitude range 64..192  -> hi-lo = 128 (>> the 12 the host gate needs)
//     * trough 64 (>6) and crest 192 (<253) -> superres.Clipped() short-circuits
//       FALSE (never flagged clipped), so the reference always locks.
//     * TRIANGLE period = 2*(AMAX-AMIN)/STEP = 256 cap_clk ticks == the shipping
//       sr_accum NBINS (256): with trigger-referenced binning (trig = samp[7],
//       one rising edge / period) each of the 256 bins gets exactly one sample
//       per period -> a maximally coherent, fully-populated grid.
//     * RAMP period = (AMAX-AMIN)/STEP = 128 ticks (fills the low 128 bins).
//   To retune: change AMAX-AMIN (keep it == NBINS/2 for a single-period triangle
//   grid) or STEP.  Amplitude = AMAX-AMIN.  Keep AMIN>6 && AMAX<253.
//
// GATE / BYTE-IDENTICAL INVARIANT
//   en=0 => samp is parked at AMIN, no toggling, module inert.  In acq.v the
//   enable is a previously-FREE run-word bit (RUN[6]); with RUN[6]=0 the acq.v
//   muxes select the ORIGINAL sample paths, so the whole datapath is
//   byte-for-byte identical to today and the drain is unchanged.  No schema /
//   regs.vh / regmux.vh edit => IFACE_BUILD_ID stays 0xc2f6eb5f.
//
// Clean-room: design-derived. Synthesizable Verilog-2001, EP4CE10.
// =========================================================================
`default_nettype none

module fast_siggen #(
    parameter integer AMIN = 64,    // trough code  (>6  so host Clipped() is false)
    parameter integer AMAX = 192,   // crest  code  (<253 likewise); AMAX-AMIN >= 12
    parameter integer STEP = 1      // per-tick increment; period scales as 1/STEP
)(
    input  wire       cap_clk,      // FAST interleave-domain clock (cap_clk200)
    input  wire       en,           // gate: 0 => parked at AMIN, inert
    input  wire       shape,        // 0 = triangle (up/down), 1 = ramp (sawtooth)
    output reg  [7:0] samp          // synthetic 8-bit ADC-code, updated every cap_clk
);
    localparam [7:0] LO   = AMIN[7:0];
    localparam [7:0] HI   = AMAX[7:0];
    localparam [7:0] STP  = STEP[7:0];

    reg dir;   // triangle direction: 0 = rising, 1 = falling

    initial begin samp = LO; dir = 1'b0; end

    always @(posedge cap_clk) begin
        if (!en) begin
            // Parked + inert while disabled.  (In acq.v this output is not even
            // selected when RUN[6]=0, so the fabric result is unaffected either way.)
            samp <= LO;
            dir  <= 1'b0;
        end else if (shape) begin
            // ---- RAMP / sawtooth: LO .. HI, then wrap to LO ----
            samp <= (samp >= HI) ? LO : (samp + STP);
            dir  <= 1'b0;
        end else begin
            // ---- TRIANGLE: LO .. HI .. LO (crest/trough each held one tick) ----
            if (!dir) begin
                if (samp >= (HI - STP)) begin samp <= HI; dir <= 1'b1; end
                else                         samp <= samp + STP;
            end else begin
                if (samp <= (LO + STP)) begin samp <= LO; dir <= 1'b0; end
                else                        samp <= samp - STP;
            end
        end
    end
endmodule

`default_nettype wire

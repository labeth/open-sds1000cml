// eth100_decode.v — in-fabric 100BASE-TX PHY decoder TOP (RX chain, chained).
//
// Chains the four sim+synth-proven stages into one end-to-end decoder that
// takes the golden model's 600 MSa/s ternary sample codes (already gearboxed
// into a wide LANES-lane word) and emits decoded MAC frame octets + an FCS
// verdict onto a byte_fifo-compatible {flags,idx,byte} strobe interface.
//
// STAGE ORDER (pinned to the golden model app/internal/eth100tx/decode.go
// DecodeSamples: Slice -> recoverSymbols -> mlt3Decode -> descramble ->
// align5B -> framer):
//
//   codes ─► eth_slicer_cdr ─► [bit serializer] ─► eth_descramble ─►
//           (slice+CDR+MLT3)    (2b/clk -> 1b/clk)   (idle-lock LFSR)
//        ─► eth_4b5b ─► eth_framer ─► emit_{stb,byte,idx,flags}
//           (align+decode)  (assemble + CRC-32)
//
// The slicer/CDR emits up to 2 recovered NRZI bits per 80 MHz clock; the
// descrambler, 4B5B and framer stages are each 1 recovered-bit/clock (the
// bit-exact standalone proof width). A tiny elastic bit SERIALIZER (a shallow
// logic shift-queue, 0 M9K) bridges the two rates: it accepts up to OBITS_W
// bits per clock from the CDR and pops exactly one bit/clock downstream.
//
// REAL-TIME CAVEAT (rigorous honesty): 100BASE-TX is 125 Mbit/s, and 80 MHz
// fabric with a 1-bit/clk descramble/4B5B tail sustains only 80 Mbit/s. So on a
// CONTINUOUS live wire this tail underruns unless the descramble+4B5B are
// unrolled to 2 bits/clk (a straightforward per-lane duplication the stage
// authors flagged as integration work). For the golden-model SIM PROOF the
// testbench simply PACES the input word so the serializer never overflows
// (in_valid gating makes inter-word gaps transparent to the CDR's carried
// fractional phase) — which is exactly the remote-achievable proof this build
// targets. The `ser_overflow` sticky flag guards against an over-fast feed.
//
// GATING / INERTNESS: rst OR !en holds every sub-stage inert (identical to the
// sibling eth_* engines). At the fabric top, drive en = dec_en & (dec_proto==3)
// so at reset (dec_cfg=0) the whole engine is dark and every existing mode is
// byte-exact. 0 M9K, 0 PLL, 0 new pins by construction (see the fit report).

module eth100_decode #(
    parameter integer SAMPLE_W = 12,   // signed sample-code width (golden +/-1000)
    parameter integer LANES    = 8,    // samples per wide word (gearbox output)
    parameter integer PHASE_W  = 10,   // CDR phase width (>=9 bit-exact)
    parameter integer OBITS_W  = 8,    // max NRZI bits/clk out of the CDR (max seen 2)
    parameter integer SERQ_D   = 16,   // elastic bit-serializer depth (logic)
    parameter integer PRE_MIN  = 8     // framer preamble-run threshold
) (
    input  wire clk,
    input  wire rst,                         // synchronous, active-high
    input  wire en,                          // engine gate; en=0 -> fully inert

    // ---- slicer thresholds (reuse DEC_THR halves at integration) ----
    input  wire signed [SAMPLE_W-1:0] thr_hi, // >= thr_hi -> +1 (golden +500)
    input  wire signed [SAMPLE_W-1:0] thr_lo, // <= thr_lo -> -1 (golden -500)

    // ---- wide sample word in (gearbox output; lane0 = earliest) ----
    input  wire [LANES*SAMPLE_W-1:0]  codes,
    input  wire [3:0]                 nvalid,
    input  wire                       in_valid,
    input  wire                       flush,

    // ---- decoded MAC-octet stream out (byte_fifo {flags,idx,byte}) ----
    output wire        emit_stb,
    output wire [7:0]  emit_byte,
    output wire [23:0] emit_idx,
    output wire [7:0]  emit_flags,

    // ---- frame-level status / trigger aids ----
    output wire        sfd_seen,    // 1-clk: SFD (frame start) — trigger source
    output wire        frame_done,  // 1-clk: end of frame (coincident w/ last emit)
    output wire        fcs_ok_o,    // FCS verdict, valid at frame_done

    // ---- health / lock status ----
    output wire        descr_locked, // descrambler idle-lock held
    output wire        cg_locked,    // 4B5B code-group alignment held
    output wire        cdr_overflow, // CDR emitted > OBITS_W bits in a word (sticky)
    output reg         ser_overflow  // serializer queue overflow (sticky)
);
    // ==================================================================
    // STAGE 1..3 : slice + CDR + MLT-3 decode  (up to 2 NRZI bits/clk)
    // ==================================================================
    wire [OBITS_W-1:0] cdr_bits;
    wire [3:0]         cdr_cnt;
    wire               cdr_valid;

    eth_slicer_cdr #(
        .SAMPLE_W(SAMPLE_W), .LANES(LANES), .PHASE_W(PHASE_W), .OBITS_W(OBITS_W)
    ) u_slicer_cdr (
        .clk(clk), .rst(rst), .en(en),
        .thr_hi(thr_hi), .thr_lo(thr_lo),
        .codes(codes), .nvalid(nvalid), .in_valid(in_valid), .flush(flush),
        .out_bits(cdr_bits), .out_cnt(cdr_cnt),
        .out_valid(cdr_valid), .overflow(cdr_overflow)
    );

    // ==================================================================
    // ELASTIC BIT SERIALIZER : {cdr_bits[cdr_cnt]} in  ->  1 bit/clk out
    // Shallow logic shift-queue; q[0] is the head (next to pop, earliest bit).
    // Accepts up to OBITS_W bits/clk, pops exactly one/clk. 0 M9K.
    //
    // TIMING: pop = a fixed >>1; push = ONE barrel-shift of the (already
    // zero-extended above cdr_cnt) cdr_bits by the post-pop occupancy, OR'd in.
    // This avoids a chain of dynamic-index writes (which was the 43 MHz critical
    // path) — one barrel shift + OR + a small add closes the 80 MHz fabric clk.
    // ==================================================================
    reg  [SERQ_D-1:0]         q;
    reg  [5:0]                qn;   // occupancy (0..SERQ_D)
    reg                       ser_valid;
    reg                       ser_bit;

    reg  [SERQ_D-1:0]         q_pop;   // queue after the 1-bit pop
    reg  [5:0]                np;      // occupancy after the pop
    reg  [6:0]                ntot;    // occupancy after pop + push
    reg  [SERQ_D+OBITS_W-1:0] ins;     // barrel-shifted insert word

    always @(posedge clk) begin
        if (rst || !en) begin
            q            <= {SERQ_D{1'b0}};
            qn           <= 6'd0;
            ser_valid    <= 1'b0;
            ser_bit      <= 1'b0;
            ser_overflow <= 1'b0;
        end else begin
            // ---- pop one bit (head = q[0]) ----
            if (qn != 6'd0) begin
                ser_bit   <= q[0];
                ser_valid <= 1'b1;
                q_pop      = q >> 1;
                np         = qn - 6'd1;
            end else begin
                ser_valid <= 1'b0;
                q_pop      = q;
                np         = qn;
            end
            // ---- push new CDR bits at the tail (single barrel-shift insert) ----
            if (cdr_valid) begin
                ins  = ({{SERQ_D{1'b0}}, cdr_bits}) << np;  // place bits at offset np
                q    <= q_pop | ins[SERQ_D-1:0];
                ntot = {1'b0, np} + {3'd0, cdr_cnt};
                qn   <= ntot[5:0];
                if (ntot > SERQ_D[6:0]) ser_overflow <= 1'b1; // queue full: flag
            end else begin
                q  <= q_pop;
                qn <= np;
            end
        end
    end

    // ==================================================================
    // STAGE E : descramble (idle-lock LFSR x^11+x^9+1)  1 bit/clk
    // ==================================================================
    wire descr_valid, descr_bit;
    wire descr_lost;   // ITEM-4: descrambler idle-lock lost pulse

    eth_descramble u_descramble (
        .clk(clk), .rst(rst), .en(en),
        .in_valid(ser_valid), .in_bit(ser_bit),
        .out_valid(descr_valid), .out_bit(descr_bit), .locked(descr_locked),
        .lock_lost(descr_lost)
    );

    // ==================================================================
    // STAGE F : 4B5B align + decode  1 bit/clk -> MII nibble stream
    // ==================================================================
    wire        cg_stb, cg_ctrl, cg_err;
    wire [4:0]  cg_code;
    wire [2:0]  cg_sym;
    wire [3:0]  nibble;
    wire        nibble_stb, sof, eof;

    eth_4b5b u_4b5b (
        .clk(clk), .rst(rst), .en(en),
        .in_bit(descr_bit), .in_valid(descr_valid),
        .cg_stb(cg_stb), .cg_code(cg_code), .cg_ctrl(cg_ctrl),
        .cg_sym(cg_sym), .cg_err(cg_err),
        .nibble(nibble), .nibble_stb(nibble_stb),
        .sof(sof), .eof(eof), .locked(cg_locked)
    );

    // ==================================================================
    // STAGE G : framer + CRC-32  MII nibbles -> MAC octets + FCS verdict
    // 4B5B eof (ESD /T/R/) is the frame terminator -> framer nib_end.
    // ==================================================================
    eth_framer #(.PRE_MIN(PRE_MIN)) u_framer (
        .clk(clk), .rst(rst), .en(en),
        .nib_valid(nibble_stb), .nib(nibble), .nib_end(eof),
        .code_err(cg_err), .lock_lost(descr_lost),   // ITEM-4: flags8[2]/flags8[0]
        .emit_stb(emit_stb), .emit_byte(emit_byte),
        .emit_idx(emit_idx), .emit_flags(emit_flags),
        .sfd_seen(sfd_seen), .frame_done(frame_done), .fcs_ok_o(fcs_ok_o)
    );

endmodule

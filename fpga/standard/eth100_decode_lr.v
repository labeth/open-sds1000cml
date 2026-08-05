// eth100_decode_lr.v — LINE-RATE in-fabric 100BASE-TX PHY decoder TOP.
//
// The line-rate sibling of eth100_decode.v.  Where eth100_decode.v used a
// 1-bit/clk descramble+4B5B tail (bit-exact, but only 80 Mbit/s -> underruns a
// CONTINUOUS 125 Mbit wire, so its sim must PACE the input), this top chains the
// gearbox + 2-bit/clk UNROLL siblings so the tail sustains 160 Mbit/s >= 125
// Mbit line rate on a continuous stream — NO pacing needed.
//
//   {c1a_p,c1b_p,c1c_p}  (interleave, 200 MHz, 3 samp/tick)
//        │  wr_clk / wr_valid / wr_samp
//        ▼
//   eth_gearbox ─► eth_slicer_cdr ─► eth_descramble2 ─► eth_4b5b2 ─► eth_framer
//    (200->80 CDC)  (slice+CDR+MLT3)   (LFSR x11+x9+1)   (4B5B+J/K)  (+CRC-32 FCS)
//        rd_clk=clk       80 MHz            2 bit/clk       2 bit/clk    <=0.4 nib/clk
//                                                                          │
//                                                                          ▼
//                                                    emit_{stb,byte,idx,flags}
//                                                    (byte_fifo-compatible)
//
// The gearbox is the 200->80 rate-matching CDC (write side = the interleave's
// 600 MSa/s 3-samples-per-tick taps; read side = the 80 MHz fabric clock feeding
// the CDR).  The descramble2/4b5b2 consume the CDR's up-to-2-bits/clk burst so
// the tail never underruns.  The framer is UNCHANGED (fed <=0.4 nibbles/clk).
//
// BYTE-EXACTNESS: proven end-to-end in sim/tb_eth100_lr.v — the golden 600 MSa/s
// codes fed CONTINUOUSLY through gearbox(async 200/80 CDC)->CDR->descramble2->
// 4b5b2->framer recover the golden MAC frame body (frame||FCS) BYTE-EXACT with
// FCS OK.  The 2-wide unroll is the 1-bit engine stepped twice (see eth_*2.v),
// so nibble order and every octet match the 1-bit reference (eth100_decode.v).
//
// GATING / INERTNESS: rst OR !en holds every sub-stage inert (identical to the
// sibling eth_* engines and to eth100_decode.v).  At the fabric top drive
// en = dec_en & (dec_proto==3) so at reset the whole engine is dark and every
// existing decode mode (UART/I2C/SPI) is byte-for-byte unchanged.  0 M9K, 0 PLL,
// 0 new pins.  regs.vh/schema untouched => IFACE_BUILD_ID 0xc2f6eb5f stable.

module eth100_decode_lr #(
    parameter integer SAMPLE_W = 12,   // signed sample-code width (golden +/-1000)
    parameter integer LANES    = 8,    // gearbox output lanes / CDR word width
    parameter integer WR_SAMP  = 3,    // samples per interleave tick (c1a/c1b/c1c)
    parameter integer DEPTHW   = 4,    // gearbox per-bank depth = 2^DEPTHW (16;
                                       // proven overflow-free continuous-fed —
                                       // 640>600 drain>fill headroom + startup)
    parameter integer PHASE_W  = 10,   // CDR phase width (>=9 bit-exact)
    parameter integer OBITS_W  = 8,    // max NRZI bits/clk out of the CDR (max seen 2)
    parameter integer PRE_MIN  = 8     // framer preamble-run threshold
) (
    // ---- read / fabric clock domain (80 MHz) ----
    input  wire clk,
    input  wire rst,                          // synchronous, active-high (rd domain)
    input  wire en,                           // engine gate; en=0 -> fully inert

    // ---- slicer thresholds (reuse DEC_THR halves at integration) ----
    input  wire signed [SAMPLE_W-1:0] thr_hi, // >= thr_hi -> +1 (golden +500)
    input  wire signed [SAMPLE_W-1:0] thr_lo, // <= thr_lo -> -1 (golden -500)

    // ---- write / interleave clock domain (200 MHz), 3 samples/tick ----
    input  wire                        wr_clk,
    input  wire                        wr_rst,   // synchronous, active-high (wr domain)
    input  wire                        wr_valid, // WR_SAMP fresh samples this wr_clk
    input  wire [WR_SAMP*SAMPLE_W-1:0] wr_samp,  // s0=[SAMPLE_W-1:0] earliest (c1a_p)

    // ---- rd-domain end-of-capture flush (drain gearbox tail + close CDR run) ----
    input  wire flush,

    // ---- decoded MAC-octet stream out (byte_fifo {flags,idx,byte}) ----
    output wire        emit_stb,
    output wire [7:0]  emit_byte,
    output wire [23:0] emit_idx,
    output wire [7:0]  emit_flags,

    // ---- frame-level status / trigger aids ----
    output wire        sfd_seen,     // 1-clk: SFD (frame start) — trigger source
    output wire        frame_done,   // 1-clk: end of frame (coincident w/ last emit)
    output wire        fcs_ok_o,     // FCS verdict, valid at frame_done

    // ---- health / lock status ----
    output wire        descr_locked, // descrambler idle-lock held
    output wire        cg_locked,    // 4B5B code-group alignment held
    output wire        gb_overflow,  // gearbox bank overflow (sticky, rd-synced)
    output wire        cdr_overflow, // CDR emitted > OBITS_W bits in a word (sticky)
    output wire        fb_ovf        // 4b5b2 emit-FIFO overflow (sticky, never fires)
);
    // ==================================================================
    // STAGE 0 : 200->80 GEARBOX  (interleave 3-samp/tick -> LANES-wide word)
    // ==================================================================
    wire [LANES*SAMPLE_W-1:0] gb_codes;
    wire [3:0]                gb_nvalid;
    wire                      gb_in_valid;

    eth_gearbox #(
        .SAMPLE_W(SAMPLE_W), .LANES(LANES), .WR_SAMP(WR_SAMP), .DEPTHW(DEPTHW)
    ) u_gearbox (
        .wr_clk(wr_clk), .wr_rst(wr_rst), .wr_valid(wr_valid), .wr_samp(wr_samp),
        .rd_clk(clk),    .rd_rst(rst),    .rd_ready(1'b1),     .flush(flush),
        .en(en),
        .codes(gb_codes), .nvalid(gb_nvalid),
        .in_valid(gb_in_valid), .overflow(gb_overflow)
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
        .codes(gb_codes), .nvalid(gb_nvalid), .in_valid(gb_in_valid), .flush(flush),
        .out_bits(cdr_bits), .out_cnt(cdr_cnt),
        .out_valid(cdr_valid), .overflow(cdr_overflow)
    );

    // ==================================================================
    // STAGE E : descramble (idle-lock LFSR x^11+x^9+1)  2 bits/clk UNROLL
    // ==================================================================
    wire        de_valid;
    wire [1:0]  de_nbits, de_bits;
    wire        descr_lost;   // 1-clk pulse: descrambler idle-lock lost (-> framer flags8[0])

    eth_descramble2 u_descramble2 (
        .clk(clk), .rst(rst), .en(en),
        .in_valid(cdr_valid), .in_nbits(cdr_cnt[1:0]), .in_bits(cdr_bits[1:0]),
        .out_valid(de_valid), .out_nbits(de_nbits), .out_bits(de_bits),
        .locked(descr_locked), .lock_lost(descr_lost)
    );

    // ==================================================================
    // STAGE F : 4B5B align + decode  2 bits/clk UNROLL -> MII nibble stream
    // ==================================================================
    wire        cg_stb, cg_ctrl, cg_err;
    wire [4:0]  cg_code;
    wire [2:0]  cg_sym;
    wire [3:0]  nibble;
    wire        nibble_stb, sof, eof;

    eth_4b5b2 u_4b5b2 (
        .clk(clk), .rst(rst), .en(en),
        .in_valid(de_valid), .in_nbits(de_nbits), .in_bits(de_bits),
        .cg_stb(cg_stb), .cg_code(cg_code), .cg_ctrl(cg_ctrl),
        .cg_sym(cg_sym), .cg_err(cg_err),
        .nibble(nibble), .nibble_stb(nibble_stb),
        .sof(sof), .eof(eof), .locked(cg_locked), .ovf(fb_ovf)
    );

    // ==================================================================
    // STAGE G : framer + CRC-32  MII nibbles -> MAC octets + FCS verdict
    // (UNCHANGED from the 1-bit reference — fed <=0.4 nibbles/clk.)
    // ==================================================================
    eth_framer #(.PRE_MIN(PRE_MIN)) u_framer (
        .clk(clk), .rst(rst), .en(en),
        .nib_valid(nibble_stb), .nib(nibble), .nib_end(eof),
        .code_err(cg_err), .lock_lost(descr_lost),   // ITEM-4: flags8[2] / flags8[0]
        .emit_stb(emit_stb), .emit_byte(emit_byte),
        .emit_idx(emit_idx), .emit_flags(emit_flags),
        .sfd_seen(sfd_seen), .frame_done(frame_done), .fcs_ok_o(fcs_ok_o)
    );

endmodule

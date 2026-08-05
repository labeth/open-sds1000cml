// uart_decode.v — in-fabric UART decoder + internal loopback TEST-GEN.
//
// GOAL: produce the SAME decoded byte sequence as the app's software oracle
// decode.DecodeUART().Bytes on clean signals, but GAPLESS and in real time.
//
// DIVISION OF LABOR (host keeps the float-heavy smarts):
//   HOST computes auto-threshold + auto-baud (histogram, sub-sample edges,
//   cluster-walk) and loads {thr8 = ceil(Thr), spb = Q16.8 samples-per-bit,
//   bits, parity} as config. This module does the DETERMINISTIC per-column
//   decode that DecodeUART's clean path does.
//
// ============================ ORACLE CONTRACT ============================
// Audit of app/internal/decode/decode.go + decode_uart.go:
//
//  * SAMPLE DECISION IS PURE THRESHOLD.  Every bit/edge decision in DecodeUART
//    goes through logicAt() (decode.go:181): i=round(x); codes[i] >= Thr ? 1:0.
//    NO hysteresis on the sample. THRESH8 = ceil(Thr) makes integer
//    `code >= THRESH8` == float `code >= Thr`.  So dec_bit = (code >= thr8).
//    (Optional hyst_en applies a 20% band to the START-EDGE detector ONLY, as a
//    bench-robustness superset; with hyst_en=0 the edge detector is also pure
//    threshold and therefore bit-exact to logicAt. Defaults off.)
//
//  * SAMPLE CADENCE = ONE DECIMATED COLUMN (colTimeS), not raw 80 MHz.  SPB is
//    in COLUMN units, so this engine advances exactly ONE step per cap_tick and
//    slices cap_word — NOT the undecimated `samp`. (RISK #1: tapping raw samp
//    makes SPB wrong by the decimation factor.)
//
//  * SAMPLING INSTANTS. DecodeUART samples phase p at
//        index = round( S + (p+0.5)*SPB )
//    p=0 start-confirm, p=1..BITS data (LSB-first), p=BITS+1 parity (if any),
//    then stop. ROUNDING IS APPLIED TO THE FULL SUM, never re-rounded per bit.
//    Because S (falling-edge column) is an INTEGER, round(S + x) == S + round(x),
//    so we accumulate the RELATIVE offset in a fractional accumulator and only
//    round the target:
//        acc  = SPB/2                    (Q_.8, = 0.5*SPB)
//        tgt  = (acc + 8'h80) >> 8       (round-half-up == Go math.Round, +ve)
//        when off == tgt: latch bit; acc += SPB; recompute tgt.
//    acc stays exact; only tgt is rounded — NEVER round-then-accumulate
//    (RISK #2: that drifts on fractional SPB like 13.717 and mis-samples late
//    bits).
//
//  * BYTES EMITTED. DecodeUART appends val for EVERY confirmed frame INCLUDING
//    parity-error and frame-error frames (decode_uart.go:215-217). So this
//    engine emits a byte for every confirmed start, carrying {frame_err,
//    parity_err}. Only the start-confirm-fail (line not low at S+0.5*SPB) and
//    record-edge "gap" frames are dropped; record-edge has no analog on a live
//    stream, so only confirm-fail aborts here.
//
//  * TRIGGER is DATA-ONLY. serialtrig matchBytes counts only Kind=="data"
//    (flags==0), so decode_trig fires only when frame_err==0 && parity_err==0.
//
// v1 = 8-bit clean path (oracle-critical). data_bits 1..15 supported in the
// shift/parity math; 16-bit words are a documented extension (need a wider
// drain word). Emit byte is 8-bit ({flags,data[7:0]}).
//
// ALL sequential logic steps ONLY on cap_tick, so "column" == cap_tick.

`default_nettype none

module uart_decode (
    input  wire        clk,        // 80 MHz single domain
    input  wire        rst_n,      // async active-low reset (tie 1'b1 if unused)
    input  wire        cap_tick,   // one pulse per decimated column
    input  wire [7:0]  sample_code,// chosen-channel code for THIS column

    // ---- config (loaded by host; latched in acq.v spare selectors) ----
    input  wire        en,         // master enable; 0 => fully inert
    input  wire [7:0]  thr8,       // ceil(Thr); dec_bit = code >= thr8
    input  wire [23:0] spb,        // samples-per-bit, Q16.8 (Result.SPB)
    input  wire [3:0]  bits_cfg,   // data bits; 0 => 8, else 1..15
    input  wire [1:0]  parity_cfg, // 0 none, 1 even, 2 odd
    input  wire        hyst_en,    // optional bench hysteresis on start-edge only
    input  wire [7:0]  hyst_band,  // +/- band around thr8 when hyst_en

    // ---- internal loopback TEST-GEN ----
    input  wire        tg_en,      // 1 => decoder input driven by generator
    input  wire [7:0]  tg_byte,    // byte the generator repeatedly transmits

    // ---- single-byte trigger (mirrors serialtrig, data-only) ----
    input  wire        trig_en,
    input  wire [7:0]  match_pattern,
    input  wire [7:0]  match_mask,

    // ---- outputs ----
    output reg         emit_stb,   // 1-clk pulse: a confirmed frame decoded
    output reg  [7:0]  emit_byte,  // decoded value (LSB-first assembled)
    output reg  [23:0] emit_idx,   // column index of the start (S)
    output reg  [1:0]  emit_flags, // {frame_err, parity_err}
    output reg         decode_trig,// 1-clk pulse into capture.v (data-only match)
    output reg         matched,    // sticky: a match has occurred since reset
    output reg  [7:0]  matched_byte// latched matching byte
);

    // ---------- effective config ----------
    wire [4:0] bits_n   = (bits_cfg == 4'd0) ? 5'd8 : {1'b0, bits_cfg}; // 1..15
    wire       has_par  = (parity_cfg != 2'd0);
    wire [4:0] ph_par   = bits_n + 5'd1;                       // parity phase
    wire [4:0] ph_stop  = bits_n + 5'd1 + (has_par ? 5'd1 : 5'd0); // stop phase

    // =====================================================================
    // TEST-GEN: floor-accumulate frame generator (mirrors oracle timeline.add)
    //   bit boundaries at floor(k*SPB); each bit lasts
    //   floor((k+1)*SPB) - floor(k*SPB) columns => fractional-SPB faithful.
    //   Accumulator resets each frame cycle (async-transmitter style) to avoid
    //   counter wrap; the inter-frame idle gap absorbs the fractional remainder.
    // Cycle layout by bit index: 0=start(0), 1..bits=data LSB-first,
    //   [bits+1]=parity (if any), stop=1, then GAP idle(1) bits.
    // =====================================================================
    localparam [4:0] TG_GAP = 5'd3;                 // idle bits between frames
    wire [4:0] tg_cyclen = ph_stop + 5'd1 + TG_GAP; // total bits per cycle

    reg  [4:0]  tg_idx;        // current bit index within the cycle
    reg  [23:0] tg_col;        // column counter within the current cycle
    reg  [31:0] tg_acc;        // Q16.8 accumulation of k*SPB (for boundaries)
    reg  [31:0] tg_bd;         // next boundary = floor(k*SPB) = tg_acc>>8

    // masked test byte + its parity (so generated frames are self-consistent)
    wire [7:0] tg_mask  = (bits_n >= 5'd8) ? 8'hFF : ((8'd1 << bits_n) - 8'd1);
    wire [7:0] tg_data  = tg_byte & tg_mask;
    wire       tg_par   = (parity_cfg == 2'd2) ? ~(^tg_data) : (^tg_data);

    // level for the current bit index
    reg tg_level;
    always @* begin
        if (tg_idx == 5'd0)                      tg_level = 1'b0;            // start
        else if (tg_idx <= bits_n)               tg_level = tg_data[tg_idx-5'd1]; // data
        else if (has_par && tg_idx == ph_par)    tg_level = tg_par;         // parity
        else                                     tg_level = 1'b1;           // stop / idle
    end
    wire [7:0] tg_code = tg_level ? 8'hFF : 8'h00;

    always @(posedge clk or negedge rst_n) begin
        if (!rst_n) begin
            tg_idx <= 5'd0; tg_col <= 24'd0; tg_acc <= 32'd0; tg_bd <= 32'd0;
        end else if (!tg_en) begin
            // Held idle at the STOP index (line high) so re-enabling always
            // leads with >=1+GAP idle bits before the first real start edge.
            tg_idx <= ph_stop;
            tg_col <= 24'd0;
            tg_acc <= {8'd0, spb};        // 1*SPB
            tg_bd  <= {16'd0, spb[23:8]};  // floor(1*SPB)
        end else if (cap_tick) begin
            if ((tg_col + 24'd1) == tg_bd) begin
                if (tg_idx == (tg_cyclen - 5'd1)) begin
                    // cycle wrap -> restart accumulator at bit 0 (start)
                    tg_idx <= 5'd0;
                    tg_col <= 24'd0;
                    tg_acc <= {8'd0, spb};
                    tg_bd  <= {16'd0, spb[23:8]};
                end else begin
                    tg_idx <= tg_idx + 5'd1;
                    tg_col <= tg_col + 24'd1;
                    tg_acc <= tg_acc + {8'd0, spb};
                    tg_bd  <= (tg_acc + {8'd0, spb}) >> 8;
                end
            end else begin
                tg_col <= tg_col + 24'd1;
            end
        end
    end

    // =====================================================================
    // SLICER
    // =====================================================================
    wire [7:0] in_code  = tg_en ? tg_code : sample_code;
    wire       pure_bit = (in_code >= thr8);           // ORACLE sample decision

    // optional hysteresis level — used ONLY for start-edge detect, off by default
    reg        h_lvl;
    wire [8:0] h_thi_x  = {1'b0, thr8} + {1'b0, hyst_band};
    wire [7:0] h_thi    = h_thi_x[8] ? 8'hFF : h_thi_x[7:0];
    wire [7:0] h_tlo    = (thr8 > hyst_band) ? (thr8 - hyst_band) : 8'd0;
    wire       h_next   = (h_lvl == 1'b0 && in_code >= h_thi) ? 1'b1
                        : (h_lvl == 1'b1 && in_code <= h_tlo) ? 1'b0
                        : h_lvl;
    wire       slice_bit = hyst_en ? h_next : pure_bit; // edge-detect level

    // =====================================================================
    // DECODE FSM
    // =====================================================================
    localparam IDLE = 1'b0, ACTIVE = 1'b1;

    reg        state;
    reg        prev_slice;   // slice_bit at previous column (for falling edge)
    reg [23:0] sidx;         // free-running column counter
    reg [23:0] s_idx;        // latched start column S
    reg [23:0] off;          // columns since S (0 at the start column)
    reg [31:0] acc;          // Q16.8 relative sampling accumulator
    reg [31:0] tgt;          // rounded target offset for the current phase
    reg [4:0]  ph;           // phase index
    reg [15:0] val;          // assembled data word
    reg        pe;           // parity error (this frame)

    wire [23:0] cur_off = off + 24'd1;         // offset AT this ACTIVE column
    wire [4:0]  ph_m1   = ph - 5'd1;           // data bit position
    wire        exp_par = (parity_cfg == 2'd2) ? ~(^val) : (^val); // expected parity
    wire        stop_bad_w = (pure_bit != 1'b1);
    wire        clean_w    = (~stop_bad_w) & (~pe);
    wire        match_w    = trig_en & ((val[7:0] & match_mask) == (match_pattern & match_mask));

    always @(posedge clk or negedge rst_n) begin
        if (!rst_n) begin
            state <= IDLE; prev_slice <= 1'b1; h_lvl <= 1'b1;
            sidx <= 24'd0; s_idx <= 24'd0; off <= 24'd0; acc <= 32'd0;
            tgt <= 32'd0; ph <= 5'd0; val <= 16'd0; pe <= 1'b0;
            emit_stb <= 1'b0; emit_byte <= 8'd0; emit_idx <= 24'd0;
            emit_flags <= 2'd0; decode_trig <= 1'b0; matched <= 1'b0;
            matched_byte <= 8'd0;
        end else begin
            // default 1-clk pulse strobes
            emit_stb    <= 1'b0;
            decode_trig <= 1'b0;

            if (!en) begin
                state   <= IDLE;
                matched <= 1'b0;    // clear sticky when disabled
            end

            if (cap_tick) begin
                sidx       <= sidx + 24'd1;
                prev_slice <= slice_bit;
                h_lvl      <= h_next;

                if (!en) begin
                    state <= IDLE;
                end else case (state)
                // -------------------------------------------------- HUNT
                IDLE: begin
                    // pure-threshold falling edge (idle=1 -> start=0)
                    if (prev_slice == 1'b1 && slice_bit == 1'b0) begin
                        state <= ACTIVE;
                        s_idx <= sidx;            // S = this column
                        off   <= 24'd0;
                        acc   <= (spb >> 1);      // 0.5*SPB
                        tgt   <= (({8'd0,spb[23:1]}) + 32'd128) >> 8; // round(0.5*SPB)
                        ph    <= 5'd0;
                        val   <= 16'd0;
                        pe    <= 1'b0;
                    end
                end
                // ---------------------------------------------- SAMPLE
                ACTIVE: begin
                    off <= cur_off;
                    if (cur_off == tgt) begin
                        // ---- sample pure_bit at column S+tgt ----
                        if (ph == 5'd0) begin
                            // start-confirm: must be low, else abort & re-hunt
                            if (pure_bit != 1'b0) begin
                                state <= IDLE;
                            end else begin
                                acc <= acc + spb;
                                tgt <= (acc + spb + 32'd128) >> 8;
                                ph  <= 5'd1;
                            end
                        end else if (ph <= bits_n) begin
                            // data bit (LSB-first)
                            if (pure_bit) val <= val | (16'd1 << ph_m1);
                            acc <= acc + spb;
                            tgt <= (acc + spb + 32'd128) >> 8;
                            ph  <= ph + 5'd1;
                        end else if (has_par && ph == ph_par) begin
                            // parity bit
                            if (pure_bit != exp_par) pe <= 1'b1;
                            acc <= acc + spb;
                            tgt <= (acc + spb + 32'd128) >> 8;
                            ph  <= ph + 5'd1;
                        end else begin
                            // stop bit -> EMIT + resume hunt
                            emit_stb   <= 1'b1;
                            emit_byte  <= val[7:0];
                            emit_idx   <= s_idx;
                            emit_flags <= {stop_bad_w, pe};   // {frame_err,parity_err}
                            if (clean_w && match_w) begin
                                decode_trig  <= 1'b1;
                                matched      <= 1'b1;
                                matched_byte <= val[7:0];
                            end
                            state <= IDLE;
                        end
                    end
                end
                endcase
            end // cap_tick
        end
    end

endmodule

`default_nettype wire

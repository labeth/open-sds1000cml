// spi_decode.v — in-fabric SPI decoder (no chip-select) + internal TEST-GEN.
//
// GOAL: produce the SAME decoded word/byte sequence as the app's software
// oracle decode.DecodeSPI().Bytes on clean signals, but GAPLESS and in real
// time, feeding the SHARED byte_fifo + byte/pattern trigger.
//
// ORACLE CONTRACT (audit of app/internal/decode/decode_i2c_spi.go:134-261 and
// decode.go logicAt):
//
//  * SAMPLE DECISION IS PURE THRESHOLD. DecodeSPI samples the data bit with
//    logicAt(DA,i): codes[i] >= Thr ? 1 : 0. THRESH8 = ceil(Thr) makes the
//    integer compare `code >= thr8` == the float `code >= Thr`.  So
//    data_bit = (data_code >= data_thr), clk level = (clk_code >= clk_thr).
//    (The oracle detects CLOCK EDGES from the hysteresis level[] array, but on
//    clean full-swing vectors — codes only at the rails 56/200, sharp single-
//    sample transitions — level[] flips at the SAME column as pure threshold,
//    so ONE pure-threshold slice per channel is bit-exact for both edge detect
//    AND bit sampling. RISK-1: bench signals near threshold may diverge by a
//    column — clean-signal-verified, NOT bench-proven.)
//
//  * SAMPLE CADENCE = ONE DECIMATED COLUMN (colTimeS), one step per cap_tick.
//    All sequential logic advances ONLY on cap_tick, so "column" == cap_tick.
//
//  * SAMPLING EDGE. sampleRising = (CPOL==CPHA) (line 163). mode0(0,0) &
//    mode3(1,1) sample on CLK RISING; mode1(0,1) & mode2(1,0) on CLK FALLING.
//
//  * PER-COLUMN FSM (decode_i2c_spi.go:218-253). Registers bitCount(0..8), val,
//    lastSample(column), free-running colIdx. On each column where the selected
//    sampling edge occurs:
//      1. GAP REFRAME (line 234): if lastSample valid AND
//         (colIdx-lastSample) > gapReset AND bitCount>0 -> bitCount=0,val=0
//         (discard partial word; this edge starts a new word).
//      2. lastSample = colIdx.
//      3. bit = data (pure threshold this column).
//      4. if bitCount==0: start new word (bitStart=colIdx, val=0).
//      5. MSB-first: val=(val<<1)|bit ; LSB-first: val|=bit<<bitCount.
//      6. bitCount++.
//      7. if bitCount==8: EMIT WORD = val&0xFF (Result.Bytes, lines 248-251);
//         bitCount=0, val=0.
//    Trailing partial word (<8 bits at EOF) is DROPPED (never reaches 8).
//    Word length is HARDCODED 8 (SPICfg has no length field; bitCount==8) — the
//    FPGA MUST use 8 to be Bytes-exact (RISK-4).
//
//  * SEED. The oracle skips column 0 (pck<0 continue, line 222) and seeds pck
//    from column 0's level. This engine mirrors that: the first cap_tick after
//    enable primes prev_clk and detects NO edge; edges are detected from the
//    2nd column onward using colIdx that matches the oracle index i.
//
// gapReset — THE ONE HOST-COMPUTED PARAMETER (division of labor, mirrors the
//   host-computed UART SPB). The oracle derives the word-reframe threshold from
//   a percentile+cluster float estimator over the whole signal (lines 191-206:
//   gapReset = 1.5*period). Fabric cannot reproduce it, so the HOST loads an
//   INTEGER column threshold (reinterpreted SEL_DEC_SPB 0x0c/0x1c) and this
//   engine does the integer compare in step 1. For strict `>` equivalence to
//   the oracle's float compare (g integer, g > 1.5*period), the host should
//   load floor(1.5*period) — then (g > gapreset) == (g > 1.5*period) exactly
//   (RISK-2). On the oracle suite every comparison gap sits on a byte boundary
//   where the reset is a no-op, so any large gapreset is Bytes-exact there; the
//   two mid-word cases need the correct loaded value.
//
//  * SYMBOL ENCODING. emit_flags[1:0]=00 for every SPI word. One FIFO entry per
//    completed 8-bit word; Result.Bytes ≡ every drained entry in order.
//  * TRIGGER fires on EVERY completed word (data-only has no meaning for SPI):
//    (word & match_mask) == (match_pattern & match_mask).
//
// GATING: en=0 holds every output inert (emit_stb=0, decode_trig=0, matched=0)
// and freezes all state, exactly like uart_decode gates on !en. Instantiated
// with en = dec_en && (dec_proto==SPI) so proto=UART/I2C or dec_en=0 leaves the
// engine fully inert. All new state is logic registers (M9K-free).

`default_nettype none

module spi_decode (
    input  wire        clk,        // 80 MHz single domain
    input  wire        rst_n,      // async active-low reset (tie 1'b1 if unused)
    input  wire        cap_tick,   // one pulse per decimated column

    input  wire [7:0]  clk_code,   // CLK channel code for THIS column
    input  wire [7:0]  data_code,  // DATA channel code for THIS column

    // ---- config (loaded by host; latched in acq.v spare selectors) ----
    input  wire        en,         // master enable; 0 => fully inert
    input  wire [7:0]  clk_thr,    // ceil(Thr) for CLK
    input  wire [7:0]  data_thr,   // ceil(Thr) for DATA
    input  wire        cpol,       // clock polarity
    input  wire        cpha,       // clock phase
    input  wire        msb,        // 1 = MSB-first (SPICfg.MSB default true)
    input  wire [23:0] gapreset,   // host floor(1.5*period) in columns

    // ---- internal TEST-GEN ----
    input  wire        tg_en,      // 1 => decoder input driven by generator
    input  wire [7:0]  tg_word,    // word the generator repeatedly transmits

    // ---- single-word trigger (mirrors byte/pattern trigger) ----
    input  wire        trig_en,
    input  wire [7:0]  match_pattern,
    input  wire [7:0]  match_mask,

    // ---- outputs (SAME emit interface as uart_decode) ----
    output reg         emit_stb,    // 1-clk pulse: a completed word
    output reg  [7:0]  emit_byte,   // decoded word value
    output reg  [23:0] emit_idx,    // column index of the word's first bit
    output reg  [1:0]  emit_flags,  // always 00 for SPI
    output reg         decode_trig, // 1-clk pulse into capture.v (word match)
    output reg         matched,     // sticky: a match has occurred since reset
    output reg  [7:0]  matched_byte // latched matching word
);

    // sampleRising = (CPOL==CPHA)
    wire samp_rising = (cpol == cpha);

    // =====================================================================
    // TEST-GEN: mode-aware SPI word generator with an inter-word idle GAP.
    //   HALF columns per clock half-period; 8 bits/word; MSB/LSB per `msb`.
    //   Mirrors oracleSPIBits phasing so sampleRising=(CPOL==CPHA) lands the
    //   selected edge on a stable data bit:
    //     !cpha: phase0 = idle(setup)  , phase1 = active(latch on leading edge)
    //      cpha: phase0 = active(change), phase1 = idle (latch on trailing edge)
    //   Data holds the bit across BOTH phases. After bit7 an idle GAP of
    //   TG_GAP columns (clock at idle, no edges) delimits words: with the host
    //   gapreset loaded below TG_GAP the decoder's reframe aligns its word
    //   boundaries to the generator's, so every emitted word == tg_word (a
    //   self-contained loopback for the shift/emit/reframe path). Without a gap,
    //   CS-less continuous clocking has no frame delimiter and the decoder would
    //   emit an arbitrary bit-rotation of the word (correct per DecodeSPI, but
    //   not self-checkable) — hence the gap.
    // =====================================================================
    localparam [3:0] TG_HALF = 4'd8;    // columns per half clock period
    localparam [5:0] TG_GAP  = 6'd40;   // idle columns between words (> gapreset)

    reg  [2:0] tg_bit;    // 0..7 bit index within the word
    reg        tg_phase;  // 0 = first half, 1 = second half
    reg  [3:0] tg_cnt;    // column counter within the current half
    reg        tg_ingap;  // 1 => streaming the inter-word idle gap
    reg  [5:0] tg_gcnt;   // gap column counter

    wire [2:0] tg_k     = msb ? (3'd7 - tg_bit) : tg_bit;
    wire       tg_b     = tg_word[tg_k];
    wire       tg_idle  = cpol;           // clock idle level
    wire       tg_active= ~cpol;
    // clock level for the current phase (idle during the gap)
    wire       tg_clk_lvl = tg_ingap ? tg_idle
                          : (!cpha) ? (tg_phase ? tg_active : tg_idle)
                                    : (tg_phase ? tg_idle   : tg_active);
    wire [7:0] tg_clk_code  = tg_clk_lvl        ? 8'hFF : 8'h00;
    wire [7:0] tg_data_code = (!tg_ingap & tg_b)? 8'hFF : 8'h00;

    always @(posedge clk or negedge rst_n) begin
        if (!rst_n) begin
            tg_bit <= 3'd0; tg_phase <= 1'b0; tg_cnt <= 4'd0;
            tg_ingap <= 1'b0; tg_gcnt <= 6'd0;
        end else if (!tg_en) begin
            // Hold at bit0/phase0 so re-enabling leads with a setup half.
            tg_bit <= 3'd0; tg_phase <= 1'b0; tg_cnt <= 4'd0;
            tg_ingap <= 1'b0; tg_gcnt <= 6'd0;
        end else if (cap_tick) begin
            if (tg_ingap) begin
                if ((tg_gcnt + 6'd1) == TG_GAP) begin
                    tg_ingap <= 1'b0; tg_gcnt <= 6'd0;   // gap done -> new word
                    tg_bit <= 3'd0; tg_phase <= 1'b0; tg_cnt <= 4'd0;
                end else begin
                    tg_gcnt <= tg_gcnt + 6'd1;
                end
            end else if ((tg_cnt + 4'd1) == TG_HALF) begin
                tg_cnt <= 4'd0;
                if (tg_phase == 1'b1) begin
                    tg_phase <= 1'b0;
                    if (tg_bit == 3'd7) tg_ingap <= 1'b1;  // word done -> gap
                    else                tg_bit   <= tg_bit + 3'd1;
                end else begin
                    tg_phase <= 1'b1;
                end
            end else begin
                tg_cnt <= tg_cnt + 4'd1;
            end
        end
    end

    // =====================================================================
    // SLICER (pure threshold — the oracle sample decision)
    // =====================================================================
    wire [7:0] in_clk  = tg_en ? tg_clk_code  : clk_code;
    wire [7:0] in_data = tg_en ? tg_data_code : data_code;
    wire       clk_lvl = (in_clk  >= clk_thr);
    wire       dat_lvl = (in_data >= data_thr);

    // =====================================================================
    // DECODE FSM
    // =====================================================================
    reg        primed;      // seeded prev_clk yet? (mirrors oracle pck<0 skip)
    reg        prev_clk;    // clk level at previous column
    reg [23:0] colIdx;      // free-running column index (== oracle i)
    reg [23:0] lastSample;  // column of the last sampling edge
    reg        lastValid;   // lastSample holds a real edge
    reg [3:0]  bitCount;    // 0..8 bits assembled in the current word
    reg [7:0]  val;         // assembled word
    reg [23:0] wordStart;   // column of the word's first sampled bit

    // combinational next-word assembly for the current sampling edge
    reg  [3:0] bc;
    reg  [7:0] vv;
    wire       edge_rising  = (~prev_clk) &  clk_lvl;
    wire       edge_falling =   prev_clk  & ~clk_lvl;
    wire       samp_edge    = samp_rising ? edge_rising : edge_falling;
    wire       gap_over     = lastValid && ((colIdx - lastSample) > gapreset);

    always @(posedge clk or negedge rst_n) begin
        if (!rst_n) begin
            primed <= 1'b0; prev_clk <= 1'b0; colIdx <= 24'd0;
            lastSample <= 24'd0; lastValid <= 1'b0;
            bitCount <= 4'd0; val <= 8'd0; wordStart <= 24'd0;
            emit_stb <= 1'b0; emit_byte <= 8'd0; emit_idx <= 24'd0;
            emit_flags <= 2'd0; decode_trig <= 1'b0;
            matched <= 1'b0; matched_byte <= 8'd0;
        end else begin
            // default 1-clk pulse strobes
            emit_stb    <= 1'b0;
            decode_trig <= 1'b0;

            if (!en) begin
                // fully inert + fresh re-seed on next enable
                primed     <= 1'b0;
                prev_clk   <= 1'b0;
                colIdx     <= 24'd0;
                lastSample <= 24'd0;
                lastValid  <= 1'b0;
                bitCount   <= 4'd0;
                val        <= 8'd0;
                matched    <= 1'b0;   // clear sticky when disabled
            end else if (cap_tick) begin
                colIdx   <= colIdx + 24'd1;
                prev_clk <= clk_lvl;

                if (!primed) begin
                    primed <= 1'b1;              // seed prev_clk; no edge (col 0)
                end else if (samp_edge) begin
                    bc = bitCount;
                    vv = val;
                    // 1. gap reframe (discard partial word)
                    if (gap_over && (bc != 4'd0)) begin
                        bc = 4'd0;
                        vv = 8'd0;
                    end
                    // 2. record this sampling edge
                    lastSample <= colIdx;
                    lastValid  <= 1'b1;
                    // 4. new word bookkeeping
                    if (bc == 4'd0) begin
                        vv        = 8'd0;
                        wordStart <= colIdx;
                    end
                    // 5. shift in the bit
                    if (msb) vv = {vv[6:0], dat_lvl};
                    else     vv = vv | ({7'd0, dat_lvl} << bc);
                    // 6. advance
                    bc = bc + 4'd1;
                    // 7. emit on full word
                    if (bc == 4'd8) begin
                        emit_stb   <= 1'b1;
                        emit_byte  <= vv;
                        emit_idx   <= wordStart;
                        emit_flags <= 2'b00;
                        if (trig_en &&
                            ((vv & match_mask) == (match_pattern & match_mask))) begin
                            decode_trig  <= 1'b1;
                            matched      <= 1'b1;
                            matched_byte <= vv;
                        end
                        bitCount <= 4'd0;
                        val      <= 8'd0;
                    end else begin
                        bitCount <= bc;
                        val      <= vv;
                    end
                end
            end // cap_tick
        end
    end

endmodule

`default_nettype wire

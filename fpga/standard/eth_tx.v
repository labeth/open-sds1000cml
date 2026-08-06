// eth_tx.v — IN-FABRIC 100BASE-TX LINE ENCODER (self-test stimulus source).
//            *** PIPELINED / RETIMED FOR 200 MHz (5 ns) CLOSURE on C8 (ITEM 2) ***
//
// PURPOSE (ITEM 2)
//   Synthesize, entirely in fabric, the 3-level 100BASE-TX line signal that the
//   in-fabric ETH decode front-end (eth_gearbox -> eth_slicer_cdr -> ...) already
//   consumes, so the WHOLE decode chain + the dec_trigger mode-1 FCS-error trigger
//   can be PROVEN on the LIVE scope with NO bench 100BASE-TX source connected.
//
//   It emits a REPEATING MAC frame with a SELECTABLE good-vs-bad FCS: a config bit
//   (bad_fcs) corrupts one transmitted FCS octet so the receiver's CRC-32 residue
//   fails => framer flags8[3] (FCS-err) => dec_trigger mode-1 fires; with bad_fcs=0
//   the FCS verifies (flags8[4] ok) and mode-1 does NOT fire.
//
// ORACLE: app/internal/eth100tx (EncodeFrame). Every coding stage MIRRORS the Go
// golden model EXACTLY so the decode chain (already held bit-exact to that model)
// recovers the frame byte-for-byte:
//   MAC bytes (7x 0x55 preamble + 0xD5 SFD + frame + CRC-32 FCS)
//     -> MII nibbles, low-nibble-first -> 4B/5B (Table 24-1) MSB-first
//     -> self-sync scrambler LFSR x^11+x^9+1 (key[n]=key[n-9]^key[n-11])
//     -> MLT-3 (transition on a 1-bit; levels cycle 0,+1,0,-1)
//     -> oversample 600 MSa/s (run-length 4,5,5,5,5 per 5-symbol group).
//
// ============================================================================
// WHY A PIPELINE REWRITE (vs. the prior single-cycle generator)
// ----------------------------------------------------------------------------
// The prior version computed, in ONE 200 MHz cycle, the whole chain
//     dcnt -> dbyte(mux incl. ~crc) -> enc4b5b -> code-group -> bit-select
//          -> scramble -> MLT-3 next-level -> nxt_lvl
// plus the 8-round combinational CRC-32 step. STA on C8 failed that
// dcnt->nxt_lvl path by ~-6.4 ns (single-cycle ~11.4 ns > 5 ns).
//
// This rewrite SPLITS that chain into registered stages and DECOUPLES symbol
// GENERATION from the OVERSAMPLER through a small logic FIFO. The line-output
// symbol sequence is FUNCTIONALLY IDENTICAL (same 4B5B/scrambler/MLT-3/CRC math,
// same emission order) — it is merely produced a few cycles earlier into the
// FIFO. Because the stream is continuous, that extra pipeline latency is
// invisible at the line.  KEY ENABLER: the scrambler here free-runs
// (key = lf[8]^lf[10], shift-in key) so the key stream is DATA-INDEPENDENT; the
// MLT-3 index is a 2-bit running accumulator — both are trivially fast. The long
// combinational work was purely code-group SELECTION (dbyte mux + 4B5B LUT) and
// the CRC-32 fold, both of which have MANY idle cycles of slack (a code group
// spans 5 symbols, an octet spans 2 code groups) and are pipelined off the
// per-symbol datapath.
//
// PIPELINE STAGES (each register-to-register path; see plan/ethtx2.md for the
// per-stage timing argument, all << 5 ns):
//   S-EMIT  (per symbol, II=1 within a code group): cg_r[bpos] 5:1 mux -> XOR
//           (scramble) -> 2-bit MLT add -> 3:1 level mux -> FIFO write.  ~5 levels.
//   S-ADV   (once per code group, at bpos==0): next-state of the code-group FSM
//           (idle/dcnt compares + small muxes) -> FSM registers.  ~4 levels.
//   S-ENC   (once per code group, RELOAD cycle): registered FSM descriptor ->
//           dbyte 4:1 mux -> nibble 2:1 mux -> enc4b5b 4->5 LUT -> cg_r.  ~5 levels.
//           (crc here is a REGISTER, not the fold — no 8-round chain on this path.)
//   S-CRC   (2 CRC rounds/cycle, 4 cycles/octet, gobs of slack): crc2() = 2x
//           (shift + conditional XOR).  ~4 levels.
// The OVERSAMPLER math (run-length 4,5,5,5,5, cur/nxt level window) is byte-for-
// byte the prior logic, now fed from the FIFO instead of the live generator.
//
// FLOW CONTROL / NO-UNDERFLOW: the generator produces 5 symbols per 6 cycles
// (0.833 sym/cyc) — one RELOAD bubble per code group. The oversampler consumes at
// most 3/R sym/cyc (R>=4) = 0.75 sym/cyc (an R=4 symbol) and only 0.625 sym/cyc
// sustained (the 4,5,5,5,5 pattern). 0.833 > 0.75 => the generator instantaneously
// dominates the consumer; the 16-deep FIFO (primed to 2 before the oversampler
// starts) therefore never underflows. When the FIFO is full the generator simply
// STALLS (freezes) — it cannot overflow.
//
// OUTPUT INTERFACE: exactly the eth_gearbox WRITE port shape — WR_SAMP(=3) signed
// SAMPLE_W(=12) sample codes per wr_clk tick (s0 = earliest = samp[SAMPLE_W-1:0]),
// with wr_valid. Drives eth100_decode_lr.wr_samp/wr_valid via a mux in acq.v when
// enabled. Runs in the 200 MHz interleave (write) clock domain, 3 samples/tick.
//
// RESOURCES: 4B5B LUT (logic) + 11-bit LFSR + 2-bit MLT-3 index + small frame ROM
// (ramstyle=logic) + a 16x12 logic FIFO + a 2-rounds/cycle CRC-32 + counters/FSMs.
// 0 M9K, 0 PLL, 0 pins by construction.
//
// GATING / INERTNESS: rst OR !en holds the module fully inert (samp=0, valid=0,
// all state cleared). Enabled ONLY in the ETH self-test (dec_en & proto==ETH &
// ethtx_en); off => acq.v selects the live interleave taps and this module's
// outputs are unused => byte-for-byte identical to today.

`default_nettype none

module eth_tx #(
    parameter integer SAMPLE_W  = 12,   // signed sample-code width (golden +/-1000)
    parameter integer WR_SAMP   = 3,    // samples emitted per wr_clk tick (== gearbox WR_SAMP)
    parameter integer AMP       = 1000, // MLT-3 +/- amplitude (golden AmpPos); 0 for the mid level
    parameter integer FRAME_LEN = 18,   // MAC frame length (dst..payload), NO preamble/SFD/FCS
    parameter integer LEAD_IDLE = 32,   // /I/ code groups before /J/K/ (>= ~9 for descrambler lock)
    parameter integer TRAIL_IDLE= 8     // /I/ code groups after /T/R/ (inter-frame idle)
) (
    input  wire clk,        // 200 MHz interleave WRITE clock (same as gearbox wr_clk)
    input  wire rst,        // synchronous, active-high
    input  wire en,         // engine gate; en=0 => fully inert
    input  wire bad_fcs,    // 1 => corrupt one transmitted FCS octet (CRC-err frame)

    output reg  [WR_SAMP*SAMPLE_W-1:0] samp,   // s0 = earliest = samp[SAMPLE_W-1:0]
    output reg                         valid   // 1 => WR_SAMP fresh samples this tick
);
    // ------------------------------------------------------------------ constants
    localparam signed [SAMPLE_W-1:0] AMP_P = AMP;
    localparam signed [SAMPLE_W-1:0] AMP_Z = 0;
    localparam signed [SAMPLE_W-1:0] AMP_N = -AMP;

    // 4B/5B control code groups (IEEE 802.3 Table 24-1), 5 bits bit4..bit0.
    localparam [4:0] C_I = 5'b11111, C_J = 5'b11000, C_K = 5'b10001,
                     C_T = 5'b01101, C_R = 5'b00111;

    // CRC-32 (IEEE 802.3 FCS): reflected poly 0xEDB88320, init/xorout 0xFFFFFFFF.
    localparam [31:0] CRC_POLY = 32'hEDB88320;
    localparam [31:0] CRC_INIT = 32'hFFFFFFFF;

    // data-byte stream layout (matches golden mac[] minus byte0 -> /J/K/):
    //   dcnt 0..5 : 6 remaining preamble octets (0x55); 6 : SFD (0xD5);
    //   7..7+L-1  : frame octets (ROM); 7+L..+3 : 4 FCS octets (one corrupted iff bad_fcs)
    localparam integer PRE_REM    = 6;                       // preamble octets after byte0
    localparam integer FCS_BASE   = 7 + FRAME_LEN;           // dcnt of FCS octet 0
    localparam integer DATA_BYTES = 7 + FRAME_LEN + 4;       // total data octets

    // 4B/5B data code table (nibble -> 5-bit code, bit4..bit0).
    function [4:0] enc4b5b(input [3:0] n);
        case (n)
            4'h0: enc4b5b = 5'h1E; 4'h1: enc4b5b = 5'h09;
            4'h2: enc4b5b = 5'h14; 4'h3: enc4b5b = 5'h15;
            4'h4: enc4b5b = 5'h0A; 4'h5: enc4b5b = 5'h0B;
            4'h6: enc4b5b = 5'h0E; 4'h7: enc4b5b = 5'h0F;
            4'h8: enc4b5b = 5'h12; 4'h9: enc4b5b = 5'h13;
            4'hA: enc4b5b = 5'h16; 4'hB: enc4b5b = 5'h17;
            4'hC: enc4b5b = 5'h1A; 4'hD: enc4b5b = 5'h1B;
            4'hE: enc4b5b = 5'h1C; 4'hF: enc4b5b = 5'h1D;
            default: enc4b5b = 5'h1F;
        endcase
    endfunction

    // TWO reflected CRC-32 rounds (S-CRC stage does one call per clock -> 8 rounds
    // over 4 clocks == the golden crc32Table update, split for timing).
    function [31:0] crc2(input [31:0] x0);
        reg [31:0] x;
        begin
            x = x0;
            x = x[0] ? ((x >> 1) ^ CRC_POLY) : (x >> 1);
            x = x[0] ? ((x >> 1) ^ CRC_POLY) : (x >> 1);
            crc2 = x;
        end
    endfunction

    // ---------------------------------------------------------------- frame ROM
    // Constant => inferred as a logic ROM (ramstyle=logic), NOT an M9K block.
    // MUST match sim/tb_eth_tx.v and the Go oracle (eth100tx TestFabricVector).
    (* ramstyle = "logic" *) reg [7:0] frame_rom [0:FRAME_LEN-1];
    initial begin
        frame_rom[0]=8'h00; frame_rom[1]=8'h11; frame_rom[2]=8'h22; frame_rom[3]=8'h33;
        frame_rom[4]=8'h44; frame_rom[5]=8'h55; frame_rom[6]=8'h66; frame_rom[7]=8'h77;
        frame_rom[8]=8'h88; frame_rom[9]=8'h99; frame_rom[10]=8'hAA; frame_rom[11]=8'hBB;
        frame_rom[12]=8'h08; frame_rom[13]=8'h06; frame_rom[14]=8'hDE; frame_rom[15]=8'hAD;
        frame_rom[16]=8'hBE; frame_rom[17]=8'hEF;
    end

    // ============================================================================
    // CODE-GROUP SOURCE FSM (one 5-bit code group per RELOAD; feeds cg_r)
    // ============================================================================
    localparam [2:0] PH_LEAD=3'd0, PH_JK=3'd1, PH_DATA=3'd2, PH_TR=3'd3, PH_TRAIL=3'd4;
    reg [2:0]  ph;
    reg [15:0] idle_cnt;   // lead/trail idle position
    reg        jk_sel;     // 0=J 1=K
    reg        tr_sel;     // 0=T 1=R
    reg [15:0] dcnt;       // data-octet index
    reg        nph;        // 0=low nibble, 1=high nibble

    // frame-ROM tap (continuous assign; only the tapped byte is combinational).
    wire [15:0] frm_i   = (dcnt >= 7 && dcnt < FCS_BASE) ? (dcnt - 16'd7) : 16'd0;
    wire [7:0]  frm_tap = frame_rom[frm_i[4:0]];

    // CRC-32 fold: 2-rounds/clock pipeline, plenty of slack (octet = 2 code groups).
    reg [31:0] crc;        // committed running CRC-32 register (read by dbyte for FCS)
    reg [31:0] crc_work;   // in-flight fold accumulator
    reg [2:0]  crc_cnt;    // remaining 2-round chunks (0 = idle)

    // current data octet (combinational)
    reg [7:0] dbyte;
    always @* begin
        if (dcnt < PRE_REM)                  dbyte = 8'h55;                       // preamble
        else if (dcnt == PRE_REM)            dbyte = 8'hD5;                       // SFD
        else if (dcnt < FCS_BASE)            dbyte = frm_tap;                     // frame
        else begin                                                               // FCS octets
            case (dcnt - FCS_BASE)
                16'd0: dbyte = ~crc[7:0];
                16'd1: dbyte = ~crc[15:8];
                16'd2: dbyte = ~crc[23:16];
                default: dbyte = ~crc[31:24];
            endcase
            if (bad_fcs && (dcnt == FCS_BASE)) dbyte = crc[7:0]; // (~crc[7:0]) ^ 0xFF
        end
    end

    // current 5-bit code group (combinational off the FSM state) -> loaded to cg_r
    reg [4:0] cur_cg;
    always @* begin
        case (ph)
            PH_JK:   cur_cg = jk_sel ? C_K : C_J;
            PH_DATA: cur_cg = enc4b5b(nph ? dbyte[7:4] : dbyte[3:0]);
            PH_TR:   cur_cg = tr_sel ? C_R : C_T;
            default: cur_cg = C_I;   // PH_LEAD / PH_TRAIL
        endcase
    end

    // ============================================================================
    // SYMBOL GENERATOR (per-symbol datapath) + code-group serialize/reload
    // ============================================================================
    reg  [4:0]  cg_r;       // registered current code group being serialized
    reg  [2:0]  bpos;       // MSB-first bit position within cg_r (4..0)
    reg         p_reload;   // 1 => RELOAD bubble cycle (load cg_r, no symbol emitted)
    reg  [10:0] lf;         // scrambler LFSR (free-run: key=lf[8]^lf[10], shift in)
    reg  [1:0]  mlt;        // MLT-3 phase index (levels 0,+1,0,-1)

    // per-symbol combinational (S-EMIT datapath)
    reg         plain_b, key_b, scr_b;
    reg  [1:0]  mlt_n;
    reg signed [SAMPLE_W-1:0] gen_amp;
    always @* begin
        plain_b = cg_r[bpos];
        key_b   = lf[8] ^ lf[10];
        scr_b   = plain_b ^ key_b;
        mlt_n   = scr_b ? (mlt + 2'd1) : mlt;
        gen_amp = (mlt_n == 2'd1) ? AMP_P : (mlt_n == 2'd3) ? AMP_N : AMP_Z;
    end

    // ============================================================================
    // SYMBOL FIFO (logic-only, 16 deep) — decouples generator from oversampler
    // ============================================================================
    localparam integer FIFO_AW = 4, FIFO_N = (1 << FIFO_AW);
    reg  signed [SAMPLE_W-1:0] fifo [0:FIFO_N-1];
    reg  [FIFO_AW-1:0] fifo_wr, fifo_rd;
    reg  [FIFO_AW:0]   fifo_cnt;                  // 0..16
    wire fifo_full  = (fifo_cnt == FIFO_N);
    wire fifo_empty = (fifo_cnt == 0);

    // ============================================================================
    // OVERSAMPLER (600 MSa/s, run-length 4,5,5,5,5) — prior math, FIFO-fed
    // ============================================================================
    reg signed [SAMPLE_W-1:0] cur_lvl, nxt_lvl;   // current + 1-symbol lookahead
    reg  [2:0]  sis;        // samples already emitted of the current symbol (0..R-1)
    reg  [2:0]  smod5;      // current symbol index mod 5 (R = (smod5==0)?4:5)
    reg  [1:0]  cprime;     // 0,1 = fill cur/nxt from FIFO; 2 = running

    wire [2:0] R   = (smod5 == 3'd0) ? 3'd4 : 3'd5;
    wire [2:0] rem = R - sis;                      // 1..5 (sis always < R)
    wire       adv = (rem <= 3'd3);                // current symbol completes this tick
    wire [2:0] k   = (rem >= 3'd3) ? 3'd3 : rem;   // #samples of cur symbol this tick
    wire signed [SAMPLE_W-1:0] s0 = (3'd0 < k) ? cur_lvl : nxt_lvl;
    wire signed [SAMPLE_W-1:0] s1 = (3'd1 < k) ? cur_lvl : nxt_lvl;
    wire signed [SAMPLE_W-1:0] s2 = (3'd2 < k) ? cur_lvl : nxt_lvl;

    // FIFO push (generator emits a symbol) / pop (oversampler consumes a symbol)
    wire push     = en & ~p_reload & ~fifo_full;             // one per S-EMIT cycle
    wire signed [SAMPLE_W-1:0] push_val = gen_amp;
    wire cons_want = (cprime != 2'd2) ? 1'b1 : adv;          // prime fills 2, then on adv
    wire pop      = en & ~fifo_empty & cons_want;
    wire signed [SAMPLE_W-1:0] pop_val = fifo[fifo_rd];

    // ---------------------------------------------------------------- FIFO block
    integer fi;
    always @(posedge clk) begin
        if (rst || !en) begin
            fifo_wr <= {FIFO_AW{1'b0}}; fifo_rd <= {FIFO_AW{1'b0}}; fifo_cnt <= {(FIFO_AW+1){1'b0}};
            for (fi = 0; fi < FIFO_N; fi = fi + 1) fifo[fi] <= {SAMPLE_W{1'b0}};
        end else begin
            if (push) begin fifo[fifo_wr] <= push_val; fifo_wr <= fifo_wr + 1'b1; end
            if (pop)  fifo_rd <= fifo_rd + 1'b1;
            fifo_cnt <= fifo_cnt + (push ? 1'b1 : 1'b0) - (pop ? 1'b1 : 1'b0);
        end
    end

    // ------------------------------------------------------------ GENERATOR block
    always @(posedge clk) begin
        if (rst || !en) begin
            ph <= PH_LEAD; idle_cnt <= 16'd0; jk_sel <= 1'b0; tr_sel <= 1'b0;
            dcnt <= 16'd0; nph <= 1'b0;
            crc <= CRC_INIT; crc_work <= 32'd0; crc_cnt <= 3'd0;
            cg_r <= C_I; bpos <= 3'd4; p_reload <= 1'b0;
            lf <= 11'h5A3 /* nonzero seed; phase is not load-bearing */; mlt <= 2'd0;
        end else begin
            // ---- S-CRC: advance any in-flight fold (2 rounds/clock), independent ----
            if (crc_cnt != 3'd0) begin
                crc_work <= crc2(crc_work);
                crc_cnt  <= crc_cnt - 3'd1;
                if (crc_cnt == 3'd1) crc <= crc2(crc_work);   // final 2 rounds -> commit
            end

            if (p_reload) begin
                // ---- RELOAD (bubble): load next code group, no symbol emitted ----
                cg_r     <= cur_cg;      // enc(dbyte) off the just-advanced FSM (S-ENC)
                bpos     <= 3'd4;
                p_reload <= 1'b0;
            end else if (~fifo_full) begin
                // ---- S-EMIT: emit one symbol (push handled by FIFO block) --------
                lf  <= {lf[9:0], key_b};
                mlt <= mlt_n;
                if (bpos == 3'd0) begin
                    p_reload <= 1'b1;    // spend next cycle reloading cg_r
                    // ---- S-ADV: advance the code-group FSM by one group ----
                    case (ph)
                        PH_LEAD: if (idle_cnt >= LEAD_IDLE-1) begin ph <= PH_JK; jk_sel <= 1'b0; idle_cnt <= 16'd0; end
                                 else idle_cnt <= idle_cnt + 16'd1;
                        PH_JK:   if (!jk_sel) jk_sel <= 1'b1;
                                 else begin ph <= PH_DATA; dcnt <= 16'd0; nph <= 1'b0; end
                        PH_DATA: if (!nph) begin
                                     nph <= 1'b1;            // low -> high nibble, same octet
                                     // CRC init/fold is triggered at the LOW-nibble
                                     // boundary (dbyte is already fully known, dcnt is
                                     // still this octet). Doing it here — a full code
                                     // group before the octet completes — gives the
                                     // 4-cycle fold pipeline ample slack to COMMIT crc
                                     // before the FCS[0] code group reads it. Same octets
                                     // (7..FCS_BASE-1), same order => identical CRC value.
                                     if (dcnt == PRE_REM)                   crc <= CRC_INIT;
                                     else if (dcnt >= 7 && dcnt < FCS_BASE) begin
                                         crc_work <= crc ^ {24'd0, dbyte};  // start fold
                                         crc_cnt  <= 3'd4;                  // 4 x (2 rounds)
                                     end
                                 end
                                 else begin
                                     nph <= 1'b0;            // octet complete
                                     if (dcnt == DATA_BYTES-1) begin ph <= PH_TR; tr_sel <= 1'b0; end
                                     else                            dcnt <= dcnt + 16'd1;
                                 end
                        PH_TR:   if (!tr_sel) tr_sel <= 1'b1;
                                 else begin ph <= PH_TRAIL; idle_cnt <= 16'd0; end
                        PH_TRAIL:if (idle_cnt >= TRAIL_IDLE-1) begin ph <= PH_LEAD; idle_cnt <= 16'd0; end
                                 else idle_cnt <= idle_cnt + 16'd1;
                        default: ph <= PH_LEAD;
                    endcase
                end else begin
                    bpos <= bpos - 3'd1;
                end
            end
            // (else: FIFO full during S-EMIT -> generator STALLS this cycle)
        end
    end

    // ----------------------------------------------------------- OVERSAMPLER block
    always @(posedge clk) begin
        if (rst || !en) begin
            cur_lvl <= AMP_Z; nxt_lvl <= AMP_Z; sis <= 3'd0; smod5 <= 3'd0; cprime <= 2'd0;
            samp <= {WR_SAMP*SAMPLE_W{1'b0}}; valid <= 1'b0;
        end else if (cprime != 2'd2) begin
            // ---- PRIME: pull the first two symbols (sym0->cur_lvl, sym1->nxt_lvl) ----
            valid <= 1'b0;
            samp  <= {WR_SAMP*SAMPLE_W{1'b0}};
            if (~fifo_empty) begin
                cur_lvl <= nxt_lvl;
                nxt_lvl <= pop_val;
                cprime  <= cprime + 2'd1;
            end
        end else begin
            // ---- RUN: emit 3 samples/tick, advance <= 1 symbol/tick ----
            samp  <= {s2, s1, s0};
            valid <= 1'b1;
            if (adv) begin
                cur_lvl <= nxt_lvl;
                nxt_lvl <= pop_val;                          // next symbol from FIFO
                sis     <= 3'd3 - rem;                       // leftover samples spent on nxt (0..2)
                smod5   <= (smod5 == 3'd4) ? 3'd0 : (smod5 + 3'd1);
            end else begin
                sis     <= sis + 3'd3;                       // stay in the current symbol
            end
        end
    end

endmodule

`default_nettype wire

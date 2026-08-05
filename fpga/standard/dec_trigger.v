// dec_trigger.v — extended in-fabric DECODE TRIGGER engine (drop-in).
//
// PURPOSE
//   Consume the SHARED post-mux decode symbol stream (emit_stb / emit_byte /
//   emit_idx / emit_flags8) that acq.v builds from the UART/I2C/SPI/ETH
//   front-ends, plus the ETH SFD side-pulse, and produce a single decode_trig
//   pulse into capture.v together with a sticky `matched` STATUS bit and a
//   latched `matched_byte` for the 0x6c readback — EXACTLY as the current inline
//   trigger does, but with four selectable MODES instead of one.
//
//   This is a strict superset of the existing per-module byte-match trigger.
//   In trig_mode==0 the module ROUTES THROUGH the untouched per-module
//   comparator outputs (legacy_trig / legacy_matched / legacy_matched_byte),
//   so mode-0 behaviour is byte-for-byte identical to today and the
//   IFACE_BUILD_ID is unaffected (no decoder module changes, no new selectors).
//
// MODES  (trig_mode = SEL_DEC_CFG(0x04)[13:12]; reset 0 => mode 0 => today)
//   0 BYTE-MATCH  : pass-through of the per-module data-only comparator pulse.
//                   (UART clean_w&&match_w, I2C data-only pend_match, SPI word
//                   match, ETH SFD). Nothing here re-implements it.
//   1 ERROR       : decode_trig on ANY emit when (emit_flags8 & err_mask)!=0.
//                   err_mask = SEL_DEC_MATCH[15:8] (today's mask byte).
//                   err bits: UART[1]=frame_err [0]=parity_err; I2C[0]=NAK;
//                   ETH[3]=FCS-err. SPI has no error flag => mode 1 is a no-op
//                   for SPI (documented). Capability the app does NOT have.
//   2 SEQUENCE    : match a 2..4 contiguous DATA-byte sequence (mirrors
//                   serialtrig matchBytes). Data-only (same per-proto predicate
//                   as mode 0); non-data (error/marker) symbols are NOT pushed,
//                   and the adjacency (idx-gap) check rejects a sequence that
//                   bridged a marker/gap — exactly like serialtrig counting
//                   Kind=="data" only + the I0/I1 adjacency rule. Fires at the
//                   LAST byte of the sequence.
//   3 ADDR/FIELD  : I2C — on the ADDRESS symbol (KIND==1) fire when
//                   (emit_byte & addr_mask)==(addr_field & addr_mask), where
//                   emit_byte={addr7,rw}. addr_field=MATCH[7:0], addr_mask=
//                   MATCH[15:8]; clear addr bits for Addr-any, clear bit0 for
//                   RW-any. ETH — fire on SFD (== the current ETH trigger), a
//                   strict superset (no serialtrig ETH parity claimed).
//
// REGISTER REUSE (no new selectors; only previously-FREE / mode-reinterpreted):
//   trig_mode   = dec_cfg[13:12]   (was FREE)
//   seqlen_cfg  = dec_cfg[15:14]   (was FREE) ; N = seqlen_cfg+1 (2..4 meaningful)
//   match_pattern = dec_match[7:0] ; mode0/2 seq[0], mode1 (unused), mode3 addr_field
//   match_mask    = dec_match[15:8]; mode0 mask, mode1 err_mask, mode2 seq[3],
//                                    mode3 addr_mask
//   seq_b1        = dec_tg[7:0]     ; mode2 seq[1]   (reuses TESTGEN reg)
//   seq_b2        = dec_tg[15:8]    ; mode2 seq[2]
//   adj_win       = parent-computed column window for mode-2 adjacency
//                   (~3*byte-width: consecutive DATA-byte start-idx gap for a
//                   contiguous stream). Imperfect-threshold caveat documented.
//
// DROP-IN CONTRACT: outputs decode_trig(1-clk)/matched(sticky)/matched_byte have
// the SAME shape as the values acq.v currently derives from the per-module
// outputs; only the CONDITION that sets them changes. en=0 forces decode_trig 0
// and clears the sticky (mirrors the per-module !en behaviour), so decode-off is
// byte-identical.
//
// All comparisons are combinational off the (registered) emit strobe, so
// decode_trig is a genuine 1-clk pulse aligned to the anchoring emit, exactly
// like the per-module pulses it can replace.

`default_nettype none

module dec_trigger (
    input  wire        clk,
    input  wire        rst,          // synchronous reset (op_reset): clears state
    input  wire        en,           // dec_en; 0 => inert, sticky cleared, trig 0

    // ---- shared post-mux decode symbol stream (acq.v dec_emit_*) ----
    input  wire        emit_stb,     // 1-clk: a symbol was emitted this column
    input  wire [7:0]  emit_byte,    // symbol value (data byte / {addr7,rw})
    input  wire [23:0] emit_idx,     // symbol column index (start/first-bit)
    input  wire [7:0]  emit_flags,   // dec_emit_flags8 (see flag map in SPEC)

    // ---- protocol select (for the data-only predicate + addr symbol) ----
    input  wire        sel_i2c,
    input  wire        sel_spi,
    input  wire        sel_eth,
    input  wire        eth_sfd,      // eth_sfd_seen raw pulse (ETH frame start)
    input  wire        i2c_start,    // i2c_decode START pulse (transaction begin)
    input  wire        i2c_stop,     // i2c_decode STOP  pulse (transaction end)

    // ---- configuration ----
    input  wire        trig_en,      // dec_trigen (dec_cfg[8])
    input  wire [1:0]  trig_mode,    // dec_cfg[13:12]  0=byte 1=err 2=seq 3=addr
    input  wire [1:0]  seqlen_cfg,   // dec_cfg[15:14]  N = seqlen_cfg+1
    input  wire [7:0]  match_pattern,// dec_match[7:0]
    input  wire [7:0]  match_mask,   // dec_match[15:8]
    input  wire [7:0]  seq_b1,       // dec_tg[7:0]   (mode 2 seq[1])
    input  wire [7:0]  seq_b2,       // dec_tg[15:8]  (mode 2 seq[2])
    input  wire [15:0] adj_win,      // mode-2 max adjacent start-idx gap (cols)

    // ---- legacy per-module comparator (mode-0 pass-through, UNTOUCHED) ----
    input  wire        legacy_trig,          // dec_trig_pulse
    input  wire        legacy_matched,       // dec_matched_sticky
    input  wire [7:0]  legacy_matched_byte,  // dec_matched_byte

    // ---- outputs (same shape as the current inline trigger) ----
    output wire        decode_trig,  // 1-clk pulse into capture.v
    output wire        matched,      // sticky status (dec_status[14])
    output wire [7:0]  matched_byte  // 0x6c readback (dec_matched)
);

    // =====================================================================
    // per-proto DATA-ONLY predicate (mirrors mode-0 gating / serialtrig)
    //   I2C : data == KIND==0        (flags[1]==0)
    //   ETH : data == non-FCS octet  (flags[5]==0)
    //   UART/SPI : flags[1:0]==0 (SPI flags are always 0)
    // =====================================================================
    wire data_only = sel_i2c ? ~emit_flags[1]
                   : sel_eth ? ~emit_flags[5]
                             : (emit_flags[1:0] == 2'b00);

    // effective mode gates
    wire m_byte = (trig_mode == 2'd0);
    wire m_err  = (trig_mode == 2'd1);
    wire m_seq  = (trig_mode == 2'd2);
    wire m_addr = (trig_mode == 2'd3);

    // =====================================================================
    // MODE 1 — ERROR (flag-mask). Any emit whose flags intersect err_mask.
    // =====================================================================
    wire mode1_trig = m_err & emit_stb & trig_en & (|(emit_flags & match_mask));

    // =====================================================================
    // MODE 2 — SEQUENCE (2..4 contiguous DATA bytes)
    //   4-deep {byte,idx} shift history, pushed on (emit_stb & data_only).
    //   The current data byte (emit_byte/emit_idx) is compared as the NEWEST
    //   element WITH the top of history, so the trigger fires in the SAME cycle
    //   as the last (anchoring) byte's emit.
    // =====================================================================
    reg  [7:0]  hb0, hb1, hb2;      // history bytes (hb0 = most-recent PAST byte)
    reg  [23:0] hi0, hi1, hi2;      // history start indices
    reg  [2:0]  fill;               // # DATA bytes pushed, saturating at 4

    wire        push = en & emit_stb & data_only;

    // sequence bytes in transmit order: seqv0 first .. seqv3 last
    wire [7:0] seqv0 = match_pattern;
    wire [7:0] seqv1 = seq_b1;
    wire [7:0] seqv2 = seq_b2;
    wire [7:0] seqv3 = match_mask;
    wire [1:0] Nsel  = seqlen_cfg;                 // N-1 (0..3 -> N 1..4)

    // post-push view (this data byte is the newest): n0 newest .. n3 oldest
    wire [7:0]  n0 = emit_byte;  wire [23:0] j0 = emit_idx;
    wire [7:0]  n1 = hb0;        wire [23:0] j1 = hi0;
    wire [7:0]  n2 = hb1;        wire [23:0] j2 = hi1;
    wire [7:0]  n3 = hb2;        wire [23:0] j3 = hi2;
    // # valid DATA bytes INCLUDING this one (capped at 4)
    wire [2:0]  fillp = (fill >= 3'd4) ? 3'd4 : (fill + 3'd1);

    // adjacency: consecutive start-idx gaps must be <= adj_win (newest-older)
    wire [23:0] winx = {8'd0, adj_win};
    wire adj01 = ((j0 - j1) <= winx);
    wire adj12 = ((j1 - j2) <= winx);
    wire adj23 = ((j2 - j3) <= winx);

    reg seq_hit;
    always @* begin
        case (Nsel)
            2'd1: seq_hit = (fillp >= 3'd2) & (n0==seqv1) & (n1==seqv0) & adj01;                    // N=2
            2'd2: seq_hit = (fillp >= 3'd3) & (n0==seqv2) & (n1==seqv1) & (n2==seqv0) & adj01 & adj12; // N=3
            2'd3: seq_hit = (fillp >= 3'd4) & (n0==seqv3) & (n1==seqv2) & (n2==seqv1) & (n3==seqv0) & adj01 & adj12 & adj23; // N=4
            default: seq_hit = (fillp >= 3'd1) & (n0==seqv0);                                       // N=1 (degenerate)
        endcase
    end
    wire mode2_trig = m_seq & emit_stb & data_only & trig_en & seq_hit;

    always @(posedge clk) begin
        if (rst | ~en) begin
            hb0 <= 8'd0; hb1 <= 8'd0; hb2 <= 8'd0;
            hi0 <= 24'd0; hi1 <= 24'd0; hi2 <= 24'd0;
            fill <= 3'd0;
        end else if (push) begin
            hb2 <= hb1; hb1 <= hb0; hb0 <= emit_byte;
            hi2 <= hi1; hi1 <= hi0; hi0 <= emit_idx;
            if (fill < 3'd4) fill <= fill + 3'd1;
        end
    end

    // =====================================================================
    // MODE 3 — ADDR/FIELD  (+ optional I2C in-transaction DATA sequence)
    //   I2C addr-only (i2c_seq_n==0): compare {addr7,rw} on the ADDRESS symbol
    //     (flags[1]==1) — byte-for-byte identical to the legacy mode-3 path.
    //   I2C addr+SEQUENCE (i2c_seq_n>0): the address symbol only ARMS (it does
    //     NOT fire); the trigger then requires a CONTIGUOUS data-byte sequence of
    //     length i2c_seq_n ({seq_b1[,seq_b2]}) WITHIN THE SAME transaction —
    //     bounded by i2c_start / i2c_stop / the next address — mirroring
    //     serialtrig.matchI2C (addr+RW recovery via addr_mask bit0, data-only
    //     counting, adjacency = consecutive data bytes in the transaction).
    //   ETH : fire on SFD (== the current ETH trigger; strict superset).
    //
    //   i2c_seq_n REUSES the mode-3-FREE seqlen_cfg field (dec_cfg[15:14]; only
    //   mode 2 used it): 0 => addr-only, 1 => addr + 1 data byte (== seq_b1),
    //   2/3 => addr + 2 contiguous data bytes {seq_b1,seq_b2}.  seqlen_cfg==0
    //   keeps the addr-only path byte-identical to today.  NO new selector:
    //   TESTGEN 0x68 supplies the data bytes, MATCH 0x48 stays addr_field/
    //   addr_mask.  APP CONTRACT: when arming addr-only, write seqlen_cfg=0.
    // =====================================================================
    wire [1:0] i2c_seq_n = seqlen_cfg;                 // 0 => addr-only (legacy)
    wire addr_hit = ((emit_byte & match_mask) == (match_pattern & match_mask));
    wire i2c_addr_ev = m_addr & sel_i2c & emit_stb &  emit_flags[1]; // ADDRESS symbol
    wire i2c_data_ev = m_addr & sel_i2c & emit_stb & ~emit_flags[1]; // DATA symbol

    // per-transaction arm + 1-deep data history (window reset on start/stop/addr)
    reg        addr_matched;   // this transaction's address matched (armed)
    reg [7:0]  sd_prev;        // most-recent data byte in this transaction
    reg [1:0]  dcount;         // # data bytes seen since arm (saturates at 2)

    // contiguous data-sequence hit evaluated AT the current data byte
    wire seq_n1_hit = (emit_byte == seq_b1);
    wire seq_n2_hit = (dcount >= 2'd1) & (sd_prev == seq_b1) & (emit_byte == seq_b2);
    wire i2c_seq_hit = (i2c_seq_n == 2'd1) ? seq_n1_hit : seq_n2_hit; // n>=2 -> 2-byte

    wire mode3_i2c_addronly = i2c_addr_ev & trig_en & (i2c_seq_n == 2'd0) & addr_hit;
    wire mode3_i2c_seq      = i2c_data_ev & trig_en & (i2c_seq_n != 2'd0)
                              & addr_matched & i2c_seq_hit;
    wire mode3_i2c  = mode3_i2c_addronly | mode3_i2c_seq;
    wire mode3_eth  = m_addr & sel_eth & trig_en & eth_sfd;
    wire mode3_trig = mode3_i2c | mode3_eth;

    // transaction tracker.  START/STOP (and each new address) reset the data
    // window; the address symbol (re)arms on a match.  A coincident STOP/START
    // flush-emit still fires COMBINATIONALLY off the pre-reset state (the flushed
    // byte belongs to the ending txn), then the boundary reset wins in the
    // register so a stale byte can never bridge two transactions.
    always @(posedge clk) begin
        if (rst | ~en) begin
            addr_matched <= 1'b0; sd_prev <= 8'd0; dcount <= 2'd0;
        end else if (sel_i2c) begin
            if (i2c_start | i2c_stop) begin
                addr_matched <= 1'b0;
                dcount       <= 2'd0;
            end else if (i2c_addr_ev) begin
                addr_matched <= addr_hit;   // arm/disarm on this address
                dcount       <= 2'd0;       // fresh data window
            end else if (i2c_data_ev) begin
                sd_prev <= emit_byte;
                if (dcount < 2'd2) dcount <= dcount + 2'd1;
            end
        end
    end

    // =====================================================================
    // combine the active mode + sticky latch
    // =====================================================================
    wire mode_trig = mode1_trig | mode2_trig | mode3_trig;

    // matched_byte source for a mode-1/2/3 hit (the anchoring symbol value)
    wire [7:0] new_byte_now = emit_byte;

    reg        new_matched;
    reg [7:0]  new_matched_byte;
    always @(posedge clk) begin
        if (rst | ~en) begin
            new_matched      <= 1'b0;
            new_matched_byte <= 8'd0;
        end else if (mode_trig) begin
            new_matched      <= 1'b1;
            new_matched_byte <= new_byte_now;
        end
    end

    // decode_trig: mode-0 routes the UNTOUCHED per-module pulse; else mode_trig.
    // en=0 forces 0 so decode-off is byte-identical.
    assign decode_trig = en ? (m_byte ? legacy_trig : mode_trig) : 1'b0;

    // sticky STATUS + latched byte: mode-0 mirrors the per-module sticky exactly.
    assign matched      = m_byte ? legacy_matched      : new_matched;
    assign matched_byte = m_byte ? legacy_matched_byte : new_matched_byte;

endmodule

`default_nettype wire

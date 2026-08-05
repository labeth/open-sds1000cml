// tb_dec_trigger.v — self-checking testbench for dec_trigger.v (all 4 modes).
//
// Drives the SHARED decode symbol stream directly (the module consumes the
// post-mux emit bus), so every mode — including the 2..4 byte SEQUENCE that the
// on-chip loopback test-gen cannot self-generate — is fully verifiable in
// iverilog. Each mode checks a HIT (decode_trig + matched fire) and a MISS
// (decode_trig must NOT fire), per the rigorous-honesty requirement.
//
// NOTE: this proves the trigger ENGINE logic. Real-bench proof (a live UART/I2C
// error / address / multi-byte stream) is a separate gated scope Verify.

`timescale 1ns/1ps
`default_nettype none

module tb_dec_trigger;
    reg         clk = 0;
    reg         rst = 0;
    reg         en  = 0;
    reg         emit_stb = 0;
    reg  [7:0]  emit_byte = 0;
    reg  [23:0] emit_idx  = 0;
    reg  [7:0]  emit_flags = 0;
    reg         sel_i2c = 0, sel_spi = 0, sel_eth = 0;
    reg         eth_sfd = 0;
    reg         trig_en = 0;
    reg  [1:0]  trig_mode = 0;
    reg  [1:0]  seqlen_cfg = 0;
    reg  [7:0]  match_pattern = 0;
    reg  [7:0]  match_mask = 0;
    reg  [7:0]  seq_b1 = 0, seq_b2 = 0;
    reg  [15:0] adj_win = 0;
    reg         legacy_trig = 0;
    reg         legacy_matched = 0;
    reg  [7:0]  legacy_matched_byte = 0;

    wire        decode_trig;
    wire        matched;
    wire [7:0]  matched_byte;

    dec_trigger dut (
        .clk(clk), .rst(rst), .en(en),
        .emit_stb(emit_stb), .emit_byte(emit_byte), .emit_idx(emit_idx),
        .emit_flags(emit_flags),
        .sel_i2c(sel_i2c), .sel_spi(sel_spi), .sel_eth(sel_eth), .eth_sfd(eth_sfd),
        .trig_en(trig_en), .trig_mode(trig_mode), .seqlen_cfg(seqlen_cfg),
        .match_pattern(match_pattern), .match_mask(match_mask),
        .seq_b1(seq_b1), .seq_b2(seq_b2), .adj_win(adj_win),
        .legacy_trig(legacy_trig), .legacy_matched(legacy_matched),
        .legacy_matched_byte(legacy_matched_byte),
        .decode_trig(decode_trig), .matched(matched), .matched_byte(matched_byte)
    );

    always #5 clk = ~clk;   // 100 MHz sim clock

    integer errors = 0;
    integer trig_seen;

    // Pulse one emit (byte,idx,flags) for exactly one clock; observe decode_trig
    // combinationally during the high phase. Returns via global trig_seen.
    task emit1(input [7:0] b, input [23:0] ix, input [7:0] fl);
        begin
            @(negedge clk);
            emit_byte  = b;
            emit_idx   = ix;
            emit_flags = fl;
            emit_stb   = 1'b1;
            #1 trig_seen = decode_trig;     // sample mid-high (combinational)
            @(negedge clk);
            emit_stb   = 1'b0;
            emit_flags = 8'd0;
        end
    endtask

    // Pulse an ETH SFD side-strobe for one clock.
    task pulse_sfd;
        begin
            @(negedge clk);
            eth_sfd = 1'b1;
            #1 trig_seen = decode_trig;
            @(negedge clk);
            eth_sfd = 1'b0;
        end
    endtask

    task expect_trig(input exp, input [127:0] name);
        begin
            if (trig_seen !== exp) begin
                $display("FAIL [%0s]: decode_trig=%b expected=%b", name, trig_seen, exp);
                errors = errors + 1;
            end else
                $display("  ok  [%0s]: decode_trig=%b", name, trig_seen);
        end
    endtask

    task expect_matched(input exp, input [127:0] name);
        begin
            if (matched !== exp) begin
                $display("FAIL [%0s]: matched=%b expected=%b", name, matched, exp);
                errors = errors + 1;
            end else
                $display("  ok  [%0s]: matched=%b", name, matched);
        end
    endtask

    task do_reset;
        begin
            @(negedge clk); rst = 1'b1; @(negedge clk); rst = 1'b0;
        end
    endtask

    initial begin
        // ------------------------------------------------------------------
        // MODE 0 — BYTE (pass-through of the legacy per-module pulse)
        // ------------------------------------------------------------------
        en = 1; trig_en = 1; trig_mode = 2'd0;
        do_reset;
        // legacy pulse must pass through verbatim
        @(negedge clk); legacy_trig = 1'b1; #1 trig_seen = decode_trig;
        expect_trig(1'b1, "mode0 legacy hit");
        @(negedge clk); legacy_trig = 1'b0;
        // legacy sticky/byte must mux through in mode 0
        legacy_matched = 1'b1; legacy_matched_byte = 8'h5A;
        @(negedge clk);
        if (matched !== 1'b1 || matched_byte !== 8'h5A) begin
            $display("FAIL [mode0 sticky mux]"); errors = errors + 1;
        end else $display("  ok  [mode0 sticky mux] byte=%02x", matched_byte);
        // an emit with an error flag must NOT trigger in mode 0 (mode1 logic off)
        emit1(8'hFF, 24'd10, 8'h02);
        expect_trig(1'b0, "mode0 ignores error flag");
        legacy_matched = 1'b0; legacy_matched_byte = 8'd0;

        // ------------------------------------------------------------------
        // MODE 1 — ERROR (flag-mask). err_mask reuses match_mask (MATCH[15:8]).
        // ------------------------------------------------------------------
        sel_i2c = 0; sel_spi = 0; sel_eth = 0;   // UART context
        trig_mode = 2'd1; match_mask = 8'h02;    // frame_err bit
        do_reset;
        emit1(8'h41, 24'd20, 8'h00);             // clean data -> no trig
        expect_trig(1'b0, "mode1 clean miss");
        emit1(8'h41, 24'd21, 8'h02);             // frame_err -> trig
        expect_trig(1'b1, "mode1 frame_err hit");
        expect_matched(1'b1, "mode1 sticky set");
        // parity-only frame with a frame-err-only mask must NOT fire
        do_reset;
        match_mask = 8'h02;
        emit1(8'h41, 24'd22, 8'h01);             // parity_err only
        expect_trig(1'b0, "mode1 parity miss (mask=frame)");
        // widen mask to catch parity too
        match_mask = 8'h03;
        emit1(8'h41, 24'd23, 8'h01);
        expect_trig(1'b1, "mode1 parity hit (mask=both)");
        // I2C NAK error (flags[0]) with mask 0x01
        sel_i2c = 1; do_reset; match_mask = 8'h01;
        emit1(8'hA5, 24'd24, 8'h01);             // data byte, NAK
        expect_trig(1'b1, "mode1 i2c NAK hit");
        emit1(8'hA5, 24'd25, 8'h00);             // data byte, ACK
        expect_trig(1'b0, "mode1 i2c ACK miss");
        sel_i2c = 0;
        // SPI: mode 1 is a no-op (flags always 0)
        sel_spi = 1; do_reset; match_mask = 8'hFF;
        emit1(8'h3C, 24'd26, 8'h00);
        expect_trig(1'b0, "mode1 spi no-op");
        sel_spi = 0;

        // ------------------------------------------------------------------
        // MODE 2 — SEQUENCE. seq = AA BB CC (N=3), UART data-only.
        //   seqv0=match_pattern, seqv1=seq_b1, seqv2=seq_b2 (seqv3=match_mask)
        // ------------------------------------------------------------------
        trig_mode = 2'd2; seqlen_cfg = 2'd2;     // N = seqlen_cfg+1 = 3
        match_pattern = 8'hAA; seq_b1 = 8'hBB; seq_b2 = 8'hCC;
        adj_win = 16'd8;                          // ~byte width; gaps >8 break it
        do_reset;
        // contiguous hit: AA@100 BB@105 CC@110 (gaps 5 <= 8)
        emit1(8'hAA, 24'd100, 8'h00); expect_trig(1'b0, "seq A (partial)");
        emit1(8'hBB, 24'd105, 8'h00); expect_trig(1'b0, "seq B (partial)");
        emit1(8'hCC, 24'd110, 8'h00); expect_trig(1'b1, "seq ABC hit");
        expect_matched(1'b1, "seq sticky set");
        if (matched_byte !== 8'hCC) begin
            $display("FAIL [seq matched_byte] %02x != CC", matched_byte); errors=errors+1;
        end else $display("  ok  [seq matched_byte]=%02x", matched_byte);
        // near-miss content: AA BB CD -> must NOT fire
        do_reset;
        emit1(8'hAA, 24'd200, 8'h00);
        emit1(8'hBB, 24'd205, 8'h00);
        emit1(8'hCD, 24'd210, 8'h00); expect_trig(1'b0, "seq near-miss content");
        // adjacency miss: AA BB then CC far away (gap 90 > 8) -> must NOT fire
        do_reset;
        emit1(8'hAA, 24'd300, 8'h00);
        emit1(8'hBB, 24'd305, 8'h00);
        emit1(8'hCC, 24'd395, 8'h00); expect_trig(1'b0, "seq adjacency miss");
        // marker between B and C: a non-data symbol is NOT pushed, but it consumed
        // wire time so the AA BB CC start-gap widens -> adjacency rejects.
        do_reset;
        emit1(8'hAA, 24'd400, 8'h00);
        emit1(8'hBB, 24'd405, 8'h00);
        emit1(8'hFF, 24'd410, 8'h02);            // frame-error frame (non-data)
        emit1(8'hCC, 24'd420, 8'h00);            // gap 420-405=15 > 8 -> reject
        expect_trig(1'b0, "seq marker-between miss");
        // N=2 sequence hit: seqv0=AA seqv1=BB
        do_reset; seqlen_cfg = 2'd1;             // N=2
        emit1(8'hAA, 24'd500, 8'h00);
        emit1(8'hBB, 24'd505, 8'h00); expect_trig(1'b1, "seq N2 hit");
        // N=4 sequence hit: AA BB CC DD (seqv3=match_mask=DD)
        do_reset; seqlen_cfg = 2'd3; match_mask = 8'hDD;
        emit1(8'hAA, 24'd600, 8'h00);
        emit1(8'hBB, 24'd605, 8'h00);
        emit1(8'hCC, 24'd610, 8'h00);
        emit1(8'hDD, 24'd615, 8'h00); expect_trig(1'b1, "seq N4 hit");

        // ------------------------------------------------------------------
        // MODE 3 — ADDR/FIELD.
        //   I2C address symbol emit_byte={addr7,rw}; flags[1]=1 (KIND=addr).
        // ------------------------------------------------------------------
        trig_mode = 2'd3; sel_i2c = 1;
        // addr 0x24 W => byte 0x48 ; exact match, mask 0xFF
        match_pattern = 8'h48; match_mask = 8'hFF;
        do_reset;
        emit1(8'h48, 24'd700, 8'h02);            // address symbol (flags[1]=1)
        expect_trig(1'b1, "mode3 i2c addr hit");
        expect_matched(1'b1, "mode3 sticky set");
        // same value but DATA symbol (flags[1]=0) must NOT fire (addr-only)
        do_reset;
        emit1(8'h48, 24'd701, 8'h00);
        expect_trig(1'b0, "mode3 i2c data-symbol miss");
        // wrong address must NOT fire
        do_reset;
        emit1(8'h4A, 24'd702, 8'h02);
        expect_trig(1'b0, "mode3 i2c wrong-addr miss");
        // RW-any: mask bit0 cleared (0xFE) -> 0x48 (W) and 0x49 (R) both hit
        do_reset; match_mask = 8'hFE; match_pattern = 8'h48;
        emit1(8'h49, 24'd703, 8'h02);            // addr 0x24 R
        expect_trig(1'b1, "mode3 i2c rw-any hit");
        // Addr-any but RW=R required: mask=0x01, pattern bit0=1
        do_reset; match_mask = 8'h01; match_pattern = 8'h01;
        emit1(8'h48, 24'd704, 8'h02);            // W -> miss
        expect_trig(1'b0, "mode3 i2c rw=R miss on W");
        emit1(8'h49, 24'd705, 8'h02);            // R -> hit
        expect_trig(1'b1, "mode3 i2c rw=R hit on R");
        sel_i2c = 0;
        // ETH mode 3: fire on SFD (superset of current ETH trigger)
        sel_eth = 1; do_reset;
        pulse_sfd; expect_trig(1'b1, "mode3 eth sfd hit");
        sel_eth = 0;

        // ------------------------------------------------------------------
        // DISARM: en=0 forces decode_trig 0 and clears sticky (byte-identical off)
        // ------------------------------------------------------------------
        trig_mode = 2'd1; match_mask = 8'hFF; sel_i2c = 1;
        do_reset;
        emit1(8'hA5, 24'd800, 8'h01); expect_trig(1'b1, "pre-disarm hit");
        expect_matched(1'b1, "pre-disarm sticky");
        en = 0;
        @(negedge clk);
        expect_matched(1'b0, "disarm clears sticky");
        emit1(8'hA5, 24'd801, 8'h01); expect_trig(1'b0, "disarm blocks trig");
        en = 1;

        // ------------------------------------------------------------------
        if (errors == 0)
            $display("\n== tb_dec_trigger: ALL CHECKS PASSED ==");
        else
            $display("\n== tb_dec_trigger: %0d FAILURE(S) ==", errors);
        if (errors != 0) $fatal(1, "dec_trigger testbench failed");
        $finish;
    end

    // watchdog
    initial begin
        #200000 $display("FAIL: timeout"); $fatal(1, "timeout");
    end
endmodule

`default_nettype wire

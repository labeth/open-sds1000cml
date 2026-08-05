// tb_i2c_addr_seq.v — FULL-CHAIN self-checking TB for ITEM 5:
//   COMBINED I2C ADDRESS + IN-TRANSACTION DATA SEQUENCE trigger.
//
// Wires the EDITED i2c_decode (START/STOP export) into the EDITED dec_trigger
// exactly as acq.v does, and drives RAW I2C waveforms (column by column, tg_en=0)
// so the whole path — decode FSM, START/STOP markers, transaction scoping, and
// the mode-3 combined trigger — is exercised end to end.
//
// It proves, against serialtrig.matchI2C semantics:
//   * addr+seq fires ONLY when BOTH the address and the contiguous data sequence
//     land inside ONE START..STOP transaction;
//   * addr-only (seqlen_cfg==0) still fires on the ADDRESS symbol, byte-identical
//     to today, and a data byte equal to the addr value does NOT fire;
//   * a matching address with the sequence in a DIFFERENT transaction does NOT
//     fire (cross-transaction / split-sequence isolation);
//   * contiguity: a non-consecutive data pair does NOT fire.

`timescale 1ns/1ps
`default_nettype none

module tb_i2c_addr_seq;
    reg         clk = 0;
    reg         rst = 0;      // dec_trigger op_reset
    reg         en  = 0;      // dec_en (drives BOTH i2c_decode.en and dec_trigger.en)
    reg         cap_tick = 0;
    reg  [7:0]  scl_code = 8'hFF, sda_code = 8'hFF;

    // config
    reg  [1:0]  trig_mode = 2'd3;
    reg  [1:0]  seqlen_cfg = 2'd0;
    reg  [7:0]  match_pattern = 8'h00; // mode3: addr_field
    reg  [7:0]  match_mask    = 8'hFF; // mode3: addr_mask
    reg  [7:0]  seq_b1 = 8'h00, seq_b2 = 8'h00;

    always #5 clk = ~clk;   // 100 MHz

    // ---- i2c_decode (edited: exports i2c_start_stb / i2c_stop_stb) ----
    wire        i2c_emit_stb;
    wire [7:0]  i2c_emit_byte;
    wire [23:0] i2c_emit_idx;
    wire [1:0]  i2c_emit_flags;
    wire        i2c_trig, i2c_matched;
    wire [7:0]  i2c_matched_byte;
    wire        i2c_start_stb, i2c_stop_stb;

    i2c_decode u_i2c (
        .clk(clk), .rst_n(1'b1), .cap_tick(cap_tick),
        .scl_code(scl_code), .sda_code(sda_code),
        .en(en), .scl_thr(8'h80), .sda_thr(8'h80),
        .tg_en(1'b0),
        .trig_en(1'b1),
        .match_pattern(match_pattern), .match_mask(match_mask),
        .emit_stb(i2c_emit_stb), .emit_byte(i2c_emit_byte), .emit_idx(i2c_emit_idx),
        .emit_flags(i2c_emit_flags),
        .decode_trig(i2c_trig), .matched(i2c_matched), .matched_byte(i2c_matched_byte),
        .i2c_start_stb(i2c_start_stb), .i2c_stop_stb(i2c_stop_stb)
    );

    // ---- shared-bus wiring, mirroring acq.v for the I2C-active case ----
    wire [7:0]  dec_emit_flags8 = {6'd0, i2c_emit_flags};

    wire        decode_trig, matched;
    wire [7:0]  matched_byte;

    dec_trigger dut (
        .clk(clk), .rst(rst), .en(en),
        .emit_stb(i2c_emit_stb), .emit_byte(i2c_emit_byte), .emit_idx(i2c_emit_idx),
        .emit_flags(dec_emit_flags8),
        .sel_i2c(1'b1), .sel_spi(1'b0), .sel_eth(1'b0), .eth_sfd(1'b0),
        .i2c_start(i2c_start_stb), .i2c_stop(i2c_stop_stb),
        .trig_en(1'b1), .trig_mode(trig_mode), .seqlen_cfg(seqlen_cfg),
        .match_pattern(match_pattern), .match_mask(match_mask),
        .seq_b1(seq_b1), .seq_b2(seq_b2), .adj_win(16'd0),
        .legacy_trig(i2c_trig), .legacy_matched(i2c_matched),
        .legacy_matched_byte(i2c_matched_byte),
        .decode_trig(decode_trig), .matched(matched), .matched_byte(matched_byte)
    );

    // ---- observation: count decode_trig pulses per scenario ----
    integer trig_cnt = 0;
    reg     cnt_clr = 0;
    always @(posedge clk) begin
        if (cnt_clr) trig_cnt <= 0;
        else if (decode_trig) trig_cnt <= trig_cnt + 1;
    end

    integer errors = 0;

    // ------------------------------------------------------------------
    // column driver: one decimated column (cap_tick) with given SCL/SDA levels
    // ------------------------------------------------------------------
    task col(input scl, input sda);
        begin
            @(negedge clk);
            scl_code = scl ? 8'hFF : 8'h00;
            sda_code = sda ? 8'hFF : 8'h00;
            cap_tick = 1'b1;
            @(negedge clk);
            cap_tick = 1'b0;
            @(negedge clk);   // emit window: dec_trigger registers here
            @(negedge clk);   // settle
        end
    endtask

    task i2c_start_seq;   // generate a (repeated) START
        begin
            col(1,1);   // idle high => prev_sda=1
            col(1,0);   // SDA falls while SCL high => START
            col(0,0);   // SCL low
        end
    endtask

    task i2c_byte(input [7:0] b, input ackbit);  // MSB-first + 1 ACK clock
        integer k;
        begin
            for (k=7; k>=0; k=k-1) begin
                col(1'b0, b[k]);  // SCL low: setup SDA
                col(1'b1, b[k]);  // SCL rising: sample
            end
            col(1'b0, ackbit);    // ACK setup
            col(1'b1, ackbit);    // ACK clock (9th) => emit
        end
    endtask

    task i2c_stop_seq;   // generate STOP
        begin
            col(0,0);   // SCL low, SDA low
            col(1,0);   // SCL high, SDA low => prev_sda=0
            col(1,1);   // SDA rises while SCL high => STOP
            col(1,1);   // idle
        end
    endtask

    // scenario reset: clear both cores + the pulse counter, re-prime the FSM
    task scen_reset;
        begin
            en = 1'b0;
            @(negedge clk); rst = 1'b1; cnt_clr = 1'b1;
            @(negedge clk); rst = 1'b0; cnt_clr = 1'b0;
            en = 1'b1;
            col(1,1); col(1,1);   // 2 idle columns prime prev_scl/prev_sda=1
        end
    endtask

    task check_cnt(input integer got, input integer exp, input [255:0] name);
        begin
            if (got !== exp) begin
                $display("FAIL [%0s]: trig pulses=%0d expected=%0d (matched=%b byte=%02x)",
                         name, got, exp, matched, matched_byte);
                errors = errors + 1;
            end else
                $display("  ok  [%0s]: trig pulses=%0d matched=%b byte=%02x",
                         name, got, matched, matched_byte);
        end
    endtask

    task check_byte(input [7:0] got, input [7:0] exp, input [255:0] name);
        begin
            if (got !== exp) begin
                $display("FAIL [%0s]: matched_byte=%02x expected=%02x", name, got, exp);
                errors = errors + 1;
            end else
                $display("  ok  [%0s]: matched_byte=%02x", name, got);
        end
    endtask

    initial begin
        // ==============================================================
        // TEST 1 — addr+seq fires when BOTH match in one transaction
        //   arm: addr 0x48 (0x24 W) exact, seq {0xA5,0x5A} (N=2)
        // ==============================================================
        trig_mode = 2'd3; seqlen_cfg = 2'd2;
        match_pattern = 8'h48; match_mask = 8'hFF;
        seq_b1 = 8'hA5; seq_b2 = 8'h5A;
        scen_reset;
        i2c_start_seq;
        i2c_byte(8'h48, 1'b0);   // ADDRESS 0x24 W  (KIND=1)
        i2c_byte(8'hA5, 1'b0);   // DATA
        i2c_byte(8'h5A, 1'b0);   // DATA  -> completes {A5,5A}
        i2c_stop_seq;
        check_cnt(trig_cnt, 1, "T1 addr+seq both match -> fire once");
        check_byte(matched_byte, 8'h5A, "T1 matched_byte = last seq byte");

        // TEST 1b — same addr, data present but WRONG (no 0x5A after 0xA5)
        scen_reset;
        i2c_start_seq;
        i2c_byte(8'h48, 1'b0);
        i2c_byte(8'hA5, 1'b0);
        i2c_byte(8'h99, 1'b0);   // A5 then 99 -> not the seq
        i2c_stop_seq;
        check_cnt(trig_cnt, 0, "T1b addr match, seq absent -> no fire");

        // TEST 1c — sliding window: intervening byte before the seq still fires
        scen_reset;
        i2c_start_seq;
        i2c_byte(8'h48, 1'b0);
        i2c_byte(8'h00, 1'b0);   // filler
        i2c_byte(8'hA5, 1'b0);
        i2c_byte(8'h5A, 1'b0);   // A5,5A consecutive -> fire
        i2c_stop_seq;
        check_cnt(trig_cnt, 1, "T1c seq after filler -> fire once");
        check_byte(matched_byte, 8'h5A, "T1c matched_byte");

        // TEST 1d — contiguity: A5, filler, 5A are NOT consecutive -> no fire
        scen_reset;
        i2c_start_seq;
        i2c_byte(8'h48, 1'b0);
        i2c_byte(8'hA5, 1'b0);
        i2c_byte(8'h00, 1'b0);   // breaks adjacency
        i2c_byte(8'h5A, 1'b0);
        i2c_stop_seq;
        check_cnt(trig_cnt, 0, "T1d non-consecutive seq -> no fire");

        // ==============================================================
        // TEST 2 — addr-only (seqlen_cfg==0) fires on the ADDRESS symbol
        // ==============================================================
        seqlen_cfg = 2'd0;
        match_pattern = 8'h48; match_mask = 8'hFF;
        scen_reset;
        i2c_start_seq;
        i2c_byte(8'h48, 1'b0);   // ADDRESS matches -> fire here
        i2c_byte(8'hA5, 1'b0);
        i2c_stop_seq;
        check_cnt(trig_cnt, 1, "T2 addr-only -> fire once on address");
        check_byte(matched_byte, 8'h48, "T2 matched_byte = address");

        // TEST 2b — addr-only: a DATA byte equal to addr value must NOT fire
        //   arm addr 0x48; send addr 0x30 (no match) then data 0x48
        scen_reset;
        match_pattern = 8'h48; match_mask = 8'hFF;   // seqlen still 0
        i2c_start_seq;
        i2c_byte(8'h30, 1'b0);   // ADDRESS 0x18 W (!=0x48) -> no addr fire
        i2c_byte(8'h48, 1'b0);   // DATA 0x48 (KIND=0) -> addr-only must ignore
        i2c_stop_seq;
        check_cnt(trig_cnt, 0, "T2b addr-only ignores matching DATA byte");

        // TEST 2c — addr-only wrong address -> no fire
        scen_reset;
        match_pattern = 8'h48; match_mask = 8'hFF;
        i2c_start_seq;
        i2c_byte(8'h4A, 1'b0);   // wrong address
        i2c_byte(8'hA5, 1'b0);
        i2c_stop_seq;
        check_cnt(trig_cnt, 0, "T2c addr-only wrong addr -> no fire");

        // ==============================================================
        // TEST 3 — matching addr with the seq in a DIFFERENT transaction
        //   Txn A: addr MATCH (0x48), data 0x11        (no seq)
        //   Txn B: addr 0x98 (!=0x48), data 0xA5,0x5A  (seq, wrong addr)
        //   => must NEVER fire
        // ==============================================================
        seqlen_cfg = 2'd2; match_pattern = 8'h48; match_mask = 8'hFF;
        seq_b1 = 8'hA5; seq_b2 = 8'h5A;
        scen_reset;
        // Txn A
        i2c_start_seq;
        i2c_byte(8'h48, 1'b0);   // addr matches
        i2c_byte(8'h11, 1'b0);   // data, not the seq
        i2c_stop_seq;
        // Txn B
        i2c_start_seq;
        i2c_byte(8'h98, 1'b0);   // addr 0x4C W (!=0x48) -> disarmed
        i2c_byte(8'hA5, 1'b0);
        i2c_byte(8'h5A, 1'b0);   // seq present but not armed
        i2c_stop_seq;
        check_cnt(trig_cnt, 0, "T3 addr & seq in DIFFERENT txns -> no fire");

        // ==============================================================
        // TEST 4 — split sequence across two matching-addr transactions
        //   Txn A: addr 0x48, data 0xA5
        //   Txn B: addr 0x48, data 0x5A
        //   {A5,5A} split by START/STOP -> must NOT fire
        // ==============================================================
        seqlen_cfg = 2'd2; match_pattern = 8'h48; match_mask = 8'hFF;
        seq_b1 = 8'hA5; seq_b2 = 8'h5A;
        scen_reset;
        i2c_start_seq;
        i2c_byte(8'h48, 1'b0);
        i2c_byte(8'hA5, 1'b0);   // last data of txn A
        i2c_stop_seq;
        i2c_start_seq;
        i2c_byte(8'h48, 1'b0);   // re-arm (resets data window)
        i2c_byte(8'h5A, 1'b0);   // first data of txn B
        i2c_stop_seq;
        check_cnt(trig_cnt, 0, "T4 split seq across txns -> no fire");

        // ==============================================================
        // TEST 5 — combined N=1 (single data byte after addr)
        // ==============================================================
        seqlen_cfg = 2'd1; match_pattern = 8'h48; match_mask = 8'hFF;
        seq_b1 = 8'hA5;
        scen_reset;
        i2c_start_seq;
        i2c_byte(8'h48, 1'b0);
        i2c_byte(8'h11, 1'b0);   // not the byte
        i2c_byte(8'hA5, 1'b0);   // == seq_b1 -> fire
        i2c_stop_seq;
        check_cnt(trig_cnt, 1, "T5 N=1 combined -> fire once");
        check_byte(matched_byte, 8'hA5, "T5 matched_byte");

        // TEST 5b — combined N=1, wrong addr with the data byte -> no fire
        scen_reset;
        seqlen_cfg = 2'd1; match_pattern = 8'h48; match_mask = 8'hFF; seq_b1 = 8'hA5;
        i2c_start_seq;
        i2c_byte(8'h98, 1'b0);   // wrong addr
        i2c_byte(8'hA5, 1'b0);   // data present but not armed
        i2c_stop_seq;
        check_cnt(trig_cnt, 0, "T5b N=1 wrong addr -> no fire");

        // ==============================================================
        // TEST 6 — RW-any (mask bit0 cleared) combined: addr 0x24 R still arms
        //   arm addr_field 0x48, mask 0xFE (RW-any), seq {0xA5,0x5A}
        //   send address 0x49 (0x24 R) -> addr_hit -> seq fires
        // ==============================================================
        seqlen_cfg = 2'd2; match_pattern = 8'h48; match_mask = 8'hFE;
        seq_b1 = 8'hA5; seq_b2 = 8'h5A;
        scen_reset;
        i2c_start_seq;
        i2c_byte(8'h49, 1'b0);   // addr 0x24 R (RW=1) -> matches under 0xFE
        i2c_byte(8'hA5, 1'b0);
        i2c_byte(8'h5A, 1'b0);
        i2c_stop_seq;
        check_cnt(trig_cnt, 1, "T6 RW-any combined -> fire once");
        check_byte(matched_byte, 8'h5A, "T6 matched_byte");

        // ==============================================================
        if (errors == 0)
            $display("\n== tb_i2c_addr_seq: ALL CHECKS PASSED ==");
        else
            $display("\n== tb_i2c_addr_seq: %0d FAILURE(S) ==", errors);
        if (errors != 0) $fatal(1, "i2c addr+seq testbench failed");
        $finish;
    end

    // watchdog
    initial begin
        #5000000 $display("FAIL: timeout"); $fatal(1, "timeout");
    end
endmodule

`default_nettype wire

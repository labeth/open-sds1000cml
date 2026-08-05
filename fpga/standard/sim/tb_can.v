// tb_can.v — self-checking testbench for can_decode.v (ITEM 7).
//
// Feeds SAMPLED CAN waveforms derived from the app oracle vectors (built by the
// package's own canOracleWire/canOracleBits/bitsToCodes and dumped by
// app/internal/decode/canvec_gen_test.go) into can_decode.v, collects the
// DATA-only emitted bytes (emit_flags[1:0]==00), and compares them byte-for-byte
// to DecodeCANFD().Bytes (the *_gold.hex file). Also verifies the end-of-frame
// STATUS MARKER error flags on the corrupted-CRC and recessive-ACK vectors.
//
// The vector dir is passed via +vecdir=<abs path>. Exits non-zero on any FAIL.

`timescale 1ns/1ps
`default_nettype none

module tb_can;
    reg         clk = 1'b0;
    reg         rst_n = 1'b0;
    reg         cap_tick = 1'b0;
    reg  [7:0]  sample_code = 8'd200;   // recessive idle
    reg         en = 1'b0;
    reg  [7:0]  thr8 = 8'd128;
    reg  [23:0] spb = 24'd5120;
    reg         dom_low = 1'b1;
    reg         trig_en = 1'b0;
    reg  [7:0]  match_pattern = 8'd0;
    reg  [7:0]  match_mask = 8'd0;

    wire        emit_stb;
    wire [7:0]  emit_byte;
    wire [23:0] emit_idx;
    wire [7:0]  emit_flags;
    wire        decode_trig;
    wire        matched;
    wire [7:0]  matched_byte;

    can_decode dut (
        .clk(clk), .rst_n(rst_n), .cap_tick(cap_tick),
        .sample_code(sample_code), .en(en), .thr8(thr8), .spb(spb),
        .dom_low(dom_low), .trig_en(trig_en),
        .match_pattern(match_pattern), .match_mask(match_mask),
        .emit_stb(emit_stb), .emit_byte(emit_byte), .emit_idx(emit_idx),
        .emit_flags(emit_flags), .decode_trig(decode_trig),
        .matched(matched), .matched_byte(matched_byte)
    );

    always #5 clk = ~clk;

    // ---- storage ----
    reg [7:0]  codes [0:16383];
    reg [7:0]  gold  [0:255];
    reg [7:0]  got   [0:255];
    integer    ngot;
    reg        collecting;
    reg        seen_crcerr;   // OR of marker CRC-err bits
    reg        seen_ackerr;   // OR of marker ACK-err bits
    integer    nmark;

    reg [8*300-1:0] vecdir;   // +vecdir=<abs dir> (trailing slash)
    reg [8*400-1:0] fname;

    // ---- emit collector (1-cycle lag vs the driving edge; all pulses caught) ----
    always @(posedge clk) begin
        if (collecting && emit_stb) begin
            if (emit_flags[1:0] == 2'b00) begin
                got[ngot] = emit_byte;
                ngot = ngot + 1;
            end else begin
                nmark = nmark + 1;
                if (emit_flags[3]) seen_crcerr = 1'b1;
                if (emit_flags[2]) seen_ackerr = 1'b1;
            end
        end
    end

    integer errors = 0;

    task run_vec;
        input [8*32-1:0] name;
        input [23:0]     spb_q8;
        input integer    ncode;
        input integer    ngold;
        input            exp_crcerr;
        input            exp_ackerr;
        integer i, k, vecerr;
        begin
            vecerr = 0;
            // load code + gold files
            $sformat(fname, "%0s%0s_codes.hex", vecdir, name);
            $readmemh(fname, codes);
            $sformat(fname, "%0s%0s_gold.hex", vecdir, name);
            $readmemh(fname, gold);

            // reset + arm
            @(negedge clk);
            en = 1'b0; rst_n = 1'b0; cap_tick = 1'b0; sample_code = 8'd200;
            @(negedge clk); @(negedge clk);
            rst_n = 1'b1;
            spb = spb_q8; thr8 = 8'd128; dom_low = 1'b1;
            ngot = 0; nmark = 0; seen_crcerr = 1'b0; seen_ackerr = 1'b0;
            collecting = 1'b1;
            @(negedge clk);
            en = 1'b1;
            cap_tick = 1'b1;   // one decimated column per clk

            // stream the waveform
            for (k = 0; k < ncode; k = k + 1) begin
                @(negedge clk);
                sample_code = codes[k];
            end
            // trailing recessive flush so the final frame's ACK/marker completes
            for (k = 0; k < 600; k = k + 1) begin
                @(negedge clk);
                sample_code = 8'd200;
            end
            @(negedge clk);
            collecting = 1'b0;
            cap_tick = 1'b0;
            en = 1'b0;

            // ---- checks ----
            if (ngot !== ngold) begin
                $display("FAIL %0s: got %0d data bytes, want %0d", name, ngot, ngold);
                vecerr = vecerr + 1;
            end else begin
                for (i = 0; i < ngold; i = i + 1) begin
                    if (got[i] !== gold[i]) begin
                        $display("FAIL %0s: byte[%0d]=%02x want %02x",
                                 name, i, got[i], gold[i]);
                        vecerr = vecerr + 1;
                    end
                end
            end
            if (exp_crcerr && !seen_crcerr) begin
                $display("FAIL %0s: expected a CRC-error marker, none seen", name);
                vecerr = vecerr + 1;
            end
            if (!exp_crcerr && seen_crcerr) begin
                $display("FAIL %0s: unexpected CRC-error marker on clean traffic", name);
                vecerr = vecerr + 1;
            end
            if (exp_ackerr && !seen_ackerr) begin
                $display("FAIL %0s: expected an ACK-error (NAK) marker, none seen", name);
                vecerr = vecerr + 1;
            end
            if (!exp_ackerr && seen_ackerr) begin
                $display("FAIL %0s: unexpected ACK-error marker", name);
                vecerr = vecerr + 1;
            end
            errors = errors + vecerr;
            if (vecerr == 0)
                $display("PASS %0s: %0d/%0d bytes, %0d markers%0s%0s",
                         name, ngot, ngold, nmark,
                         seen_crcerr ? " [CRC-err]" : "",
                         seen_ackerr ? " [ACK-err]" : "");
        end
    endtask

    initial begin
        if (!$value$plusargs("vecdir=%s", vecdir)) begin
            $display("FATAL: pass +vecdir=<abs dir with trailing slash>");
            $finish;
        end

        run_vec("std_sweep",   24'd5120, 15239, 36, 1'b0, 1'b0);
        run_vec("ext_id",      24'd5120,  2600,  4, 1'b0, 1'b0);
        run_vec("rtr0",        24'd5120,  1520,  0, 1'b0, 1'b0);
        run_vec("rtr3",        24'd5120,  1500,  0, 1'b0, 1'b0);
        run_vec("stuffmax",    24'd5120,  7740, 24, 1'b0, 1'b0);
        run_vec("crc_corrupt", 24'd5120,  1860,  2, 1'b1, 1'b0);
        run_vec("b2b_frac",    24'd3512,  1961,  2, 1'b0, 1'b0);
        run_vec("nack",        24'd5120,  1860,  2, 1'b0, 1'b1);
        run_vec("multi3",      24'd5120,  4980,  9, 1'b0, 1'b0);
        run_vec("sparse",      24'd5120,  2840,  8, 1'b0, 1'b0);
        run_vec("dlc12",       24'd5120,  2900,  8, 1'b0, 1'b0);
        run_vec("fd_base",     24'd5120,  3500, 12, 1'b0, 1'b0);

        if (errors == 0)
            $display("ALL CAN VECTORS PASSED");
        else
            $display("CAN TB FAILED: %0d error(s)", errors);
        $finish;
    end
endmodule

`default_nettype wire

// tb_patterngen.v — self-checking TB proving dec_patterngen drives the REAL
// uart/i2c/spi decoders such that each injected fault / sequence / address makes
// the matching dec_trigger mode fire (matched=1 + correct matched_byte) and a
// clean / near-miss does NOT.
//
// The harness reproduces acq.v's decode section EXACTLY (per-engine enables, the
// emit-bus mux, the data_only predicate lives inside dec_trigger, adj_win =
// {dec_spb[17:8],6'b0}, and the mode-0 legacy pass-through). The generator is
// muxed AHEAD of the slicers by feeding its rail outputs into the decoders'
// sample_code / scl_code+sda_code / clk_code+data_code, with each decoder's OWN
// test-gen held off (.tg_en(1'b0)) — the same wiring the acq.v integration patch
// installs.
//
// Run:  iverilog -g2001 dec_patterngen.v uart_decode.v i2c_decode.v \
//                 spi_decode.v dec_trigger.v sim/tb_patterngen.v -o /tmp/x && vvp /tmp/x

`timescale 1ns/1ps
`default_nettype none

module tb_patterngen;
    // ---- clock / column tick (one column per clk for speed) ----
    reg clk = 0;
    always #5 clk = ~clk;           // 100 MHz sim clock
    reg cap_tick = 1'b0;

    integer errors = 0;

    // ---- shared decode config (mirrors acq.v dec_* wires) ----
    reg        dec_en    = 1'b0;
    reg        pg_on     = 1'b0;    // pattern-gen master enable (tg_en, CFG[9])
    reg  [1:0] proto     = 2'd0;    // dec_proto (CFG[11:10])
    reg  [7:0] thr8      = 8'h80;
    reg  [23:0] spb      = 24'h000800;  // 8.0 (Q16.8); adj_win = 8<<6 = 512
    reg  [3:0] bits_cfg  = 4'd0;    // => 8
    reg  [1:0] par_cfg   = 2'd1;    // even
    reg        cpol=1'b0, cpha=1'b0, msb=1'b1;
    reg        rstn      = 1'b1;    // decoder async reset (active low)
    reg        dtrst     = 1'b0;    // dec_trigger sync reset

    // ---- trigger config (dec_trigger inputs) ----
    reg        trig_en   = 1'b1;
    reg  [1:0] trig_mode = 2'd0;
    reg  [1:0] seqlen    = 2'd0;
    reg  [7:0] mpat      = 8'h00;
    reg  [7:0] mmask     = 8'h00;
    reg  [7:0] sb1       = 8'h00;
    reg  [7:0] sb2       = 8'h00;

    wire sel_uart = (proto==2'd0);
    wire sel_i2c  = (proto==2'd1);
    wire sel_spi  = (proto==2'd2);
    wire sel_eth  = (proto==2'd3);
    wire uart_en  = dec_en & sel_uart;
    wire i2c_en   = dec_en & sel_i2c;
    wire spi_en   = dec_en & sel_spi;
    wire [15:0] adj_win = {spb[17:8], 6'b0};

    // ================= pattern generator =================
    wire [7:0] pg_uart, pg_a, pg_b;
    dec_patterngen u_gen (
        .clk(clk), .cap_tick(cap_tick), .en(pg_on), .proto(proto), .spb(spb),
        .gen_uart_code(pg_uart), .gen_a_code(pg_a), .gen_b_code(pg_b)
    );

    // mux ahead of slicers (acq.v integration): when pg_on, decoders read the gen
    wire [7:0] dec_sample_code = pg_on ? pg_uart : 8'hFF;
    wire [7:0] dec_a           = pg_on ? pg_a    : 8'hFF;
    wire [7:0] dec_b           = pg_on ? pg_b    : 8'hFF;

    // ================= real decoders =================
    wire        uart_stb;  wire [7:0] uart_byte;  wire [23:0] uart_idx;
    wire [1:0]  uart_fl;   wire uart_trig, uart_matched;  wire [7:0] uart_mb;
    uart_decode u_uart (
        .clk(clk), .rst_n(rstn), .cap_tick(cap_tick),
        .sample_code(dec_sample_code),
        .en(uart_en), .thr8(thr8), .spb(spb), .bits_cfg(bits_cfg),
        .parity_cfg(par_cfg), .hyst_en(1'b0), .hyst_band(8'd0),
        .tg_en(1'b0), .tg_byte(8'd0),
        .trig_en(trig_en), .match_pattern(mpat), .match_mask(mmask),
        .emit_stb(uart_stb), .emit_byte(uart_byte), .emit_idx(uart_idx),
        .emit_flags(uart_fl), .decode_trig(uart_trig),
        .matched(uart_matched), .matched_byte(uart_mb)
    );

    wire        i2c_stb;   wire [7:0] i2c_byte;   wire [23:0] i2c_idx;
    wire [1:0]  i2c_fl;    wire i2c_trig, i2c_matched;    wire [7:0] i2c_mb;
    i2c_decode u_i2c (
        .clk(clk), .rst_n(rstn), .cap_tick(cap_tick),
        .scl_code(dec_a), .sda_code(dec_b),
        .en(i2c_en), .scl_thr(thr8), .sda_thr(thr8),
        .tg_en(1'b0),
        .trig_en(trig_en), .match_pattern(mpat), .match_mask(mmask),
        .emit_stb(i2c_stb), .emit_byte(i2c_byte), .emit_idx(i2c_idx),
        .emit_flags(i2c_fl), .decode_trig(i2c_trig),
        .matched(i2c_matched), .matched_byte(i2c_mb)
    );

    wire        spi_stb;   wire [7:0] spi_byte;   wire [23:0] spi_idx;
    wire [1:0]  spi_fl;    wire spi_trig, spi_matched;    wire [7:0] spi_mb;
    spi_decode u_spi (
        .clk(clk), .rst_n(rstn), .cap_tick(cap_tick),
        .clk_code(dec_a), .data_code(dec_b),
        .en(spi_en), .clk_thr(thr8), .data_thr(thr8),
        .cpol(cpol), .cpha(cpha), .msb(msb), .gapreset(spb),
        .tg_en(1'b0), .tg_word(8'd0),
        .trig_en(trig_en), .match_pattern(mpat), .match_mask(mmask),
        .emit_stb(spi_stb), .emit_byte(spi_byte), .emit_idx(spi_idx),
        .emit_flags(spi_fl), .decode_trig(spi_trig),
        .matched(spi_matched), .matched_byte(spi_mb)
    );

    // ================= emit-bus mux (acq.v) =================
    wire        dec_emit_stb   = sel_i2c ? i2c_stb  : sel_spi ? spi_stb  : uart_stb;
    wire [7:0]  dec_emit_byte  = sel_i2c ? i2c_byte : sel_spi ? spi_byte : uart_byte;
    wire [23:0] dec_emit_idx   = sel_i2c ? i2c_idx  : sel_spi ? spi_idx  : uart_idx;
    wire [1:0]  dec_emit_flags = sel_i2c ? i2c_fl   : sel_spi ? spi_fl   : uart_fl;
    wire [7:0]  dec_emit_flags8= {6'd0, dec_emit_flags};
    wire        dec_trig_pulse = sel_i2c ? i2c_trig    : sel_spi ? spi_trig    : uart_trig;
    wire        dec_matched_sk = sel_i2c ? i2c_matched : sel_spi ? spi_matched : uart_matched;
    wire [7:0]  dec_matched_by = sel_i2c ? i2c_mb      : sel_spi ? spi_mb      : uart_mb;

    // ================= dec_trigger (real) =================
    wire        dtrig;
    wire        dmatched;
    wire [7:0]  dmatched_byte;
    dec_trigger u_trig (
        .clk(clk), .rst(dtrst), .en(dec_en),
        .emit_stb(dec_emit_stb), .emit_byte(dec_emit_byte),
        .emit_idx(dec_emit_idx), .emit_flags(dec_emit_flags8),
        .sel_i2c(sel_i2c), .sel_spi(sel_spi), .sel_eth(sel_eth), .eth_sfd(1'b0),
        .trig_en(trig_en), .trig_mode(trig_mode), .seqlen_cfg(seqlen),
        .match_pattern(mpat), .match_mask(mmask), .seq_b1(sb1), .seq_b2(sb2),
        .adj_win(adj_win),
        .legacy_trig(dec_trig_pulse), .legacy_matched(dec_matched_sk),
        .legacy_matched_byte(dec_matched_by),
        .decode_trig(dtrig), .matched(dmatched), .matched_byte(dmatched_byte)
    );

    // ================= helpers =================
    task do_reset;   // clear decoder sticky + dec_trigger state + restart gen
        begin
            pg_on = 1'b0;
            @(posedge clk); cap_tick = 1'b0;
            rstn = 1'b0; dtrst = 1'b1;
            @(posedge clk);
            @(posedge clk);
            rstn = 1'b1; dtrst = 1'b0;
            @(posedge clk);
            pg_on = 1'b1;
            cap_tick = 1'b1;      // free-running column tick
        end
    endtask

    task run_cols(input integer n);
        integer k;
        begin
            for (k = 0; k < n; k = k + 1) @(posedge clk);
        end
    endtask

    // arm a dec_trigger mode
    task arm(input [1:0] md, input [1:0] sl,
             input [7:0] p, input [7:0] m, input [7:0] b1, input [7:0] b2);
        begin
            trig_mode = md; seqlen = sl;
            mpat = p; mmask = m; sb1 = b1; sb2 = b2;
        end
    endtask

    task check(input exp_fire, input [7:0] exp_byte, input [8*24:1] name);
        begin
            if (dmatched !== exp_fire) begin
                $display("FAIL [%0s]: matched=%b expected=%b", name, dmatched, exp_fire);
                errors = errors + 1;
            end else if (exp_fire && (dmatched_byte !== exp_byte)) begin
                $display("FAIL [%0s]: matched_byte=%02x expected=%02x",
                         name, dmatched_byte, exp_byte);
                errors = errors + 1;
            end else begin
                $display("  ok  [%0s] matched=%b byte=%02x", name, dmatched, dmatched_byte);
            end
        end
    endtask

    // run one scenario: reset, arm, play cols, check
    task scen(input [1:0] pr, input [1:0] md, input [1:0] sl,
              input [7:0] p, input [7:0] m, input [7:0] b1, input [7:0] b2,
              input integer cols, input exp_fire, input [7:0] exp_byte,
              input [8*24:1] name);
        begin
            proto = pr;
            arm(md, sl, p, m, b1, b2);
            do_reset;
            run_cols(cols);
            check(exp_fire, exp_byte, name);
        end
    endtask

    localparam integer UCOLS = 4000;   // > 1 UART script loop
    localparam integer CCOLS = 1000;   // > 1 I2C script loop
    localparam integer SCOLS = 1400;   // > 1 SPI script loop

    initial begin
        dec_en   = 1'b1;
        trig_en  = 1'b1;
        @(posedge clk);

        $display("== UART: mode 0 (legacy byte-match pass-through) ==");
        // clean byte 0xC1 decodes and legacy byte-match fires in mode 0
        scen(2'd0, 2'd0, 2'd0, 8'hC1, 8'hFF, 8'h00, 8'h00, UCOLS, 1'b1, 8'hC1,
             "u.m0 clean C1 hit");
        // absent byte 0x77 -> no fire (proves selectivity, mode-0 path live)
        scen(2'd0, 2'd0, 2'd0, 8'h77, 8'hFF, 8'h00, 8'h00, UCOLS, 1'b0, 8'h00,
             "u.m0 absent 77 no-fire");

        $display("== gen-OFF sanity (pg_on=0 => no decode activity) ==");
        proto = 2'd0; arm(2'd0, 2'd0, 8'hC1, 8'hFF, 8'h00, 8'h00);
        do_reset;                     // do_reset ends with pg_on=1
        pg_on = 1'b0;                 // force generator OFF: lines idle high
        run_cols(UCOLS);
        check(1'b0, 8'h00, "gen-off no-fire");

        $display("== UART: mode 1 (error) ==");
        // frame_err (bad stop) -> mask 0x02 fires on 0x5A
        scen(2'd0, 2'd1, 2'd0, 8'h00, 8'h02, 8'h00, 8'h00, UCOLS, 1'b1, 8'h5A,
             "u.m1 frame_err hit 5A");
        // parity_err -> mask 0x01 fires on 0x3C
        scen(2'd0, 2'd1, 2'd0, 8'h00, 8'h01, 8'h00, 8'h00, UCOLS, 1'b1, 8'h3C,
             "u.m1 parity_err hit 3C");
        // unused flag bit -> no error present -> clean, no fire
        scen(2'd0, 2'd1, 2'd0, 8'h00, 8'h04, 8'h00, 8'h00, UCOLS, 1'b0, 8'h00,
             "u.m1 clean no-fire");

        $display("== UART: mode 2 (sequence) ==");
        // contiguous AA,BB (N=2): match_pattern=AA seq_b1=BB -> fires on BB
        scen(2'd0, 2'd2, 2'd1, 8'hAA, 8'h00, 8'hBB, 8'h00, UCOLS, 1'b1, 8'hBB,
             "u.m2 seq AA,BB hit");
        // near-miss 33,44 are NON-adjacent (big idle) -> must NOT fire
        scen(2'd0, 2'd2, 2'd1, 8'h33, 8'h00, 8'h44, 8'h00, UCOLS, 1'b0, 8'h00,
             "u.m2 near-miss no-fire");

        $display("== I2C: mode 1 (NAK) / mode 3 (addr) / mode 2 (seq) ==");
        // NAK data byte 0xD5 -> mask 0x01 fires
        scen(2'd1, 2'd1, 2'd0, 8'h00, 8'h01, 8'h00, 8'h00, CCOLS, 1'b1, 8'hD5,
             "i2c.m1 NAK hit D5");
        // no error bit set that occurs -> mask 0x04 no fire
        scen(2'd1, 2'd1, 2'd0, 8'h00, 8'h04, 8'h00, 8'h00, CCOLS, 1'b0, 8'h00,
             "i2c.m1 ack-only no-fire");
        // ADDRESS symbol 0x48 -> mode3 fires
        scen(2'd1, 2'd3, 2'd0, 8'h48, 8'hFF, 8'h00, 8'h00, CCOLS, 1'b1, 8'h48,
             "i2c.m3 addr hit 48");
        // mode3 armed on a DATA byte value (0xAA) -> requires addr symbol -> no fire
        scen(2'd1, 2'd3, 2'd0, 8'hAA, 8'hFF, 8'h00, 8'h00, CCOLS, 1'b0, 8'h00,
             "i2c.m3 data-symbol no-fire");
        // wrong address 0x50 -> no fire
        scen(2'd1, 2'd3, 2'd0, 8'h50, 8'hFF, 8'h00, 8'h00, CCOLS, 1'b0, 8'h00,
             "i2c.m3 wrong-addr no-fire");
        // contiguous data seq AA,BB -> mode2 fires
        scen(2'd1, 2'd2, 2'd1, 8'hAA, 8'h00, 8'hBB, 8'h00, CCOLS, 1'b1, 8'hBB,
             "i2c.m2 seq AA,BB hit");

        $display("== SPI: mode 0 (word match, legacy) ==");
        // word 0xC3 present -> fires
        scen(2'd2, 2'd0, 2'd0, 8'hC3, 8'hFF, 8'h00, 8'h00, SCOLS, 1'b1, 8'hC3,
             "spi.m0 word C3 hit");
        // word 0xAA present -> fires (proves decode)
        scen(2'd2, 2'd0, 2'd0, 8'hAA, 8'hFF, 8'h00, 8'h00, SCOLS, 1'b1, 8'hAA,
             "spi.m0 word AA hit");
        // absent word 0x99 -> no fire
        scen(2'd2, 2'd0, 2'd0, 8'h99, 8'hFF, 8'h00, 8'h00, SCOLS, 1'b0, 8'h00,
             "spi.m0 absent no-fire");

        if (errors == 0)
            $display("\n== tb_patterngen: ALL CHECKS PASSED ==");
        else
            $display("\n== tb_patterngen: %0d FAILURE(S) ==", errors);
        $finish;
    end

    initial begin
        #40000000 $display("FAIL: timeout"); $fatal(1, "timeout");
    end

endmodule

`default_nettype wire

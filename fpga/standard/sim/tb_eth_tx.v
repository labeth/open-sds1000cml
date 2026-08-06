// tb_eth_tx.v — self-proof of the in-fabric 100BASE-TX line encoder (ITEM 2).
//
// Drives fpga/standard/eth_tx.v into the REAL fabric decode chain
//   eth_tx -> eth100_decode_lr (eth_gearbox -> eth_slicer_cdr -> eth_descramble2
//             -> eth_4b5b2 -> eth_framer) -> dec_trigger (mode-1 ERROR)
// and asserts, for BOTH a good-FCS and a bad-FCS emission:
//   * the decoded MAC body (frame || FCS) matches the eth100tx-oracle vector
//     (app/internal/eth100tx TestFabricVector) byte-for-byte,
//   * good  => framer flags8[4]=OK, flags8[3]=0, dec_trigger mode-1 does NOT fire,
//   * bad   => framer flags8[3]=ERR, flags8[4]=0, dec_trigger mode-1 FIRES,
//   * the frame DATA octets are identical good-vs-bad (only the FCS differs).
//
// Two clock domains, exactly as acq.v wires it: eth_tx + gearbox WRITE side at
// 200 MHz (wr_clk); slicer/descramble/4b5b/framer/dec_trigger at 80 MHz (clk).
//
// Run:  iverilog -g2012 -o /tmp/tb_eth_tx tb_eth_tx.v \
//         ../eth_tx.v ../eth100_decode_lr.v ../eth_gearbox.v ../eth_slicer_cdr.v \
//         ../eth_descramble2.v ../eth_4b5b2.v ../eth_framer.v ../dec_trigger.v
//       vvp /tmp/tb_eth_tx

`timescale 1ns/1ps
`default_nettype none

// ---- one decode-chain instance fed by eth_tx(bad_fcs=BADFCS) -----------------
module eth_tx_check #(parameter integer BADFCS = 0) (
    input  wire        clk80,      // 80 MHz fabric/read clock
    input  wire        clk200,     // 200 MHz interleave/write clock
    input  wire        rst,        // synchronous reset (both domains)
    output reg  [175:0] body,      // captured body octets, octet i = body[8*i +:8]
    output reg  [5:0]  ncap,       // octet count of the captured frame
    output reg  [7:0]  end_flags,  // flags8 on the terminal (F_END) octet
    output reg         got_frame,  // a full frame (F_START..F_END) captured
    output reg         m1_fired    // dec_trigger mode-1 fired during the frame
);
    localparam BAD = (BADFCS != 0);

    // ---- eth_tx (200 MHz write domain) ----
    wire [35:0] tx_samp;
    wire        tx_valid;
    eth_tx #(.SAMPLE_W(12), .WR_SAMP(3), .AMP(1000), .FRAME_LEN(18)) u_tx (
        .clk(clk200), .rst(rst), .en(1'b1), .bad_fcs(BAD),
        .samp(tx_samp), .valid(tx_valid)
    );

    // ---- real decode chain ----
    wire        emit_stb;  wire [7:0] emit_byte;  wire [23:0] emit_idx;
    wire [7:0]  emit_flags;
    wire        sfd_seen, frame_done, fcs_ok;
    wire        descr_locked, cg_locked, gb_ovf, cdr_ovf, fb_ovf;

    eth100_decode_lr #(.SAMPLE_W(12), .LANES(8), .WR_SAMP(3), .DEPTHW(4)) u_dec (
        .clk(clk80), .rst(rst), .en(1'b1),
        .thr_hi(12'sd500), .thr_lo(-12'sd500),
        .wr_clk(clk200), .wr_rst(rst), .wr_valid(tx_valid), .wr_samp(tx_samp),
        .flush(1'b0),
        .emit_stb(emit_stb), .emit_byte(emit_byte), .emit_idx(emit_idx), .emit_flags(emit_flags),
        .sfd_seen(sfd_seen), .frame_done(frame_done), .fcs_ok_o(fcs_ok),
        .descr_locked(descr_locked), .cg_locked(cg_locked),
        .gb_overflow(gb_ovf), .cdr_overflow(cdr_ovf), .fb_ovf(fb_ovf)
    );

    // ---- dec_trigger in mode-1 (ERROR), err_mask = flags8[3] (FCS-err) = 0x08 ----
    wire decode_trig, matched;  wire [7:0] matched_byte;
    dec_trigger u_trig (
        .clk(clk80), .rst(rst), .en(1'b1),
        .emit_stb(emit_stb), .emit_byte(emit_byte), .emit_idx(emit_idx), .emit_flags(emit_flags),
        .sel_i2c(1'b0), .sel_spi(1'b0), .sel_eth(1'b1),
        .eth_sfd(sfd_seen), .i2c_start(1'b0), .i2c_stop(1'b0),
        .trig_en(1'b1), .trig_mode(2'd1), .seqlen_cfg(2'd0),
        .match_pattern(8'h00), .match_mask(8'h08),   // err_mask bit3 = FCS-err
        .seq_b1(8'h00), .seq_b2(8'h00), .adj_win(16'd0),
        .legacy_trig(1'b0), .legacy_matched(1'b0), .legacy_matched_byte(8'h00),
        .decode_trig(decode_trig), .matched(matched), .matched_byte(matched_byte)
    );

    // ---- capture the FIRST complete frame (F_START .. F_END) ----
    localparam integer F_START = 7, F_END = 6;
    reg [5:0] widx;
    reg       capturing;
    always @(posedge clk80) begin
        if (rst) begin
            widx <= 6'd0; ncap <= 6'd0; got_frame <= 1'b0;
            m1_fired <= 1'b0; end_flags <= 8'd0; capturing <= 1'b0;
            body <= 176'd0;
        end else if (!got_frame) begin
            if (decode_trig) m1_fired <= 1'b1;
            if (emit_stb) begin
                if (emit_flags[F_START]) begin
                    body[7:0] <= emit_byte;
                    widx      <= 6'd1;
                    capturing <= 1'b1;
                    if (emit_flags[F_END]) begin end_flags <= emit_flags; ncap <= 6'd1; got_frame <= 1'b1; end
                end else if (capturing) begin
                    body[widx*8 +: 8] <= emit_byte;
                    widx <= widx + 6'd1;
                    if (emit_flags[F_END]) begin
                        end_flags <= emit_flags;
                        ncap      <= widx + 6'd1;
                        got_frame <= 1'b1;
                    end
                end
            end
        end
    end
endmodule

// ---- top: run good + bad in parallel, evaluate ------------------------------
module tb_eth_tx;
    reg clk80 = 1'b0, clk200 = 1'b0, rst = 1'b1;
    always #6.25 clk80  = ~clk80;   // 80 MHz
    always #2.5  clk200 = ~clk200;  // 200 MHz

    wire [175:0] g_body, b_body;
    wire [5:0]   g_ncap, b_ncap;
    wire [7:0]   g_flags, b_flags;
    wire         g_done, b_done, g_m1, b_m1;

    eth_tx_check #(.BADFCS(0)) GOOD (
        .clk80(clk80), .clk200(clk200), .rst(rst),
        .body(g_body), .ncap(g_ncap), .end_flags(g_flags),
        .got_frame(g_done), .m1_fired(g_m1)
    );
    eth_tx_check #(.BADFCS(1)) BAD (
        .clk80(clk80), .clk200(clk200), .rst(rst),
        .body(b_body), .ncap(b_ncap), .end_flags(b_flags),
        .got_frame(b_done), .m1_fired(b_m1)
    );

    // expected GOOD body from the eth100tx oracle (TestFabricVector):
    //   frame(18) || FCS D0 2C BF 52 ; FCS value 0x52BF2CD0
    reg [7:0] exp [0:21];
    localparam integer F_OK = 4, F_ERR = 3;
    integer i, errs, timeout;

    initial begin
        exp[0]=8'h00; exp[1]=8'h11; exp[2]=8'h22; exp[3]=8'h33;
        exp[4]=8'h44; exp[5]=8'h55; exp[6]=8'h66; exp[7]=8'h77;
        exp[8]=8'h88; exp[9]=8'h99; exp[10]=8'hAA; exp[11]=8'hBB;
        exp[12]=8'h08; exp[13]=8'h06; exp[14]=8'hDE; exp[15]=8'hAD;
        exp[16]=8'hBE; exp[17]=8'hEF; exp[18]=8'hD0; exp[19]=8'h2C;
        exp[20]=8'hBF; exp[21]=8'h52;

        errs = 0;
        repeat (4) @(posedge clk80);
        rst = 1'b0;

        timeout = 0;
        while (!(g_done && b_done) && timeout < 60000) begin
            @(posedge clk80); timeout = timeout + 1;
        end

        // ---------------- GOOD frame checks ----------------
        if (!g_done) begin $display("FAIL: good frame never completed"); errs=errs+1; end
        else begin
            if (g_ncap !== 6'd22) begin $display("FAIL: good ncap=%0d != 22", g_ncap); errs=errs+1; end
            for (i = 0; i < 22; i = i + 1)
                if (g_body[i*8 +: 8] !== exp[i]) begin
                    $display("FAIL: good body[%0d]=%02x != %02x", i, g_body[i*8 +: 8], exp[i]);
                    errs=errs+1;
                end
            if (!g_flags[F_OK])  begin $display("FAIL: good flags8[4](OK) not set (flags=%02x)", g_flags); errs=errs+1; end
            if ( g_flags[F_ERR]) begin $display("FAIL: good flags8[3](ERR) set (flags=%02x)", g_flags); errs=errs+1; end
            if ( g_m1)           begin $display("FAIL: good frame fired dec_trigger mode-1"); errs=errs+1; end
        end

        // ---------------- BAD frame checks ----------------
        if (!b_done) begin $display("FAIL: bad frame never completed"); errs=errs+1; end
        else begin
            if (b_ncap !== 6'd22) begin $display("FAIL: bad ncap=%0d != 22", b_ncap); errs=errs+1; end
            for (i = 0; i < 18; i = i + 1)   // data octets identical to good
                if (b_body[i*8 +: 8] !== exp[i]) begin
                    $display("FAIL: bad data[%0d]=%02x != %02x", i, b_body[i*8 +: 8], exp[i]);
                    errs=errs+1;
                end
            if (b_body[18*8 +: 8] !== (exp[18] ^ 8'hFF)) begin
                $display("FAIL: bad FCS[0]=%02x != %02x (corrupted)", b_body[18*8 +: 8], exp[18]^8'hFF);
                errs=errs+1;
            end
            if (!b_flags[F_ERR]) begin $display("FAIL: bad flags8[3](ERR) not set (flags=%02x)", b_flags); errs=errs+1; end
            if ( b_flags[F_OK])  begin $display("FAIL: bad flags8[4](OK) set (flags=%02x)", b_flags); errs=errs+1; end
            if (!b_m1)           begin $display("FAIL: bad frame did NOT fire dec_trigger mode-1"); errs=errs+1; end
        end

        if (errs == 0)
            $display("PASS: eth_tx self-proof OK (good: body byte-exact, FCS OK, mode-1 silent; bad: FCS-err, mode-1 fired)");
        else
            $display("FAILED with %0d error(s)", errs);
        $finish;
    end
endmodule

`default_nettype wire

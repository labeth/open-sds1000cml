// ENGMODEL-OWNER-UNIT: FU-RTL-DEFAULT-TESTS
// tb_top.v -- the default image through the GPMC model (schema v3, 128 selectors): identity,
// the 7-bit selector decode (0x25 vs 0x24 independent, 0x21/0x57 undecoded, unclaimed and stub
// selectors read 0 / ignore writes), SNOOP with the A2/B1 selector bits, RUN storing only its v2.2
// fields, CLK_STAT lock + ratio, the DIAG window incl. the read-only LANEMAP against
// lanemap_seed.vh, MISC_RD; then the
// datapath: an ADC model on the encode -> lanes -> baked map -> record -> BURST drain with
// DRAIN_STAT/POP_MON, a windowed re-drain through OP_REWIND, the TSRC ramp drained bit-exact
// for the full 20478-word record plus a REWIND window over it, the column-tag source, RESET.
`timescale 1ns/1ps
`include "regs.vh"
`include "lanemap_seed.vh"
// TRLC-LINKS: REQ-SDS-060, REQ-SDS-194, REQ-SDS-195, REQ-SDS-196, REQ-SDS-197
module tb_top;
    reg clk = 0;  always #6.25  clk = ~clk;       // C2 80 MHz
    reg mclk = 0; always #3.125 mclk = ~mclk;     // M2 160 MHz
    wire nCS1, nOE, nWE; wire [6:0] sel; wire [15:0] d;
    reg trig_sense = 0, p6 = 0;
    reg d1_drv = 0, d1_en = 1;  wire d1 = d1_en ? d1_drv : 1'bz;
    wire [4:0] enc_p, enc_n; wire a11, f1, g1, g2, k1, d2, k2, f2, j2;
    wire [26:0] bus; wire [4:0] single;
    reg [79:0] lane = 0;
    gpmc_bfm bfm (.nCS1(nCS1), .nOE(nOE), .nWE(nWE), .sel(sel), .d(d));
    // balls A2 / B1 carry GPMC A2 / A1 = selector bits 1 / 0 (R0 report)
    default_top dut (.clk(clk), .mclk_in(mclk), .gpmc_d(d), .nCS1(nCS1), .nOE(nOE), .nWE(nWE), .sel(sel[6:2]),
        .trig_sense(trig_sense), .gpmc_a2(sel[1]), .gpmc_b1(sel[0]), .enc_p(enc_p), .enc_n(enc_n),
        .a11(a11), .f1(f1), .g1(g1), .g2(g2), .k1(k1), .d2(d2), .p6(p6), .k2(k2), .f2(f2), .j2(j2),
        .bus(bus), .single(single), .d1(d1), .lane(lane));
    integer errors = 0, i, n;
    task check(input cond, input [8*64-1:0] msg); begin if (!cond) begin errors = errors + 1; $display("FAIL: %0s", msg); end end endtask
    reg [15:0] v, v2;
    reg [15:0] rec [0:63];                 // the first drain, for the window comparison
    localparam [`LANEMAP_PACKED_W-1:0] SEED = `LANEMAP_SEED_PACKED;
    // ---- ADC model: converter on encode pair E1, CH1 = ramp, CH2 = ~ramp, tpd 1 ns ----
    // Lanes per the baked seed (lanemap_seed.vh / lanecal-2026-09-05): E1 CH1 bit7..0 = lanes
    // 56 57 54 53 52 62 63 61, E1 CH2 bit7..0 = lanes 55 48 58 60 51 59 50 49.
    reg [7:0] code = 0; reg adc_on = 0;
    always @(posedge enc_p[0]) if (adc_on) begin
        #1;
        lane[56] <= code[7]; lane[57] <= code[6]; lane[54] <= code[5]; lane[53] <= code[4];
        lane[52] <= code[3]; lane[62] <= code[2]; lane[63] <= code[1]; lane[61] <= code[0];
        lane[55] <= ~code[7]; lane[48] <= ~code[6]; lane[58] <= ~code[5]; lane[60] <= ~code[4];
        lane[51] <= ~code[3]; lane[59] <= ~code[2]; lane[50] <= ~code[1]; lane[49] <= ~code[0];
        code <= code + 8'd1;
    end
    task wait_done; begin i = 0; v = 0;
        while (!(v & `STATUS_A_DONE_MASK) && i < 2000) begin bfm.read(`SEL_STATUS_A, v); i = i + 1; end
        check((v & `STATUS_A_DONE_MASK) != 0, "capture DONE");
    end endtask
    initial begin
        #2000;
        // ---- identity ----
        bfm.read(`SEL_BUILDID_LO, v); check(v == `IFACE_BUILD_ID_LO, "BUILDID_LO");
        bfm.read(`SEL_BUILDID_HI, v); check(v == `IFACE_BUILD_ID_HI, "BUILDID_HI");
        bfm.read(`SEL_VERSION, v);    check(v == 16'h00a3 && v == `VERSION_MAGIC, "VERSION 0x00A3");
        bfm.read(`SEL_FABRIC_ID, v);  check(v == 16'ha2f1 && v == `FABRIC_ID, "FABRIC_ID 0xA2F1");
        bfm.read(`SEL_ENV_DATA, v);   check(v == 16'h0000, "ENV_DATA reads 0");
        // ---- 128-selector decode: 0x25 (IL_CTRL) and 0x24 (RUN) are independent ----
        bfm.write(`SEL_IL_CTRL, 16'h000c);                 // TSRC = 3
        bfm.read(`SEL_RUN, v); check(v == 16'h0000, "write 0x25 leaves RUN (0x24) alone");
        bfm.read(`SEL_IL_CTRL, v); check(v == 16'h000c, "IL_CTRL reads back TSRC");
        bfm.read(`SEL_SNOOP_SEL, v); check(v[6:0] == 7'h25 && v[7] == 1'b0 && v[8] == 1'b1, "SNOOP_SEL: full selector 0x25, A2 = 0, B1 = 1");
        check(v[15:9] == 7'd1, "SNOOP count 1");
        bfm.read(`SEL_SNOOP_DATA, v); check(v == 16'h000c, "SNOOP_DATA");
        bfm.write(`SEL_IL_CTRL, 16'hffff);
        bfm.read(`SEL_IL_CTRL, v); check(v == 16'h000c, "IL_CTRL stores only TSRC (v3.0+ fields read 0)");
        bfm.write(`SEL_RUN, 16'h0034);
        bfm.read(`SEL_IL_CTRL, v); check(v == 16'h000c, "write 0x24 leaves IL_CTRL (0x25) alone");
        bfm.read(`SEL_RUN, v); check(v == 16'h0034, "RUN written at 0x24");
        bfm.read(`SEL_SNOOP_SEL, v); check(v[6:0] == 7'h24 && v[8:7] == 2'b00 && v[15:9] == 7'd3, "SNOOP_SEL 0x24, A2 = B1 = 0, count 3");
        bfm.write(`SEL_RUN, 16'hffc4);                     // STACK | REDUCE | bits 15:8 | RUN
        bfm.read(`SEL_RUN, v); check(v == 16'h0004, "RUN stores only MODE/RUN/STREAM/CHMODE (STACK/REDUCE read 0)");
        bfm.write(`SEL_RUN, 16'h003f);
        bfm.read(`SEL_RUN, v); check(v == 16'h003f, "RUN: every v2.2 field reads back");
        bfm.write(`SEL_IL_CTRL, 16'h0000); bfm.write(`SEL_RUN, 16'h0000);
        // ---- undecoded 0x21 / 0x57 (the vendor arm/halt words): read 0, change nothing ----
        bfm.write(`SEL_RUN, `RUN_RUN_MASK);
        bfm.write(7'h21, `OP_GO); #300;
        bfm.read(`SEL_STATUS_A, v); check(v == 16'h0000, "0x21 is not OPCODE: no GO");
        bfm.read(7'h21, v); check(v == 16'h0000, "0x21 reads 0");
        bfm.write(7'h57, 16'h1234);
        bfm.read(7'h57, v); check(v == 16'h0000, "0x57 reads 0");
        bfm.read(`SEL_FILL, v); check(v == 16'h0000, "0x57 did not touch FILL");
        bfm.read(`SEL_ACQ_CTRL, v); check(v == 16'h7c00, "0x57 did not touch ACQ_CTRL");
        bfm.read(`SEL_SNOOP_SEL, v); check(v[6:0] == 7'h57, "SNOOP still records an undecoded write");
        bfm.write(`SEL_RUN, 16'h0000);
        bfm.write(`SEL_OPCODE, 16'h00c0);                 // vendor word on OPCODE: ignored
        bfm.read(`SEL_STATUS_A, v); check(v == 16'h0000, "unknown OPCODE ignored");
        bfm.write(`SEL_OPCODE, `OP_STACK_CLEAR);          // decoded, ignored in v2.2
        bfm.read(`SEL_STATUS_A, v); check(v == 16'h0000, "STACK_CLEAR ignored");
        // ---- stubs (v3.0+) read 0 and ignore writes; unclaimed selectors read 0 ----
        bfm.write(`SEL_DEC_RECIP, 16'h1234);  bfm.read(`SEL_DEC_RECIP, v);  check(v == 0, "DEC_RECIP stub");
        bfm.write(`SEL_DEC_PRE, 16'h001f);    bfm.read(`SEL_DEC_PRE, v);    check(v == 0, "DEC_PRE stub");
        bfm.write(`SEL_STACK_N, 16'h0100);    bfm.read(`SEL_STACK_N, v);    check(v == 0, "STACK_N stub");
        bfm.write(`SEL_STACK_CTRL, 16'hffff); bfm.read(`SEL_STACK_CTRL, v); check(v == 0, "STACK_CTRL stub");
        bfm.write(`SEL_STACK_PRE, 16'h01ff);  bfm.read(`SEL_STACK_PRE, v);  check(v == 0, "STACK_PRE stub");
        bfm.write(`SEL_STACK_LEN, 16'h0fff);  bfm.read(`SEL_STACK_LEN, v);  check(v == 0, "STACK_LEN stub");
        bfm.write(`SEL_EYE_CTRL, 16'h8000);   bfm.read(`SEL_EYE_CTRL, v);   check(v == 0, "EYE_CTRL stub");
        bfm.read(`SEL_STACK_CNT, v); check(v == 0, "STACK_CNT stub");
        bfm.read(`SEL_STATUS_B, v);  check(v == 0, "STATUS_B stub");
        bfm.read(`SEL_EYE_CNT, v);   check(v == 0, "EYE_CNT stub");
        bfm.read(`SEL_DBG2, v);      check(v == 0, "DBG2 stub");
        n = 0;
        for (i = 0; i < 128; i = i + 1) begin
            case (i[6:0])
                7'h25, 7'h26, 7'h27, 7'h41, 7'h42, 7'h43, 7'h45, 7'h46, 7'h47, 7'h49, 7'h4a, 7'h4b, 7'h4d, 7'h4e, 7'h4f, 7'h7d: ;
                default: if (i[1:0] != 2'b00) begin bfm.write(i[6:0], 16'hbeef); bfm.read(i[6:0], v); if (v != 16'h0000) n = n + 1; end
            endcase
        end
        check(n == 0, "every unclaimed odd selector reads 0 after a write");
        bfm.read(`SEL_RUN, v); bfm.read(`SEL_ACQ_CTRL, v2);
        check(v == 16'h0000 && v2 == 16'h7c00, "the unclaimed writes touched no v2 register");
        // ---- the live v3 registers ----
        bfm.write(`SEL_DRAIN_START, 16'hffff); bfm.read(`SEL_DRAIN_START, v); check(v == 16'h7fff, "DRAIN_START stores IDX[14:0]");
        bfm.write(`SEL_DRAIN_START, 16'h1234); bfm.read(`SEL_DRAIN_START, v); check(v == 16'h1234, "DRAIN_START readback");
        bfm.write(`SEL_DRAIN_LEN, 16'hbeef);   bfm.read(`SEL_DRAIN_LEN, v);   check(v == 16'hbeef, "DRAIN_LEN readback");
        bfm.write(`SEL_DRAIN_START, 16'h0000); bfm.write(`SEL_DRAIN_LEN, 16'h0000);
        bfm.read(`SEL_DRAIN_STAT, v); check(v == 16'h0000, "DRAIN_STAT 0");
        bfm.read(`SEL_POP_MON, v); check(v == 16'h00ff, "POP_MON: MIN_GAP 255, UNDERRUN 0");
        bfm.read(`SEL_ACQ_CTRL, v); check(v == 16'h7c00, "ACQ_CTRL reset: encode off, pairs enabled");
        bfm.read(`SEL_TRIG_LEVEL, v); check(v == 16'h0080, "TRIG_LEVEL reset 0x80");
        bfm.read(`SEL_DIAG_CTRL, v); check(v == 16'h0102, "DIAG_CTRL reset");
        bfm.read(`SEL_BURST_REMAIN, v); check(v == 16'h0000, "no record: READY 0");
        bfm.read(`SEL_BURST, v); check(v == 16'h0000, "no record: BURST 0");
        bfm.read(`SEL_POP_MON, v); check(v == 16'h01ff, "a pop with no record is an underrun");
        // ---- DIAG window: LANEMAP read-only against the include, writes ignored ----
        n = 0;
        for (i = 0; i < `LANEMAP_N; i = i + 1) begin
            bfm.write(`SEL_DIAG_IDX, `DIAG_LANEMAP_BASE + i[15:0]); bfm.read(`SEL_DIAG_DATA, v);
            if (v != {9'd0, SEED[7*i +: 7]}) n = n + 1;
        end
        check(n == 0, "DIAG LANEMAP[0..79] = lanemap_seed.vh");
        bfm.write(`SEL_DIAG_IDX, 16'h0017); bfm.read(`SEL_DIAG_DATA, v); check(v == 16'd56, "LANEMAP[7] = lane 56 (lanecal)");
        bfm.write(`SEL_DIAG_DATA, 16'h0001); bfm.read(`SEL_DIAG_DATA, v); check(v == 16'd56, "LANEMAP write ignored");
        bfm.write(`SEL_DIAG_IDX, `DIAG_PAIR_TRIM); bfm.write(`SEL_DIAG_DATA, 16'h1234); bfm.read(`SEL_DIAG_DATA, v); check(v == 0, "PAIR_TRIM stub reads 0");
        bfm.write(`SEL_DIAG_IDX, 16'h0007); bfm.write(`SEL_DIAG_DATA, 16'h0000); #300;
        check(bus[5] === 1'bz && bus[26] === 1'bz, "ADC_HOLD 0 releases L4/T7");
        bfm.write(`SEL_DIAG_DATA, 16'h0007); #300; check(bus[5] === 1'b1, "ADC_HOLD restored");
        bfm.read(`SEL_DIAG_IDX, v); check(v == 16'h0007, "DIAG_IDX readback");
        // A2/B1 are selector bits now: during a MISC_RD read (0x74) they are 0/0 by construction
        p6 = 1; #200; bfm.read(`SEL_MISC_RD, v); check(v[0] == 1'b1 && v[7:6] == 2'b00, "MISC_RD P6 = 1, A2/B1 = the 0x74 selector bits");
        // ---- CLK_STAT: lock + ratio (160/80 MHz -> 8192 edges per 4096 C2 cycles -> 512) ----
        #120000;
        bfm.read(`SEL_CLK_STAT, v); check(v[1:0] == 2'b11, "CLK_STAT both PLLs locked");
        check(v[15:4] >= 12'd510 && v[15:4] <= 12'd514, "CLK_STAT ratio ~512");
        // ---- end-to-end capture: encode 200 MHz, AUTO, pre 10 post 20 ----
        bfm.write(`SEL_ACQ_CTRL, 16'h7c07); adc_on = 1;   // ENC_EN, rate 3, all pairs
        #500; n = 0; repeat (10) begin #5; if (enc_n[0] !== ~enc_p[0]) n = n + 1; end; check(n == 0, "E1 complementary");
        bfm.write(`SEL_DECIM_LO, 16'd1); bfm.write(`SEL_DECIM_HI, 16'd0);
        bfm.write(`SEL_PRETRIG_LO, 16'd10); bfm.write(`SEL_PRETRIG_HI, 16'd0);
        bfm.write(`SEL_POSTTRIG_LO, 16'd20); bfm.write(`SEL_POSTTRIG_HI, 16'd0);
        bfm.write(`SEL_RUN, `RUN_RUN_MASK);
        bfm.write(`SEL_OPCODE, `OP_GO);
        bfm.read(`SEL_POP_MON, v); check(v == 16'h00ff, "GO clears POP_MON");
        wait_done;
        check((v & `STATUS_A_VALID_MASK) != 0, "capture VALID");
        bfm.read(`SEL_FILL, v); check(v == 16'd30, "FILL 30");
        bfm.read(`SEL_TRIGPOS_HI, v); check(v == 16'd10, "TRIGPOS_HI = pre");
        bfm.read(`SEL_BURST_REMAIN, v); check(v == (16'h8000 | 16'd30), "BURST_REMAIN 30");
        bfm.read(`SEL_BURST, v); rec[0] = v; n = 0;
        for (i = 1; i < 30; i = i + 1) begin
            if (i[0]) bfm.read(`SEL_BURST_ALIAS, v2); else bfm.read(`SEL_BURST, v2);   // alias pops too
            if (v2[15:8] != v[15:8] + 8'd1 || v2[7:0] != ~v2[15:8]) n = n + 1;
            rec[i] = v2; v = v2;
        end
        check(n == 0, "BURST: 30 consecutive ramp words, CH2 = ~CH1");
        bfm.read(`SEL_BURST_REMAIN, v); check(v == 16'h8000, "drained: REMAIN 0");
        bfm.read(`SEL_DRAIN_STAT, v); check(v == 16'd30, "DRAIN_STAT.POPS 30");
        bfm.read(`SEL_POP_MON, v); check(v[15:8] == 8'd0, "POP_MON: no underrun");
        check(v[7:0] >= 8'd12 && v[7:0] <= 8'd14, "POP_MON: MIN_GAP = the BFM read cadence (~165 ns = 13 clk)");
        bfm.read(`SEL_BURST, v2); check(v2 == rec[29], "flat tail: the last word again");
        bfm.read(`SEL_POP_MON, v); check(v[15:8] == 8'd1, "POP_MON: the flat pop is an underrun");
        bfm.read(`SEL_DRAIN_STAT, v); check(v == 16'd31, "DRAIN_STAT.POPS 31");
        // ---- windowed re-drain of the same frozen record through REWIND ----
        bfm.write(`SEL_DRAIN_START, 16'd5); bfm.write(`SEL_DRAIN_LEN, 16'd10);
        bfm.read(`SEL_BURST_REMAIN, v); check(v == 16'h8000, "DRAIN_* writes alone: still drained");
        bfm.write(`SEL_OPCODE, `OP_REWIND);
        bfm.read(`SEL_BURST_REMAIN, v); check(v == (16'h8000 | 16'd10), "REWIND: window of 10");
        bfm.read(`SEL_DRAIN_STAT, v); check(v == 16'd0, "REWIND clears POPS");
        bfm.read(`SEL_POP_MON, v); check(v[15:8] == 8'd1, "REWIND keeps POP_MON");
        n = 0; for (i = 0; i < 10; i = i + 1) begin bfm.read(`SEL_BURST, v); if (v != rec[5 + i]) n = n + 1; end
        check(n == 0, "window 5..14 byte-identical to the full drain");
        bfm.read(`SEL_BURST_REMAIN, v); check(v == 16'h8000, "window drained");
        bfm.write(`SEL_DRAIN_START, 16'd25); bfm.write(`SEL_DRAIN_LEN, 16'd100); bfm.write(`SEL_OPCODE, `OP_REWIND);
        bfm.read(`SEL_BURST_REMAIN, v); check(v == (16'h8000 | 16'd5), "REWIND: window clipped at the record end");
        n = 0; for (i = 0; i < 5; i = i + 1) begin bfm.read(`SEL_BURST, v); if (v != rec[25 + i]) n = n + 1; end
        check(n == 0, "window 25..29 identical");
        bfm.write(`SEL_DRAIN_START, 16'd0); bfm.write(`SEL_DRAIN_LEN, 16'd0); bfm.write(`SEL_OPCODE, `OP_REWIND);
        bfm.read(`SEL_BURST_REMAIN, v); check(v == (16'h8000 | 16'd30), "REWIND to the whole record");
        bfm.read(`SEL_BURST, v); check(v == rec[0], "word 0 again");
        bfm.read(`SEL_DBG, v); check(v[1:0] == 2'd2 && v[9] && v[10], "DBG: halt state, PLLs locked");
        // ---- TSRC ramp: the full 20478-word record, drained bit-exact (rung R1 / R2b) ----
        bfm.write(`SEL_IL_CTRL, `IL_CTRL_TSRC_RAMP << `IL_CTRL_TSRC_LSB);
        bfm.write(`SEL_PRETRIG_LO, 16'd10000); bfm.write(`SEL_POSTTRIG_LO, 16'd10478);
        bfm.write(`SEL_OPCODE, `OP_GO);
        wait_done;
        bfm.read(`SEL_FILL, v); check(v == 16'd20478, "ramp: FILL 20478");
        bfm.read(`SEL_BURST_REMAIN, v); check(v == (16'h8000 | 16'd20478), "ramp: BURST_REMAIN 20478");
        n = 0;
        for (i = 0; i < 20478; i = i + 1) begin
            if (i[0]) bfm.read(`SEL_BURST_ALIAS, v); else bfm.read(`SEL_BURST, v);
            if (v != i[15:0]) begin if (n < 5) $display("ramp break at %0d: 0x%04x", i, v); n = n + 1; end
        end
        check(n == 0, "ramp: 20478 words drained bit-exact (word i == i)");
        bfm.read(`SEL_BURST_REMAIN, v); check(v == 16'h8000, "ramp: REMAIN 0");
        bfm.read(`SEL_DRAIN_STAT, v); check(v == 16'd20478, "ramp: DRAIN_STAT.POPS 20478");
        bfm.read(`SEL_POP_MON, v); check(v[15:8] == 8'd0, "ramp: 0 underruns");
        bfm.write(`SEL_DRAIN_START, 16'd20000); bfm.write(`SEL_DRAIN_LEN, 16'd0); bfm.write(`SEL_OPCODE, `OP_REWIND);
        bfm.read(`SEL_BURST_REMAIN, v); check(v == (16'h8000 | 16'd478), "ramp: REWIND window 20000..20477");
        n = 0; for (i = 20000; i < 20478; i = i + 1) begin bfm.read(`SEL_BURST, v); if (v != i[15:0]) n = n + 1; end
        check(n == 0, "ramp: the window is the slice");
        bfm.write(`SEL_DRAIN_START, 16'd0);
        // ---- column tag through the top, pre 10 post 20 ----
        bfm.write(`SEL_IL_CTRL, `IL_CTRL_TSRC_COLTAG << `IL_CTRL_TSRC_LSB);
        bfm.write(`SEL_PRETRIG_LO, 16'd10); bfm.write(`SEL_POSTTRIG_LO, 16'd20);
        bfm.write(`SEL_OPCODE, `OP_GO); wait_done;
        n = 0; for (i = 0; i < 30; i = i + 1) begin bfm.read(`SEL_BURST, v); v2 = (i % 5) * 256 + (i / 5) % 256; if (v != v2) n = n + 1; end
        check(n == 0, "coltag: 30 words {k mod 5, k / 5}");
        bfm.write(`SEL_IL_CTRL, 16'h0000);
        // ---- RESET ----
        bfm.write(`SEL_OPCODE, `OP_RESET); #200;
        bfm.read(`SEL_STATUS_A, v); check(v == 16'h0000, "RESET clears status");
        bfm.read(`SEL_BURST_REMAIN, v); check(v == 16'h0000, "RESET: READY 0");
        bfm.read(`SEL_DRAIN_STAT, v); check(v == 16'h0000, "RESET clears POPS");
        // GO while RUN=0 is ignored
        bfm.write(`SEL_RUN, 16'h0000); bfm.write(`SEL_OPCODE, `OP_GO); #500;
        bfm.read(`SEL_STATUS_A, v); check(v == 16'h0000, "GO ignored while RUN=0");
        if (errors == 0) $display("PASS tb_top"); else $display("FAIL tb_top: %0d errors", errors);
        $finish;
    end
    initial begin #30000000; $display("FAIL tb_top: timeout"); $finish; end
endmodule

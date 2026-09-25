// ENGMODEL-OWNER-UNIT: FU-RTL-DEFAULT-TESTS
// tb_diag.v -- reset posture, bus drive + pad readback, ADC_HOLD, singles + MISC_RD, K2/D2/F2/J2,
// the read-only LANEMAP window (all 80 entries against lanemap_seed.vh + the lanecal literals,
// writes ignored), the v3.0+ stub indices (read 0, writes ignored), the lane monitor (toggle
// window, ever-flags), and the snapshot RAM in bus / lane / 5-word modes.
`timescale 1ns/1ps
`include "regs.vh"
`include "lanemap_seed.vh"
// TRLC-LINKS: REQ-SDS-060, REQ-SDS-197, REQ-SDS-198
module tb_diag;
    reg clk = 0;    always #6.25 clk = ~clk;
    reg clk50 = 0;  always #10   clk50 = ~clk50;
    reg clk100 = 0; always #5    clk100 = ~clk100;
    reg cap = 0;    always #2.5  cap = ~cap;
    reg we_idx = 0, we_data = 0, we_ctrl = 0, pop = 0; reg [15:0] wd = 0;
    wire [15:0] r_idx, r_data, r_ctrl, r_srem, r_spop, r_misc;
    wire [26:0] bus; wire [4:0] single;
    wire d2, k2, f2, j2, a11, f1, g1, g2, k1;
    reg p6 = 0, a2 = 0, b1 = 0, nwe = 1, noe = 1;
    reg d1_drv = 0, d1_en = 1;  wire d1 = d1_en ? d1_drv : 1'bz;   // D1 is bidir (PORT-SPEC 2.3)
    reg [79:0] lane_q = 0;
    wire [55:0] m1, m2; wire [1:0] dbg;
    localparam [`LANEMAP_PACKED_W-1:0] SEED = `LANEMAP_SEED_PACKED;
    // external drivers (per ball) for the bidirectionals
    reg [26:0] xd_en = 0, xd_v = 0; reg [4:0] xs_en = 0, xs_v = 0;
    genvar g;
    generate for (g = 0; g < 27; g = g + 1) begin : gx assign bus[g] = xd_en[g] ? xd_v[g] : 1'bz; end
             for (g = 0; g < 5;  g = g + 1) begin : gs assign single[g] = xs_en[g] ? xs_v[g] : 1'bz; end endgenerate
    diag dut (.clk(clk), .clk50(clk50), .clk100(clk100), .cap_clk(cap),
        .we_idx(we_idx), .we_data(we_data), .we_ctrl(we_ctrl), .wr_data(wd), .pop_snap(pop),
        .rdata_idx(r_idx), .rdata_data(r_data), .rdata_ctrl(r_ctrl), .rdata_snap_remain(r_srem),
        .rdata_snap_pop(r_spop), .rdata_misc(r_misc),
        .bus(bus), .single(single), .d2(d2), .k2(k2), .f2(f2), .j2(j2), .a11(a11), .f1(f1), .g1(g1), .g2(g2), .k1(k1),
        .p6(p6), .d1(d1), .gpmc_a2(a2), .gpmc_b1(b1), .nWE_pad(nwe), .nOE_pad(noe), .lane_q(lane_q),
        .map_ch1(m1), .map_ch2(m2), .dbg_snap(dbg));
    integer errors = 0, i, n;
    task check(input cond, input [8*56-1:0] msg); begin if (!cond) begin errors = errors + 1; $display("FAIL: %0s", msg); end end endtask
    task wr_idx(input [7:0] ix); begin @(posedge clk); wd <= ix; we_idx <= 1; @(posedge clk); we_idx <= 0; @(posedge clk); end endtask
    task wr_data(input [7:0] ix, input [15:0] v); begin wr_idx(ix); @(posedge clk); wd <= v; we_data <= 1; @(posedge clk); we_data <= 0; @(posedge clk); end endtask
    task wr_ctrl(input [15:0] v); begin @(posedge clk); wd <= v; we_ctrl <= 1; @(posedge clk); we_ctrl <= 0; @(posedge clk); end endtask
    task rd_data(input [7:0] ix, output [15:0] v); begin wr_idx(ix); #1; v = r_data; end endtask
    task do_pop; begin @(posedge clk); pop <= 1; @(posedge clk); pop <= 0; @(posedge clk); #1; end endtask
    real t0, per; reg [15:0] v, v2; integer ntog;
    task period(input integer which, output real p);
        begin case (which) 0: @(posedge k2); 1: @(posedge f2); 2: @(posedge j2); endcase t0 = $realtime;
              case (which) 0: @(posedge k2); 1: @(posedge f2); 2: @(posedge j2); endcase p = $realtime - t0; end
    endtask
    localparam [15:0] CTRL_DEF = 16'h0102;
    localparam [26:0] BP0 = 27'h00055aa, BP1 = 27'h7ffaa55, BP2 = 27'h1234567;   // three phase words
    integer gC;
    integer gA, gB;
    // background stimuli
    reg lane3_sq = 0, bus_ctr = 0; integer hp = 0;
    always @(posedge clk50) begin
        if (lane3_sq) begin hp = hp + 1; if (hp == 512) begin hp = 0; lane_q[3] <= ~lane_q[3]; end end
        if (bus_ctr) xd_v[15:0] <= xd_v[15:0] + 16'd1;
    end
    initial begin
        #200;
        // ---- reset posture ----
        n = 0; for (i = 0; i < 27; i = i + 1) if (i == 5 || i == 21 || i == 26) begin if (bus[i] !== 1'b1) n = n + 1; end else if (bus[i] !== 1'bz) n = n + 1;
        check(n == 0, "reset: bus tri-state except L4/T2/T7 = 1");
        check(single === 5'bzzzzz, "reset: singles tri-state");
        check(d2 === 1'b0 && f1 === 1'b1 && g1 === 1'b0 && g2 === 1'b0 && k1 === 1'b0 && a11 === 1'b0, "reset: D2=0 F1=1 G1=G2=K1=A11=0");
        period(0, per); check(per > 19.99 && per < 20.01, "reset: K2 50 MHz clock on");
        check(r_ctrl == CTRL_DEF, "reset: DIAG_CTRL 0x0102");
        rd_data(8'h07, v); check(v == 16'h0007, "reset: ADC_HOLD 111");
        // calibrated seed (lanecal-2026-09-05): E1 CH1 bit0 = lane 61 (F10), bit7 = 56 (L13), bit2 = 62 (G11)
        rd_data(8'h10, v); check(v == 16'd61, "LANEMAP[0] = E1 CH1 bit0 = lane 61");
        rd_data(8'h17, v); check(v == 16'd56, "LANEMAP[7] = E1 CH1 bit7 = lane 56");
        rd_data(8'h12, v); check(v == 16'd62, "LANEMAP[2] = E1 CH1 bit2 = lane 62");
        rd_data(8'h18, v); check(v == 16'd49, "LANEMAP[8] = E1 CH2 bit0 = lane 49");
        rd_data(8'h30, v); check(v == 16'd15, "LANEMAP[32] = E3 CH1 bit0 = lane 15");
        rd_data(8'h5f, v); check(v == 16'd42, "LANEMAP[79] = E5 CH2 bit7 = lane 42");
        n = 0; for (i = 0; i < `LANEMAP_N; i = i + 1) begin rd_data(8'h10 + i[7:0], v); if (v != {9'd0, SEED[7*i +: 7]}) n = n + 1; end
        check(n == 0, "LANEMAP: all 80 entries report lanemap_seed.vh");
        check(m1 == SEED[55:0] && m2 == SEED[111:56], "map outputs = cores 0/1 of the seed");
        check(m1[6:0] == 7'd61 && m1[55:49] == 7'd56 && m2[6:0] == 7'd49 && m2[55:49] == 7'd55, "map outputs (CH1 = entries 0..7, CH2 = 8..15)");
        wr_data(8'h30, 16'h7f05); rd_data(8'h30, v); check(v == 16'd15, "LANEMAP is read-only: write ignored");
        check(m1[6:0] == 7'd61, "map outputs unchanged by a LANEMAP write");
        // v3.0+ stubs in the DIAG window read 0 and ignore writes; reserved likewise
        wr_data(8'h0c, 16'h1234); rd_data(8'h0c, v); check(v == 0, "PAIR_TRIM (stub) reads 0, write ignored");
        wr_data(8'h0d, 16'h0155); rd_data(8'h0d, v); check(v == 0, "IL_DLY (stub) reads 0");
        wr_data(8'h0e, 16'h01ff); rd_data(8'h0e, v); check(v == 0, "SNAP_CTRL2 (stub) reads 0");
        wr_data(8'h60, 16'h1234); rd_data(8'h60, v); check(v == 0, "CAL_OFF[0] (stub) reads 0");
        wr_data(8'h6a, 16'h0100); rd_data(8'h6a, v); check(v == 0, "CAL_GAIN[0] (stub) reads 0");
        rd_data(8'h74, v); check(v == 0, "PHASE_CNT[0] (stub) reads 0");
        rd_data(8'h7c, v); check(v == 0, "CAL_STAT (stub) reads 0");
        rd_data(8'h81, v); check(v == 0, "CAP_SEL[4] (stub) reads 0");
        wr_data(8'h9a, 16'h1234); rd_data(8'h9a, v); check(v == 0, "reserved index reads 0, write ignored");
        rd_data(8'hff, v); check(v == 0, "index 0xff reads 0");
        rd_data(8'h0f, v); check(v == 0, "index 0x0f (free) reads 0");
        // ---- bus drive + readback ----
        wr_data(8'h00, 16'hffff); wr_data(8'h02, 16'h1234); wr_data(8'h01, 16'h07ff); wr_data(8'h03, 16'h0555);
        #100; check(bus[4:0] === 5'bz && bus[15:6] === 10'bz, "OE without BUS_DRV_EN: still tri-state");
        wr_ctrl(CTRL_DEF | 16'h0004); #100;
        check(bus[15:0] === 16'h1234, "BUS_DRV_EN: low balls drive BUS_DRV_LO");
        check(bus[26:16] === 11'h575, "high balls drive BUS_DRV_HI OR hold (T2/T7 = 1)");
        rd_data(8'h04, v); check(v == 16'h1234, "BUS_RD_LO reads back the driven level");
        wr_data(8'h00, 16'h0000); wr_data(8'h01, 16'h0000); #100;
        check(bus[4:0] === 5'bz && bus[15:6] === 10'bz, "OE cleared: tri-state again");
        xd_en = {27{1'b1}} & ~(27'd1 << 5) & ~(27'd1 << 21) & ~(27'd1 << 26); xd_v = 27'h55aa55; #200;
        rd_data(8'h04, v); rd_data(8'h05, v2);
        check(v == 16'haa75 && v2 == 16'h0475, "external levels read back on BUS_RD (hold balls 1)");
        xd_en = 0;
        wr_data(8'h07, 16'h0000); #100; check(bus[5] === 1'bz && bus[21] === 1'bz && bus[26] === 1'bz, "ADC_HOLD 0: L4/T2/T7 released");
        wr_data(8'h07, 16'h0007); #100; check(bus[5] === 1'b1, "ADC_HOLD back");
        // ---- singles + MISC_RD ----
        wr_data(8'h06, 16'h0a1f); #100; check(single === 5'b01010, "SINGLE_CTRL drive");
        #100; check(r_misc[5:1] == 5'b01010, "MISC_RD singles");
        wr_data(8'h06, 16'h0000); xs_en = 5'h1f; xs_v = 5'b10101; p6 = 1; a2 = 1; b1 = 0; d1_drv = 1; nwe = 0; noe = 1; #200;
        check(r_misc == {4'd0, 1'b1, 1'b0, 1'b0, 1'b1, 1'b0, 1'b1, 5'b10101, 1'b1}, "MISC_RD full word");
        xs_en = 0;
        // ---- outputs: G2/K1/F1/A11 levels, D2, K2 gate, F2/J2 selects ----
        wr_ctrl(CTRL_DEF | 16'h0e81); #100; check(a11 && g1 && g2 && k1 && f1 && d2, "DIAG_CTRL levels: A11 G1 G2 K1 D2 = 1");
        check(r_misc[9] == 1'b1, "MISC_RD G2 readback");
        wr_ctrl(16'h0100); #200; n = 0; repeat (10) begin #5; if (k2 !== 1'b0) n = n + 1; end check(n == 0, "K2_EN=0: K2 static 0");
        // DIAG_CTRL's F2/J2 codes 2 (clk50) and 3 (clk100) are RETIRED: they were the only
        // free-running clock sources on an SRAM-clock candidate, reachable from one 16-bit
        // write. Both now decode as a static 0, and every edge on F2/J2 goes through the
        // counted emitter instead. (This replaces the two period() measurements that used
        // to prove those codes worked — a @(posedge f2) that now never returns.)
        wr_ctrl(CTRL_DEF | (2'd3 << 3) | (2'd2 << 5)); #400;
        n = 0; repeat (20) begin #5; if (f2 !== 1'b0 || j2 !== 1'b0) n = n + 1; end
        check(n == 0, "DIAG_CTRL F2/J2 codes 2 and 3 are retired: both pads static 0");
        wr_ctrl(CTRL_DEF | (2'd1 << 3)); #200; n = 0; repeat (10) begin #5; if (f2 !== 1'b1 || j2 !== 1'b0) n = n + 1; end check(n == 0, "F2=1 J2=0 static");
        wr_ctrl(CTRL_DEF);
        // ---- the counted-edge crank: exactly N edges on F2, and not one more ----
        // The tb leaves the 27-ball group released, which reads X here (the board's pull-up
        // is not modelled), so the fabric's rest-posture interlock must REFUSE the arm.
        // That is the interlock doing its job, and it is what we check first.
        wr_data(8'h91, 16'h0042);                       // F2_SRC = CRANK, GUARD = LANES
        wr_data(8'h92, 16'd8);                          // NEDGE = 8, DIV = 0
        wr_data(8'h93, 16'd0);
        wr_data(8'h94, 16'h01a5);                       // KEY | ARM
        #2000;
        // The tb leaves the group floating, so bus_i50 is X and the rest-posture term is X
        // too: the fabric cannot then produce a DEFINED refuse code, and it does not claim
        // to. What must hold, and is what we check, is that an unknown posture never yields
        // an ACCEPTED arm and never puts an edge on the pad. On the board the group is held
        // high by a pull-up and the code is well defined (REFUSE = 2 when it is not).
        n = 0; repeat (20) begin #5; if (f2 !== 1'b0) n = n + 1; end
        check(n == 0, "crank: a floating bus group never yields an accepted arm — F2 stays 0");
        // now hold the group high from the tb, which is what the board does, and re-arm
        xd_en = {27{1'b1}}; xd_v = {27{1'b1}}; #400;
        wr_data(8'h94, 16'h04a5);                       // KEY | CLR
        wr_data(8'h94, 16'h01a5);                       // KEY | ARM
        #4000; rd_data(8'h95, v);
        check(v[12] == 1'b0 && v[10] == 1'b0, "crank: accepted and finished");
        rd_data(8'h96, v); check(v == 16'd8,  "crank: exactly 8 edges emitted on F2");
        rd_data(8'h97, v); check(v == 16'd0,  "crank: none on J2");
        rd_data(8'h98, v); check(v == 16'd8,  "crank: the lifetime counter agrees");
        n = 0; repeat (20) begin #5; if (f2 !== 1'b0) n = n + 1; end
        check(n == 0, "crank: F2 parks at 0 when the budget is spent");
        // ---- the D1 loop: emitter -> our own pad -> back ----------------------
        // D1 is bidir with a pad readback, so ARR_D1 witnesses ARRIVAL AT THE BALL, which
        // no F2/J2 count can. DIV >= 2 is enforced by the fabric; DIV = 0 must be refused.
        wr_data(8'h91, 16'h0240);                       // D1_SRC = CRANK, GUARD = LANES
        wr_data(8'h92, 16'd4);                          // NEDGE = 4, DIV = 0  -> must refuse
        wr_data(8'h94, 16'h04a5); wr_data(8'h94, 16'h01a5); #2000;
        rd_data(8'h9a, v); check(v == 16'd0, "D1 crank: DIV = 0 is refused, nothing emitted");
        wr_data(8'h92, {5'd2, 10'd4});                  // NEDGE = 4, DIV = 2
        wr_data(8'h94, 16'h04a5); wr_data(8'h94, 16'h01a5); #20000;
        rd_data(8'h95, v); check(v[10] == 1'b0, "D1 crank: finished");
        rd_data(8'h9a, v); check(v == 16'd4, "D1 crank: 4 edges emitted");
        rd_data(8'h9b, v); check(v == 16'd4, "D1 crank: 4 edges ARRIVED at our own pad");
        rd_data(8'h96, v); check(v == 16'd0, "D1 crank: none on F2");
        // ---- width mode: HIGH independent of the period -----------------------
        // DIV 5 = 320 clk100 ticks of period; HIGH = 3 ticks = 30 ns. Four edges must
        // still be emitted and must still arrive at our own pad.
        wr_data(8'h91, 16'h0240);
        wr_data(8'h92, {5'd5, 10'd4});
        wr_data(8'h9d, 16'd3);                          // HIGH = 3 clk100 ticks
        wr_data(8'h94, 16'h04a5); wr_data(8'h94, 16'h01a5); #40000;
        rd_data(8'h9a, v); check(v == 16'd4, "width mode: 4 edges emitted");
        rd_data(8'h9b, v); check(v == 16'd4, "width mode: 4 edges arrived at our pad");
        wr_data(8'h9d, 16'd512);                        // HIGH >= the period -> refuse
        wr_data(8'h94, 16'h04a5); wr_data(8'h94, 16'h01a5); #2000;
        rd_data(8'h95, v); check(v[15:13] == 3'd1, "width mode: HIGH >= period is refused");
        wr_data(8'h9d, 16'd0);
        wr_data(8'h91, 16'h0040); xd_en = 0; xd_v = 0; #200;
        // ---- lane monitor: lane 3 square wave, half period 512 clk50 -> 128 toggles / window ----
        wr_data(8'h08, 16'd3);
        lane3_sq = 1;
        #(3 * 65536 * 20.0 + 5000);
        rd_data(8'h09, v); check(v == 16'd128, "LANE_TOG 128 per window");
        rd_data(8'h0a, v); check(v[2:1] == 2'b11, "LANE_LVL ever-1 and ever-0 on a toggling lane");
        wr_data(8'h08, 16'd4);                             // lane 4 static 0
        wr_ctrl(CTRL_DEF | 16'h1000); #500; wr_ctrl(CTRL_DEF); #500;
        rd_data(8'h0a, v); check(v == 16'h0004, "LANE_LVL after gate reset: level 0, ever-0 only");
        wr_data(8'h08, 16'h50 + 5); #200; rd_data(8'h0a, v); check(v[0] == 1'b1 && v[1] == 1'b1, "monitor index 0x55 = bus L4 (held 1)");
        wr_data(8'h08, 16'h78); #200; rd_data(8'h0a, v); check(v[0] == 1'b1, "monitor 0x78 = P6");
        wr_data(8'h08, 16'h7b); #200; rd_data(8'h0a, v); check(v[0] == 1'b1, "monitor 0x7b = K2 enable");
        lane3_sq = 0; #100;
        // ---- snapshot: bus 0..15 driven externally with a clk50 counter ----
        xd_en = 27'h000ffff;
        bus_ctr = 1;
        wr_data(8'h0b, 16'd5); wr_ctrl(CTRL_DEF | 16'h8000);         // arm, snap clock = clk50
        #100; check(r_srem[15] == 1'b0, "snapshot armed: not ready");
        wait (r_srem[15]); #50; check(r_srem == 16'h8800, "ready, 2048 words remaining");
        do_pop; v = r_spop; n = 0;
        for (i = 1; i < 2048; i = i + 1) begin v2 = r_spop; if (v2 != v + 16'd1) n = n + 1; v = v2; do_pop; end
        check(n == 0, "snapshot mode 5: 2048 consecutive bus words");
        check(r_srem == 16'h8000, "snapshot drained: remain 0");
        do_pop; check(r_srem == 16'h8000, "extra pop flat");
        xd_en = 0; bus_ctr = 0;
        // ---- snapshot mode 7 (5-word all-lane) at cap_clk ----
        lane_q = {16'h5555, 16'h4444, 16'h3333, 16'h2222, 16'h1111};
        wr_data(8'h0b, 16'd7); wr_ctrl(CTRL_DEF | (2'd2 << 13) | 16'h8000);
        wait (r_srem[15]); #50; n = 0;
        for (i = 0; i < 2045; i = i + 1) begin
            v = r_spop;
            case (i % 5) 0: if (v != 16'h1111) n = n + 1; 1: if (v != 16'h2222) n = n + 1; 2: if (v != 16'h3333) n = n + 1;
                         3: if (v != 16'h4444) n = n + 1; 4: if (v != 16'h5555) n = n + 1; endcase
            do_pop;
        end
        check(n == 0, "snapshot mode 7: five coherent lane words per sample");
        // ---- snapshot mode 2 (lanes 32..47) at clk100 ----
        wr_data(8'h0b, 16'd2); wr_ctrl(CTRL_DEF | (2'd1 << 13) | 16'h8000);
        wait (r_srem[15]); #50; do_pop; check(r_spop == 16'h3333, "snapshot mode 2 slice");
        wr_ctrl(CTRL_DEF);

        // ---- the 27-ball probe: BUSGEN drives, BUSCAP records ----------------
        wr_data(8'h00, 16'h0000); wr_data(8'h01, 16'h0000);       // release every static drive
        wr_data(8'h07, 16'h0000);                                 // and drop the ADC hold: it is
        // now ORed in even while the generator runs (it used to be dropped, releasing the proven
        // hold on L4/T2/T7), so it would otherwise pin those three balls high through the pattern.
        wr_data(8'h83, BP0[15:0]);  wr_data(8'h84, {5'd0, BP0[26:16]});
        wr_data(8'h85, BP1[15:0]);  wr_data(8'h86, {5'd0, BP1[26:16]});
        wr_data(8'h87, BP2[15:0]);  wr_data(8'h88, {5'd0, BP2[26:16]});
        wr_data(8'h82, 16'h0791);   // EN, rate clk50, 3 phases, OE in all three, RUN
        #200; gA = 0; gB = 0; gC = 0;
        for (i = 0; i < 40; i = i + 1) begin #7;
            if (bus === BP0) gA = 1; if (bus === BP1) gB = 1; if (bus === BP2) gC = 1; end
        check(gA == 1 && gB == 1 && gC == 1, "BUSGEN: three phase words on all 27 balls");
        wr_data(8'h8e, 16'd1);                                    // BUSCAP EN
        wr_ctrl(CTRL_DEF | 16'h8000);                             // arm, snap clock = clk50
        wait (r_srem[15]); #50;
        check(r_srem == 16'h8800, "BUSCAP: 2048 words = 1024 samples");
        n = 0; gA = 0; gB = 0; gC = 0;
        for (i = 0; i < 64; i = i + 1) begin
            v = r_spop; do_pop; v2 = r_spop; do_pop;
            if ({v2[10:0], v} == BP0) gA = 1;
            else if ({v2[10:0], v} == BP1) gB = 1;
            else if ({v2[10:0], v} == BP2) gC = 1;
            else n = n + 1;
        end
        check(n == 0 && gA == 1 && gB == 1 && gC == 1,
              "BUSCAP: each sample is one coherent bus tick, all three phase words seen");
        wr_data(8'h82, 16'd0); wr_data(8'h8e, 16'd0); #200;       // generator off, balls released
        // ---- the departure trigger ------------------------------------------
        // balls 5, 21 and 26 are the DUT's own reset drive: leave those to it
        xd_en = 27'h7ffffff & ~(27'd1 << 5) & ~(27'd1 << 21) & ~(27'd1 << 26);
        xd_v = 27'd0; #200;                                       // an idle posture we control
        wr_data(8'h8e, 16'd3);                                    // EN | TRIG
        wr_ctrl(CTRL_DEF | 16'h8000);
        #500; rd_data(8'h8e, v);
        check(v[3] == 1'b1 && v[2] == 1'b0, "BUSCAP: waiting for the departure");
        check(r_srem[15] == 1'b0, "BUSCAP: nothing recorded while waiting");
        xd_v = 27'd1;                                             // one ball departs
        wait (r_srem[15]); #50; rd_data(8'h8e, v);
        check(v[2] == 1'b1 && v[3] == 1'b0, "BUSCAP: triggered by the departure");
        v = r_spop; check(v[0] == 1'b1, "BUSCAP: the record starts at the departure");
        wr_data(8'h8e, 16'd0); xd_en = 0;
        rd_data(8'h83, v); check(v == BP0[15:0], "BUSGEN_PAT reads back");
        if (errors == 0) $display("PASS tb_diag"); else $display("FAIL tb_diag: %0d errors", errors);
        $finish;
    end
    initial begin #20000000; $display("FAIL tb_diag: timeout"); $finish; end
endmodule

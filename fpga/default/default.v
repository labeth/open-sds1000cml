// ENGMODEL-OWNER-UNIT: FU-RTL-DEFAULT-DEFAULT
// default.v -- default_top: THE default acq2 image for the SDS1102CML+ acquisition FPGA
// (Cyclone IV E EP4CE10F17C8). Pins per fpga/default/default.qsf (GENERATED from the contract);
// register map per fpga/default/regs.vh + regmux.vh (GENERATED from the codegen schema
// sds1000cml-default v3: 128 selectors, sel[1:0] = GPMC A2/A1 on balls A2/B1). Never hand-define
// a selector here: regmux.vh owns the decode. This is the v2.2 image (06-TIERS s7 step 1): the
// v2.1 datapath, the C2-side v3 additions (IL_CTRL.TSRC, DRAIN_START/LEN, OP_REWIND, DRAIN_STAT,
// POP_MON, read-only LANEMAP from lanemap_seed.vh); every v3.0+ register is a stub (reads 0,
// writes ignored, no strobe -- regmux.vh) and IL_CTRL stores only TSRC.
//
//   gpmc_slave  (C2)      16-bit CS1 slave, one tri-state driver, we_commit / rd_pop strobes
//   pll_m2      (M2)      200 MHz c0 = ph0 (the encode clock; c1..c3 unused since v2.2) + capture clock
//                         c4 (step engine idle: CAP_STEP retired), 100 / 50 MHz
//   adc_front   (cap_clk) encode pairs (every pair on ph0), 80 lane registers, baked E1 map -> samp/samp_tick
//   capture     (cap_clk) decimator + 20480x16 M9K ring + trigger + TSRC substitution; read port on clk
//   drain       (C2)      BURST pop pointer over the drain window, BURST_REMAIN, REWIND, DRAIN_STAT, POP_MON
//   diag        (mixed)   27-ball bus, singles, MAX-V outputs, monitor, snapshot, DIAG window (LANEMAP read-only)
//
// CDC summary (every crossing listed; see default.sdc):
//   C2 -> cap_clk : OPCODE pulses (sync_pulse); configuration words (quasi-static, re-registered)
//   cap_clk -> C2 : status bits (sync_bit), done event (sync_pulse), {wrote_count, wr_ptr}
//                   (sync_word), frozen record words (read only after done -- quasi-static)
//   C2 -> cap_clk : drain read pointer (sync_word) for the stream overflow check
//   M2 -> C2      : CLK_STAT ratio (gated count, sync_word); PLL lock (sync_bit)
//   the record M9K and the snapshot M9K are dual-clock RAMs (write fast, read on C2)
//
// DBG (0x7c) layout: [1:0] capture state (0 idle 1 fill 2 halt) [2] triggered [3] valid
//   [4] stream_on [5] cap-clock phase-step engine busy [6] phase-step error (sticky)
//   [7] snapshot ready [8] snapshot running [9] PLL A locked [10] PLL B locked
//   [11] encode enabled (cap domain) [15:12] cap-clock steps applied, low nibble
// TRIGPOS_HI: [14:0] IDX = index of the trigger word in the drained record; bit 15 = 1 when, in
//   single-channel mode, the trigger sample is the FIRST (high-byte) sample of that word.
// FILL: wrote_count (words; saturates at REC_DEPTH) while a record is frozen or filling; in
//   stream mode it is the live write pointer.
// IL_CTRL: only TSRC[3:2] is stored (reads back), every other field reads 0 (v3.0+).
// RUN: MODE/RUN/STREAM/CHMODE are stored; STACK[6] and REDUCE[7] read 0 (v3.0+), bits 15:8 too.
// DRAIN_START stores IDX[14:0] (bit 15 reads 0), DRAIN_LEN all 16 bits; both are live registers
//   the drain latches at DONE and at OP_REWIND (drain.v header). OP_STACK_CLEAR is decoded and
//   ignored (v3.1).

`timescale 1ns/1ps
`include "regs.vh"

// TRLC-LINKS: REQ-SDS-060, REQ-SDS-194, REQ-SDS-195, REQ-SDS-196, REQ-SDS-197
module default_top (
    input  wire        clk,            // C2, GPMC domain (~80 MHz)
    input  wire        mclk_in,        // M2, PLL reference (~160 MHz)
    inout  wire [15:0] gpmc_d,
    input  wire        nCS1,
    input  wire        nOE,
    input  wire        nWE,
    input  wire [6:2]  sel,            // A3..A7 (declared [6:2]: the QSF has no sel[1:0] balls)
    input  wire        trig_sense,     // A12
    input  wire        gpmc_a2,        // ball A2 = GPMC A2 = selector bit 1 (R0 report)
    input  wire        gpmc_b1,        // ball B1 = GPMC A1 = selector bit 0
    output wire [4:0]  enc_p,
    output wire [4:0]  enc_n,
    output wire        a11,
    output wire        f1,
    output wire        g1,
    output wire        g2,
    output wire        k1,
    output wire        d2,
    input  wire        p6,
    // B11 and J1: input-class balls the factory image reads and this branch has
    // never listened to. MAXV-INTERFACE.md s4 makes B11 "the only far-end-ish
    // input in the loop" -- XNOR-compared against two CPU-written registers, its
    // transition releasing the path that terminates the burst window -- and s6
    // records that driving/observing it is "the first stimulus the 79-condition
    // sweep never tried, since all of them drove the bus balls and left B11
    // alone". Wired as INPUTS ONLY: we listen, we never drive, so no posture of
    // ours can contend with whatever the board puts on them.
    input  wire        b11,
    input  wire        j1,
    output wire        k2,
    output wire        f2,
    output wire        j2,
    inout  wire [26:0] bus,
    inout  wire [4:0]  single,
    inout  wire        d1,
    input  wire [79:0] lane
);
    localparam [15:0] REC_DEPTH16   = `REC_DEPTH;
    localparam [15:0] PRETRIG_MAX16 = `PRETRIG_MAX;

    // =====================================================================
    // clocks
    // =====================================================================
    wire ph0, ph90, ph180, ph270, cap_clk, clk100, clk50, locked_a_raw, locked_b_raw;
    wire [7:0] step_applied;
    wire       step_busy, step_err;
    wire [7:0] cap_step  = 8'd0;                   // CAP_STEP retired (06-TIERS s1.7): reset alignment
    // ph90/ph180/ph270 (c1..c3) have no load since v2.2: every encode pair is clocked by ph0
    // (adc_front.v header); the PLL keeps the counters so pll_m2 / tb_pll are unchanged.
    pll_m2 u_pll (
        .mclk_in(mclk_in), .ph0(ph0), .ph90(ph90), .ph180(ph180), .ph270(ph270), .cap_clk(cap_clk),
        .clk100(clk100), .clk50(clk50), .locked_a(locked_a_raw), .locked_b(locked_b_raw),
        .scanclk(clk), .step_target(cap_step), .step_applied(step_applied),
        .step_busy(step_busy), .step_err(step_err)
    );
    wire locked_a, locked_b;
    sync_bit #(.W(2)) u_lock (.clk(clk), .d({locked_b_raw, locked_a_raw}), .q({locked_b, locked_a}));

    // =====================================================================
    // GPMC slave + generated decode
    // =====================================================================
    wire        we_commit, rd_pop, drive_active;
    wire [7:0]  wr_sel, rd_sel;
    wire [15:0] wr_data;
    wire [1:0]  wr_aux;
    wire [15:0] rdata_CLK_STAT, rdata_DIAG_IDX, rdata_DIAG_DATA, rdata_RUN;
    wire [15:0] rdata_DECIM_LO, rdata_DECIM_HI, rdata_PRETRIG_LO, rdata_PRETRIG_HI;
    wire [15:0] rdata_POSTTRIG_LO, rdata_POSTTRIG_HI, rdata_BURST, rdata_BURST_REMAIN;
    wire [15:0] rdata_STATUS_A, rdata_TRIGPOS_LO, rdata_TRIGPOS_HI, rdata_FILL;
    wire [15:0] rdata_ACQ_CTRL, rdata_TRIG_LEVEL, rdata_SNOOP_SEL, rdata_SNOOP_DATA;
    wire [15:0] rdata_DIAG_CTRL, rdata_SNAP_REMAIN, rdata_SNAP_POP, rdata_MISC_RD;
    wire [15:0] rdata_ENV_DATA, rdata_DBG;
    wire [15:0] rdata_IL_CTRL, rdata_DRAIN_START, rdata_DRAIN_LEN, rdata_DRAIN_STAT, rdata_POP_MON;

    `include "regmux.vh"

    gpmc_slave u_gpmc (
        .clk(clk), .nCS1(nCS1), .nOE(nOE), .nWE(nWE), .sel({sel, gpmc_a2, gpmc_b1}), .gpmc_d(gpmc_d),
        .we_commit(we_commit), .wr_sel(wr_sel), .wr_data(wr_data), .wr_aux(wr_aux),
        .rd_pop(rd_pop), .rd_sel(rd_sel), .rdata(rmux_rdata), .drive_active(drive_active)
    );

    // =====================================================================
    // host registers (C2)
    // =====================================================================
    reg [15:0] run_word   = 16'h0000;
    reg [31:0] decim_reg  = 32'd1;
    reg [31:0] pretrig_reg = 32'd10240;
    reg [31:0] posttrig_reg= 32'd10240;
    reg [15:0] acq_ctrl   = 16'h7c00;      // encode off, all five pairs enabled
    reg [15:0] trig_level = 16'h0080;
    reg [6:0]  snoop_cnt  = 7'd0;
    reg [6:0]  snoop_sel  = 7'd0;
    reg [1:0]  snoop_aux  = 2'b00;
    reg [15:0] snoop_data = 16'h0000;
    reg [1:0]  il_tsrc    = `IL_CTRL_TSRC_ADC;         // IL_CTRL.TSRC (the only stored field)
    reg [`ADDR_W-1:0] drain_start = {`ADDR_W{1'b0}};   // DRAIN_START.IDX
    reg [15:0] drain_len  = 16'h0000;                  // DRAIN_LEN.LEN (0 = to the record end)
    // RUN keeps only the fields v2.2 implements; STACK / REDUCE are v3.0+ stubs and read 0, the
    // same way IL_CTRL stores only TSRC.
    localparam [15:0] RUN_LIVE = `RUN_MODE_MASK | `RUN_RUN_MASK | `RUN_STREAM_MASK | `RUN_CHMODE_MASK;

    always @(posedge clk) begin
        if (we_RUN)         run_word          <= wr_data & RUN_LIVE;
        if (we_IL_CTRL)     il_tsrc           <= wr_data[`IL_CTRL_TSRC_LSB +: 2];
        if (we_DRAIN_START) drain_start       <= wr_data[`DRAIN_START_IDX_LSB +: `ADDR_W];
        if (we_DRAIN_LEN)   drain_len         <= wr_data;
        if (we_DECIM_LO)    decim_reg[15:0]   <= wr_data;
        if (we_DECIM_HI)    decim_reg[31:16]  <= wr_data;
        if (we_PRETRIG_LO)  pretrig_reg[15:0] <= wr_data;
        if (we_PRETRIG_HI)  pretrig_reg[31:16]<= wr_data;
        if (we_POSTTRIG_LO) posttrig_reg[15:0]<= wr_data;
        if (we_POSTTRIG_HI) posttrig_reg[31:16]<= wr_data;
        if (we_ACQ_CTRL)    acq_ctrl          <= wr_data;
        if (we_TRIG_LEVEL)  trig_level        <= wr_data;
        if (we_commit) begin                             // SNOOP: every CS1 write
            snoop_cnt  <= snoop_cnt + 7'd1;
            snoop_sel  <= wr_sel[6:0];
            snoop_aux  <= wr_aux;
            snoop_data <= wr_data;
        end
    end

    wire       run_en    = run_word[`RUN_RUN_LSB];
    wire       mode_norm = (run_word[`RUN_MODE_LSB +: 2] == 2'd1);
    wire       stream_on = run_word[`RUN_STREAM_LSB];
    wire [1:0] chmode    = run_word[`RUN_CHMODE_LSB +: 2];
    wire       enc_en    = acq_ctrl[`ACQ_CTRL_ENC_EN_LSB];
    wire [1:0] enc_rate  = acq_ctrl[`ACQ_CTRL_ENC_RATE_LSB +: 2];
    wire [3:0] trig_hyst = acq_ctrl[`ACQ_CTRL_TRIG_HYST_LSB +: 4];
    wire       trig_src  = acq_ctrl[`ACQ_CTRL_TRIG_SRC_LSB];
    wire       trig_slope= acq_ctrl[`ACQ_CTRL_TRIG_SLOPE_LSB];
    wire [4:0] pair_en   = acq_ctrl[`ACQ_CTRL_PAIR_EN_LSB +: 5];
    wire [7:0] trig_lvl  = trig_level[`TRIG_LEVEL_LEVEL_LSB +: 8];
    wire       hw_sel    = trig_level[`TRIG_LEVEL_HW_SEL_LSB];

    // ---- arm-time working depths in WORDS (single-channel mode packs 2 samples/word) ----
    reg [`ADDR_W-1:0] pre_w = 15'd10240, post_w = 15'd10240;
    reg [31:0]        decim_m1 = 32'd0;
    wire [31:0] pre_s  = (chmode == 2'd0) ? pretrig_reg  : {1'b0, pretrig_reg[31:1]};
    wire [31:0] post_s = (chmode == 2'd0) ? posttrig_reg : {1'b0, posttrig_reg[31:1]};
    wire [15:0] pre_c  = (pre_s  > {16'd0, PRETRIG_MAX16}) ? PRETRIG_MAX16 : pre_s[15:0];
    wire [15:0] post_room = PRETRIG_MAX16 - pre_c;                   // depth-2-pre
    wire [15:0] post_c = (post_s > {16'd0, post_room}) ? post_room : post_s[15:0];
    always @(posedge clk) begin
        pre_w    <= pre_c[`ADDR_W-1:0];
        post_w   <= (post_c == 16'd0) ? 15'd1 : post_c[`ADDR_W-1:0];
        decim_m1 <= (decim_reg == 32'd0) ? 32'd0 : (decim_reg - 32'd1);
    end

    // =====================================================================
    // opcode pulses into the capture domain
    // =====================================================================
    wire arm_c, halt_c, rst_c;
    sync_pulse u_arm  (.clk_src(clk), .pulse_src(op_GO & run_en), .clk_dst(cap_clk), .pulse_dst(arm_c));
    sync_pulse u_halt (.clk_src(clk), .pulse_src(op_HALT),        .clk_dst(cap_clk), .pulse_dst(halt_c));
    sync_pulse u_rst  (.clk_src(clk), .pulse_src(op_RESET),       .clk_dst(cap_clk), .pulse_dst(rst_c));
    // The pulses reach ~150 flops in capture at 200 MHz: re-register them here and let synthesis
    // duplicate the register (maxfan) so no copy drives more than 24 loads (fit iteration 8).
    (* maxfan = 24 *) reg arm_cq  = 1'b0;
    (* maxfan = 24 *) reg halt_cq = 1'b0;
    (* maxfan = 24 *) reg rst_cq  = 1'b0;
    always @(posedge cap_clk) begin arm_cq <= arm_c; halt_cq <= halt_c; rst_cq <= rst_c; end

    // =====================================================================
    // ADC front end + capture
    // =====================================================================
    wire [79:0] lane_q;
    wire [15:0] samp;
    wire        samp_tick;
    wire [3:0]  enc_dbg;
    wire [55:0] map_ch1, map_ch2;                   // the baked E1 map (constants from diag)
    adc_front u_adc (
        .cap_clk(cap_clk), .ph0(ph0),
        .lane(lane), .enc_p(enc_p), .enc_n(enc_n),
        .enc_en(enc_en), .enc_rate(enc_rate), .pair_en(pair_en),
        .map_ch1(map_ch1), .map_ch2(map_ch2),
        .lane_q(lane_q), .samp(samp), .samp_tick(samp_tick), .dbg(enc_dbg)
    );

    wire              c_filling, c_armed, c_triggered, c_done, c_valid, c_overflow, c_done_evt;
    wire [`ADDR_W-1:0] c_wr_ptr, c_rec_start, c_trig_idx, rd_addr, rd_ptr, rd_ptr_c;
    wire [15:0]       c_wrote, c_rec_len, c_trig_frac, rec_rd_data;
    wire              c_trig_half;
    wire [1:0]        c_state;
    capture u_cap (
        .cap_clk(cap_clk), .samp(samp), .samp_tick(samp_tick),
        .arm(arm_cq), .halt(halt_cq), .rst(rst_cq),
        .decim_m1(decim_m1), .pre_w(pre_w), .post_w(post_w),
        .mode_norm(mode_norm), .stream_on(stream_on), .chmode(chmode),
        .trig_src(trig_src), .trig_slope(trig_slope), .trig_level(trig_lvl), .trig_hyst(trig_hyst),
        .hw_sel(hw_sel), .trig_sense(trig_sense), .tsrc(il_tsrc),
        .rd_ptr_s(rd_ptr_c),
        .rd_clk(clk), .rd_addr(rd_addr), .rd_data(rec_rd_data),
        .filling(c_filling), .armed(c_armed), .triggered(c_triggered), .done(c_done), .valid(c_valid),
        .overflow(c_overflow), .wr_ptr(c_wr_ptr), .wrote_count(c_wrote),
        .rec_start(c_rec_start), .rec_len(c_rec_len), .trig_idx(c_trig_idx), .trig_half(c_trig_half),
        .trig_frac(c_trig_frac), .done_evt(c_done_evt), .state_dbg(c_state)
    );

    // ---- capture -> C2 status ----
    wire s_armed, s_triggered, s_done, s_valid, s_overflow;
    wire [1:0] s_state;
    sync_bit #(.W(7)) u_stat (.clk(clk),
        .d({c_state, c_overflow, c_valid, c_done, c_triggered, c_armed}),
        .q({s_state, s_overflow, s_valid, s_done, s_triggered, s_armed}));
    wire [`ADDR_W-1:0] wr_ptr_s;
    wire [15:0]        wrote_s;
    sync_word #(.W(16 + `ADDR_W)) u_wp (.clk_src(cap_clk), .d_src({c_wrote, c_wr_ptr}),
                                         .clk_dst(clk), .q_dst({wrote_s, wr_ptr_s}));
    sync_word #(.W(`ADDR_W)) u_rp (.clk_src(clk), .d_src(rd_ptr), .clk_dst(cap_clk), .q_dst(rd_ptr_c));

    // =====================================================================
    // drain
    // =====================================================================
    drain u_drain (
        .clk(clk), .arm(op_GO & run_en), .rst(op_RESET), .rewind(op_REWIND),
        .valid(s_valid), .stream_on(stream_on),
        .rec_start(c_rec_start), .rec_len(c_rec_len), .wr_ptr_s(wr_ptr_s),
        .drain_start(drain_start), .drain_len(drain_len), .pop(pop_BURST),
        .rd_addr(rd_addr), .rd_ptr(rd_ptr), .remain_word(rdata_BURST_REMAIN),
        .pops(rdata_DRAIN_STAT), .pop_mon(rdata_POP_MON)
    );

    // =====================================================================
    // diagnostics
    // =====================================================================
    wire [1:0] dbg_snap;
    diag u_diag (
        .clk(clk), .clk50(clk50), .clk100(clk100), .cap_clk(cap_clk),
        .we_idx(we_DIAG_IDX), .we_data(we_DIAG_DATA), .we_ctrl(we_DIAG_CTRL), .wr_data(wr_data),
        .pop_snap(pop_SNAP_POP),
        .rdata_idx(rdata_DIAG_IDX), .rdata_data(rdata_DIAG_DATA), .rdata_ctrl(rdata_DIAG_CTRL),
        .rdata_snap_remain(rdata_SNAP_REMAIN), .rdata_snap_pop(rdata_SNAP_POP), .rdata_misc(rdata_MISC_RD),
        .bus(bus), .single(single), .d2(d2), .k2(k2), .f2(f2), .j2(j2),
        .a11(a11), .f1(f1), .g1(g1), .g2(g2), .k1(k1),
        .p6(p6), .b11(b11), .j1(j1), .d1(d1), .gpmc_a2(gpmc_a2), .gpmc_b1(gpmc_b1), .nWE_pad(nWE), .nOE_pad(nOE),
        .lane_q(lane_q), .map_ch1(map_ch1), .map_ch2(map_ch2), .dbg_snap(dbg_snap)
    );

    // =====================================================================
    // CLK_STAT: M2 edges during a 4096-cycle C2 gate, /16, saturating
    // =====================================================================
    reg [11:0] gate_cnt = 12'd0;
    reg        gate     = 1'b0;
    always @(posedge clk) begin
        gate_cnt <= gate_cnt + 12'd1;
        if (gate_cnt == 12'd0) gate <= ~gate;          // 4096 high, 4096 low
    end
    wire gate_m;
    sync_bit u_gate_m (.clk(mclk_in), .d(gate), .q(gate_m));
    reg        gate_m_q = 1'b0;
    reg [15:0] m2_acc = 16'd0, m2_cnt = 16'd0;
    always @(posedge mclk_in) begin
        gate_m_q <= gate_m;
        if (gate_m & ~gate_m_q) m2_acc <= 16'd0;
        else if (gate_m && m2_acc != 16'hffff) m2_acc <= m2_acc + 16'd1;
        if (~gate_m & gate_m_q) m2_cnt <= m2_acc;
    end
    wire [15:0] m2_cnt_s;
    sync_word #(.W(16)) u_m2 (.clk_src(mclk_in), .d_src(m2_cnt), .clk_dst(clk), .q_dst(m2_cnt_s));
    assign rdata_CLK_STAT = {m2_cnt_s[15:4], 2'b00, locked_b, locked_a};

    // =====================================================================
    // read values
    // =====================================================================
    assign rdata_RUN         = run_word;
    assign rdata_DECIM_LO    = decim_reg[15:0];
    assign rdata_DECIM_HI    = decim_reg[31:16];
    assign rdata_PRETRIG_LO  = pretrig_reg[15:0];
    assign rdata_PRETRIG_HI  = pretrig_reg[31:16];
    assign rdata_POSTTRIG_LO = posttrig_reg[15:0];
    assign rdata_POSTTRIG_HI = posttrig_reg[31:16];
    assign rdata_BURST       = (s_valid || stream_on) ? rec_rd_data : 16'h0000;
    assign rdata_STATUS_A    = {11'd0, s_armed, s_overflow, s_done, s_triggered, s_valid};
    assign rdata_TRIGPOS_LO  = c_trig_frac;
    assign rdata_TRIGPOS_HI  = {c_trig_half, c_trig_idx};
    assign rdata_FILL        = stream_on ? {1'b0, wr_ptr_s} : wrote_s;
    assign rdata_ACQ_CTRL    = acq_ctrl;
    assign rdata_TRIG_LEVEL  = trig_level;
    assign rdata_SNOOP_SEL   = {snoop_cnt, snoop_aux, snoop_sel};   // {COUNT, B1, A2, SEL}
    assign rdata_SNOOP_DATA  = snoop_data;
    assign rdata_ENV_DATA    = 16'h0000;                              // reserved (stack readout goes through BURST)
    assign rdata_IL_CTRL     = {12'd0, il_tsrc, 2'b00};               // TSRC only; IL_EN/CAL_EN/REDUCE_MODE/IL_RATE/DIV_PH read 0
    assign rdata_DRAIN_START = {1'b0, drain_start};
    assign rdata_DRAIN_LEN   = drain_len;
    assign rdata_DBG         = {step_applied[3:0], enc_dbg[1], locked_b, locked_a, dbg_snap[1], dbg_snap[0],
                                step_err, step_busy, stream_on, s_valid, s_triggered, s_state};
endmodule

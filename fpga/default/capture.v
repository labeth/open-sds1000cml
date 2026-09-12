// capture.v -- decimator + circular pre/post-trigger record (20480 x 16 in 40 M9K) + trigger,
// entirely in the 200 MHz capture domain; the drain reads the same M9K on the C2 clock.
//
// Shape after owned-fpga fpga/standard/capture.v (exact post-count finalize, atomic write+advance,
// Q16 interpolation by 16-step long division), re-designed for the fast domain and for a WRAPPED
// record: the drain starts at rec_start = trig_word - pre_w (mod depth), so NORM mode may wait for
// a trigger indefinitely without corrupting the pre-trigger history (the prior forced a trigger at
// a "norm bound" to keep the record linear).
//
// GEOMETRY: `REC_DEPTH / `ADDR_W from regs.vh (schema truth). All counts here are in WORDS:
//   dual mode          word = {CH1, CH2}                    one word per cap_tick
//   single-channel     word = {s[n], s[n+1]} of one channel one word per two cap_ticks (40960 samples)
// pre_w/post_w arrive already converted to words and clamped by the top (pre+post <= depth-2,
// post >= 1). FILL = wrote_count (words, saturating at depth).
//
// TRIGGER
//   software: level compare on the selected channel at every cap_tick with hysteresis:
//     rising  : armed when s <= level-hyst, fires at the first s >= level while armed
//     falling : armed when s >= level+hyst, fires at the first s <= level while armed
//     frac = (level - s[k-1]) / (s[k] - s[k-1])  (Q16, 16-step long division kicked at accept)
//   hardware (A12 comparator, TRIG_LEVEL.HW_SEL): 3-flop synced edge (slope-selected), sticky
//     until accepted; frac = 0.
//   mode 0 (AUTO): fires as soon as the pre-trigger depth is filled (free-run, owned-fpga
//     semantics); mode 1 (NORM): waits for the condition; the AUTO timeout policy is the app's.
//   trig_idx = position of the trigger word inside the drained record (= pre_w); trig_half = 1
//   when, in single-channel mode, the trigger sample is the FIRST (high-byte) sample of its word.
//
// STREAM (RUN.STREAM): ring never finalizes; wr_ptr is exported live; overflow (sticky) is
// raised when a write would make the ring look empty to a reader at rd_ptr_s (conservative: the
// synchronized read pointer lags the real one).
//
// HALT: with a trigger -> finalize on the partial post window; without -> a valid untriggered
// record of the last min(wrote,depth) words (TRIG=0, trig_idx = rec_len). RESET -> idle, cleared.
//
// TSRC (IL_CTRL.TSRC, v2.2, 06-TIERS s2): a test source substituted for the 16-bit record word at
// the writer input, after decimation and single-channel packing, so the pattern index k is the
// writer's word count since GO (arm), continuing across ring wraps in stream mode and identical in
// every CHMODE: RAMP k & 0xffff; COLTAG {k mod 5, (k / 5) & 0xff}; GLITCH 0xffff when k mod 512 ==
// 0, else 0 (iface ModelTsrc / TsrcWord is the reference). The generator is three small counters
// enabled by wr_q, a registered pattern word, and ONE registered 2:1 mux per bit in front of the
// M9K data register; nothing of the v2.1 sample/trigger/commit path changes, the RAM write is
// three register stages behind the commit instead of one (invisible: the drain reads after DONE
// or behind a synchronized pointer many cycles old). A GO while still FILLING (no HALT first)
// leaves the old record's last commit in wr_q on the cycle after the arm; the counters ignore
// that cycle (arm_q) so the new record's pattern starts at k = 0 (v2.2 review finding).
//
// M9K: one write port (cap_clk), one registered read port (rd_clk); Quartus infers a dual-clock
// simple dual-port RAM (16 x 20480 = exactly 40 M9K). No init contents, no read enable.
//
// TIMING (WP1c: 200 MHz on a C8 device; fit iteration 4 had -4.7 ns on the trigger paths, the
// un-pipelined writer cannot close). The sample path is a three-stage register pipeline:
//   A: samp_a / tick_a / s_a (the channel select)          B: the two level compares fire_b, armc_b
//   C: samp_c / s_c + tc_c, the registered trigger condition (hysteresis / hardware)
// so trig_fire is a 4-input AND of registers. The decimator runs at stage B (cap_tick_b) and its
// tick is re-registered into C (ct_c). Everything the trigger latches is taken one cycle
// later from delayed copies (kick[0]); the pre-trigger depth is a down counter with a registered
// "reached" flag; the post-trigger count runs one cycle behind on a registered enable and the
// "post window full" flag is computed exactly (W(t) == post_l) from three register compares; the
// record start of a triggered record is a running pointer (waddr - pre_l mod depth) latched at
// the trigger; the Q16 divider set-up runs in four registered steps (TRIGPOS_LO is final ~24
// cap_clk cycles = 120 ns after the trigger, before the C2 side can have seen DONE and read it).
// The only host-visible differences to the plain design: HALT acts one cycle late, and a HALT of a
// triggered record freezes the post-trigger count as it stood two writes earlier (the last <= 2
// words are in memory but not in rec_len) -- HALT is a host action with microsecond granularity.

`timescale 1ns/1ps
`include "regs.vh"

module capture (
    input  wire              cap_clk,
    input  wire [15:0]       samp,
    input  wire              samp_tick,

    // control pulses (cap_clk domain, mutually exclusive)
    input  wire              arm,
    input  wire              halt,
    input  wire              rst,

    // quasi-static configuration (C2 registers; latched at arm where it matters)
    input  wire [31:0]       decim_m1,       // decimation factor - 1
    input  wire [`ADDR_W-1:0] pre_w,         // words, clamped
    input  wire [`ADDR_W-1:0] post_w,        // words, clamped, >= 1
    input  wire              mode_norm,
    input  wire              stream_on,
    input  wire [1:0]        chmode,         // 0 dual, 1 CH1 only, 2 CH2 only
    input  wire              trig_src,       // 0 CH1, 1 CH2
    input  wire              trig_slope,     // 0 rising, 1 falling
    input  wire [7:0]        trig_level,
    input  wire [3:0]        trig_hyst,
    input  wire              hw_sel,
    input  wire              trig_sense,     // A12 pad (async)
    input  wire [1:0]        tsrc,           // IL_CTRL.TSRC (quasi-static): 0 ADC 1 RAMP 2 COLTAG 3 GLITCH

    // read pointer of the drain (synchronized by the top) for stream overflow detection
    input  wire [`ADDR_W-1:0] rd_ptr_s,

    // record read port (drain clock)
    input  wire              rd_clk,
    input  wire [`ADDR_W-1:0] rd_addr,
    output reg  [15:0]       rd_data,

    // status (cap_clk registers; the top synchronizes)
    output wire              filling,
    output wire              armed,
    output reg               triggered,
    output reg               done,
    output reg               valid,
    output reg               overflow,
    output wire [`ADDR_W-1:0] wr_ptr,
    output reg  [15:0]       wrote_count,
    output reg  [`ADDR_W-1:0] rec_start,     // frozen
    output reg  [15:0]       rec_len,        // frozen (words)
    output reg  [`ADDR_W-1:0] trig_idx,      // frozen
    output reg               trig_half,      // frozen
    output reg  [15:0]       trig_frac,      // frozen (Q16)
    output reg               done_evt,       // 1-cycle pulse at finalize
    output wire [1:0]        state_dbg
);
    localparam [`ADDR_W-1:0] REC_LAST    = `REC_DEPTH - 1;
    localparam [`ADDR_W-1:0] REC_DEPTH_A = `REC_DEPTH;
    localparam [15:0]        REC_DEPTH16 = `REC_DEPTH;
    localparam [1:0] ST_IDLE = 2'd0, ST_FILL = 2'd1, ST_HALT = 2'd2;

    // state fans out to every datapath enable (filling); let the fitter duplicate it so no copy
    // drives more than 24 loads (fit iteration 17: ST_FILL -> rec_start was -0.34 ns at 200 MHz).
    (* maxfan = 24 *) reg [1:0] state = ST_IDLE;
    reg [`ADDR_W-1:0] waddr = {`ADDR_W{1'b0}};
    reg [`ADDR_W-1:0] pre_l = {`ADDR_W{1'b0}}, post_l = {`ADDR_W{1'b0}};
    reg [15:0]        post_lm1 = 16'd0, post_lm2 = 16'd0, post_lm3 = 16'd0;   // post_l - 1/2/3 (mod 2^16)
    reg [31:0]        dec_l = 32'd0;
    reg               dec_l_zero = 1'b1, dec_l_lo_zero = 1'b1;
    reg               norm_l = 1'b0, stream_l = 1'b0, single_l = 1'b0, src_l = 1'b0, hw_l = 1'b0;
    reg [`ADDR_W-1:0] pre_left  = {`ADDR_W{1'b0}};   // pre-trigger words still missing
    reg               pre_ok_q  = 1'b0;               // pre_left == 0 (registered, exact)
    reg               pre_nz    = 1'b0;               // pre_left != 0 (registered, exact: loaded at arm, tracks every decrement)
    reg [`ADDR_W-1:0] start_ptr = {`ADDR_W{1'b0}};   // (waddr - pre_l) mod depth, running
    reg [`ADDR_W-1:0] trig_start = {`ADDR_W{1'b0}};  // start_ptr latched at the trigger
    reg               half = 1'b0;          // single mode: 1 = high byte already held
    reg [7:0]         hold_hi = 8'd0;
    reg               hyst_armed = 1'b0;
    reg               hw_pend = 1'b0;
    reg [7:0]         prev_s = 8'd0;
    reg               halt_q = 1'b0;         // halt acts one cycle late (keeps the halt path shallow)

    initial begin
        triggered = 1'b0; done = 1'b0; valid = 1'b0; overflow = 1'b0; wrote_count = 16'd0;
        rec_start = {`ADDR_W{1'b0}}; rec_len = 16'd0; trig_idx = {`ADDR_W{1'b0}};
        trig_half = 1'b0; trig_frac = 16'd0; done_evt = 1'b0; rd_data = 16'd0;
    end

    assign filling   = (state == ST_FILL);
    assign armed     = filling & ~triggered;
    assign wr_ptr    = waddr;
    assign state_dbg = state;

    // ---- stage A: sample, tick, selected channel (ticks are admitted from the arm edge on, so
    //      the first recorded sample is the one present at the arm, as in the plain design) ----
    reg  [15:0] samp_a = 16'd0;  reg tick_a = 1'b0;  reg [7:0] s_a = 8'd0;
    always @(posedge cap_clk) begin
        samp_a <= samp; tick_a <= samp_tick & (filling | arm); s_a <= src_l ? samp[7:0] : samp[15:8];
    end

    // ---- stage B: level compares (quasi-static thresholds re-registered) -------------------
    wire [8:0] lvl_lo9 = {1'b0, trig_level} - {5'd0, trig_hyst};
    wire [8:0] lvl_hi9 = {1'b0, trig_level} + {5'd0, trig_hyst};
    reg  [7:0] lvl_lo_q = 8'd0, lvl_hi_q = 8'd255;
    reg  [15:0] samp_b = 16'd0;  reg tick_b = 1'b0;  reg [7:0] s_b = 8'd0;
    reg  fire_b = 1'b0, armc_b = 1'b0;
    always @(posedge cap_clk) begin
        lvl_lo_q <= lvl_lo9[8] ? 8'd0   : lvl_lo9[7:0];
        lvl_hi_q <= lvl_hi9[8] ? 8'd255 : lvl_hi9[7:0];
        samp_b <= samp_a; tick_b <= tick_a; s_b <= s_a;
        fire_b <= trig_slope ? (s_a <= trig_level) : (s_a >= trig_level);
        armc_b <= trig_slope ? (s_a >= lvl_hi_q)   : (s_a <= lvl_lo_q);
    end

    // ---- decimator at stage B (cap_tick_b every decim ticks) ---------------------------------
    reg [15:0] cnt_lo = 16'd0, cnt_hi = 16'd0;
    reg        dec_zero = 1'b1;                         // cnt_lo == 0 && cnt_hi == 0
    reg        lo_zero  = 1'b1;                         // cnt_lo == 0
    wire cap_tick_b = filling & tick_b & dec_zero;
    always @(posedge cap_clk) begin
        if (arm) begin
            cnt_lo <= 16'd0; cnt_hi <= 16'd0; dec_zero <= 1'b1; lo_zero <= 1'b1;   // first sample after arm is taken
        end else if (filling && tick_b) begin
            if (dec_zero) begin
                cnt_lo <= dec_l[15:0]; cnt_hi <= dec_l[31:16]; dec_zero <= dec_l_zero; lo_zero <= dec_l_lo_zero;
            end else if (lo_zero) begin
                cnt_lo <= 16'hffff; cnt_hi <= cnt_hi - 16'd1; dec_zero <= 1'b0; lo_zero <= 1'b0;
            end else begin
                cnt_lo <= cnt_lo - 16'd1; dec_zero <= (cnt_lo == 16'd1) && (cnt_hi == 16'd0);
                lo_zero <= (cnt_lo == 16'd1);
            end
        end
    end

    // ---- hardware trigger sense -------------------------------------------------------------
    wire hw_level, hw_rise, hw_fall;
    stretch3 u_hw (.clk(cap_clk), .d(trig_sense), .level(hw_level), .rise(hw_rise), .fall(hw_fall));
    wire hw_edge = trig_slope ? hw_fall : hw_rise;

    // ---- stage C: datapath sample + registered trigger condition --------------------------
    //   hyst_armed is set at stage B from samples < k when sample k is evaluated (nonblocking
    //   order), exactly as the single-stage design evaluated it.
    reg  [15:0] samp_c = 16'd0;  reg [7:0] s_c = 8'd0;
    reg         ct_c = 1'b0;                          // cap_tick_b delayed
    reg         tc_c = 1'b0;                          // trigger condition for the stage-C sample
    always @(posedge cap_clk) begin
        samp_c <= samp_b; s_c <= s_b; ct_c <= cap_tick_b;
        tc_c   <= !norm_l ? 1'b1 : (hw_l ? (hw_edge | hw_pend) : (hyst_armed & fire_b));
    end
    wire cap_tick  = ct_c & filling;
    wire trig_fire = cap_tick & ~triggered & pre_ok_q & tc_c;

    // ---- write commit ---------------------------------------------------------------
    // dual: every cap_tick writes; single: every second cap_tick writes {hold_hi, s}
    reg         post_done = 1'b0;                     // W == post_l (registered, exact)
    wire        wr_word_now = cap_tick & (single_l ? half : 1'b1);
    wire        post_full   = triggered & post_done;
    // the same commit decision twice (keep: synthesis must not merge them) so that neither copy
    // drives more than ~35 flops: _p for the pointers and the RAM write, _c for the counters
    (* keep *) wire wr_commit_p = wr_word_now & (stream_l | ~post_full);
    (* keep *) wire wr_commit_c = wr_word_now & (stream_l | ~post_full);
    wire [15:0] wdata = single_l ? {hold_hi, s_c} : samp_c;
    wire [`ADDR_W-1:0] waddr_nx     = (waddr == REC_LAST)     ? {`ADDR_W{1'b0}} : (waddr + 1'b1);
    wire [`ADDR_W-1:0] start_ptr_nx = (start_ptr == REC_LAST) ? {`ADDR_W{1'b0}} : (start_ptr + 1'b1);
    reg                full         = 1'b0;             // wrote_count == depth (registered)
    wire               almost_full  = (wrote_count == REC_DEPTH16 - 16'd1);

    // The RAM write is register stages behind the commit: stage 1 (wr_q / wa_q / wd_q, v2.1) takes
    // the commit decision off the M9K path; stages 2 and 3 (v2.2) carry the TSRC substitution --
    // stage 3's data register is the one 2:1 mux per bit between the ADC word and the pattern word
    // registered one stage earlier. The 40 M9K blocks take write enable, address and data from
    // wr_q3 / wa_q3 / wd_q3 (wr_q3 duplicated by maxfan). The drain reads after DONE (or, in
    // stream mode, behind a synchronized pointer many cycles old), so the lag is never visible.
    (* maxfan = 40 *) reg wr_q = 1'b0;                 // enables the pattern counters + wr_q2
    reg [`ADDR_W-1:0] wa_q = {`ADDR_W{1'b0}};
    reg [15:0]        wd_q = 16'd0;
    always @(posedge cap_clk) begin wr_q <= wr_commit_p; wa_q <= waddr; wd_q <= wdata; end

    // ---- TSRC pattern generator (see header): index k of the word in wr_q = the count of wr_q
    //      pulses since arm; the state advances at the end of each wr_q cycle, and pat_q -- the
    //      pattern word of the state as it stood during that cycle -- is valid one cycle later,
    //      exactly when the same word sits in wd_q2.
    reg [1:0]  tsrc_q  = 2'd0;                         // quasi-static re-registration (C2 register)
    reg        tsrc_on = 1'b0;
    reg        arm_q   = 1'b0;                         // the cycle after arm: wr_q is the old record's
    reg [15:0] k_ramp  = 16'd0;                        // k & 0xffff
    reg [2:0]  k_col   = 3'd0;                         // k mod ROW_COLS
    reg [7:0]  k_row   = 8'd0;                         // (k / ROW_COLS) & 0xff
    reg [8:0]  k_gl    = 9'd0;                         // k mod 512
    reg        gl_flag = 1'b1;                         // k mod 512 == 0 (registered, exact)
    reg [15:0] pat_q   = 16'd0;
    always @(posedge cap_clk) begin
        tsrc_q  <= tsrc;
        tsrc_on <= (tsrc_q != `IL_CTRL_TSRC_ADC);
        arm_q   <= arm;
        if (arm || rst) begin
            k_ramp <= 16'd0; k_col <= 3'd0; k_row <= 8'd0; k_gl <= 9'd0; gl_flag <= 1'b1;
        end else if (wr_q && !arm_q) begin
            k_ramp  <= k_ramp + 16'd1;
            k_col   <= (k_col == `ROW_COLS - 1) ? 3'd0 : (k_col + 3'd1);
            if (k_col == `ROW_COLS - 1) k_row <= k_row + 8'd1;
            k_gl    <= k_gl + 9'd1;
            gl_flag <= (k_gl == 9'd511);
        end
        case (tsrc_q)
            `IL_CTRL_TSRC_RAMP:   pat_q <= k_ramp;
            `IL_CTRL_TSRC_COLTAG: pat_q <= {5'd0, k_col, k_row};
            `IL_CTRL_TSRC_GLITCH: pat_q <= {16{gl_flag}};
            default:              pat_q <= 16'd0;
        endcase
    end

    reg               wr_q2 = 1'b0;
    (* maxfan = 40 *) reg wr_q3 = 1'b0;
    reg [`ADDR_W-1:0] wa_q2 = {`ADDR_W{1'b0}}, wa_q3 = {`ADDR_W{1'b0}};
    reg [15:0]        wd_q2 = 16'd0, wd_q3 = 16'd0;
    always @(posedge cap_clk) begin
        wr_q2 <= wr_q;  wa_q2 <= wa_q;  wd_q2 <= wd_q;
        wr_q3 <= wr_q2; wa_q3 <= wa_q2; wd_q3 <= tsrc_on ? pat_q : wd_q2;   // one 2:1 mux per bit
    end
    (* ramstyle = "M9K" *) reg [15:0] mem [0:`REC_DEPTH-1];
    always @(posedge cap_clk) if (wr_q3) mem[wa_q3] <= wd_q3;
    always @(posedge rd_clk) rd_data <= mem[rd_addr];

    // stream overflow: the next write would land on the (synchronized) read pointer, i.e.
    // waddr == rd_ptr_s - 1; the "minus one" is registered so the check is one compare.
    reg [`ADDR_W-1:0] rd_m1 = {`ADDR_W{1'b0}};
    always @(posedge cap_clk) rd_m1 <= (rd_ptr_s == {`ADDR_W{1'b0}}) ? REC_LAST : (rd_ptr_s - 1'b1);

    // post-trigger words: en(t) = a post word is committed this cycle. The counter runs one
    // cycle behind (cnt_en_q), so W(t-1) = post_count + cnt_en_q and
    //   post_done(t+1) = [W(t) == post_l] = [post_count + cnt_en_q + en == post_l]
    // evaluated with three register compares and a 4:1 select (no adder in the path).
    wire        post_en  = wr_commit_c & (triggered | trig_fire);
    reg         cnt_en_q = 1'b0;
    reg [15:0]  post_count = 16'd0;
    wire [15:0] post_count_nx = post_count + {15'd0, cnt_en_q};
    // the three compares are registered from the counter's next value, so they describe the
    // counter's current value without a compare in the post_done path (fit iteration 13)
    // ([count + 1 == L] is written as [count == L - 1]: four parallel compares, no adder)
    reg         eq_l = 1'b0, eq_lm1 = 1'b0, eq_lm2 = 1'b0;
    wire        c_eq_l   = (post_count == {1'b0, post_l});
    wire        c_eq_lm1 = (post_count == post_lm1);
    wire        c_eq_lm2 = (post_count == post_lm2);
    wire        c_eq_lm3 = (post_count == post_lm3);
    always @(posedge cap_clk) begin
        if (arm || rst) begin eq_l <= 1'b0; eq_lm1 <= 1'b0; eq_lm2 <= 1'b0; end
        else begin
            eq_l   <= cnt_en_q ? c_eq_lm1 : c_eq_l;
            eq_lm1 <= cnt_en_q ? c_eq_lm2 : c_eq_lm1;
            eq_lm2 <= cnt_en_q ? c_eq_lm3 : c_eq_lm2;
        end
    end
    wire        post_done_nx = post_en ? (cnt_en_q ? eq_lm2 : eq_lm1) : (cnt_en_q ? eq_lm1 : eq_l);
    reg [15:0]  full_len_q = 16'd0;                   // pre_l + post_l
    reg [15:0]  halt_len_q = 16'd0;                   // pre_l + post_count (two writes behind)

    // one-cycle delayed copies for what the trigger latches (kick[0] = trig_fire delayed)
    reg [`ADDR_W-1:0] start_ptr_d = {`ADDR_W{1'b0}};
    reg [7:0]         s_d = 8'd0, prev_s_d = 8'd0;
    reg               half_d = 1'b0;
    always @(posedge cap_clk) begin
        start_ptr_d <= start_ptr; s_d <= s_c; prev_s_d <= prev_s; half_d <= half;
        full_len_q  <= {1'b0, pre_l} + {1'b0, post_l};
        halt_len_q  <= {1'b0, pre_l} + post_count;
    end

    // ---- interpolation divider, set up in three registered steps after the trigger ---------
    //   kick[0]: latch start/half, the four 8-bit differences and both directions
    //   kick[1]: pick the magnitudes and the direction agreement
    //   kick[2]: decide the degenerate cases (0 / 0xffff) and preload the divider
//   kick[3]: apply the decision or start the 16-step divider (result copied one step later)
    reg [3:0]  kick = 4'b0000;
    reg [7:0]  d_a = 8'd0, d_b = 8'd0, n_a = 8'd0, n_b = 8'd0;
    reg        frac_zero = 1'b0, frac_one = 1'b0;       // kick[2] decisions, applied at kick[3]
    reg        rise_q = 1'b0, lvlup_q = 1'b0;
    reg [7:0]  den_mag = 8'd0, num_mag = 8'd0;
    reg        same_dir = 1'b0;
    reg        div_busy = 1'b0;
    reg        div_last = 1'b0;                 // the 16th shift happened: copy the quotient
    reg [4:0]  div_cnt  = 5'd0;
    reg [8:0]  div_rem  = 9'd0;
    reg [15:0] div_q    = 16'd0;
    reg [7:0]  div_den  = 8'd0;
    wire [8:0] rem_sh   = {div_rem[7:0], 1'b0};
    wire       q_bit    = (rem_sh >= {1'b0, div_den});
    wire [8:0] rem_nx   = q_bit ? (rem_sh - {1'b0, div_den}) : rem_sh;

    // ---- main FSM ------------------------------------------------------------------
    always @(posedge cap_clk) begin
        done_evt <= 1'b0;
        halt_q   <= halt;

        kick <= {kick[2:0], 1'b0};
        if (kick[0]) begin                          // the trigger was accepted one cycle ago
            trig_start <= start_ptr_d;              // (trigger word address - pre_l) mod depth
            trig_half  <= single_l & ~half_d;       // first sample of a word
            d_a <= s_d - prev_s_d;        d_b <= prev_s_d - s_d;        rise_q  <= (s_d >= prev_s_d);
            n_a <= trig_level - prev_s_d; n_b <= prev_s_d - trig_level; lvlup_q <= (trig_level >= prev_s_d);
        end
        if (kick[1]) begin
            den_mag  <= rise_q ? d_a : d_b;
            num_mag  <= lvlup_q ? n_a : n_b;
            same_dir <= (rise_q == lvlup_q);
        end
        if (kick[2]) begin
            frac_zero <= hw_l || (den_mag == 8'd0) || !same_dir;
            frac_one  <= (num_mag >= den_mag);
            div_rem <= {1'b0, num_mag}; div_q <= 16'd0; div_den <= den_mag; div_cnt <= 5'd16;
        end
        if (kick[3]) begin
            if (frac_zero)     trig_frac <= 16'h0000;
            else if (frac_one) trig_frac <= 16'hffff;
            else               div_busy  <= 1'b1;
        end
        div_last <= 1'b0;
        if (div_busy) begin
            div_rem <= rem_nx;
            div_q   <= {div_q[14:0], q_bit};
            div_cnt <= div_cnt - 5'd1;
            if (div_cnt == 5'd1) begin div_busy <= 1'b0; div_last <= 1'b1; end
        end
        if (div_last) trig_frac <= div_q;

        if (filling) begin
            if (hw_edge && !triggered) hw_pend <= 1'b1;
            if (cap_tick_b && armc_b) hyst_armed <= 1'b1;
            pre_ok_q <= (pre_left == {`ADDR_W{1'b0}}) ||
                        ((pre_left == {{(`ADDR_W-1){1'b0}}, 1'b1}) && wr_commit_c);
            if (cap_tick) begin
                prev_s <= s_c;
                if (single_l && !half) begin hold_hi <= s_c; half <= 1'b1; end
                if (wr_word_now) half <= 1'b0;
            end
            if (wr_commit_p) begin
                waddr     <= waddr_nx;
                start_ptr <= start_ptr_nx;
            end
            // pre_left needs no post_full gate: a trigger fires only with pre_ok_q, so pre_left is
            // already 0 whenever triggered and the != 0 test holds it there. Gating it on the
            // cheaper wr_word_now keeps the triggered -> post_full -> 15-bit decrement path out of
            // the 200 MHz budget (fit iteration 17: -0.57 ns). The != 0 test itself is the
            // registered pre_nz (v2.2 flow 2: the 15-input OR tree in the decrement enable was
            // the worst cap_clk path at -0.007 ns); pre_nz is exact because pre_left changes only
            // here and at arm/reset, where pre_nz is loaded alongside it.
            if (wr_word_now && pre_nz) begin
                pre_left <= pre_left - 1'b1;
                pre_nz   <= (pre_left != {{(`ADDR_W-1){1'b0}}, 1'b1});
            end
            if (wr_commit_c) begin
                if (!full) wrote_count <= wrote_count + 16'd1;
                if (almost_full) full <= 1'b1;
                if (stream_l && (waddr == rd_m1)) overflow <= 1'b1;
            end
            cnt_en_q   <= post_en;
            post_count <= post_count_nx;
            post_done  <= post_done_nx;
            if (trig_fire) begin
                triggered  <= 1'b1;
                hw_pend    <= 1'b0;
                hyst_armed <= 1'b0;
                kick       <= 4'b0001;
            end
            if (post_full && !stream_l) begin
                state     <= ST_HALT;
                rec_start <= kick[0] ? start_ptr_d : trig_start;   // finalize may follow the trigger by one cycle
                rec_len   <= full_len_q;
                valid     <= 1'b1;
                done      <= 1'b1;
                done_evt  <= 1'b1;
            end
        end

        // opcode overrides last (priority over the datapath on the same edge)
        if (rst) begin
            state <= ST_IDLE; waddr <= {`ADDR_W{1'b0}}; wrote_count <= 16'd0; post_count <= 16'd0;
            post_done <= 1'b0; cnt_en_q <= 1'b0; pre_left <= {`ADDR_W{1'b0}}; pre_ok_q <= 1'b0; pre_nz <= 1'b0; full <= 1'b0;
            start_ptr <= {`ADDR_W{1'b0}};
            triggered <= 1'b0; done <= 1'b0; valid <= 1'b0; overflow <= 1'b0;
            hw_pend <= 1'b0; hyst_armed <= 1'b0; half <= 1'b0; div_busy <= 1'b0; kick <= 4'b0000;
            trig_frac <= 16'd0; trig_idx <= {`ADDR_W{1'b0}}; trig_half <= 1'b0;
            rec_start <= {`ADDR_W{1'b0}}; rec_len <= 16'd0; prev_s <= 8'd0;
        end else if (arm) begin
            state <= ST_FILL; waddr <= {`ADDR_W{1'b0}}; wrote_count <= 16'd0; post_count <= 16'd0;
            post_done <= 1'b0; cnt_en_q <= 1'b0; full <= 1'b0;
            triggered <= 1'b0; done <= 1'b0; valid <= 1'b0; overflow <= 1'b0;
            hw_pend <= 1'b0; hyst_armed <= 1'b0; half <= 1'b0; div_busy <= 1'b0; kick <= 4'b0000;
            trig_frac <= 16'd0; trig_idx <= pre_w; trig_half <= 1'b0;
            rec_start <= {`ADDR_W{1'b0}}; rec_len <= 16'd0; prev_s <= 8'd0;
            pre_l <= pre_w; post_l <= post_w; dec_l <= decim_m1;
            post_lm1 <= {1'b0, post_w} - 16'd1;
            post_lm2 <= {1'b0, post_w} - 16'd2;
            post_lm3 <= {1'b0, post_w} - 16'd3;
            dec_l_zero <= (decim_m1 == 32'd0);
            dec_l_lo_zero <= (decim_m1[15:0] == 16'd0);
            pre_left   <= pre_w;
            pre_ok_q   <= (pre_w == {`ADDR_W{1'b0}});
            pre_nz     <= (pre_w != {`ADDR_W{1'b0}});
            start_ptr  <= (pre_w == {`ADDR_W{1'b0}}) ? {`ADDR_W{1'b0}} : (REC_DEPTH_A - pre_w);
            norm_l <= mode_norm; stream_l <= stream_on; single_l <= (chmode != 2'd0);
            src_l <= (chmode == 2'd0) ? trig_src : (chmode == 2'd2);
            hw_l  <= hw_sel;
        end else if (halt_q && filling) begin
            state    <= ST_HALT;
            done     <= 1'b1;
            done_evt <= 1'b1;
            if (stream_l) begin
                valid <= 1'b0;                              // stream: nothing frozen, drain continues
            end else if (triggered) begin
                rec_start <= kick[0] ? start_ptr_d : trig_start;
                rec_len   <= halt_len_q;                    // see header: <= 2 words behind
                valid     <= 1'b1;
            end else begin                              // untriggered: every word committed before this edge
                rec_start <= full ? waddr : {`ADDR_W{1'b0}};   // oldest word of the last wrote_count
                rec_len   <= wrote_count;
                trig_idx  <= wrote_count[`ADDR_W-1:0];
                valid     <= 1'b1;
            end
        end
    end
endmodule

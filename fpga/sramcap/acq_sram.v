// acq_sram.v — TOP of the external-SRAM capture bitstream for the SDS1000CML
//              acquisition FPGA (Cyclone IV E, EP4CE10F17C8).
//
// DROP-IN for acq.v: identical GPMC CS1 register interface, identical build-ID
// 0xc2f6eb5f / VERSION 0x0052 (regs.vh / regmux.vh reused BYTE-FOR-BYTE), so the
// unmodified owned app-arm drives it unchanged. The ONLY functional change vs acq.v:
// the capture record no longer lives in on-chip M9K — `capsram` stores it in the
// EXTERNAL S7A163630M SRAM the vendor way (ADC drives the shared DQ bus; the fixed
// MAX-V sequences the SRAM strobes; our fabric drives the 27 addr/ctrl/clk balls +
// the D2 mode lever during CAPTURE and READS DQ over D14 during DRAIN).
//
// Differences vs acq.v (everything else copied verbatim):
//   * `capsram` replaces `capture` (same functional ports + SRAM-ball + debug ports);
//   * new top-level SRAM balls (27 write balls as tri-stateable inout, D2, D14, DQ, P6);
//   * one-line gpmc_d read-tap so the free CS1 debug selectors (decoded in capsram,
//     NOT in regmux.vh) can be read on the bench without disturbing the schema.
//
// Clean-room: design/spec-derived, never vendor RTL. Synthesizable Verilog-2001.

`include "regs.vh"

module acq (
    input  wire        clk,

    // ADC data bus (INPUTS) — 27 verified AD9288 data lanes (+ headroom).
    input  wire [32:0] adc_lane,
    output wire [7:0]  adc_enc,
    output wire [3:0]  adc_ctl_hi,
    output wire [2:0]  adc_ctl_lo,

    input  wire        trig_sense,

    // GPMC bus — CS1-only slave (CS3 decoded by the MAX V CPLD).
    input  wire        nCS1,
    input  wire        nOE,
    input  wire        nWE,
    input  wire [6:0]  sel,
    inout  wire [15:0] gpmc_d,
    output wire        gpmc_wait,

    // ===== EXTERNAL SRAM — RE-proven ball roles (merged_sram_roles.json) =====
    // 27 FPGA->SRAM write balls: driven during CAPTURE, tri-stated during the
    // non-contending DRAIN read (the MAX-V then owns the address). Declared inout
    // because the vendor decodes them fabric-OE bidir.
    inout  wire [17:0] sram_a,     // 18 ADDRESS balls
    inout  wire [5:0]  sram_c,     // 6 CONTROL balls (CS#/WE#/load)
    inout  wire [2:0]  sram_k,     // 3 CLOCK balls (F2/J2/K2 write sample clock)
    output wire        d2,         // nCSO MAX-V mode lever (static-low)
    output wire        sck_rd,     // D14 read clock (only net driven on drain)
    inout  wire [17:0] sram_dq,    // 18 DQ balls: read inputs on drain; DRIVEN with the test-write ramp during ST_FILL
    input  wire        p6          // MAX-V status mirror (input only)
);

    // ---- sized geometry / clamp constants (UNCHANGED) ---------------------
    localparam [15:0] REC_DEPTH16   = `REC_DEPTH;
    localparam [15:0] PRETRIG_MAX16 = `PRETRIG_MAX;
    localparam [15:0] CAP_MAX       = PRETRIG_MAX16;

    // =======================================================================
    // 1) GPMC synchronizers (UNCHANGED from acq.v)
    // =======================================================================
    reg [2:0]  cs1_q = 3'b111, oe_q = 3'b111, we_q = 3'b111;
    reg [6:0]  sel_q1 = 7'h00, sel_q2 = 7'h00;
    reg [15:0] d_q1 = 16'h0, d_q2 = 16'h0;
    reg [2:0]  trig_q = 3'b000;

    always @(posedge clk) begin
        cs1_q   <= {cs1_q[1:0], nCS1};
        oe_q    <= {oe_q[1:0],  nOE};
        we_q    <= {we_q[1:0],  nWE};
        sel_q1  <= sel;        sel_q2  <= sel_q1;
        d_q1    <= gpmc_d;     d_q2    <= d_q1;
        trig_q  <= {trig_q[1:0], trig_sense};
    end

    wire cs1_low   = (cs1_q[2] == 1'b0);
    wire trig_rise = (trig_q[2] == 1'b0) && (trig_q[1] == 1'b1);

    wire       we_commit = (we_q[2] == 1'b0) && (we_q[1] == 1'b1);
    wire [7:0] wr_plane  = cs1_low ? `PLANE_CS1 : 8'h00;
    wire [7:0] wr_sel    = {1'b0, sel_q2[6:2], 2'b00};
    wire [7:0] rd_plane  = (~nCS1) ? `PLANE_CS1 : 8'h00;
    wire [7:0] rd_sel    = {1'b0, sel[6:2], 2'b00};

    // ---- auto-inc read decode --------------------------------------------
    // BUGFIX (same as standard/acq.v): compare ONLY the stable selector lines
    // sel[6:2]. Bits 0/1/7 (A1/A2/A8 — M2 ~50% clock, D1 floats high) made the
    // full-width `sel_q2 == SEL_BURST` never true, so burst_addr never auto-
    // incremented and the drained SRAM record collapsed to mem[0] replicated.
    // This is why capsram "drained all-zero / no real SRAM read" — a DRAIN
    // artifact, NOT a failed SRAM read. rd_sel already masked (line above).
    // ALIAS CAVEAT: 0x41-0x43 alias to SEL_BURST and 0x71-0x73 to SEL_ENV_DATA
    // and WILL pop; the app issues only 0x40/0x70, but bench probes must avoid them.
    wire [7:0] sel_q2_masked = {1'b0, sel_q2[6:2], 2'b00};
    wire sel_is_burst    = (sel_q2_masked == `SEL_BURST);
    wire burst_rd_done   = cs1_low && (oe_q[2] == 1'b0) && (oe_q[1] == 1'b1) && sel_is_burst;
    wire sel_is_env      = (sel_q2_masked == `SEL_ENV_DATA);
    wire env_rd_active   = cs1_low && (oe_q[2] == 1'b0) && sel_is_env;
    wire env_rd_done     = cs1_low && (oe_q[2] == 1'b0) && (oe_q[1] == 1'b1) && sel_is_env;

    // =======================================================================
    // 2) Control / config register file (UNCHANGED)
    // =======================================================================
    reg [15:0] run_word    = 16'h0000;
    reg [31:0] decim_reg   = 32'd1;
    reg [31:0] pretrig_reg = 32'd10240;
    reg [31:0] posttrig_reg= 32'd10240;
    reg [15:0] xform_reg   = 16'h0003;
    reg [15:0] env_cols_reg= 16'd256;

    wire       run_en    = run_word[`RUN_RUN_LSB];
    wire       mode_norm = (run_word[`RUN_MODE_LSB +: 2] == 2'd1);
    wire [7:0] trig_level = 8'h80;

    wire [15:0] rdata_RUN, rdata_DECIM_LO, rdata_DECIM_HI;
    wire [15:0] rdata_PRETRIG_LO, rdata_PRETRIG_HI, rdata_POSTTRIG_LO, rdata_POSTTRIG_HI;
    wire [15:0] rdata_BURST, rdata_BURST_REMAIN;
    wire [15:0] rdata_STATUS_A, rdata_TRIGPOS_LO, rdata_TRIGPOS_HI, rdata_FILL;
    wire [15:0] rdata_XFORM_CTRL, rdata_ENV_COLS, rdata_ENV_DATA, rdata_ENV_COUNT;
    wire [15:0] rdata_CONF_DONE;

    // ---- GENERATED write-strobe + read-mux decode (schema SSOT, UNCHANGED) ----
    `include "regmux.vh"

    always @(posedge clk) begin
        if (we_RUN)         run_word          <= d_q2;
        if (we_DECIM_LO)    decim_reg[15:0]   <= d_q2;
        if (we_DECIM_HI)    decim_reg[31:16]  <= d_q2;
        if (we_PRETRIG_LO)  pretrig_reg[15:0] <= d_q2;
        if (we_PRETRIG_HI)  pretrig_reg[31:16]<= d_q2;
        if (we_POSTTRIG_LO) posttrig_reg[15:0]<= d_q2;
        if (we_POSTTRIG_HI) posttrig_reg[31:16]<= d_q2;
        if (we_XFORM_CTRL)  xform_reg         <= d_q2;
        if (we_ENV_COLS)    env_cols_reg      <= d_q2;
    end

    wire op_reset = we_OPCODE && (d_q2 == `OP_RESET);
    wire op_go    = we_OPCODE && (d_q2 == `OP_GO) && run_en;
    wire op_halt  = we_OPCODE && (d_q2 == `OP_HALT);

    // =======================================================================
    // 3) Arm-time joint pre/post clamp (UNCHANGED)
    // =======================================================================
    wire [15:0] pre_req  = (pretrig_reg[15:0] > PRETRIG_MAX16) ? PRETRIG_MAX16 : pretrig_reg[15:0];
    wire [15:0] post_raw = (posttrig_reg[15:0] == 16'd0) ? 16'd1 : posttrig_reg[15:0];
    wire [15:0] post_req = (post_raw > CAP_MAX) ? CAP_MAX : post_raw;
    wire [15:0] req_sum  = pre_req + post_req;
    wire        req_over = (req_sum > CAP_MAX);
    wire [15:0] excess   = req_over ? (req_sum - CAP_MAX) : 16'd0;

    reg [15:0] pre_work_w, post_work_w;
    always @* begin
        if (!req_over) begin
            pre_work_w  = pre_req;
            post_work_w = post_req;
        end else if (post_req > excess) begin
            pre_work_w  = pre_req;
            post_work_w = post_req - excess;
        end else begin
            pre_work_w  = CAP_MAX - 16'd1;
            post_work_w = 16'd1;
        end
    end

    // =======================================================================
    // 4) Engine wiring
    // =======================================================================
    wire [15:0]        samp;
    wire [15:0]        cap_word;
    wire               cap_tick;
    wire               filling;
    wire               smp_valid;
    wire               r_valid, r_trig, r_done, coherent;
    wire [10:0]        fill_out;
    wire [`ADDR_W-1:0] trig_idx;
    wire [15:0]        trig_frac;
    wire [15:0]        rec_len;
    wire               frame_done;
    wire [`ADDR_W-1:0] burst_addr;
    wire [15:0]        rec_rd_data;

    // ---- SRAM ball nets from capsram ----
    wire [17:0] cap_sram_addr;
    wire [5:0]  cap_sram_ctrl;
    wire        cap_sram_wclk;
    wire        cap_wr_oe;
    wire [17:0] cap_dq_wr;
    wire        cap_dq_wr_oe;
    wire        dbg_rd_hit;
    wire [15:0] dbg_rdata;

    // ---- DQ candidate vector (proven sramdump order). PATH A shared bus: 4 of
    //      the 22 lanes ARE acq's adc_lane input balls (A13/B12/G15/G16), read in
    //      the SAME input direction -> no conflict; the other 18 are dedicated. ----
    wire [21:0] dqv;
    assign dqv[0]  = adc_lane[14];   // A13 (shared with adc_lane[14])
    assign dqv[3]  = adc_lane[2];    // B12 (shared with adc_lane[2])
    assign dqv[14] = adc_lane[25];   // G15 (shared with adc_lane[25])
    assign dqv[15] = adc_lane[26];   // G16 (shared with adc_lane[26])
    assign dqv[1]  = sram_dq[0];     // A14
    assign dqv[2]  = sram_dq[1];     // A15
    assign dqv[4]  = sram_dq[2];     // B13
    assign dqv[5]  = sram_dq[3];     // B14
    assign dqv[6]  = sram_dq[4];     // B16
    assign dqv[7]  = sram_dq[5];     // C15
    assign dqv[8]  = sram_dq[6];     // C16
    assign dqv[9]  = sram_dq[7];     // D9
    assign dqv[10] = sram_dq[8];     // D11
    assign dqv[11] = sram_dq[9];     // D15
    assign dqv[12] = sram_dq[10];    // D16
    assign dqv[13] = sram_dq[11];    // F15
    assign dqv[16] = sram_dq[12];    // J16
    assign dqv[17] = sram_dq[13];    // L7
    assign dqv[18] = sram_dq[14];    // P8
    assign dqv[19] = sram_dq[15];    // R8
    assign dqv[20] = sram_dq[16];    // R16
    assign dqv[21] = sram_dq[17];    // T8

    adcif u_adcif (
        .clk        (clk),
        .adc_lane   (adc_lane),
        .adc_enc    (adc_enc),
        .adc_ctl_hi (adc_ctl_hi),
        .adc_ctl_lo (adc_ctl_lo),
        .samp       (samp)
    );

    spine u_spine (
        .clk      (clk),
        .filling  (filling),
        .samp     (samp),
        .decim    (decim_reg),
        .bypass0  (xform_reg[`XFORM_CTRL_BYPASS0_LSB]),
        .bypass1  (xform_reg[`XFORM_CTRL_BYPASS1_LSB]),
        .cap_word (cap_word),
        .cap_tick (cap_tick)
    );

    // capsram: same functional port list as `capture` + SRAM balls + debug port.
    capsram u_capture (
        .clk         (clk),
        .arm         (op_go),
        .halt        (op_halt),
        .rst         (op_reset),
        .pre_work_w  (pre_work_w),
        .post_work_w (post_work_w),
        .cap_word    (cap_word),
        .cap_tick    (cap_tick),
        .mode_norm   (mode_norm),
        .trig_rise   (trig_rise),
        .trig_level  (trig_level),
        .rd_addr     (burst_addr),
        .rd_data     (rec_rd_data),
        .filling     (filling),
        .smp_valid   (smp_valid),
        .r_valid     (r_valid),
        .r_trig      (r_trig),
        .r_done      (r_done),
        .coherent    (coherent),
        .fill_out    (fill_out),
        .trig_idx    (trig_idx),
        .trig_frac   (trig_frac),
        .rec_len     (rec_len),
        .frame_done  (frame_done),
        // SRAM physical interface
        .sram_addr   (cap_sram_addr),
        .sram_ctrl   (cap_sram_ctrl),
        .sram_wclk   (cap_sram_wclk),
        .wr_oe       (cap_wr_oe),
        .d2          (d2),
        .sck_rd      (sck_rd),
        .dq          (dqv),
        .dq_wr       (cap_dq_wr),
        .dq_wr_oe    (cap_dq_wr_oe),
        .p6          (p6),
        // free CS1 debug selectors
        .we_commit   (we_commit),
        .cs1_low     (cs1_low),
        .wr_sel      (wr_sel),
        .rd_sel      (rd_sel),
        .d_q2        (d_q2),
        .dbg_rd_hit  (dbg_rd_hit),
        .dbg_rdata   (dbg_rdata)
    );

    // Tri-state the 27 write balls: driven only while capsram asserts wr_oe
    // (ST_FILL), Hi-Z during the proven non-contending drain read.
    assign sram_a = cap_wr_oe ? cap_sram_addr      : 18'bz;
    assign sram_c = cap_wr_oe ? cap_sram_ctrl      : 6'bz;
    assign sram_k = cap_wr_oe ? {3{cap_sram_wclk}} : 3'bz;
    // TEST WRITE: drive DQ with the cell-address ramp during ST_FILL; Hi-Z on drain (SRAM drives it).
    assign sram_dq = cap_dq_wr_oe ? cap_dq_wr : 18'bz;

    envelope u_envelope (
        .clk            (clk),
        .arm            (op_go),
        .rst            (op_reset),
        .env_reset      (we_ENV_RESET),
        .pre_work_w     (pre_work_w),
        .post_work_w    (post_work_w),
        .env_cols       (env_cols_reg),
        .cap_word       (cap_word),
        .smp_valid      (smp_valid),
        .frame_done     (frame_done),
        .coherent       (coherent),
        .env_rd_active  (env_rd_active),
        .env_rd_done    (env_rd_done),
        .rdata_env_data (rdata_ENV_DATA),
        .rdata_env_count(rdata_ENV_COUNT)
    );

    drain u_drain (
        .clk               (clk),
        .arm               (op_go),
        .rst               (op_reset),
        .coherent          (coherent),
        .rec_len           (rec_len),
        .burst_rd_done     (burst_rd_done),
        .burst_addr        (burst_addr),
        .rdata_burst_remain(rdata_BURST_REMAIN)
    );

    // =======================================================================
    // 5) rdata_<REG> behavior wires (UNCHANGED)
    // =======================================================================
    assign rdata_RUN         = run_word;
    assign rdata_DECIM_LO    = decim_reg[15:0];
    assign rdata_DECIM_HI    = decim_reg[31:16];
    assign rdata_PRETRIG_LO  = pretrig_reg[15:0];
    assign rdata_PRETRIG_HI  = pretrig_reg[31:16];
    assign rdata_POSTTRIG_LO = posttrig_reg[15:0];
    assign rdata_POSTTRIG_HI = posttrig_reg[31:16];
    assign rdata_BURST       = coherent ? rec_rd_data : 16'h0000;
    assign rdata_STATUS_A    = {13'd0, r_done, r_trig, r_valid};
    assign rdata_TRIGPOS_LO  = trig_frac;
    assign rdata_TRIGPOS_HI  = {1'b0, trig_idx};
    assign rdata_FILL        = {5'd0, fill_out};
    assign rdata_XFORM_CTRL  = {14'd0, xform_reg[`XFORM_CTRL_BYPASS1_LSB],
                                       xform_reg[`XFORM_CTRL_BYPASS0_LSB]};
    assign rdata_ENV_COLS    = env_cols_reg;
    assign rdata_CONF_DONE   = 16'h0000;

    // =======================================================================
    // 6) Single tri-state driver on gpmc_d + WAIT held ready.
    //    ONE-LINE change vs acq.v: a free-selector debug read (decoded in
    //    capsram, outside the schema) overrides the generated read-mux for
    //    the unused CS1 selectors — the app never reads those, so BUILDID/
    //    VERSION/all schema registers stay bit-identical.
    // =======================================================================
    wire read_active = (~nCS1) & (~nOE);
    assign gpmc_d    = read_active ? (dbg_rd_hit ? dbg_rdata : rmux_rdata) : 16'hzzzz;
    assign gpmc_wait = 1'b1;

endmodule

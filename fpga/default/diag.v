// diag.v -- the indexed diagnostic block of the default image (05-WORKPLAN s2.1 + DIAG_CTRL,
// SNAP_*, MISC_RD): the 27-ball OE-gated bus, the five singles, the MAX-V-facing outputs, the
// ADC control levels, the lane/bus monitor, the 2048 x 16 snapshot RAM, and the read-only
// LANEMAP window reporting the baked ten-core map (lanemap_seed.vh, 06-TIERS s1.7 / s2).
//
// DOMAINS
//   clk    (C2)      every register the host writes/reads; MISC_RD/BUS_RD are 2-flop synced here
//   clk50  (PLL B)   the 27-ball bus output + input registers (bank 3 out-clk = K2's clock, 01-
//                    CONTRACT s3), the singles, D2, the K2 forward, the lane/bus monitor window
//   clk100 (PLL B)   the F2/J2 DDIO cells (0 / 1 / clk50 / clk100 selectable)
//   snap_clk         a LUT mux of {clk50, clk100, cap_clk, clk} = DIAG_CTRL.SNAP_CLK; change it
//                    only while no snapshot is running
//
// RESET POSTURE (05-WORKPLAN s3): bus all tri-state, D2 = 0, K2 clock on, F1 = 1, G1 = G2 = K1 =
// A11 = 0, ADC_HOLD = 111 (L4/T2/T7 driven 1 regardless of BUS_OE -- the proven ADC recipe,
// owned-fpga c8ec66d), singles tri-state.
//
// LANEMAP (DIAG 0x10..0x5f) is READ-ONLY from v2.2: entry i reports `LANEMAP_SEED_PACKED[7i+6:7i]
// of fpga/default/lanemap_seed.vh (GENERATED from lanecal-2026-09-05 by the acq2 analysis branch/
// lanemapgen.py); writes are ignored. map_ch1/map_ch2 (pair E1 = cores 0/1, the v2.1 dual-E1
// datapath) are the same constants, so adc_front's runtime assembler folds to fixed wiring in
// synthesis. A different map is a rebuild (build-ID -> app reload). The v3.0+ DIAG entries
// (PAIR_TRIM 0x0c, IL_DLY, SNAP_CTRL2, CAL_*, PHASE_CNT, CAL_STAT, CAP_SEL) read 0 and ignore
// writes here; PHASE_SEL/CAP_STEP are retired (06-TIERS s1.7) and the top holds both at 0 = the
// v2.1 reset alignment.
//
// BUS drive: ball i drives BUS_DRV[i] when DIAG_CTRL.BUS_DRV_EN & BUS_OE[i], or 1 when its
// ADC_HOLD bit is set (i = 5 L4, 21 T2, 26 T7); otherwise Hi-Z. BUS_RD is the clk50 pad register.
//
// LANE_IDX space: 0x00..0x4f lanes (cap_clk-registered, resampled at clk50), 0x50..0x6a bus,
// 0x70..0x74 singles, 0x78 P6, 0x79 A2, 0x7a B1, 0x7b K2 (= the K2 enable level; a 50 MHz clock
// sampled at 50 MHz has no toggles to count). LANE_TOG is one windowed counter on the selected
// signal (valid two 65536-cycle windows after LANE_IDX changes); ever-flags are per signal.
//
// SNAPSHOT: a write to DIAG_CTRL with SNAP_ARM=1 (re)arms; 2048 words are written at snap_clk
// from the SNAP_MODE slice; then SNAP_REMAIN.READY=1 and SNAP_POP drains word 0 first.
// Slice 6 packs {F3 F5 G5 D3, P6, bus 16..26} (17 sources for 16 bits: F7 is dropped -- schema
// defect, see README). Slice 7 writes 5 words per 5 clocks: lanes 0-15, 16-31, ... 64-79 of ONE
// coherent lane sample.

`timescale 1ns/1ps
`include "regs.vh"
`include "lanemap_seed.vh"

module diag (
    input  wire        clk,
    input  wire        clk50,
    input  wire        clk100,
    input  wire        cap_clk,

    // register interface (clk)
    input  wire        we_idx,
    input  wire        we_data,
    input  wire        we_ctrl,
    input  wire [15:0] wr_data,
    input  wire        pop_snap,
    output wire [15:0] rdata_idx,
    output reg  [15:0] rdata_data,
    output wire [15:0] rdata_ctrl,
    output wire [15:0] rdata_snap_remain,
    output wire [15:0] rdata_snap_pop,
    output wire [15:0] rdata_misc,

    // pads
    inout  wire [26:0] bus,
    inout  wire [4:0]  single,
    output wire        d2,
    output wire        k2,
    output wire        f2,
    output wire        j2,
    output reg         a11,
    output reg         f1,
    output reg         g1,
    output wire        g2,
    output wire        k1,
    input  wire        p6,
    input  wire        b11,          // listen-only; see default.v
    input  wire        j1,           // listen-only
    inout  wire        d1,
    input  wire        gpmc_a2,
    input  wire        gpmc_b1,
    input  wire        nWE_pad,
    input  wire        nOE_pad,

    // lanes (cap_clk registered, from adc_front)
    input  wire [79:0] lane_q,

    // to adc_front: the baked E1 map (constants)
    output wire [55:0] map_ch1,
    output wire [55:0] map_ch2,
    output wire [1:0]  dbg_snap       // {snapshot running (synced), ready}
);
    // =====================================================================
    // host registers (clk)
    // =====================================================================
    reg [7:0]  diag_idx   = 8'd0;
    reg [26:0] bus_oe     = 27'd0;
    reg [26:0] bus_drv    = 27'd0;
    reg [4:0]  single_oe  = 5'd0;
    reg [4:0]  single_drv = 5'd0;
    reg [2:0]  adc_hold   = 3'b111;
    reg [6:0]  lane_idx   = 7'd0;
    reg [2:0]  snap_mode  = 3'd0;
    reg [15:0] diag_ctrl  = 16'h0102;     // K2_EN=1, F1=1
    // ---- the 27-ball bus probe (the acq2 analysis branch) ----
    reg [15:0] busgen_ctl = 16'd0;
    reg [15:0] busgen_aux = 16'd0;
    reg [15:0] buscap_ctl = 16'd0;
    reg [15:0] busgen_oe_lo = 16'hffff;   // per-ball drive mask, reset = the whole group
    reg [15:0] busgen_oe_hi = 16'h07ff;
    // ---- the counted-edge SRAM-clock crank (SRCLK) ----
    reg [15:0] srclk_ctrl = 16'h0040;   // GUARD = LANES; everything else 0 = today's posture
    reg [15:0] srclk_n    = 16'd0;      // NEDGE[9:0], DIV[14:10]
    reg [15:0] srclk_dly  = 16'd0;      // DLY[9:0]
    reg [15:0] srclk_w    = 16'd0;      // HIGH[9:0]; 0 = the legacy half-period shape
    reg [15:0] busgen_pre = 16'd0;      // phase-advance prescaler; 0 = one phase per bus tick
    (* ramstyle = "logic" *) reg [15:0] busgen_pat [0:9];
    integer bi;
    initial for (bi = 0; bi < 10; bi = bi + 1) busgen_pat[bi] = 16'd0;
    wire idx_is_pat = (diag_idx >= `DIAG_BUSGEN_PAT_BASE) && (diag_idx <= `DIAG_BUSGEN_PAT_LAST);
    wire [7:0] pat_i8 = diag_idx - `DIAG_BUSGEN_PAT_BASE;
    wire [3:0] pat_i  = pat_i8[3:0];
    // The arm/stop/clear key must be in the SAME 16-bit word, so no replayed or
    // partially-decoded write can arm the emitter.
    wire srclk_key    = we_data & (diag_idx == `DIAG_SRCLK_ARM)
                      & (wr_data[`DIAG_SRCLK_ARM_KEY_LSB +: 8] == 8'ha5);
    wire srclk_arm_c  = srclk_key & wr_data[`DIAG_SRCLK_ARM_ARM_LSB];
    wire srclk_stop_c = srclk_key & wr_data[`DIAG_SRCLK_ARM_STOP_LSB];
    wire srclk_clr_c  = srclk_key & wr_data[`DIAG_SRCLK_ARM_CLR_LSB];

    localparam [`LANEMAP_PACKED_W-1:0] SEED = `LANEMAP_SEED_PACKED;   // the baked map
    // REGISTERED, and it reads 0 outside its own index range, so it can REPLACE the
    // 16'h0000 constant the read mux already had rather than add a level to it. The
    // sel -> gpmc_d cone is combinational and was 0.463 ns short with this mux in it;
    // diag_idx is a clk register and the host reads DIAG_DATA many clocks after writing
    // DIAG_IDX, so registering costs nothing observable.
    wire idx_is_srclk = (diag_idx >= `DIAG_SRCLK_CTRL) && (diag_idx <= `DIAG_BUSGEN_PRE);
    reg [15:0] srclk_word = 16'd0;
    always @(posedge clk)
        srclk_word <= !idx_is_srclk                     ? 16'd0
                    : (diag_idx == `DIAG_SRCLK_CTRL)    ? srclk_ctrl
                    : (diag_idx == `DIAG_SRCLK_N)       ? srclk_n
                    : (diag_idx == `DIAG_SRCLK_DLY)     ? srclk_dly
                    : (diag_idx == `DIAG_SRCLK_ARM)     ? 16'h00a5 : srclk_rd;
    wire idx_is_map = (diag_idx >= `DIAG_LANEMAP_BASE) && (diag_idx <= `DIAG_LANEMAP_LAST);
    wire [7:0] map_i8 = diag_idx - `DIAG_LANEMAP_BASE;
    wire [6:0] map_i  = map_i8[6:0];

    always @(posedge clk) begin
        if (we_idx)  diag_idx <= wr_data[7:0];
        if (we_ctrl) diag_ctrl <= wr_data;
        if (we_data) begin
            case (diag_idx)
                `DIAG_BUS_OE_LO:   bus_oe[15:0]    <= wr_data;
                `DIAG_BUS_OE_HI:   bus_oe[26:16]   <= wr_data[10:0];
                `DIAG_BUS_DRV_LO:  bus_drv[15:0]   <= wr_data;
                `DIAG_BUS_DRV_HI:  bus_drv[26:16]  <= wr_data[10:0];
                `DIAG_SINGLE_CTRL: begin single_oe <= wr_data[4:0]; single_drv <= wr_data[12:8]; end
                `DIAG_ADC_HOLD:    adc_hold  <= wr_data[2:0];
                `DIAG_LANE_IDX:    lane_idx  <= wr_data[6:0];
                `DIAG_SNAP_MODE:   snap_mode <= wr_data[2:0];
                `DIAG_BUSGEN_CTL:  busgen_ctl <= wr_data;
                `DIAG_BUSGEN_AUX:  busgen_aux <= wr_data;
                `DIAG_BUSCAP_CTL:  buscap_ctl <= wr_data;
                `DIAG_BUSGEN_OE_LO: busgen_oe_lo <= wr_data;
                `DIAG_BUSGEN_OE_HI: busgen_oe_hi <= wr_data;
                `DIAG_SRCLK_CTRL:  srclk_ctrl <= wr_data;
                `DIAG_SRCLK_N:     srclk_n    <= wr_data;
                `DIAG_SRCLK_DLY:   srclk_dly  <= wr_data;
                `DIAG_SRCLK_W:     srclk_w    <= wr_data;
                `DIAG_BUSGEN_PRE:  busgen_pre <= wr_data;
                // DIAG_SRCLK_ARM has no storage: it is a keyed strobe, below.
                default: if (idx_is_pat) busgen_pat[pat_i] <= wr_data;   // LANEMAP (read-only), stubs, reserved
            endcase
        end
    end

    assign rdata_idx  = {8'd0, diag_idx};
    assign rdata_ctrl = diag_ctrl;
    assign map_ch1    = SEED[`LANEMAP_CORE_W*0 +: `LANEMAP_CORE_W];      // core 0 = E1 CH1
    assign map_ch2    = SEED[`LANEMAP_CORE_W*1 +: `LANEMAP_CORE_W];      // core 1 = E1 CH2
    wire [6:0] map_entry = SEED[map_i * `LANEMAP_ENTRY_W +: `LANEMAP_ENTRY_W];   // constant select
    genvar gk;

    // =====================================================================
    // 27-ball bus + singles (clk50 pad registers)
    // =====================================================================
    wire [26:0] hold_mask = {adc_hold[2], 4'd0, adc_hold[1], 15'd0, adc_hold[0], 5'd0}; // T7=26 T2=21 L4=5
    wire        bus_master = diag_ctrl[`DIAG_CTRL_BUS_DRV_EN_LSB];

    // ---- the bus clock ---------------------------------------------------
    // The 27 balls' pad registers (both directions) run on ONE selectable
    // clock so the generator can emit and the capture can watch at the rate
    // under test; the static DIAG drive is quasi-static and does not care
    // which clock carries it. Reset value 0 = clk50 = the behaviour before the
    // probe existed. A LUT clock mux, like the snapshot's (README §6): change
    // BUSGEN_CTL.RATE only while the generator is off.
    wire [1:0] bus_rate = busgen_ctl[`DIAG_BUSGEN_CTL_RATE_LSB +: 2];
    wire busclk = (bus_rate == 2'd0) ? clk50 : (bus_rate == 2'd1) ? clk100
                : (bus_rate == 2'd2) ? cap_clk : clk;

    // ---- the generator ---------------------------------------------------
    // A programmable version of what the factory image drives there: a phase
    // counter of NPHASE+1 states, one 27-bit word per phase, one enable bit
    // per phase for the whole group (the factory's enable is a level, not a
    // function of the phase, but a per-phase mask covers that case too).
    wire        gen_en_c   = busgen_ctl[`DIAG_BUSGEN_CTL_EN_LSB];
    wire        gen_run_c  = busgen_ctl[`DIAG_BUSGEN_CTL_RUN_LSB];
    wire        gen_1shot_c= busgen_ctl[`DIAG_BUSGEN_CTL_ONESHOT_LSB];
    wire [2:0]  gen_nph_c  = busgen_ctl[`DIAG_BUSGEN_CTL_NPHASE_LSB +: 3];
    wire [4:0]  gen_oeph_c = busgen_ctl[`DIAG_BUSGEN_CTL_OE_PH_LSB +: 5];
    wire        gen_oeinv_c= busgen_ctl[`DIAG_BUSGEN_CTL_OE_INV_LSB];
    wire        gen_en, gen_run, gen_1shot, gen_oeinv;
    wire [2:0]  gen_nph;
    wire [4:0]  gen_oeph;
    sync_bit           u_gen_en   (.clk(busclk), .d(gen_en_c),    .q(gen_en));
    sync_bit           u_gen_run  (.clk(busclk), .d(gen_run_c),   .q(gen_run));
    sync_bit           u_gen_1s   (.clk(busclk), .d(gen_1shot_c), .q(gen_1shot));
    sync_bit           u_gen_oei  (.clk(busclk), .d(gen_oeinv_c), .q(gen_oeinv));
    sync_bit #(.W(3))  u_gen_nph  (.clk(busclk), .d(gen_nph_c),   .q(gen_nph));
    sync_bit #(.W(5))  u_gen_oeph (.clk(busclk), .d(gen_oeph_c),  .q(gen_oeph));

    // the five 27-bit words, re-registered in the bus domain (quasi-static)
    reg [26:0] pat_s [0:4];
    reg [4:0]  ph_oh = 5'b00001;          // one-hot phase
    reg [2:0]  ph_n  = 3'd0;              // the phase number, for the aux outputs
    reg        gen_active = 1'b0;
    reg        wrap = 1'b0;
    integer pi;
    // The phase advance is prescaled, so the group can be driven far more slowly than the
    // bus clock. Every earlier experiment on these balls ran at one phase per tick.
    wire [15:0] pre_s;
    sync_bit #(.W(16)) u_bgpre (.clk(busclk), .d(busgen_pre), .q(pre_s));
    reg  [15:0] pre_cnt = 16'd0;
    wire        pre_tick = (pre_cnt == 16'd0);
    always @(posedge busclk)
        pre_cnt <= (!gen_run || pre_tick) ? pre_s : (pre_cnt - 16'd1);

    always @(posedge busclk) begin
        for (pi = 0; pi < 5; pi = pi + 1)
            pat_s[pi] <= {busgen_pat[2*pi+1][10:0], busgen_pat[2*pi]};
        if (!gen_run) begin
            gen_active <= 1'b0; ph_oh <= 5'b00001; ph_n <= 3'd0; wrap <= 1'b0;
        end else if (!gen_active && !wrap) begin
            gen_active <= 1'b1;
        end else if (gen_active && pre_tick) begin
            if (ph_n == gen_nph || ph_n == 3'd4) begin
                ph_oh <= 5'b00001; ph_n <= 3'd0;
                if (gen_1shot) begin gen_active <= 1'b0; wrap <= 1'b1; end
            end else begin
                ph_oh <= {ph_oh[3:0], 1'b0}; ph_n <= ph_n + 3'd1;
            end
        end
    end
    // one 5:1 mux per ball, selected one-hot (two LUT levels at 200 MHz)
    reg [26:0] gen_drv = 27'd0;
    reg        gen_oe1 = 1'b0;
    always @(posedge busclk) begin
        gen_drv <= ({27{ph_oh[0]}} & pat_s[0]) | ({27{ph_oh[1]}} & pat_s[1])
                 | ({27{ph_oh[2]}} & pat_s[2]) | ({27{ph_oh[3]}} & pat_s[3])
                 | ({27{ph_oh[4]}} & pat_s[4]);
        gen_oe1 <= (|(ph_oh & gen_oeph)) ^ gen_oeinv;
    end

    wire [26:0] gen_oemask;
    sync_bit #(.W(27)) u_oemask (.clk(busclk), .d({busgen_oe_hi[10:0], busgen_oe_lo}), .q(gen_oemask));
    // masked combinationally, not pipelined: the enable must stay aligned with
    // gen_drv, and both feed the pad registers one AND level later.
    // hold_mask is ORed in on BOTH arms: dropping it while the generator ran released the
    // proven ADC hold on L4/T2/T7, so every prior busprobe condition ran with the hold off.
    wire [26:0] oe_eff  = ((gen_en && gen_active) ? ({27{gen_oe1}} & gen_oemask)
                                                 : ({27{bus_master}} & bus_oe)) | hold_mask;
    wire [26:0] drv_eff = ((gen_en && gen_active) ? gen_drv : bus_drv)          | hold_mask;

    (* useioff = 1 *) reg [26:0] bus_oe_q = 27'd0;
    (* useioff = 1 *) reg [26:0] bus_o_q  = 27'd0;
    (* useioff = 1 *) reg [26:0] bus_i_q  = 27'd0;
    (* useioff = 1 *) reg [4:0]  sgl_oe_q = 5'd0;
    (* useioff = 1 *) reg [4:0]  sgl_o_q  = 5'd0;
    (* useioff = 1 *) reg [4:0]  sgl_i_q  = 5'd0;
    always @(posedge busclk) begin
        bus_oe_q <= oe_eff;   bus_o_q <= drv_eff;   bus_i_q <= bus;
    end
    always @(posedge clk50) begin
        sgl_oe_q <= single_oe; sgl_o_q <= single_drv; sgl_i_q <= single;
    end
    // the monitor and the ever-flags live in clk50: bring the bus back
    wire [26:0] bus_i50;
    sync_bit #(.W(27)) u_busi50 (.clk(clk50), .d(bus_i_q), .q(bus_i50));
    generate
        for (gk = 0; gk < 27; gk = gk + 1) begin : g_bus
            assign bus[gk] = bus_oe_q[gk] ? bus_o_q[gk] : 1'bz;
        end
        for (gk = 0; gk < 5; gk = gk + 1) begin : g_sgl
            assign single[gk] = sgl_oe_q[gk] ? sgl_o_q[gk] : 1'bz;
        end
    endgenerate
    // ---- the four clock-shaped balls K2, D1, D2, G2 ----------------------
    // PORT-SPEC §3.2 G13/G24: all four are DDIO outputs whose data lines the
    // decode cannot name; the acq2 analysis branch §10.1 reads that shape
    // (both data lines silent, exactly one edge inverted) as a forwarded
    // clock, K2 carrying the one the bus registers run on. Each ball takes a
    // source from BUSGEN_AUX: 0 keeps what this image did before the probe
    // (K2 = the enable-gated forward, D2/G2 = the DIAG_CTRL level, D1 not
    // driven), 1/2 a constant, 3 the bus clock, 4 the bus clock halved, 5 one
    // pulse per generator rotation. D1 is driven only when its code is not 0:
    // the factory drives it as a plain output, we keep an enable so a wrong
    // reading of the board cannot fight another driver.
    wire k2_en_s, d2_lvl, g2_lvl;
    sync_bit u_k2en (.clk(busclk), .d(diag_ctrl[`DIAG_CTRL_K2_EN_LSB]), .q(k2_en_s));
    sync_bit u_d2l  (.clk(busclk), .d(diag_ctrl[`DIAG_CTRL_D2_LSB]),   .q(d2_lvl));
    sync_bit u_g2l  (.clk(busclk), .d(diag_ctrl[`DIAG_CTRL_G2_LSB]),   .q(g2_lvl));
    wire k1_lvl;
    sync_bit u_k1l  (.clk(busclk), .d(diag_ctrl[`DIAG_CTRL_K1_LSB]),   .q(k1_lvl));
    wire [14:0] aux_c;
    sync_bit #(.W(15)) u_auxc (.clk(busclk), .d(busgen_aux[14:0]), .q(aux_c));
    wire [2:0] aux_k2 = aux_c[`DIAG_BUSGEN_AUX_K2_LSB +: 3];
    wire [2:0] aux_d1 = aux_c[`DIAG_BUSGEN_AUX_D1_LSB +: 3];
    wire [2:0] aux_d2c= aux_c[`DIAG_BUSGEN_AUX_D2_LSB +: 3];
    wire [2:0] aux_g2 = aux_c[`DIAG_BUSGEN_AUX_G2_LSB +: 3];
    wire [2:0] aux_k1 = aux_c[`DIAG_BUSGEN_AUX_K1_LSB +: 3];
    reg  div2 = 1'b0;
    always @(posedge busclk) div2 <= ~div2;
    // registered: at 200 MHz the phase counter must not reach the DDIO inputs
    // through the aux mux in one tick. One tick of skew on a marker is nothing.
    reg rot_tick = 1'b0;
    always @(posedge busclk) rot_tick <= gen_active & (ph_n == 3'd0);
    // {dh, dl} per code; code 0 is the legacy source of that ball
    function [1:0] auxpair;
        input [2:0] code; input lgh; input lgl; input d2v; input tickv; input oev;
        begin
            case (code)
                3'd0: auxpair = {lgh, lgl};
                3'd1: auxpair = 2'b00;
                3'd2: auxpair = 2'b11;
                3'd3: auxpair = 2'b10;              // forward busclk
                3'd4: auxpair = {d2v, d2v};         // busclk / 2
                3'd5: auxpair = {tickv, tickv};     // one pulse per rotation
                3'd6: auxpair = {~oev, ~oev};       // the factory's K1: !OE, low while driving
                3'd7: auxpair = {oev, oev};         // the drive enable itself
                default: auxpair = 2'b00;
            endcase
        end
    endfunction
    // gen_oe1 is the group drive enable for this phase, already registered in the bus domain.
    wire [1:0] k2p = auxpair(aux_k2, k2_en_s, 1'b0,  div2, rot_tick, gen_oe1);
    wire [1:0] d1p = auxpair(aux_d1, 1'b0,    1'b0,  div2, rot_tick, gen_oe1);
    wire [1:0] d2p = auxpair(aux_d2c, d2_lvl, d2_lvl, div2, rot_tick, gen_oe1);
    wire [1:0] g2p = auxpair(aux_g2, g2_lvl, g2_lvl, div2, rot_tick, gen_oe1);
    wire [1:0] k1p = auxpair(aux_k1, k1_lvl, k1_lvl, div2, rot_tick, gen_oe1);
    ddio_out1 u_k2 (.outclock(busclk), .dh(k2p[1]), .dl(k2p[0]), .pad(k2));
    // CTRL_G2 is NOT wired in this build, and the fabric refuses any arm that asks for it.
    // G2's DDIO runs on busclk, which can be cap_clk, so routing the clk100 emitter to it
    // creates a 100 -> 200 MHz transfer into the cell's own input flop — measured at
    // -0.735 ns combinational and -1.716 ns registered, against a domain with 0.2 ns to
    // spare. The SDC false-paths G2's PAD, not that flop. The control ball is J2 instead:
    // it is a DDIO on clk100, the emitter's own domain, so it costs no crossing at all, and
    // being the other SRAM-clock candidate it is the more useful comparison anyway.
    ddio_out1 u_g2 (.outclock(busclk), .dh(g2p[1]), .dl(g2p[0]), .pad(g2));
    ddio_out1 u_d2 (.outclock(busclk), .dh(d2p[1]), .dl(d2p[0]), .pad(d2));
    ddio_out1 u_k1 (.outclock(busclk), .dh(k1p[1]), .dl(k1p[0]), .pad(k1));
    // D1's pad block moved to the crank section: it now takes the counted emitter,
    // an SDR register on clk100 rather than busclk, and gains a pad readback.

    // ADC-side levels (clk registers -> pads)
    initial begin a11 = 1'b0; f1 = 1'b1; g1 = 1'b0; end
    always @(posedge clk) begin
        a11 <= diag_ctrl[`DIAG_CTRL_A11_LSB];
        f1  <= diag_ctrl[`DIAG_CTRL_F1_LSB];
        g1  <= diag_ctrl[`DIAG_CTRL_G1_LSB];
    end

    // =====================================================================
    // F2 / J2 and the counted-edge crank (clk100)
    // =====================================================================
    // fpga-specs/22 s2.5.1: tLZC 1.5 ns min, tHZC 2.6 ns max. On a pipelined
    // SyncBurst part the DQ driver is gated by OE AND the clocked pipeline state,
    // so one edge can take ~22 SRAM outputs Low-Z into an AD9288 that has no
    // output enable and no Cyclone-reachable standby lever. Stopping the clock
    // does not undo it; the exit is a power cycle. Hence: the budget IS the
    // emitter, the budget is loaded only by a keyed arm, and the whole
    // configuration has a ceiling that no register write can raise.
    localparam [15:0] SRCLK_LIFE_CEIL = 16'd4096;   // no clear path exists in the fabric
    localparam [1:0]  SRC_LEGACY = 2'd0, SRC_ZERO = 2'd1, SRC_CRANK = 2'd2;
    localparam [2:0]  S_IDLE = 3'd0, S_WAIT = 3'd1, S_DLY = 3'd2,
                      S_FAST = 3'd3, S_LO   = 3'd4, S_HI  = 3'd5, S_WID = 3'd6;

    // ---- configuration, in the emitter's own domain --------------------
    wire [9:0] sc_cfg;
    sync_bit #(.W(10)) u_sc_cfg (.clk(clk100), .d(srclk_ctrl[9:0]), .q(sc_cfg));
    wire [1:0] f2_src    = sc_cfg[1:0];
    wire [1:0] j2_src    = sc_cfg[3:2];
    wire       ctrl_g2   = sc_cfg[4];
    wire       snap_on_arm = sc_cfg[5];
    wire [1:0] guard_sel = sc_cfg[7:6];
    wire [1:0] d1_src    = sc_cfg[9:8];
    wire       d1_crank  = (d1_src == SRC_CRANK);
    wire [15:0] sc_n, sc_d;
    sync_bit #(.W(16)) u_sc_n (.clk(clk100), .d(srclk_n),   .q(sc_n));
    sync_bit #(.W(16)) u_sc_d (.clk(clk100), .d(srclk_dly), .q(sc_d));
    wire [9:0] nedge_s = sc_n[9:0];
    wire [4:0] div_s   = sc_n[14:10];
    wire [9:0] dly_s   = sc_d[9:0];
    wire [15:0] sc_w;
    sync_bit #(.W(16)) u_sc_w (.clk(clk100), .d(srclk_w), .q(sc_w));
    wire [9:0] hi_s = sc_w[9:0];
    // DIAG_CTRL's own F2/J2 fields: these synchronizers already exist (lines 326-328)
    wire [1:0] f2_sel_s, j2_sel_s;
    sync_bit #(.W(2)) u_f2s (.clk(clk100), .d(diag_ctrl[`DIAG_CTRL_F2_SEL_LSB +: 2]), .q(f2_sel_s));
    sync_bit #(.W(2)) u_j2s (.clk(clk100), .d(diag_ctrl[`DIAG_CTRL_J2_SEL_LSB +: 2]), .q(j2_sel_s));
    wire [3:0] host_s;   // {gen_en, bus_master, bus_rate}
    sync_bit #(.W(4)) u_sc_h (.clk(clk100),
        .d({busgen_ctl[`DIAG_BUSGEN_CTL_EN_LSB], bus_master, bus_rate}), .q(host_s));
    wire       gen_en_s = host_s[3], drv_en_s = host_s[2];
    wire [1:0] rate_s   = host_s[1:0];
    wire arm_100, stop_100, clr_100, dep_100, grd_100, rest_100;
    sync_pulse u_sc_ar (.clk_src(clk), .pulse_src(srclk_arm_c),  .clk_dst(clk100), .pulse_dst(arm_100));
    sync_pulse u_sc_sp (.clk_src(clk), .pulse_src(srclk_stop_c), .clk_dst(clk100), .pulse_dst(stop_100));
    sync_pulse u_sc_cl (.clk_src(clk), .pulse_src(srclk_clr_c),  .clk_dst(clk100), .pulse_dst(clr_100));
    sync_bit   u_sc_d1 (.clk(clk100), .d(dep50),       .q(dep_100));
    sync_bit   u_sc_gr (.clk(clk100), .d(guard_rdy50), .q(grd_100));
    sync_bit   u_sc_rs (.clk(clk100), .d(rest50),      .q(rest_100));

    // ---- state ----------------------------------------------------------
    reg  [2:0]  st      = S_IDLE;
    reg  [9:0]  budget  = 10'd0;      // THE interlock: the pad's rising term is ANDed with it
    reg  [9:0]  loaded  = 10'd0;
    reg  [9:0]  dly_r   = 10'd0;      // latched at arm
    reg  [4:0]  div_r   = 5'd0;       // latched at arm
    // width mode: the period and the high time are counted separately, so a sweep of HIGH at a
    // fixed period separates "the node needs N us to charge after D1 releases it" from "the far
    // end needs a minimum width". DIV is capped at 23 here so per_r cannot overflow.
    reg  [23:0] per_r   = 24'd0, per_cnt = 24'd0;
    reg  [9:0]  hi_r    = 10'd0, hi_cnt  = 10'd0;
    reg  [15:0] life    = 16'd0;      // never cleared by any write
    reg         aborted = 1'b0, refused = 1'b0, grd_q = 1'b1;
    reg  [2:0]  refuse  = 3'd0;
    reg         f2_arm_r = 1'b0, j2_arm_r = 1'b0, g2_arm_r = 1'b0, d1_arm_r = 1'b0;  // latched at arm:
    reg  [9:0]  abort_edge = 10'd0;                                 // a mid-burst write
    reg         emit_dh = 1'b0, emit_dl = 1'b0;                     // cannot attach a ball
    reg  [31:0] pre     = 32'd0;
    reg         pre_q   = 1'b0;

    wire       pre_sel  = pre[div_r - 5'd1];        // 32:1 bit tap; ignored for DIV <= 1
    wire       tick     = (div_r <= 5'd1) ? 1'b1 : (pre_sel ^ pre_q);
    wire       f2_crank = (f2_src == SRC_CRANK);
    wire       j2_crank = (j2_src == SRC_CRANK);

    // every arm precondition, ANDed in the fabric
    wire [2:0] n_crank = {2'd0, f2_crank} + {2'd0, j2_crank} + {2'd0, d1_crank};
    wire posture_ok = (f2_sel_s == 2'd0) & (j2_sel_s == 2'd0)
                    & (f2_crank | j2_crank | d1_crank | ctrl_g2)
                    & (n_crank <= 3'd1)                  // never two balls at once
                    & (~d1_crank | (div_s >= 5'd2))      // SDR shape + unambiguous sampling
                    & ((hi_s == 10'd0)                   // width mode: HIGH must fit the period,
                       | ((div_s <= 5'd23)               // and DIV must not overflow per_r
                          & ({14'd0, hi_s} < (24'd1 << div_s[4:0]))));
    wire rest_ok    = rest_100 & ~drv_en_s & ~gen_en_s;       // the 27-ball group, proven not driven
    wire life_ok    = (({6'd0, nedge_s} + life) <= SRCLK_LIFE_CEIL);
    wire ctrl_ok    = ~ctrl_g2;   // CTRL_G2 is unimplemented in this build: always refuse
    wire guard_ok   = (guard_sel != 2'd0) | (nedge_s == 10'd1);
    wire arm_ok     = posture_ok & rest_ok & (nedge_s != 10'd0)
                    & grd_100 & ~aborted & life_ok & ctrl_ok & guard_ok & (st == S_IDLE);
    wire arm_go     = arm_100 & arm_ok;

    always @(posedge clk100) begin
        pre     <= (st == S_IDLE) ? 32'd0 : (pre + 32'd1);
        pre_q   <= pre_sel;
        grd_q   <= grd_100;
        refused <= 1'b0;
        if (clr_100) begin aborted <= 1'b0; refuse <= 3'd0; abort_edge <= 10'd0; end
        case (st)
        S_IDLE: begin
            emit_dh <= 1'b0; emit_dl <= 1'b0;
            if (arm_100) begin
                if (arm_ok) begin
                    budget <= nedge_s; loaded <= nedge_s;
                    div_r  <= div_s;   dly_r  <= dly_s;
                    hi_r   <= hi_s;    per_r  <= (24'd1 << div_s[4:0]);
                    f2_arm_r <= f2_crank; j2_arm_r <= j2_crank; g2_arm_r <= ctrl_g2;
                    d1_arm_r <= d1_crank;
                    refuse <= 3'd0; st <= S_WAIT;
                end else begin
                    refused <= 1'b1;
                    refuse  <= ~posture_ok        ? 3'd1 : ~rest_ok  ? 3'd2
                             : (nedge_s == 10'd0) ? 3'd3 : ~grd_100  ? 3'd4
                             : aborted            ? 3'd5 : ~life_ok  ? 3'd6 : 3'd7;
                end
            end
        end
        S_WAIT:                                  // the lane gate is resetting: the baseline is taken here
            if (stop_100) st <= S_IDLE;
            else if (grd_100 & ~grd_q) st <= S_DLY;
        S_DLY:                                   // place the burst inside the snapshot record
            if (dep_100 | stop_100) begin
                aborted <= 1'b1; abort_edge <= 10'd0; st <= S_IDLE;
            end else if (dly_r == 10'd0) begin
                per_cnt <= 24'd0; hi_cnt <= 10'd0;
                st <= (hi_r != 10'd0) ? S_WID : (div_r == 5'd0) ? S_FAST : S_LO;
            end
            else dly_r <= dly_r - 10'd1;
        S_FAST:                                  // DIV = 0: one budgeted rising edge per clk100 cycle
            if (dep_100 | stop_100) begin
                emit_dh <= 1'b0; emit_dl <= 1'b0;
                aborted <= 1'b1; abort_edge <= loaded - budget; st <= S_IDLE;
            end else if (budget == 10'd0) begin
                emit_dh <= 1'b0; emit_dl <= 1'b0; st <= S_IDLE;
            end else begin
                emit_dh <= 1'b1; emit_dl <= 1'b0;             // 5 ns high half = one rising edge
                budget  <= budget - 10'd1;  life <= life + 16'd1;
            end
        S_LO: begin
            emit_dh <= 1'b0; emit_dl <= 1'b0;
            if (dep_100 | stop_100) begin
                aborted <= 1'b1; abort_edge <= loaded - budget; st <= S_IDLE;
            end else if (budget == 10'd0) st <= S_IDLE;
            else if (tick) begin
                emit_dh <= 1'b1; emit_dl <= 1'b1;             // the budgeted rising edge
                budget  <= budget - 10'd1;  life <= life + 16'd1;  st <= S_HI;
            end
        end
        S_HI: begin
            emit_dh <= 1'b1; emit_dl <= 1'b1;
            if (dep_100 | stop_100) begin
                emit_dh <= 1'b0; emit_dl <= 1'b0;
                aborted <= 1'b1; abort_edge <= loaded - budget; st <= S_IDLE;
            end else if (tick) begin emit_dh <= 1'b0; emit_dl <= 1'b0; st <= S_LO; end
        end
        S_WID: begin
            if (dep_100 | stop_100) begin
                emit_dh <= 1'b0; emit_dl <= 1'b0;
                aborted <= 1'b1; abort_edge <= loaded - budget; st <= S_IDLE;
            end else if (per_cnt != 24'd0) begin
                per_cnt <= per_cnt - 24'd1;
                if (hi_cnt != 10'd0) hi_cnt <= hi_cnt - 10'd1;
                else begin emit_dh <= 1'b0; emit_dl <= 1'b0; end
            end else if (budget == 10'd0) begin
                emit_dh <= 1'b0; emit_dl <= 1'b0; st <= S_IDLE;
            end else begin
                per_cnt <= per_r - 24'd1;
                hi_cnt  <= hi_r  - 10'd1;
                emit_dh <= 1'b1; emit_dl <= 1'b1;
                budget  <= budget - 10'd1;  life <= life + 16'd1;
            end
        end
        default: st <= S_IDLE;
        endcase
    end

    // ---- per-ball data: one LUT between the emitter flop and the DDIO cell ----
    // DIAG_CTRL codes 2 (clk50) and 3 (clk100) are RETIRED: they were free-running
    // clock sources on an SRAM-clock candidate. 0 -> 0, 1 -> 1, 2/3 -> 0.
    wire f2_leg  = (f2_sel_s == 2'd1);
    wire j2_leg  = (j2_sel_s == 2'd1);
    wire f2_dh_n = f2_crank ? (f2_arm_r & emit_dh) : ((f2_src == SRC_ZERO) ? 1'b0 : f2_leg);
    wire f2_dl_n = f2_crank ? (f2_arm_r & emit_dl) : ((f2_src == SRC_ZERO) ? 1'b0 : f2_leg);
    wire j2_dh_n = j2_crank ? (j2_arm_r & emit_dh) : ((j2_src == SRC_ZERO) ? 1'b0 : j2_leg);
    wire j2_dl_n = j2_crank ? (j2_arm_r & emit_dl) : ((j2_src == SRC_ZERO) ? 1'b0 : j2_leg);
    ddio_out1 u_f2 (.outclock(clk100), .dh(f2_dh_n), .dl(f2_dl_n), .pad(f2));
    ddio_out1 u_j2 (.outclock(clk100), .dh(j2_dh_n), .dl(j2_dl_n), .pad(j2));

    // ---- D1: the counted emitter's third ball, SDR on clk100, with a pad readback ----
    // D1 is already bidir with a fabric OE, so this costs no pad direction change and no
    // DDIO cell. The output pair MOVES OFF busclk — which the bus-rate mux can make
    // cap_clk — onto clk100, so the +0.128 ns domain loses two flops rather than gaining
    // any. BUSGEN_AUX.D1 codes 3..7 retire to undriven; 0/1/2 are unchanged.
    wire [2:0] aux_d1_100;
    sync_bit #(.W(3)) u_auxd1c (.clk(clk100),
        .d(busgen_aux[`DIAG_BUSGEN_AUX_D1_LSB +: 3]), .q(aux_d1_100));
    wire d1_leg_oe  = (aux_d1_100 == 3'd1) | (aux_d1_100 == 3'd2);
    wire d1_leg_lvl = (aux_d1_100 == 3'd2);
    wire d1_zero    = (d1_src == SRC_ZERO);
    wire d1_lvl_n   = d1_crank ? (d1_arm_r & emit_dh) : d1_zero ? 1'b0 : d1_leg_lvl;
    wire d1_oe_n    = d1_crank | d1_zero | d1_leg_oe;
    (* useioff = 1 *) reg d1_oe_q = 1'b0, d1_o_q = 1'b0, d1_i_q = 1'b0;
    always @(posedge clk100) begin
        d1_oe_q <= d1_oe_n;
        d1_o_q  <= d1_lvl_n;
        d1_i_q  <= d1;                 // the PAD node, the same construct as bus_i_q
    end
    assign d1 = d1_oe_q ? d1_o_q : 1'bz;

    // Three witnesses of one burst: at the emitter, at our own ball, and at the far die.
    // arr_d1 is not an async crossing (driven and sampled in clk100); arr_p6 is, and takes
    // the two-flop sync.
    wire p6_100;
    sync_bit u_p6100 (.clk(clk100), .d(p6), .q(p6_100));
    reg d1_dq = 1'b0, d1_i2 = 1'b0, d1_i3 = 1'b0, p6_q100 = 1'b0;
    reg [15:0] cnt_d1 = 16'd0, arr_d1 = 16'd0, arr_p6 = 16'd0;
    always @(posedge clk100) begin
        d1_dq <= d1_lvl_n;  d1_i2 <= d1_i_q;  d1_i3 <= d1_i2;  p6_q100 <= p6_100;
        cnt_d1 <= arm_go ? 16'd0 : (cnt_d1 == 16'hffff) ? cnt_d1 : cnt_d1 + {15'd0, (~d1_dq & d1_lvl_n)};
        arr_d1 <= arm_go ? 16'd0 : (arr_d1 == 16'hffff) ? arr_d1 : arr_d1 + {15'd0, (~d1_i3 & d1_i2)};
        arr_p6 <= arm_go ? 16'd0 : (arr_p6 == 16'hffff) ? arr_p6 : arr_p6 + {15'd0, (~p6_q100 & p6_100)};
    end

    // ---- the witness: rising transitions of the data the DDIO cell latches, in the
    //      cell's own domain. pad = {.. dl(k-1), dh(k), dl(k) ..}
    reg f2_dl_q = 1'b0, j2_dl_q = 1'b0;
    reg [15:0] cnt_f2 = 16'd0, cnt_j2 = 16'd0;
    wire [1:0] f2_inc = {1'b0, ~f2_dl_q & f2_dh_n} + {1'b0, ~f2_dh_n & f2_dl_n};
    wire [1:0] j2_inc = {1'b0, ~j2_dl_q & j2_dh_n} + {1'b0, ~j2_dh_n & j2_dl_n};
    always @(posedge clk100) begin
        f2_dl_q <= f2_dl_n; j2_dl_q <= j2_dl_n;
        cnt_f2 <= arm_go ? 16'd0 : (cnt_f2 >= 16'hfffd) ? 16'hffff : (cnt_f2 + {14'd0, f2_inc});
        cnt_j2 <= arm_go ? 16'd0 : (cnt_j2 >= 16'hfffd) ? 16'hffff : (cnt_j2 + {14'd0, j2_inc});
    end

    // ---- every status word crossed to clk, and muxed THERE ----------------
    // The muxed-then-crossed shape (select in clk100 from a synchronised diag_idx, then
    // sync one word back) costs fewer flops but gives these five registers a read latency
    // no other DIAG window has: the index has to reach clk100 and the word has to come
    // back, so a read taken a few cycles after DIAG_IDX returns the PREVIOUS index's word.
    // tb_diag caught exactly that. A register file where one group needs a settling rule
    // nobody else needs is a trap for every future reader, so pay the flops instead.
    wire [15:0] rd_stat, rd_cf2, rd_cj2, rd_life, rd_dep;
    sync_bit #(.W(16)) u_sc_st (.clk(clk), .d({refuse, refused, aborted, (st != S_IDLE), budget}), .q(rd_stat));
    sync_bit #(.W(16)) u_sc_f2 (.clk(clk), .d(cnt_f2),               .q(rd_cf2));
    sync_bit #(.W(16)) u_sc_j2 (.clk(clk), .d(cnt_j2),               .q(rd_cj2));
    sync_bit #(.W(16)) u_sc_lf (.clk(clk), .d(life),                 .q(rd_life));
    sync_bit #(.W(16)) u_sc_dp (.clk(clk), .d({abort_edge, dep_grp}), .q(rd_dep));
    wire [15:0] rd_cd1, rd_ad1, rd_ap6;
    sync_bit #(.W(16)) u_sc_cd1 (.clk(clk), .d(cnt_d1), .q(rd_cd1));
    sync_bit #(.W(16)) u_sc_ad1 (.clk(clk), .d(arr_d1), .q(rd_ad1));
    sync_bit #(.W(16)) u_sc_ap6 (.clk(clk), .d(arr_p6), .q(rd_ap6));
    wire [15:0] srclk_rd = (diag_idx == `DIAG_SRCLK_CNT_D1) ? rd_cd1
                         : (diag_idx == `DIAG_SRCLK_ARR_D1) ? rd_ad1
                         : (diag_idx == `DIAG_SRCLK_ARR_P6) ? rd_ap6
                         : (diag_idx == `DIAG_SRCLK_STAT)   ? rd_stat
                         : (diag_idx == `DIAG_SRCLK_CNT_F2) ? rd_cf2
                         : (diag_idx == `DIAG_SRCLK_CNT_J2) ? rd_cj2
                         : (diag_idx == `DIAG_SRCLK_LIFE)   ? rd_life : rd_dep;

    // =====================================================================
    // lane / bus monitor (clk50)
    // =====================================================================
    wire [79:0] lane_s;
    wire        p6_s50, a2_s50, b1_s50, gate_s;
    wire        b11_s50, j1_s50;
    wire [6:0]  lidx_s;
    sync_bit #(.W(80)) u_lane50 (.clk(clk50), .d(lane_q),   .q(lane_s));
    sync_bit #(.W(3))  u_misc50 (.clk(clk50), .d({gpmc_b1, gpmc_a2, p6}), .q({b1_s50, a2_s50, p6_s50}));
    sync_bit #(.W(2))  u_in50   (.clk(clk50), .d({j1, b11}), .q({j1_s50, b11_s50}));
    sync_bit           u_gate   (.clk(clk50), .d(diag_ctrl[`DIAG_CTRL_LANE_GATE_RST_LSB]), .q(gate_s));
    sync_bit #(.W(7))  u_lidx   (.clk(clk50), .d(lane_idx), .q(lidx_s));

    // bits 124/125 were part of the old 4'd0 tail: LANE_IDX 0x7c = B11, 0x7d = J1.
    wire [127:0] mon = {2'd0, j1_s50, b11_s50, k2_en_s, b1_s50, a2_s50, p6_s50,
                        3'd0, sgl_i_q, 5'd0, bus_i50, lane_s};
    wire [127:0] ever1, ever0;
    // ---- SRCLK lane guard (clk50) -------------------------------------
    // The accepted arm pulses the gate itself, so the baseline cannot be forgotten;
    // the burst does not start until the pulse has cleared (S_WAIT below).
    wire arm_50;
    sync_pulse u_sc_a50 (.clk_src(clk100), .pulse_src(arm_go), .clk_dst(clk50), .pulse_dst(arm_50));
    reg  [3:0] gate_cnt = 4'd0;
    always @(posedge clk50)
        if (arm_50)         gate_cnt <= 4'd8;
        else if (|gate_cnt) gate_cnt <= gate_cnt - 4'd1;
    wire guard_rdy50 = (gate_cnt == 4'd0);

    wire [1:0] gsel50;
    sync_bit #(.W(2)) u_sc_gs (.clk(clk50), .d(srclk_ctrl[7:6]), .q(gsel50));
    wire d1_crank_50;
    sync_bit u_d1c50 (.clk(clk50), .d(d1_crank), .q(d1_crank_50));
    wire [127:0] moved_raw = ever1 & ever0;
    // P6 (mon bit 120) is the far-end witness: a D1 crank moves it BY CONSTRUCTION. It must
    // not be able to fire the guard or set dep_grp, or every D1 burst would abort at edge 1
    // and report a phantom departure. Its per-signal truth stays at LANE_IDX 0x78, unmasked,
    // and its count is SRCLK_ARR_P6. The mask is conditional, so an F2 control rung still
    // watches P6 with the full guard — and a P6 departure THERE is real information.
    wire [127:0] moved = moved_raw & ~{7'd0, d1_crank_50, 120'd0};
    reg  [5:0] dep_grp = 6'd0;
    reg        dep50   = 1'b0;
    always @(posedge clk50) begin
        dep_grp <= { |moved[127:80], |moved[79:64], |moved[63:48],
                     |moved[47:32],  |moved[31:16], |moved[15:0] };
        dep50   <= (gsel50 == 2'd0) ? 1'b0
                 : (gsel50 == 2'd1) ? (|moved[79:0]) : (|moved);
    end
    // the 27-ball rest posture, checked in fabric at the instant of arm (REFUSE code 2)
    reg rest50 = 1'b0;
    always @(posedge clk50) rest50 <= &bus_i50;

    wire gate_eff = gate_s | (gate_cnt > 4'd2);
    lane_mon #(.N(128)) u_mon (.clk(clk50), .d(mon), .gate_rst(gate_eff), .ever1(ever1), .ever0(ever0));
    reg mon_sel_q = 1'b0;
    always @(posedge clk50) mon_sel_q <= mon[lidx_s];
    wire [15:0] tog_cnt;
    wire        tog_done;
    toggle_ctr u_tog (.clk(clk50), .d(mon_sel_q), .count(tog_cnt), .window_done(tog_done));
    wire [18:0] lane_rep50 = {tog_cnt, ever0[lidx_s], ever1[lidx_s], mon_sel_q};
    wire [18:0] lane_rep;
    sync_word #(.W(19)) u_lanerep (.clk_src(clk50), .d_src(lane_rep50), .clk_dst(clk), .q_dst(lane_rep));

    wire [26:0] bus_rd;
    sync_bit #(.W(27)) u_busrd (.clk(clk), .d(bus_i_q), .q(bus_rd));

    // =====================================================================
    // MISC_RD (clk)
    // =====================================================================
    wire [10:0] misc_s;
    sync_bit #(.W(11)) u_misc (.clk(clk),
        .d({nOE_pad, nWE_pad, d1, gpmc_b1, gpmc_a2, single[4], single[3], single[2], single[1], single[0], p6}),
        .q(misc_s));
    // G2 is a DDIO output now (BUSGEN_AUX), and a DDIO output port may not feed
    // core logic, so MISC_RD reports the level DIAG_CTRL asks for rather than the
    // pad. They are the same whenever BUSGEN_AUX.G2 is 0, which is its reset value.
    assign rdata_misc = {4'd0, misc_s[10:9], diag_ctrl[`DIAG_CTRL_G2_LSB], misc_s[8:0]};

    // =====================================================================
    // DIAG_DATA read mux (clk)
    // =====================================================================
    always @* begin
        case (diag_idx)
            `DIAG_BUS_OE_LO:   rdata_data = bus_oe[15:0];
            `DIAG_BUS_OE_HI:   rdata_data = {5'd0, bus_oe[26:16]};
            `DIAG_BUS_DRV_LO:  rdata_data = bus_drv[15:0];
            `DIAG_BUS_DRV_HI:  rdata_data = {5'd0, bus_drv[26:16]};
            `DIAG_BUS_RD_LO:   rdata_data = bus_rd[15:0];
            `DIAG_BUS_RD_HI:   rdata_data = {5'd0, bus_rd[26:16]};
            `DIAG_SINGLE_CTRL: rdata_data = {3'd0, single_drv, 3'd0, single_oe};
            `DIAG_ADC_HOLD:    rdata_data = {13'd0, adc_hold};
            `DIAG_LANE_IDX:    rdata_data = {9'd0, lane_idx};
            `DIAG_LANE_TOG:    rdata_data = lane_rep[18:3];
            `DIAG_LANE_LVL:    rdata_data = {13'd0, lane_rep[2:0]};     // {ever0, ever1, level}
            `DIAG_SNAP_MODE:   rdata_data = {13'd0, snap_mode};
            `DIAG_BUSGEN_CTL:  rdata_data = busgen_ctl;
            `DIAG_BUSGEN_AUX:  rdata_data = busgen_aux;
            `DIAG_BUSGEN_OE_LO: rdata_data = busgen_oe_lo;
            `DIAG_BUSGEN_OE_HI: rdata_data = {5'd0, busgen_oe_hi[10:0]};
            `DIAG_BUSCAP_CTL:  rdata_data = {12'd0, cap_wait_c, cap_trigd_c,
                                             buscap_ctl[`DIAG_BUSCAP_CTL_TRIG_LSB],
                                             buscap_ctl[`DIAG_BUSCAP_CTL_EN_LSB]};
            default:           rdata_data = idx_is_map ? {9'd0, map_entry}
                                            : idx_is_pat ? busgen_pat[pat_i] : srclk_word;  // 0 elsewhere
        endcase
    end

    // =====================================================================
    // snapshot RAM 2048 x 16 (4 M9K), write at snap_clk, read at clk
    // =====================================================================
    reg [1:0] snap_clk_sel = 2'd0;
    reg [2:0] snap_mode_q  = 3'd0;
    always @(posedge clk) begin snap_clk_sel <= diag_ctrl[`DIAG_CTRL_SNAP_CLK_LSB +: 2]; snap_mode_q <= snap_mode; end
    wire snap_clk = (snap_clk_sel == 2'd0) ? clk50 : (snap_clk_sel == 2'd1) ? clk100
                  : (snap_clk_sel == 2'd2) ? cap_clk : clk;                 // LUT clock mux

    // SNAP_ON_ARM is NOT wired in this build. Coupling the crank into arm_c is the one
    // new path into an existing, working, timing-critical block, and it made tb_diag hang:
    // an unknown on the released bus reaches the crank's rest-posture interlock, and from
    // there the snapshot arm. The convenience it buys — one record holding both the
    // pre-edge baseline and the post-edge window — is not worth putting the snapshot arm
    // downstream of an interlock that reads floating inputs. The app arms the snapshot
    // itself immediately before arming the crank, and the spec's own edge-lock proof is
    // the delay-shift rung (repeat at DLY+400, the onset must move by +160 +/- 4), not the
    // pre/post split.
    wire arm_c = we_ctrl & wr_data[`DIAG_CTRL_SNAP_ARM_LSB];
    wire arm_s;
    sync_pulse u_arm (.clk_src(clk), .pulse_src(arm_c), .clk_dst(snap_clk), .pulse_dst(arm_s));

    wire [79:0] lane_ss;
    wire [26:0] bus_ss;
    wire [4:0]  sgl_ss;
    wire        p6_ss;
    wire [2:0]  mode_ss;
    sync_bit #(.W(80)) u_lane_ss (.clk(snap_clk), .d(lane_q),   .q(lane_ss));
    sync_bit #(.W(27)) u_bus_ss  (.clk(snap_clk), .d(bus_i_q),  .q(bus_ss));
    sync_bit #(.W(5))  u_sgl_ss  (.clk(snap_clk), .d(sgl_i_q),  .q(sgl_ss));
    sync_bit           u_p6_ss   (.clk(snap_clk), .d(p6),       .q(p6_ss));
    sync_bit #(.W(3))  u_mode_ss (.clk(snap_clk), .d(snap_mode_q), .q(mode_ss));

    // BUSCAP: the 27-ball bus recorded two words per sample, with a departure trigger.
    // Set SNAP_CLK to the same source as BUSGEN_CTL.RATE and every bus tick lands in the RAM.
    wire [26:0] oe_ss;                       // what the generator is driving, in the writer's domain
    wire        cap_en_ss, cap_trig_ss;
    sync_bit #(.W(27)) u_oe_ss  (.clk(snap_clk), .d(bus_oe_q), .q(oe_ss));
    sync_bit u_capen  (.clk(snap_clk), .d(buscap_ctl[`DIAG_BUSCAP_CTL_EN_LSB]),   .q(cap_en_ss));
    sync_bit u_captrg (.clk(snap_clk), .d(buscap_ctl[`DIAG_BUSCAP_CTL_TRIG_LSB]), .q(cap_trig_ss));
    reg  [26:0] cap_base = 27'd0;            // the idle posture latched at arm
    reg         cap_wait = 1'b0;
    reg         cap_trigd = 1'b0;
    wire [26:0] cap_undriven = ~oe_ss;
    // The comparator is 27 wide and the reduction two LUT levels deep: at
    // 200 MHz it cannot also reach the address walk in the same tick, so it is
    // pipelined. dep_q is therefore one tick behind the sample that departed,
    // and the record starts one bus tick after the departure. dep_hold blanks
    // the two ticks after arm, while cap_base is still propagating.
    reg  [26:0] dep_bits = 27'd0;
    reg         dep_q    = 1'b0;
    reg  [1:0]  dep_hold = 2'b11;
    // A lone sample is not evidence.  With the group switching rail to rail a floating ball reads
    // low for one sample often enough to fire a 1-sample trigger in 6 runs of 10, and the record
    // then shows it already back at its idle level.  BUSCAP_CTL.CONFIRM says how many consecutive
    // samples the departure must hold.  Registered, not combinational: the 200 MHz domain has no
    // room for the compare, so the trigger is one further tick behind.
    reg  [6:0]  dep_sh   = 7'd0;
    reg         dep_ok   = 1'b0;
    wire [1:0]  cap_conf;
    sync_bit #(.W(2)) u_capcf (.clk(snap_clk), .d(buscap_ctl[`DIAG_BUSCAP_CTL_CONFIRM_LSB +: 2]), .q(cap_conf));
    // The depth select is quasi-static, so it is registered into a mask and kept OFF the trigger
    // path: what is left is one 7-bit mask-and-reduce, two LUT levels, which the 200 MHz domain has
    // room for (a 4:1 mux over four AND reductions did not).
    reg  [6:0]  conf_mask = 7'd0;
    always @(posedge snap_clk)
        conf_mask <= (cap_conf == 2'd0) ? 7'b0000000 : (cap_conf == 2'd1) ? 7'b0000001
                   : (cap_conf == 2'd2) ? 7'b0000111 : 7'b1111111;
    wire        dep_run = dep_q & ~(|(~dep_sh & conf_mask));
    wire        cap_depart   = dep_ok;
    wire        start_now    = cap_wait & cap_depart;

    // Write pipeline (WP1c, 200 MHz when SNAP_CLK = cap_clk): stage 0 walks the address and the
    // 5-word phase; stage 1 registers the per-mode candidates (a 5:1 lane-slice mux and the 5:1
    // hold-word mux each), stage 2 registers the 8:1 mode mux; the M9K write follows. Nothing
    // deeper than two LUT levels sits between registers; the data written per address is exactly
    // what the un-pipelined writer wrote (mode 7: ph 0 = the live lanes 0-15, ph 1..4 = the held
    // sample's lanes 16-31 .. 64-79).
    reg         running = 1'b0;
    reg [10:0]  wa      = 11'd0;
    reg [2:0]   ph      = 3'd0;
    reg [79:0]  lane_hold = 80'd0;
    reg [2:0]   msel    = 3'd0;          // mode_ss clamped to the five lane slices
    always @(posedge snap_clk) msel <= (mode_ss < 3'd5) ? mode_ss : 3'd0;
    // stage 1
    reg         v1 = 1'b0;  reg [10:0] wa1 = 11'd0;  reg [2:0] ph1 = 3'd0;
    reg [15:0]  sl_q = 16'd0, hold_q = 16'd0, bus_lo_q = 16'd0, misc_q = 16'd0;
    reg [15:0]  misc_hold = 16'd0;   // BUSCAP: the high half of the SAME tick as the low half
    reg [15:0]  cap_q     = 16'd0;   // BUSCAP: the word this beat writes
    // stage 2
    reg         v2 = 1'b0;  reg [10:0] wa2 = 11'd0;
    reg [15:0]  wdata_q = 16'd0;
    reg         snap_done = 1'b0;
    always @(posedge snap_clk) begin
        snap_done <= 1'b0;
        dep_hold <= arm_s ? 2'b11 : {dep_hold[0], 1'b0};
        dep_bits <= (bus_ss ^ cap_base) & cap_undriven;
        // ~arm_s as well as the blank: at the arm edge dep_bits still holds the
        // comparison against the PREVIOUS run's cap_base, and cap_wait is set on
        // that same edge, so an unmasked dep_q would trigger the run immediately.
        dep_q    <= (|dep_bits) & ~dep_hold[1] & ~arm_s;
        dep_sh   <= arm_s ? 7'd0 : {dep_sh[5:0], dep_q};
        dep_ok   <= arm_s ? 1'b0 : dep_run;
        // stage 0: address / phase walk
        if (arm_s) begin
            wa <= 11'd0; ph <= 3'd0; cap_base <= bus_ss; cap_trigd <= 1'b0;
            if (cap_en_ss & cap_trig_ss) begin running <= 1'b0; cap_wait <= 1'b1; end
            else                         begin running <= 1'b1; cap_wait <= 1'b0; end
        end else if (cap_wait) begin
            // the departure sample is sample 0: stage 1 latches it on this very
            // edge, so mark it valid (start_now) and step the walk past it.
            if (cap_depart) begin
                cap_wait <= 1'b0; cap_trigd <= 1'b1; running <= 1'b1;
                wa <= 11'd1; ph <= 3'd1;
            end
        end else if (running) begin
            wa <= wa + 11'd1;
            if (ph == 3'd0) lane_hold <= lane_ss;
            ph <= cap_en_ss ? ((ph == 3'd1) ? 3'd0 : 3'd1)
                            : ((ph == 3'd4) ? 3'd0 : (ph + 3'd1));
            if (wa == 11'd2047) running <= 1'b0;
        end
        // stage 1: candidates
        v1 <= (running | start_now) & ~arm_s; wa1 <= wa; ph1 <= ph;
        sl_q     <= lane_ss[16*msel +: 16];
        hold_q   <= lane_hold[16*ph +: 16];
        bus_lo_q <= bus_ss[15:0];
        misc_q   <= {sgl_ss[3:0], p6_ss, bus_ss[26:16]};
        if (ph == 3'd0) misc_hold <= {sgl_ss[3:0], p6_ss, bus_ss[26:16]};
        cap_q    <= (ph == 3'd0) ? bus_ss[15:0] : misc_hold;
        // stage 2: mode select
        v2 <= v1; wa2 <= wa1;
        if (cap_en_ss) wdata_q <= cap_q;                                // one coherent tick per sample
        else case (mode_ss)
            3'd0, 3'd1, 3'd2, 3'd3, 3'd4: wdata_q <= sl_q;
            3'd5:    wdata_q <= bus_lo_q;
            3'd6:    wdata_q <= misc_q;
            default: wdata_q <= (ph1 == 3'd0) ? sl_q : hold_q;
        endcase
        if (v2 && wa2 == 11'd2047) snap_done <= 1'b1;
    end
    (* ramstyle = "M9K" *) reg [15:0] smem [0:2047];
    always @(posedge snap_clk) if (v2) smem[wa2] <= wdata_q;

    wire cap_trigd_c, cap_wait_c;
    sync_bit u_captd (.clk(clk), .d(cap_trigd), .q(cap_trigd_c));
    sync_bit u_capwt (.clk(clk), .d(cap_wait),  .q(cap_wait_c));

    wire done_c, running_c;
    sync_pulse u_done (.clk_src(snap_clk), .pulse_src(snap_done), .clk_dst(clk), .pulse_dst(done_c));
    sync_bit   u_run  (.clk(clk), .d(running), .q(running_c));

    reg        ready  = 1'b0;
    reg [11:0] rp     = 12'd0;       // 0..2048
    reg [15:0] snap_q = 16'd0;
    wire [10:0] ra = (rp == 12'd2048) ? 11'd2047 : rp[10:0];
    always @(posedge clk) begin
        snap_q <= smem[ra];
        if (arm_c) begin ready <= 1'b0; rp <= 12'd0; end
        else if (done_c) begin ready <= 1'b1; rp <= 12'd0; end
        else if (pop_snap && ready && rp != 12'd2048) rp <= rp + 12'd1;
    end
    wire [11:0] snap_remain = 12'd2048 - rp;
    assign rdata_snap_remain = {ready, 3'd0, snap_remain};
    assign rdata_snap_pop    = ready ? snap_q : 16'h0000;
    assign dbg_snap          = {running_c, ready};
endmodule

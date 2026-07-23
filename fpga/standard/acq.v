// acq.v — TOP of the standard (owned) acquisition bitstream for the SDS1000CML
//         acquisition FPGA (Altera Cyclone IV E, EP4CE10F17C8).
//
// This is the GPMC async slave + the generated register interface + the wiring of the
// six-module streaming-spine acquisition engine. It replaces the vendor acquisition
// path outright (the app refuses a fabric whose build-ID differs). Module set:
//
//   spine.v     canonical >=18-bit stream {ch1,ch2,valid,idx,trig_mark}: the live
//               decimator (transform stage 0) + two reserved bypassable stages (C1).
//   capture.v   circular pre/post-trigger writer + record M9K + trigger accept +
//               EXACT post-count window + interpolating TRIGPOS + static freeze (C2).
//   envelope.v  LIVE-STREAM min/max reducer + envelope result FIFO (C3, overflow).
//   drain.v     single auto-inc BURST port (1-D DMA source) + BURST_REMAIN.
//   dac.v       trigger-level DAC serializer, tri-stated until the first level load.
//
// GENERATED INTERFACE (single source of truth — do NOT hand-define selectors)
//   `include "regs.vh"   : `SEL_* selectors, plane ids, field _MASK/_LSB, the capture
//                          geometry `REC_DEPTH/`ADDR_W/`PRETRIG_MAX, `IFACE_BUILD_ID.
//   `include "regmux.vh" : generated write-strobe (we_<REG>) + read-mux (rmux_rdata)
//                          decode from the schema Access/Sem, so the selector decode
//                          can NEVER drift. Hand RTL only assigns behavior behind the
//                          named rdata_<REG> wires; it never writes a selector case.
//   BUILDID_LO/HI return `IFACE_BUILD_ID_LO/HI; VERSION returns 0x0052 (both from the
//   generated read mux, straight from the schema).
//
// HARDWARE-SAFETY ENVELOPE (NON-NEGOTIABLE, DESIGN.md sec.1)
//   * ONE tri-state driver on gpmc_d, enabled ONLY on read_active = ~nCS1 & ~nOE
//     (CS1 reads only); every other cycle Hi-Z. CS3 reads (incl. CONF_DONE) stay
//     Hi-Z — the config port is NEVER driven.
//   * gpmc_wait held ready at all times (never wedge the bus).
//   * DAC balls Hi-Z until the first level load (inside dac.v).
//   * clk is the SAMPLE-BUS domain (BENCH-VERIFY #2); the async GPMC strobes/selector/
//     data + the sample bus + trig_sense cross in via 2-3 FF synchronizers + edge
//     detect. sample_in / trig_sense / the GPMC strobe balls are inputs only.
//   * every driven ball is capped to MINIMUM CURRENT in acq.qsf.
//
// Clean-room: design/spec-derived, never vendor RTL. Synthesizable Verilog-2001.

`include "regs.vh"

module acq (
    // free-running reference / SAMPLE-BUS clock (BENCH-VERIFY #2).
    input  wire        clk,

    // inter-FPGA sample bus (INPUT — the aux FPGA drives it; hi=CH1, lo=CH2).
    input  wire [15:0] sample_in,

    // HW trigger comparator output (level-DAC-fed): "a crossing occurred".
    input  wire        trig_sense,

    // GPMC bus.
    input  wire        nCS1,        // CS1 acquisition/read plane, active-low
    input  wire        nCS3,        // CS3 config/control plane, active-low
    input  wire        nOE,         // read strobe, active-low
    input  wire        nWE,         // write strobe, active-low
    input  wire [7:0]  sel,         // selector == byte_addr>>1 (pre-decoded)
    inout  wire [15:0] gpmc_d,      // 16-bit data bus; driven ONLY on a CS1 read
    output wire        gpmc_wait,   // WAIT/ready line (held ready)

    // trigger LEVEL DAC (3-wire serial, FPGA-driven; Hi-Z until first load).
    output wire        dac_sync,
    output wire        dac_sclk,
    output wire        dac_sdi
);

    // ---- OPCODE command payloads: values written to the OPCODE strobe
    //      register (NOT selectors). `OP_RESET/`OP_GO/`OP_HALT come from the
    //      generated regs.vh, so the app and this decode share one literal and
    //      the encoding is folded into the build-ID (no silent drift). ------

    // ---- sized geometry / clamp constants ---------------------------------
    localparam [15:0] REC_DEPTH16   = `REC_DEPTH;    // 20480
    localparam [15:0] PRETRIG_MAX16 = `PRETRIG_MAX;  // 20478 = RecordDepth-Margin (schema)
    // pre+post ceiling == the schema's PretrigMax. Derived from the generated
    // `PRETRIG_MAX (not a hand copy of MARGIN) so a schema Margin change can't
    // leave this clamp one cell too loose while `PRETRIG_MAX tracks it.
    localparam [15:0] CAP_MAX       = PRETRIG_MAX16;

    // =======================================================================
    // 1) GPMC-strobe / selector / data / sample / trigger synchronizers
    //    (bus -> clk domain). 3-bit shift regs: [2]=oldest/settled, [0]=newest.
    // =======================================================================
    reg [2:0]  cs1_q = 3'b111, cs3_q = 3'b111, oe_q = 3'b111, we_q = 3'b111;
    reg [7:0]  sel_q1 = 8'h00, sel_q2 = 8'h00;
    reg [15:0] d_q1 = 16'h0, d_q2 = 16'h0;
    reg [15:0] samp_q1 = 16'h0, samp_q2 = 16'h0;
    reg [2:0]  trig_q = 3'b000;

    always @(posedge clk) begin
        cs1_q   <= {cs1_q[1:0], nCS1};
        cs3_q   <= {cs3_q[1:0], nCS3};
        oe_q    <= {oe_q[1:0],  nOE};
        we_q    <= {we_q[1:0],  nWE};
        sel_q1  <= sel;        sel_q2  <= sel_q1;
        d_q1    <= gpmc_d;     d_q2    <= d_q1;      // meaningful only during a write
        samp_q1 <= sample_in;  samp_q2 <= samp_q1;
        trig_q  <= {trig_q[1:0], trig_sense};
    end

    wire cs1_low   = (cs1_q[2] == 1'b0);
    wire cs3_low   = (cs3_q[2] == 1'b0);
    wire trig_rise = (trig_q[2] == 1'b0) && (trig_q[1] == 1'b1);   // comparator rising edge

    // ---- regmux write/read handshake signals (provided to the generated include) --
    wire       we_commit = (we_q[2] == 1'b0) && (we_q[1] == 1'b1);  // nWE rising: data+sel settled
    wire [7:0] wr_plane  = cs1_low ? `PLANE_CS1 : (cs3_low ? `PLANE_CS3 : 8'h00);
    wire [7:0] wr_sel    = sel_q2;
    wire [7:0] rd_plane  = (~nCS1) ? `PLANE_CS1 : ((~nCS3) ? `PLANE_CS3 : 8'h00);
    wire [7:0] rd_sel    = sel;    // raw for the combinational read mux (CPU holds nOE)

    // ---- auto-inc read decode (synchronized; one pop / nOE-rise) -----------
    wire sel_is_burst    = (sel_q2 == `SEL_BURST);
    wire burst_rd_done   = cs1_low && (oe_q[2] == 1'b0) && (oe_q[1] == 1'b1) && sel_is_burst;
    wire sel_is_env      = (sel_q2 == `SEL_ENV_DATA);
    wire env_rd_active   = cs1_low && (oe_q[2] == 1'b0) && sel_is_env;
    wire env_rd_done     = cs1_low && (oe_q[2] == 1'b0) && (oe_q[1] == 1'b1) && sel_is_env;

    // =======================================================================
    // 2) Control / configuration register file (written via the generated
    //    we_<REG> strobes -> no hand-written selector case).
    // =======================================================================
    reg [15:0] run_word    = 16'h0000;
    reg [31:0] decim_reg   = 32'd1;        // native-fast at reset (tick every clk)
    reg [31:0] pretrig_reg = 32'd10240;
    reg [31:0] posttrig_reg= 32'd10240;
    reg [15:0] xform_reg   = 16'h0003;     // both transform stages bypassed at reset (raw)
    reg [15:0] env_cols_reg= 16'd256;
    reg [15:0] led_reg     = 16'h0000;     // reserved front-end latches (no ball this cut)
    reg [15:0] off_c1      = 16'h0000;
    reg [15:0] off_c2      = 16'h0000;
    reg [7:0]  dac_lo_a    = 8'h00;
    reg [7:0]  dac_lo_b    = 8'h00;
    reg [15:0] dac_code    = 16'h0000;     // last-loaded serializer code
    reg [15:0] dac_code_a  = 16'h0000;     // lane-A code (source of the CH1 trig level)
    reg        dac_load    = 1'b0;

    wire       run_en    = run_word[`RUN_RUN_LSB];
    wire       mode_norm = (run_word[`RUN_MODE_LSB +: 2] == 2'd1);   // 0=AUTO, 1=NORM
    wire [7:0] trig_level= dac_code_a[15:8];   // CH1 trigger level, sample units [BENCH-TUNE]

    // ---- rdata_<REG> nets consumed by the generated read mux (declared before
    //      the include; driven by continuous assigns / instance ports below). --
    wire [15:0] rdata_RUN, rdata_DECIM_LO, rdata_DECIM_HI;
    wire [15:0] rdata_PRETRIG_LO, rdata_PRETRIG_HI, rdata_POSTTRIG_LO, rdata_POSTTRIG_HI;
    wire [15:0] rdata_BURST, rdata_BURST_REMAIN;
    wire [15:0] rdata_STATUS_A, rdata_TRIGPOS_LO, rdata_TRIGPOS_HI, rdata_FILL;
    wire [15:0] rdata_XFORM_CTRL, rdata_ENV_COLS, rdata_ENV_DATA, rdata_ENV_COUNT;
    wire [15:0] rdata_CONF_DONE;

    // ---- GENERATED write-strobe + read-mux decode (schema SSOT) ----
    `include "regmux.vh"

    // ---- register writes (one we_<REG> pulse per accepted write) ----
    always @(posedge clk) begin
        dac_load <= 1'b0;                          // 1-cycle pulse default
        if (we_RUN)         run_word          <= d_q2;
        if (we_DECIM_LO)    decim_reg[15:0]   <= d_q2;
        if (we_DECIM_HI)    decim_reg[31:16]  <= d_q2;
        if (we_PRETRIG_LO)  pretrig_reg[15:0] <= d_q2;
        if (we_PRETRIG_HI)  pretrig_reg[31:16]<= d_q2;
        if (we_POSTTRIG_LO) posttrig_reg[15:0]<= d_q2;
        if (we_POSTTRIG_HI) posttrig_reg[31:16]<= d_q2;
        if (we_XFORM_CTRL)  xform_reg         <= d_q2;
        if (we_ENV_COLS)    env_cols_reg      <= d_q2;
        if (we_LED_LO)      led_reg[7:0]      <= d_q2[7:0];
        if (we_LED_HI)      led_reg[15:8]     <= d_q2[7:0];
        if (we_OFF_C1_LO)   off_c1[7:0]       <= d_q2[7:0];
        if (we_OFF_C1_HI)   off_c1[15:8]      <= d_q2[7:0];
        if (we_OFF_C2_LO)   off_c2[7:0]       <= d_q2[7:0];
        if (we_OFF_C2_HI)   off_c2[15:8]      <= d_q2[7:0];
        if (we_LVL_A_LO)    dac_lo_a          <= d_q2[7:0];
        if (we_LVL_B_LO)    dac_lo_b          <= d_q2[7:0];
        if (we_LVL_A_HI) begin
            dac_code   <= {d_q2[7:0], dac_lo_a};
            dac_code_a <= {d_q2[7:0], dac_lo_a};
            dac_load   <= 1'b1;                     // self-latch + load serializer
        end
        if (we_LVL_B_HI) begin
            dac_code <= {d_q2[7:0], dac_lo_b};
            dac_load <= 1'b1;
        end
        // we_LED_STROBE / we_ENV_RESET are consumed by their target blocks; no-op here.
    end

    // ---- OPCODE decode -> single-cycle engine pulses ----
    // Full 16-bit compare against the generated payload macros (d_q2 is the
    // registered 16-bit write word); the app writes the identical iface.OP_* value.
    wire op_reset = we_OPCODE && (d_q2 == `OP_RESET);
    wire op_go    = we_OPCODE && (d_q2 == `OP_GO) && run_en;    // honored only while RUN
    wire op_halt  = we_OPCODE && (d_q2 == `OP_HALT);

    // =======================================================================
    // 3) Arm-time joint pre/post clamp (C2): field-clamp each, then trim the
    //    sum to CAP_MAX = REC_DEPTH-MARGIN (post first, then pre). Combinational;
    //    both capture and envelope latch these on OP_GO. pre+post <= REC_DEPTH-2
    //    can never wrap the circular buffer over a still-needed pre-trigger cell.
    // =======================================================================
    wire [15:0] pre_req  = (pretrig_reg[15:0] > PRETRIG_MAX16) ? PRETRIG_MAX16 : pretrig_reg[15:0];
    wire [15:0] post_raw = (posttrig_reg[15:0] == 16'd0) ? 16'd1 : posttrig_reg[15:0];
    // clamp post to CAP_MAX just like pre, so req_sum cannot overflow 16 bits and
    // silently bypass the pre+post <= REC_DEPTH-2 guarantee for out-of-range post.
    wire [15:0] post_req = (post_raw > CAP_MAX) ? CAP_MAX : post_raw;
    wire [15:0] req_sum  = pre_req + post_req;                 // <= 2*CAP_MAX = 40956, fits 16 bits
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
            pre_work_w  = CAP_MAX - 16'd1;   // post floored to 1 -> pre absorbs the rest
            post_work_w = 16'd1;
        end
    end

    // =======================================================================
    // 4) Engine wiring
    // =======================================================================
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

    spine u_spine (
        .clk      (clk),
        .filling  (filling),
        .samp     (samp_q2),
        .decim    (decim_reg),
        .bypass0  (xform_reg[`XFORM_CTRL_BYPASS0_LSB]),
        .bypass1  (xform_reg[`XFORM_CTRL_BYPASS1_LSB]),
        .cap_word (cap_word),
        .cap_tick (cap_tick)
    );

    capture u_capture (
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
        .frame_done  (frame_done)
    );

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

    dac u_dac (
        .clk      (clk),
        .dac_load (dac_load),
        .dac_code (dac_code),
        .dac_sync (dac_sync),
        .dac_sclk (dac_sclk),
        .dac_sdi  (dac_sdi)
    );

    // =======================================================================
    // 5) rdata_<REG> behavior wires (the generated mux selects among these)
    // =======================================================================
    assign rdata_RUN         = run_word;
    assign rdata_DECIM_LO    = decim_reg[15:0];
    assign rdata_DECIM_HI    = decim_reg[31:16];
    assign rdata_PRETRIG_LO  = pretrig_reg[15:0];
    assign rdata_PRETRIG_HI  = pretrig_reg[31:16];
    assign rdata_POSTTRIG_LO = posttrig_reg[15:0];
    assign rdata_POSTTRIG_HI = posttrig_reg[31:16];
    assign rdata_BURST       = coherent ? rec_rd_data : 16'h0000;   // auto-inc record word
    assign rdata_STATUS_A    = {13'd0, r_done, r_trig, r_valid};    // clean level status
    assign rdata_TRIGPOS_LO  = trig_frac;                           // FRAC[15:0] (Q16)
    assign rdata_TRIGPOS_HI  = {1'b0, trig_idx};                    // IDX[14:0] (`ADDR_W=15)
    assign rdata_FILL        = {5'd0, fill_out};                    // COUNT[10:0]
    assign rdata_XFORM_CTRL  = {14'd0, xform_reg[`XFORM_CTRL_BYPASS1_LSB],
                                       xform_reg[`XFORM_CTRL_BYPASS0_LSB]};
    assign rdata_ENV_COLS    = env_cols_reg;
    assign rdata_CONF_DONE   = 16'h0000;   // CS3 read stays Hi-Z (never electrically driven)
    // rdata_BURST_REMAIN / rdata_ENV_DATA / rdata_ENV_COUNT are driven by the instances.

    // =======================================================================
    // 6) Single tri-state driver on gpmc_d + WAIT held ready
    // =======================================================================
    wire read_active = (~nCS1) & (~nOE);            // CS1 reads only
    assign gpmc_d    = read_active ? rmux_rdata : 16'hzzzz;   // SINGLE tri-state driver
    assign gpmc_wait = 1'b1;                          // held ready (never wedge the bus)

endmodule

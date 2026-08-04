// acq.v — TOP of the standard (owned) acquisition bitstream for the SDS1000CML
//         acquisition FPGA (Altera Cyclone IV E, EP4CE10F17C8).
//
// This is the GPMC async slave + the generated register interface + the wiring of the
// five-module streaming-spine acquisition engine. It replaces the vendor acquisition
// path outright (the app refuses a fabric whose build-ID differs). The Cyclone is a
// CS1-ONLY slave: CS3 (config, offset/level DACs, LED) is decoded by the MAX V CPLD
// (bench-proven — nCS3 never reaches this device), so there is NO DAC serializer and
// NO CS3 decode here. Module set:
//
//   adcif.v     ADC front-end: the AD9288-class converters feed the Cyclone DIRECTLY
//               on 36 raw lanes (CH1=21, CH2=15); de-interleave + drive ENCODE (JTAG).
//   spine.v     canonical >=18-bit stream {ch1,ch2,valid,idx,trig_mark}: the live
//               decimator (transform stage 0) + two reserved bypassable stages (C1).
//   capture.v   circular pre/post-trigger writer + record M9K + trigger accept +
//               EXACT post-count window + interpolating TRIGPOS + static freeze (C2).
//   envelope.v  LIVE-STREAM min/max reducer + envelope result FIFO (C3, overflow).
//   drain.v     single auto-inc BURST port (1-D DMA source) + BURST_REMAIN.
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
//     (CS1 reads only); every other cycle Hi-Z. There is no CS3 decode in this device.
//   * gpmc_wait held ready at all times (never wedge the bus; WAIT-monitoring is off).
//   * clk is the fabric reference (ball C2, ~80 MHz free-running). The ADC lanes are
//     captured in this domain (source-synchronous to the ENCODE we drive); the async
//     GPMC strobes/selector/data + trig_sense cross in via 2-3 FF synchronizers + edge
//     detect. adc_ch1/adc_ch2 / trig_sense / the GPMC strobe balls are inputs only.
//   * the only driven balls are gpmc_d (on CS1 reads), gpmc_wait, and adc_encode;
//     every driven ball is capped to MINIMUM CURRENT in acq.qsf.
//
// Clean-room: design/spec-derived, never vendor RTL. Synthesizable Verilog-2001.

`include "regs.vh"

module acq (
    // fabric reference clock: verified ball C2 (~80 MHz free-running input). The ADC
    // ENCODE we drive is derived from it; the whole datapath is this single domain.
    input  wire        clk,

    // ADC data bus (INPUTS) — 33 verified AD9288 lanes [32:0] + 18 candidate wide-bus/SRAM-DQ
    // balls [50:33] (the ADC drives the shared wide bus during CAPTURE) to hunt CH2's missing
    // LSB lanes via raw-lane capture. adcif still de-interleaves from [32:0] only.
    input  wire [50:0] adc_lane,
    // ADC DRIVE (bench-cracked recipe, docs/aux-bus-re.md): 8 ENCODE clocks + the held
    // mode controls (F1/L4/T2/T7=1, G1/G2/K1=0) bring the converters out of power-down.
    output wire [7:0]  adc_enc,
    output wire [1:0]  adc_enc2,    // C14/D14 differential ADC sample clock (factory drives these; was the ADC-dead regression)
    output wire        adc_enc3,    // A11 — 3rd ENCODE candidate (factory drives toggling)
    output wire [3:0]  adc_ctl_hi,
    output wire [2:0]  adc_ctl_lo,

    // HW trigger comparator output (MAX V level-DAC-fed, ball A12): "a crossing occurred".
    input  wire        trig_sense,

    // GPMC bus — the Cyclone is a CS1-ONLY slave (CS3 is decoded by the MAX V CPLD;
    // nCS3 does not reach this device, bench-proven).
    input  wire        nCS1,        // CS1 acquisition/read plane, active-low
    input  wire        nOE,         // read strobe, active-low
    input  wire        nWE,         // write strobe, active-low
    input  wire [6:0]  sel,         // selector = byte_addr>>1 => sel[k]=GPMC A(k+1). Only
                                    // 7 bits: the map is 0x00-0x7f, so A8/bit7 is never
                                    // needed and is not brought in (can't misdecode).
    inout  wire [15:0] gpmc_d,      // 16-bit data bus; driven ONLY on a CS1 read
    output wire        gpmc_wait    // WAIT/ready line (held ready; WAIT-monitoring off)
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
    reg [2:0]  cs1_q = 3'b111, oe_q = 3'b111, we_q = 3'b111;
    reg [6:0]  sel_q1 = 7'h00, sel_q2 = 7'h00;
    reg [15:0] d_q1 = 16'h0, d_q2 = 16'h0;
    reg [2:0]  trig_q = 3'b000;

    always @(posedge clk) begin
        cs1_q   <= {cs1_q[1:0], nCS1};
        oe_q    <= {oe_q[1:0],  nOE};
        we_q    <= {we_q[1:0],  nWE};
        sel_q1  <= sel;        sel_q2  <= sel_q1;
        d_q1    <= gpmc_d;     d_q2    <= d_q1;      // meaningful only during a write
        trig_q  <= {trig_q[1:0], trig_sense};
    end

    wire cs1_low   = (cs1_q[2] == 1'b0);
    wire trig_rise = (trig_q[2] == 1'b0) && (trig_q[1] == 1'b1);   // comparator rising edge

    // ---- regmux write/read handshake signals (provided to the generated include) --
    wire       we_commit = (we_q[2] == 1'b0) && (we_q[1] == 1'b1);  // nWE rising: data+sel settled
    wire [7:0] wr_plane  = cs1_low ? `PLANE_CS1 : 8'h00;   // CS1-only slave (CS3 = MAX V)
    // bits 0/1/7 (A1/A2/A8) forced 0 — all THREE are unusable Cyclone address lines,
    // HW-confirmed by a flashed-fabric readback: A2 (D1) floats high, and A1 (M2) carries
    // a CLOCK (M2 toggles ~50% at rest, so sel[0] flipped mid-read and mixed adjacent
    // registers). The CS1 map is laid out with bit0=bit1=0 for every selector (mult-of-4,
    // codegen) so the decode uses ONLY the verified, stable lines A3,A4,A5,A6,A7 (sel[6:2]).
    wire [7:0] wr_sel    = {1'b0, sel_q2[6:2], 2'b00};
    wire [7:0] rd_plane  = (~nCS1) ? `PLANE_CS1 : 8'h00;   // CS1-only slave (CS3 = MAX V)
    wire [7:0] rd_sel    = {1'b0, sel[6:2], 2'b00};   // bits 0/1/7 forced 0 (see wr_sel)

    // ---- auto-inc read decode (synchronized; one pop / nOE-rise) -----------
    // BUGFIX: compare ONLY the stable selector lines sel[6:2] (bits 0/1/7 are the
    // unusable A1/A2/A8 lines — D1 floats high, M2 carries a ~50% clock — see wr_sel/
    // rd_sel above). The old full-width `sel_q2 == SEL_BURST` was never true once A1/A2
    // float high, so burst_addr never auto-incremented and the whole drained record
    // collapsed to mem[0] replicated (the "flat trace / ADC-dead" symptom).
    // ALIAS CAVEAT: masking bits 0/1/7 means reads to 0x41-0x43 alias to SEL_BURST
    // (0x40) and 0x71-0x73 to SEL_ENV_DATA (0x70) and WILL pop the port. The app
    // only issues 0x40/0x70 so it is unaffected; a bench probe must avoid those.
    wire [7:0] sel_q2_masked = {1'b0, sel_q2[6:2], 2'b00};
    // sel 0x00 is unused by the schema; ALIAS it to the burst-pop so the GPMC prefetch/sDMA
    // engine (which reads the CS base = sel 0x00) drains the record. Additive: nothing reads 0x00,
    // the app only issues 0x40, and CS3/config (MAX-V) is a different plane — all unaffected.
    wire sel_is_burst    = (sel_q2_masked == `SEL_BURST) || (sel_q2_masked == 8'h00);
    wire burst_rd_done   = cs1_low && (oe_q[2] == 1'b0) && (oe_q[1] == 1'b1) && sel_is_burst;
    wire sel_is_env      = (sel_q2_masked == `SEL_ENV_DATA);
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

    wire       run_en    = run_word[`RUN_RUN_LSB];
    wire       mode_norm = (run_word[`RUN_MODE_LSB +: 2] == 2'd1);   // 0=AUTO, 1=NORM
    // gapless raw-stream mode (run_word bit 3): continuous ring capture + live drain.
    wire       stream_on = run_word[3];
    wire       test_ramp = run_word[4];   // stream ramp test pattern (drop/reorder proof)
    wire [`ADDR_W-1:0] wr_ptr;   // capture's live write pointer -> drain (stream mode)
    // CH1 trigger level in sample units, for the TRIGPOS sub-sample interpolation.
    // The physical level DAC now lives on the MAX V (CS3), so the Cyclone no longer
    // sees the code; mid-scale keeps the interpolator well-defined (the trigger itself
    // is the A12 comparator edge). [BENCH-TUNE] wire the real level in via a CS1 reg.
    wire [7:0] trig_level = 8'h80;

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
        if (we_RUN)         run_word          <= d_q2;
        if (we_DECIM_LO)    decim_reg[15:0]   <= d_q2;
        if (we_DECIM_HI)    decim_reg[31:16]  <= d_q2;
        if (we_PRETRIG_LO)  pretrig_reg[15:0] <= d_q2;
        if (we_PRETRIG_HI)  pretrig_reg[31:16]<= d_q2;
        if (we_POSTTRIG_LO) posttrig_reg[15:0]<= d_q2;
        if (we_POSTTRIG_HI) posttrig_reg[31:16]<= d_q2;
        if (we_XFORM_CTRL)  xform_reg         <= d_q2;
        if (we_ENV_COLS)    env_cols_reg      <= d_q2;
        // The CS3 front-end writes (LED / offset DAC / trigger-level DAC) are decoded
        // by the MAX V, not the Cyclone: the generated we_LED_*/we_OFF_*/we_LVL_*
        // strobes never fire here (wr_plane is CS1-only) and are intentionally unused.
        // we_ENV_RESET is consumed by envelope.v; no-op here.
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
    wire [15:0]        samp;         // canonical {CH1[7:0],CH2[7:0]} from the ADC front-end
    // RAW-LANE DEBUG capture: XFORM_CTRL[4]=raw_mode routes a 16-lane slice of adc_lane
    // (selected by [6:5]) into the record instead of the de-interleaved sample, so a known
    // railed triangle can be captured per-lane to solve the CH2 de-interleave bit-order.
    reg  [50:0] adc_lane_q = 51'd0;
    always @(posedge clk) adc_lane_q <= adc_lane;
    wire        raw_mode = xform_reg[4];
    wire [1:0]  raw_sel  = xform_reg[6:5];
    // raw_sel=3 = CURATED 16 CH2-active lanes (across the original + new wide-bus balls), so all
    // of CH2's bits are captured in ONE time-aligned record for the de-interleave order search.
    wire [15:0] raw_curated = {adc_lane_q[46], adc_lane_q[45], adc_lane_q[44], adc_lane_q[43],
                               adc_lane_q[42], adc_lane_q[41], adc_lane_q[39], adc_lane_q[32],
                               adc_lane_q[31], adc_lane_q[27], adc_lane_q[26], adc_lane_q[24],
                               adc_lane_q[23], adc_lane_q[22], adc_lane_q[20], adc_lane_q[18]};
    wire [15:0] raw_word = (raw_sel == 2'd0) ? adc_lane_q[15:0]
                         : (raw_sel == 2'd1) ? adc_lane_q[31:16]
                         : (raw_sel == 2'd2) ? adc_lane_q[47:32]        // lane32 + new 33..47
                                             : raw_curated;             // curated CH2 lanes
    wire [15:0] samp_eff = raw_mode ? raw_word : samp;
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

    // ADC front-end: 36 raw lanes -> canonical {CH1,CH2} sample + drive ENCODE.
    adcif u_adcif (
        .clk        (clk),
        .enc_div_sel(xform_reg[13:12]),   // ENCODE rate: 0=10MHz(default) 1=20MHz 2=40MHz
        .enc_split  (xform_reg[14]),      // interleave probe: balls 4-7 half-period late
        .adc_lane   (adc_lane[32:0]),
        .adc_enc    (adc_enc),
        .adc_enc2   (adc_enc2),
        .adc_enc3   (adc_enc3),
        .adc_ctl_hi (adc_ctl_hi),
        .adc_ctl_lo (adc_ctl_lo),
        .samp       (samp)
    );

    spine u_spine (
        .clk      (clk),
        .filling  (filling),
        .stream_on(stream_on),
        .test_ramp(test_ramp),
        .samp     (samp_eff),
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
        .stream_on   (stream_on),
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
        .wr_ptr      (wr_ptr)
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
        .fill_active    (filling),
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
        .stream_on         (stream_on),
        .wr_ptr            (wr_ptr),
        .burst_addr        (burst_addr),
        .rdata_burst_remain(rdata_BURST_REMAIN)
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
    assign rdata_BURST       = (coherent || stream_on) ? rec_rd_data : 16'h0000; // auto-inc record word (stream = live ring)
    assign rdata_STATUS_A    = {13'd0, r_done, r_trig, r_valid};    // clean level status
    assign rdata_TRIGPOS_LO  = trig_frac;                           // FRAC[15:0] (Q16)
    assign rdata_TRIGPOS_HI  = {1'b0, trig_idx};                    // IDX[14:0] (`ADDR_W=15)
    assign rdata_FILL        = {5'd0, fill_out};                    // COUNT[10:0]
    assign rdata_XFORM_CTRL  = {14'd0, xform_reg[`XFORM_CTRL_BYPASS1_LSB],
                                       xform_reg[`XFORM_CTRL_BYPASS0_LSB]};
    assign rdata_ENV_COLS    = env_cols_reg;
    assign rdata_CONF_DONE   = 16'h0000;   // required stub: regmux.vh (schema SSOT) still
                                           // names this CS3 read; the mux case never fires
                                           // (rd_plane is CS1-only) — CONF_DONE is a MAX V reg.
    // rdata_BURST_REMAIN / rdata_ENV_DATA / rdata_ENV_COUNT are driven by the instances.

    // =======================================================================
    // 6) Single tri-state driver on gpmc_d + WAIT held ready
    // =======================================================================
    wire read_active = (~nCS1) & (~nOE);            // CS1 reads only
    assign gpmc_d    = read_active ? rmux_rdata : 16'hzzzz;   // SINGLE tri-state driver
    assign gpmc_wait = 1'b1;                          // held ready (never wedge the bus)

endmodule

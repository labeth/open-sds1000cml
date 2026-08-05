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

    // M2 clock test: M2 is HW-verified to carry a ~50% clock AT REST (acq.v sel-decode comment) AND
    // is a dedicated PLL-INCLK ball. sel[0] (=M2, ignored by the decode) is freed to E1 and M2 is
    // brought in here as a candidate PLL reference — measured (frequency counter) + fed to a real PLL.
    input  wire        mclk_in,

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
    // DEC_BYTE (0x7c) auto-inc drain: one FIFO pop per nOE-rising, exactly like
    // SEL_BURST. ALIAS: 0x7d-0x7f mask to 0x7c and would also pop; the app never
    // issues those (it touches only 0x00/0x40/0x70) so it is unaffected.
    wire sel_is_decbyte  = (sel_q2_masked == 8'h7c);
    wire dec_rd_done     = cs1_low && (oe_q[2] == 1'b0) && (oe_q[1] == 1'b1) && sel_is_decbyte;
    // STATUS (0x4c) read strobe. The host reads STATUS for `fill` immediately before
    // each wide-frame drain, so this is the natural per-drain sync point: it re-anchors
    // the wide-frame sub-word phase to 0. Non-popping (0x4c never pops the FIFO).
    wire sel_is_decstat  = (sel_q2_masked == 8'h4c);
    wire dec_status_rd_done = cs1_low && (oe_q[2] == 1'b0) && (oe_q[1] == 1'b1) && sel_is_decstat;

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

    // =======================================================================
    // 2b) IN-FABRIC UART DECODE — hand-decoded SPARE selectors (ADDITIVE)
    //     schema/regs.vh/regmux.vh are UNTOUCHED => IFACE_BUILD_ID (c2f6eb5f)
    //     invariant. These selectors have NO generated we_*/read-mux case, so
    //     hand-decoding them against hardwired literals is purely additive,
    //     exactly like the SEL_BURST 0x00 alias and the OPCODE payload compares.
    //     wr_sel/rd_sel are {1'b0, sel[6:2], 2'b00} (mult-of-4, A1/A2/A8 masked).
    //     All decode is GATED by dec_cfg[0] (=dec_en); dec_cfg resets to 0 so
    //     decode is fully inert at power-up (every existing mode byte-for-byte
    //     unchanged: decode_trig into capture is forced 0, no FIFO activity, and
    //     the DEC read selectors are never issued by the app/prefetch/sDMA).
    //
    //   WRITES:  0x04 CFG {tg_en[9],trig_en[8],parity[7:6],bits[5:2],srcch[1],en[0]}
    //            0x08 THR  threshold[7:0]=ceil(Thr)
    //            0x0c SPB_LO SPB[15:0] (Q16.8)
    //            0x1c SPB_HI SPB[23:16]
    //            0x48 MATCH {mask[15:8], pattern[7:0]}
    //            0x68 TESTGEN tg_byte[7:0]
    //   READS :  0x4c STATUS  {overflow[15],matched[14],busy[13],2'b0,fill[10:0]}
    //            0x6c MATCHED {frame_err[9],parity_err[8],matched_byte[7:0]}
    //            0x7c BYTE    auto-inc pop {frame_err[9],parity_err[8],data[7:0]}
    wire we_DEC_CFG     = we_commit & (wr_plane == `PLANE_CS1) & (wr_sel == 8'h04);
    wire we_DEC_THR     = we_commit & (wr_plane == `PLANE_CS1) & (wr_sel == 8'h08);
    wire we_DEC_SPB_LO  = we_commit & (wr_plane == `PLANE_CS1) & (wr_sel == 8'h0c);
    wire we_DEC_SPB_HI  = we_commit & (wr_plane == `PLANE_CS1) & (wr_sel == 8'h1c);
    wire we_DEC_MATCH   = we_commit & (wr_plane == `PLANE_CS1) & (wr_sel == 8'h48);
    wire we_DEC_TESTGEN = we_commit & (wr_plane == `PLANE_CS1) & (wr_sel == 8'h68);

    reg [15:0] dec_cfg    = 16'h0000;   // reset disabled => decode inert
    reg [15:0] dec_thr    = 16'h0080;
    reg [15:0] dec_spb_lo = 16'h0000;
    reg [7:0]  dec_spb_hi = 8'h00;
    reg [15:0] dec_match  = 16'h0000;
    reg [15:0] dec_tg     = 16'h0000;
    reg [7:0]  dec_canx   = 8'h00;   // CAN proto-extend byte: prev-free SPB_HI(0x1c)[15:8]
    always @(posedge clk) begin
        if (we_DEC_CFG)     dec_cfg    <= d_q2;
        if (we_DEC_THR)     dec_thr    <= d_q2;
        if (we_DEC_SPB_LO)  dec_spb_lo <= d_q2;
        if (we_DEC_SPB_HI)  dec_spb_hi <= d_q2[7:0];
        if (we_DEC_SPB_HI)  dec_canx   <= d_q2[15:8];   // ADDITIVE: 0x1c[15:8] was discarded
        if (we_DEC_MATCH)   dec_match  <= d_q2;
        if (we_DEC_TESTGEN) dec_tg     <= d_q2;
    end
    wire        dec_en     = dec_cfg[0];
    wire        dec_srcch  = dec_cfg[1];
    wire [3:0]  dec_bits   = dec_cfg[5:2];              // 0 => 8 inside uart_decode
    wire [1:0]  dec_par    = dec_cfg[7:6];
    wire        dec_trigen = dec_cfg[8];
    wire        dec_tgen   = dec_cfg[9];
    wire [7:0]  dec_thr8   = dec_thr[7:0];
    wire [23:0] dec_spb    = {dec_spb_hi, dec_spb_lo};  // Q16.8

    // ---- PROTOCOL-DISPATCH reinterpretation (ADDITIVE, previously-FREE bits) --
    // The UART decode above used only dec_cfg[9:0]; dec_cfg[15:10] were FREE.
    // Add a 2-bit protocol selector in [11:10] (0=UART,1=I2C,2=SPI) and reinterpret
    // the already-hand-decoded spare selectors per protocol. regs.vh/regmux.vh/schema
    // are UNTOUCHED => IFACE_BUILD_ID stays 0xc2f6eb5f. With dec_cfg=0 at reset =>
    // dec_proto=0 (UART) and dec_en=0, so I2C/SPI engines are gated fully inert and
    // every existing mode (incl. UART decode) is byte-for-byte unchanged.
    wire [1:0]  dec_proto   = dec_cfg[11:10];      // 0=UART 1=I2C 2=SPI 3=ETH  ([15:12] free)
    wire        dec_chswap  = dec_cfg[1];          // I2C/SPI: 0=>a=CH1,b=CH2; 1=>swapped
    wire        spi_cpol    = dec_cfg[2];          // SPI clock polarity
    wire        spi_cpha    = dec_cfg[3];          // SPI clock phase
    wire        spi_msb     = dec_cfg[4];          // SPI 1=MSB-first
    wire [7:0]  dec_thr_a   = dec_thr[7:0];        // SCL / CLK threshold (== UART thr8)
    wire [7:0]  dec_thr_b   = dec_thr[15:8];       // SDA / DATA threshold
    wire [23:0] dec_gapreset= dec_spb;             // SPI gapReset in INTEGER columns (reuses SPB regs)
    wire        sel_uart    = (dec_proto == 2'd0);
    wire        sel_i2c     = (dec_proto == 2'd1);
    wire        sel_spi     = (dec_proto == 2'd2);
    wire        sel_eth     = (dec_proto == 2'd3);   // 100BASE-TX line-rate PHY decode
    // CAN/CAN-FD proto-extend (ADDITIVE): SEL_DEC_SPB_HI(0x1c)[8] reinterprets the
    // previously-DISCARDED upper byte of SPB_HI. dec_canx resets 0 and existing app
    // code (fabricSPB) always writes 0x1c[15:8]=0, so can_ext==0 for every existing
    // config => the 4 protos above are byte-for-byte identical. can_ext==1 selects CAN.
    wire        can_ext     = dec_canx[0];             // 1 => CAN/CAN-FD decoder
    wire        can_domlow  = dec_canx[1];             // dominantLow (standard CAN = 1)

    // ---- OPCODE decode -> single-cycle engine pulses ----
    // Full 16-bit compare against the generated payload macros (d_q2 is the
    // registered 16-bit write word); the app writes the identical iface.OP_* value.
    wire op_reset = we_OPCODE && (d_q2 == `OP_RESET);
    wire op_go    = we_OPCODE && (d_q2 == `OP_GO) && run_en;    // honored only while RUN
    wire op_halt  = we_OPCODE && (d_q2 == `OP_HALT);

    // ---- WIDE-FRAME DRAIN ENABLE (ADDITIVE; previously-FREE SPB_HI[10]) -----------
    // 0x1c (DEC_SPB_HI) latches dec_spb_hi<=d_q2[7:0] only; bits [15:8] were DISCARDED.
    // Reuse d_q2[10] as the wide-frame drain enable. 0 => 0x7c is the legacy one-word
    // pop {flags,byte} (byte-identical to today). 1 => 0x7c presents a 3-word frame per
    // FIFO entry so the host reconstructs {flags,idx,byte}. INTEGRATION NOTE: relocated
    // from bit8 to bit10 because item-7 CAN claims 0x1c[8]=can_ext and 0x1c[9]=can_domlow;
    // bit10 keeps wide-frame and CAN proto-extend independent (dec_canx[2] is ignored by
    // can_decode). The app writes SPB_HI[15:8]=0 on every trigger Arm (fabricSPB hi byte),
    // so the live-trigger path always stays legacy; the streaming host sets bit10=1
    // explicitly. Cleared on op_reset.
    reg dec_wide = 1'b0;
    always @(posedge clk) begin
        if (op_reset)           dec_wide <= 1'b0;
        else if (we_DEC_SPB_HI) dec_wide <= d_q2[10];
    end

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
    // M2 CLOCK TEST (XFORM_CTRL[7]): is M2's ~50%-at-rest clock a PLL-usable reference?
    //   m2_ctr counts M2 (mclk_in) edges; synced to clk, its advance rate across the record gives M2's
    //   frequency. A LIVE altpll (x5/2) takes mclk_in as inclk0: lock_s2 says M2 is a usable reference,
    //   and pll_hb (a counter on the multiplied clock, synced) proves the fast clock actually runs.
    //   Captured word = {locked, 0, m2_ctr[7:0], pll_hb[5:0]}. The main datapath still runs on clk(C2).
    reg  [15:0] m2_ctr = 16'd0;
    always @(posedge mclk_in) m2_ctr <= m2_ctr + 1'b1;
    reg  [15:0] m2_ctr_s1 = 16'd0, m2_ctr_s2 = 16'd0;
    always @(posedge clk) begin m2_ctr_s1 <= m2_ctr; m2_ctr_s2 <= m2_ctr_s1; end
    wire [4:0] pll_out;
    wire       pll_locked_raw;
    // VCO = M2(160) x5 = 800 MHz. c0 = 800/2 = 400 (lock heartbeat). c1/c2/c3 = 800/5 = 160 MHz at
    // phase 90/180/270 deg (period 6250 ps): phase-shifted ENCODE so the ADC data-valid window lands
    // clear of the 80 MHz DDR capture edges. fast phase select (xform[9:8]): 0=M2 160@0, 1/2/3=90/180/270.
    altpll #(
        .bandwidth_type("AUTO"),
        .clk0_divide_by(2), .clk0_duty_cycle(50), .clk0_multiply_by(5), .clk0_phase_shift("0"),
        .clk1_divide_by(5), .clk1_duty_cycle(50), .clk1_multiply_by(5), .clk1_phase_shift("1562"),
        .clk2_divide_by(5), .clk2_duty_cycle(50), .clk2_multiply_by(5), .clk2_phase_shift("3125"),
        .clk3_divide_by(5), .clk3_duty_cycle(50), .clk3_multiply_by(5), .clk3_phase_shift("4687"),
        .compensate_clock("CLK0"), .inclk0_input_frequency(6250),
        .intended_device_family("Cyclone IV E"), .operation_mode("NORMAL"), .pll_type("AUTO"),
        .port_clk0("PORT_USED"), .port_clk1("PORT_USED"), .port_clk2("PORT_USED"), .port_clk3("PORT_USED"),
        .port_inclk0("PORT_USED"), .port_locked("PORT_USED")
    ) u_m2pll ( .inclk({1'b0, mclk_in}), .clk(pll_out), .locked(pll_locked_raw) );
    // CAPTURE-clock phase select (xform[9:8]): the 160 MHz clock that latches the source-synchronous
    // sample pair. 0 = M2 160@0deg (= ENCODE phase), 1/2/3 = PLL 160@90/180/270deg. Pick the phase that
    // lands mid ADC-data-valid-window (offset from the ENCODE) so consecutive samples are clean.
    wire [1:0] cap_phase = xform_reg[9:8];
    wire       cap_clk   = (cap_phase == 2'd1) ? pll_out[1]
                         : (cap_phase == 2'd2) ? pll_out[2]
                         : (cap_phase == 2'd3) ? pll_out[3] : mclk_in;
    wire       pll_c0 = pll_out[0];
    reg  [7:0] pll_hb = 8'd0;
    always @(posedge pll_c0) pll_hb <= pll_hb + 1'b1;
    reg  [7:0] pll_hb_s1 = 0, pll_hb_s2 = 0;
    reg        lock_s1 = 0, lock_s2 = 0;
    always @(posedge clk) begin
        pll_hb_s1 <= pll_hb; pll_hb_s2 <= pll_hb_s1;
        lock_s1   <= pll_locked_raw; lock_s2 <= lock_s1;
    end
    wire        m2test_en = xform_reg[7];

    // =======================================================================
    // TIME-INTERLEAVE clocking + control (ADDITIVE — the 2nd/last Cyclone PLL)
    // -----------------------------------------------------------------------
    // u_m2pll (above) is left UNTOUCHED so the M2 ring's 160 MHz 2:1 lockstep
    // (fast_capmode) stays byte-for-byte identical. u_pll200 is the 2nd PLL,
    // fed from the SAME M2 pin: VCO=160*5=800 MHz, five 200 MHz phase outputs.
    //   pll200_out[0]=0deg   -> ph0   (C1 core a / C2 core a)
    //   pll200_out[1]=120deg -> ph120 (C1 core b)
    //   pll200_out[2]=240deg -> ph240 (C1 core c)
    //   pll200_out[3]=180deg -> ph180 (C2 core b)
    //   pll200_out[4]=90deg  -> cap_clk200 (fill/capture clock, tunable phase)
    // XFORM_CTRL free bits [3:2] (only unallocated bits): [2]=interleave_en,
    // [3]=chan_sel (0=C1-600 3-wide, 1=C2-400 2-wide). With [2]=0 the whole
    // interleave path is dead: PLL runs but drives nothing, il_capture never
    // arms, and the cap_word/cap_tick mux below selects the spine as before.
    wire       interleave_en = xform_reg[2];                 // drives the phased 200 MHz ENCODE
    // il_en gates the MERGE capture only; raw_mode (XFORM[4]) with interleave_en=1 lets us capture the
    // RAW lanes UNDER the phased ENCODE — under staggered phases the redundant pin-aliases break, so
    // only the real connected cores drive their lanes (diagnostic for the ball->core map / empty core).
    wire       il_en    = xform_reg[2] & ~raw_mode;
    wire       chan_sel = xform_reg[3];
    wire [4:0] pll200_out;   // {90,180,240,120,0}
    wire       pll200_locked;
    altpll #(
        .bandwidth_type("AUTO"),
        .clk0_divide_by(4), .clk0_duty_cycle(50), .clk0_multiply_by(5), .clk0_phase_shift("0"),
        .clk1_divide_by(4), .clk1_duty_cycle(50), .clk1_multiply_by(5), .clk1_phase_shift("1667"),
        .clk2_divide_by(4), .clk2_duty_cycle(50), .clk2_multiply_by(5), .clk2_phase_shift("3333"),
        .clk3_divide_by(4), .clk3_duty_cycle(50), .clk3_multiply_by(5), .clk3_phase_shift("2500"),
        .clk4_divide_by(4), .clk4_duty_cycle(50), .clk4_multiply_by(5), .clk4_phase_shift("1250"),
        .compensate_clock("CLK0"), .inclk0_input_frequency(6250),
        .intended_device_family("Cyclone IV E"), .operation_mode("NORMAL"), .pll_type("AUTO"),
        .port_clk0("PORT_USED"), .port_clk1("PORT_USED"), .port_clk2("PORT_USED"),
        .port_clk3("PORT_USED"), .port_clk4("PORT_USED"),
        .port_inclk0("PORT_USED"), .port_locked("PORT_USED")
    ) u_pll200 ( .inclk({1'b0, mclk_in}), .clk(pll200_out), .locked(pll200_locked) );
    wire ph0        = pll200_out[0];   // 0 deg
    wire ph120      = pll200_out[1];   // 120 deg
    wire ph240      = pll200_out[2];   // 240 deg
    wire ph180      = pll200_out[3];   // 180 deg
    wire cap_clk200 = pll200_out[4];   // 90 deg — il_capture fill clock

    // ---- 5-core de-interleave (must live here: lanes 40/46/47 are in the wide
    //      bus adc_lane[50:33] that adcif never receives). Each core byte = its
    //      freeze-census lanes MSB..LSB, zero-padded to 8. Combinational off the
    //      raw wide bus so il_capture registers each core in the cap_clk200 phase.
    //      EXACT ball->core + per-core bit order are bench-calibration-deferred
    //      (fast-signal cal remote-blocked); re-map = edit only these 5 lists.
    wire [7:0] c1a = { adc_lane[0],  adc_lane[10], adc_lane[1],  adc_lane[11],
                       adc_lane[40], adc_lane[9],  adc_lane[12], 1'b0 };          // C1 ball2 (7 lanes)
    wire [7:0] c1b = { adc_lane[3],  adc_lane[15], adc_lane[5],  adc_lane[6],  4'b0000 }; // C1 ball5 (4)
    wire [7:0] c1c = { adc_lane[7],  adc_lane[17], adc_lane[8],  adc_lane[47], 4'b0000 }; // C1 ball3 (4)
    wire [7:0] c2a = { adc_lane[20], adc_lane[28], adc_lane[27], 5'b00000 };             // C2 ball0 (3)
    wire [7:0] c2b = { adc_lane[21], adc_lane[29], adc_lane[31], adc_lane[46], 4'b0000 }; // C2 ball6 (4)
    // Per-core PHASE-MATCHED capture: each core is ENCODEd at a distinct PLL phase (ball2/ball0 @0,
    // ball5 @120, ball3 @240, ball6 @180). Registering each in a PLL phase offset into its data-valid
    // window lets ALL 5 sample in-window (a single capture clock misses the mis-phased cores -> reads 0,
    // and WHICH core reads 0 wanders — the artifact we saw). pll200_out = {[4]90,[3]180,[2]240,[1]120,[0]0}.
    // Snapped to available phases (~ENCODE+180); exact per-core skew is a bench trim of these 5 picks.
    reg [7:0] c1a_p = 8'd0, c1b_p = 8'd0, c1c_p = 8'd0, c2a_p = 8'd0, c2b_p = 8'd0;
    always @(posedge pll200_out[3]) c1a_p <= c1a;   // ball2 @0   -> capture @180
    always @(posedge pll200_out[2]) c1b_p <= c1b;   // ball5 @120 -> capture @240 (best avail; ideal ~300 not in the 5 PLL phases -> bench trim)
    always @(posedge pll200_out[4]) c1c_p <= c1c;   // ball3 @240 -> capture @90
    always @(posedge pll200_out[3]) c2a_p <= c2a;   // ball0 @0   -> capture @180
    always @(posedge pll200_out[0]) c2b_p <= c2b;   // ball6 @180 -> capture @0
    // raw_sel=3 = CURATED 16 CH2-active lanes (across the original + new wide-bus balls), so all
    // of CH2's bits are captured in ONE time-aligned record for the de-interleave order search.
    wire [15:0] raw_curated = {adc_lane_q[46], adc_lane_q[45], adc_lane_q[44], adc_lane_q[43],
                               adc_lane_q[42], adc_lane_q[41], adc_lane_q[39], adc_lane_q[32],
                               adc_lane_q[31], adc_lane_q[27], adc_lane_q[26], adc_lane_q[24],
                               adc_lane_q[23], adc_lane_q[22], adc_lane_q[20], adc_lane_q[18]};
    wire [15:0] raw_word = m2test_en       ? {lock_s2, 1'b0, m2_ctr_s2[7:0], pll_hb_s2[5:0]} // M2 clock test
                         : (raw_sel == 2'd0) ? adc_lane_q[15:0]
                         : (raw_sel == 2'd1) ? adc_lane_q[31:16]
                         : (raw_sel == 2'd2) ? adc_lane_q[47:32]        // lane32 + new 33..47
                                             : raw_curated;             // curated CH2 lanes
    // ===== M2 fast-capture ring RETIRED (ITEM 6: M9K reclaim) ==========================
    // The dual-clock 160-pair diagnostic ring (cbuf, 512x16 = 1 M9K) was the ONLY
    // reclaimable block on the 46/46-full device (record 40 = app build-ID contract;
    // envelope 4 = 336 app columns need 2016/2048 words). Its block is reallocated to
    // il_capture below, doubling the interleave record depth (N 256 -> 512).
    //
    // The retired path was reachable ONLY via XFORM_CTRL[13:12]==3 && XFORM_CTRL[7]==1
    // (fast_capmode && ddr_pack) — a manual-poke diagnostic the app never programs (it
    // leaves XFORM_CTRL at its 0x0003 reset). So for EVERY app-reachable state samp_eff
    // is byte-identical before and after this edit; and whenever the interleave record
    // is active (il_en=1) cap_word=il_word bypasses samp_eff entirely.
    wire [15:0] samp_eff = raw_mode ? raw_word : samp;
    // cap_word/cap_tick feed capture + envelope. They are MUXed: when il_en the
    // interleave stream (il_capture) drives them; otherwise the spine, byte-exact.
    wire [15:0]        spine_word;
    wire               spine_tick;
    wire [15:0]        il_word;
    wire               il_tick;
    wire               il_busy;
    wire [15:0]        cap_word = il_en ? il_word : spine_word;
    wire               cap_tick = il_en ? il_tick : spine_tick;
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
        .enc_off_en (xform_reg[15]),      // core map probe: hold one ENCODE output static
        .enc_off_ball(xform_reg[11:8]),   // 0-7=balls, 8=differential C14/D14, 9=A11
        .fast_enc   (mclk_in),            // ENCODE = M2 160@0deg when enc_div_sel==3 (capture clock is phased separately)
        .interleave_en(interleave_en),    // XFORM_CTRL[2]: drive phased 200 MHz ENCODE
        .ph0        (ph0),                // 200 MHz @   0 deg
        .ph120      (ph120),              // 200 MHz @ 120 deg
        .ph240      (ph240),              // 200 MHz @ 240 deg
        .ph180      (ph180),              // 200 MHz @ 180 deg
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
        .cap_word (spine_word),
        .cap_tick (spine_tick)
    );

    // ===== TIME-INTERLEAVE fill-then-drain capture (ADDITIVE, gated on il_en) =====
    // Fills {c1a,c1b,c1c}/{c2a,c2b} at cap_clk200 (200 MHz) on op_go, then drains
    // 16-bit record words at clk (80 MHz) into cap_word/cap_tick (MUXed above).
    // arm=op_go; when il_en==0 it never arms and il_word/il_tick stay quiet.
    // N=512 (ITEM 6): the M2 fast-capture ring (cbuf, 1 M9K) was retired above and its
    // block reallocated here, so the interleave ring is now 512-deep = 2 M9K
    // (512x32 -> two M9K in 512x18 mode; deterministic — 512-deep caps at x18/block,
    // so 32-bit entries force exactly 2 blocks regardless of de-interleave bit-fill).
    // Budget after reclaim: record 40 + envelope 4 + il_capture 2 = 46/46 (exact fit).
    // POST=384/PRE=128 keeps today's 1:3 pre/post window shape at 2x depth.
    il_capture #(.N(512), .AW(9), .POST(384)) u_il_capture (
        .clk      (clk),
        .cap_clk  (cap_clk200),
        .arm      (op_go),
        .il_en    (il_en),
        .chan_sel (chan_sel),
        .trig     (c1a_p[7]),          // DATA trigger: core-A MSB rising = signal crosses mid-scale
        .trig_en  (xform_reg[9]),      // 1 = wait for that crossing before filling (cap_phase[1], free)
        .c1a      (c1a_p),             // phase-matched per-core captures (all 5 sampled in-window)
        .c1b      (c1b_p),
        .c1c      (c1c_p),
        .c2a      (c2a_p),
        .c2b      (c2b_p),
        .il_word  (il_word),
        .il_tick  (il_tick),
        .il_busy  (il_busy)
    );

    // dec_trigger outputs (driven by u_dec_trigger below, after the protocol mux).
    // Declared here so the capture feed at .decode_trig can reference them.
    wire        dec_trig_final;       // mode-selected decode_trig into capture.v
    wire        dec_matched_out;      // mode-selected sticky STATUS bit14 source
    wire [7:0]  dec_matched_byte_out; // mode-selected 0x6c matched-byte source

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
        .decode_trig (dec_trig_final),   // dec_trigger: mode0==(dec_en?dec_trig_pulse:0), byte-identical
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

    // ===== IN-FABRIC UART DECODE engine + lossless byte FIFO (ADDITIVE) =====
    // Taps cap_word/cap_tick (the SAME decimated column stream capture/envelope
    // consume — NOT raw 80 MHz samp), so SPB is in host colTimeS units and the
    // decoded bytes match app decode.DecodeUART().Bytes. The engine emits on a
    // strobe (no block-RAM); byte_fifo is logic-register only (M9K is 46/46 full).
    // Everything is held inert while dec_en=0.
    // UART taps its single source channel (unchanged). I2C/SPI slice TWO channels:
    //   dec_a = SCL / CLK   (CH1 by default, or CH2 when dec_chswap)
    //   dec_b = SDA / DATA  (CH2 by default, or CH1 when dec_chswap)
    // ---- FABRIC PATTERN-GEN (dec_patterngen.v) : fault-injection stimulus ----
    // Master enable = the EXISTING tg_en (dec_cfg[9]); when armed it REPLACES the
    // per-decoder internal test-gens (their .tg_en is tied 0 below) and drives the
    // slicer code lines with clean+faulty patterns. pg_en=0 => today's wiring
    // verbatim (gen-off == today). No schema/selector change.
    wire        pg_en = dec_tgen;
    wire [7:0]  pg_uart_code, pg_a_code, pg_b_code;
    dec_patterngen u_dec_patterngen (
        .clk           (clk),
        .cap_tick      (cap_tick),
        .en            (pg_en),
        .proto         (dec_proto),      // dec_cfg[11:10]: 0=UART 1=I2C 2=SPI 3=ETH
        .spb           (dec_spb),        // Q16.8; UART bit width = spb[15:8]
        .gen_uart_code (pg_uart_code),
        .gen_a_code    (pg_a_code),
        .gen_b_code    (pg_b_code)
    );

    wire [7:0]  dec_sample_code = pg_en ? pg_uart_code
                                : dec_srcch  ? cap_word[7:0]  : cap_word[15:8];
    wire [7:0]  dec_a           = pg_en ? pg_a_code
                                : dec_chswap ? cap_word[7:0]  : cap_word[15:8];
    wire [7:0]  dec_b           = pg_en ? pg_b_code
                                : dec_chswap ? cap_word[15:8] : cap_word[7:0];

    // per-engine enables: exactly one is ever hot (dec_proto is 2 bits, sel_spi
    // covers proto==2, proto==3 leaves all three off). dec_en=0 => all off.
    wire uart_en = dec_en & sel_uart & ~can_ext;   // ~can_ext: byte-identical when can_ext==0
    wire i2c_en  = dec_en & sel_i2c  & ~can_ext;
    wire spi_en  = dec_en & sel_spi  & ~can_ext;
    wire eth_en  = dec_en & sel_eth  & ~can_ext;   // 100BASE-TX line-rate PHY decode gate
    wire can_en  = dec_en & can_ext;               // CAN/CAN-FD (proto-extend, additive)

    // ---- per-engine emit interfaces (uniform across the three front-ends) ----
    wire        uart_emit_stb;   wire [7:0] uart_emit_byte;  wire [23:0] uart_emit_idx;
    wire [1:0]  uart_emit_flags; wire uart_trig;             wire uart_matched;
    wire [7:0]  uart_matched_byte;
    wire        i2c_emit_stb;    wire [7:0] i2c_emit_byte;   wire [23:0] i2c_emit_idx;
    wire [1:0]  i2c_emit_flags;  wire i2c_trig;              wire i2c_matched;
    wire [7:0]  i2c_matched_byte;
    wire        i2c_start_stb, i2c_stop_stb;  // I2C txn markers -> dec_trigger mode-3
    wire        spi_emit_stb;    wire [7:0] spi_emit_byte;   wire [23:0] spi_emit_idx;
    wire [1:0]  spi_emit_flags;  wire spi_trig;              wire spi_matched;
    wire [7:0]  spi_matched_byte;
    wire        can_emit_stb;    wire [7:0] can_emit_byte;   wire [23:0] can_emit_idx;
    wire [7:0]  can_emit_flags;  wire can_trig;              wire can_matched;
    wire [7:0]  can_matched_byte;

    uart_decode u_uart_decode (
        .clk          (clk),
        .rst_n        (1'b1),                 // no async reset; en=0 holds it IDLE
        .cap_tick     (cap_tick),
        .sample_code  (dec_sample_code),
        .en           (uart_en),
        .thr8         (dec_thr8),
        .spb          (dec_spb),
        .bits_cfg     (dec_bits),
        .parity_cfg   (dec_par),
        .hyst_en      (1'b0),                 // oracle-exact: pure-threshold slicer
        .hyst_band    (8'd0),
        .tg_en        (1'b0),            // superseded by dec_patterngen (muxed on sample_code)
        .tg_byte      (dec_tg[7:0]),
        .trig_en      (dec_trigen),
        .match_pattern(dec_match[7:0]),
        .match_mask   (dec_match[15:8]),
        .emit_stb     (uart_emit_stb),
        .emit_byte    (uart_emit_byte),
        .emit_idx     (uart_emit_idx),
        .emit_flags   (uart_emit_flags),
        .decode_trig  (uart_trig),
        .matched      (uart_matched),
        .matched_byte (uart_matched_byte)
    );

    // ---- IN-FABRIC I2C DECODE (ADDITIVE, gated i2c_en=dec_en&&proto==I2C) ----
    // Same emit interface as uart_decode; taps the two sliced channels dec_a/dec_b.
    // en=0 (proto!=I2C or dec_en=0) => fully inert (no strobes, sticky cleared).
    i2c_decode u_i2c_decode (
        .clk          (clk),
        .rst_n        (1'b1),
        .cap_tick     (cap_tick),
        .scl_code     (dec_a),
        .sda_code     (dec_b),
        .en           (i2c_en),
        .scl_thr      (dec_thr_a),
        .sda_thr      (dec_thr_b),
        .tg_en        (1'b0),            // superseded by dec_patterngen (muxed on scl/sda)
        .trig_en      (dec_trigen),
        .match_pattern(dec_match[7:0]),
        .match_mask   (dec_match[15:8]),
        .emit_stb     (i2c_emit_stb),
        .emit_byte    (i2c_emit_byte),
        .emit_idx     (i2c_emit_idx),
        .emit_flags   (i2c_emit_flags),
        .decode_trig  (i2c_trig),
        .matched      (i2c_matched),
        .matched_byte (i2c_matched_byte),
        .i2c_start_stb(i2c_start_stb),
        .i2c_stop_stb (i2c_stop_stb)
    );

    // ---- IN-FABRIC SPI DECODE (ADDITIVE, gated spi_en=dec_en&&proto==SPI) ----
    // CPOL/CPHA/MSB from reinterpreted CFG bits; gapReset from reinterpreted SPB regs.
    spi_decode u_spi_decode (
        .clk          (clk),
        .rst_n        (1'b1),
        .cap_tick     (cap_tick),
        .clk_code     (dec_a),
        .data_code    (dec_b),
        .en           (spi_en),
        .clk_thr      (dec_thr_a),
        .data_thr     (dec_thr_b),
        .cpol         (spi_cpol),
        .cpha         (spi_cpha),
        .msb          (spi_msb),
        .gapreset     (dec_gapreset),
        .tg_en        (1'b0),            // superseded by dec_patterngen (muxed on clk/data)
        .tg_word      (dec_tg[7:0]),
        .trig_en      (dec_trigen),
        .match_pattern(dec_match[7:0]),
        .match_mask   (dec_match[15:8]),
        .emit_stb     (spi_emit_stb),
        .emit_byte    (spi_emit_byte),
        .emit_idx     (spi_emit_idx),
        .emit_flags   (spi_emit_flags),
        .decode_trig  (spi_trig),
        .matched      (spi_matched),
        .matched_byte (spi_matched_byte)
    );

    // ---- IN-FABRIC CAN / CAN-FD DECODE (ADDITIVE, gated can_en=dec_en&&can_ext) ----
    // Proto-extend can_ext = SEL_DEC_SPB_HI(0x1c)[8]. Single sliced line =
    // dec_sample_code (the UART source-channel tap). dom_low = 0x1c[9]. Same emit
    // shape as the others; 8-bit emit_flags carry the CAN error/FD bits (like ETH).
    can_decode u_can_decode (
        .clk          (clk),
        .rst_n        (1'b1),
        .cap_tick     (cap_tick),
        .sample_code  (dec_sample_code),
        .en           (can_en),
        .thr8         (dec_thr8),
        .spb          (dec_spb),
        .dom_low      (can_domlow),
        .trig_en      (dec_trigen),
        .match_pattern(dec_match[7:0]),
        .match_mask   (dec_match[15:8]),
        .emit_stb     (can_emit_stb),
        .emit_byte    (can_emit_byte),
        .emit_idx     (can_emit_idx),
        .emit_flags   (can_emit_flags),
        .decode_trig  (can_trig),
        .matched      (can_matched),
        .matched_byte (can_matched_byte)
    );

    // ---- IN-FABRIC 100BASE-TX PHY DECODE (ADDITIVE, gated eth_en=dec_en&&proto==ETH) ----
    // Line-rate top eth100_decode_lr: gearbox(200->80 CDC) -> slicer/CDR -> 2b/clk
    // descramble2 -> 2b/clk 4b5b2 -> framer(+CRC-32 FCS) -> byte_fifo-compatible emit.
    // WRITE side taps the interleave's per-core phase captures c1a_p/c1b_p/c1c_p
    // (600 MSa/s, 3 samples per 200 MHz tick); READ side runs at the 80 MHz fabric clk.
    // en=0 (proto!=ETH or dec_en=0) => the WHOLE module (both clock domains) is inert.
    //
    // c1*_p are unsigned 8-bit ADC codes (midscale ~128). Re-center to signed and
    // scale x8 -> ~+/-1024 to match the CDR/golden +/-1000 ternary codes. The exact
    // per-core skew + this scaling/threshold trim are BENCH-CAL (same status as the
    // interleave taps themselves — see the c1a_p/c1b_p/c1c_p capture note above).
    wire signed [11:0] eth_s0 = ($signed({4'd0, c1a_p}) - 12'sd128) <<< 3; // phase0 earliest
    wire signed [11:0] eth_s1 = ($signed({4'd0, c1b_p}) - 12'sd128) <<< 3; // phase120
    wire signed [11:0] eth_s2 = ($signed({4'd0, c1c_p}) - 12'sd128) <<< 3; // phase240
    wire [35:0] eth_wr_samp = {eth_s2, eth_s1, eth_s0};   // s0=[11:0] earliest
    // slicer thresholds = DEC_THR low byte scaled x8 -> +/-(dec_thr_a*8) (bench trim).
    wire signed [11:0] eth_thr_hi = $signed({1'b0, dec_thr_a, 3'b000}); // dec_thr_a<<3, >=0
    wire signed [11:0] eth_thr_lo = -eth_thr_hi;

    wire        eth_emit_stb;   wire [7:0] eth_emit_byte;  wire [23:0] eth_emit_idx;
    wire [7:0]  eth_emit_flags;
    wire        eth_sfd_seen, eth_frame_done, eth_fcs_ok;
    wire        eth_descr_locked, eth_cg_locked, eth_gb_ovf, eth_cdr_ovf, eth_fb_ovf;

    eth100_decode_lr #(.SAMPLE_W(12), .LANES(8), .WR_SAMP(3), .DEPTHW(4)) u_eth_decode (
        .clk       (clk),                  // 80 MHz fabric (read/chain domain)
        .rst       (op_reset),
        .en        (eth_en),
        .thr_hi    (eth_thr_hi),
        .thr_lo    (eth_thr_lo),
        .wr_clk    (pll200_out[0]),        // 200 MHz interleave write clock
        .wr_rst    (op_reset),
        .wr_valid  (1'b1),                 // interleave taps present fresh samples every tick
        .wr_samp   (eth_wr_samp),
        .flush     (1'b0),                 // frames self-terminate on ESD; no tail flush needed live
        .emit_stb  (eth_emit_stb),
        .emit_byte (eth_emit_byte),
        .emit_idx  (eth_emit_idx),
        .emit_flags(eth_emit_flags),
        .sfd_seen  (eth_sfd_seen),
        .frame_done(eth_frame_done),
        .fcs_ok_o  (eth_fcs_ok),
        .descr_locked(eth_descr_locked),
        .cg_locked (eth_cg_locked),
        .gb_overflow(eth_gb_ovf),
        .cdr_overflow(eth_cdr_ovf),
        .fb_ovf    (eth_fb_ovf)
    );
    // SFD (frame start) is the ETH decode trigger source (gated by dec_trigen).
    wire eth_trig = dec_trigen ? eth_sfd_seen : 1'b0;

    // ---- PROTOCOL DISPATCH: mux the active engine into the SHARED sinks ----
    // Each engine is inert unless its own en is hot, so the losing engines drive
    // emit_stb=0/decode_trig=0; the mux simply selects the active one's outputs.
    wire        dec_emit_stb   = can_ext ? can_emit_stb  : sel_eth ? eth_emit_stb   : sel_i2c ? i2c_emit_stb   : sel_spi ? spi_emit_stb   : uart_emit_stb;
    wire [7:0]  dec_emit_byte  = can_ext ? can_emit_byte : sel_eth ? eth_emit_byte  : sel_i2c ? i2c_emit_byte  : sel_spi ? spi_emit_byte  : uart_emit_byte;
    wire [23:0] dec_emit_idx   = can_ext ? can_emit_idx  : sel_eth ? eth_emit_idx   : sel_i2c ? i2c_emit_idx   : sel_spi ? spi_emit_idx   : uart_emit_idx;
    wire [1:0]  dec_emit_flags = sel_i2c ? i2c_emit_flags : sel_spi ? spi_emit_flags : uart_emit_flags;
    // ETH carries the framer's full 8-bit flags (start/end/fcs/ok/err); the UART/I2C/SPI
    // 2-bit flags zero-extend, so the shared 8-bit sink is byte-identical for them.
    wire [7:0]  dec_emit_flags8 = can_ext ? can_emit_flags : sel_eth ? eth_emit_flags : {6'd0, dec_emit_flags};
    wire        dec_trig_pulse = can_ext ? can_trig : sel_eth ? eth_trig        : sel_i2c ? i2c_trig       : sel_spi ? spi_trig       : uart_trig;
    wire        dec_matched_sticky = can_ext ? can_matched      : sel_i2c ? i2c_matched      : sel_spi ? spi_matched      : uart_matched;
    wire [7:0]  dec_matched_byte   = can_ext ? can_matched_byte : sel_i2c ? i2c_matched_byte : sel_spi ? spi_matched_byte : uart_matched_byte;

    // ---- EXTENDED DECODE TRIGGER (dec_trigger.v) --------------------------------
    // Drop-in over the inline byte-match trigger. In trig_mode==0 (reset/default)
    // it routes the UNTOUCHED per-module pulse (dec_trig_pulse) and per-module
    // sticky/byte verbatim => decode_trig into capture.v is byte-for-byte identical
    // to `dec_en ? dec_trig_pulse : 1'b0` and 0x4c[14]/0x6c are unchanged. Modes
    // 1 (error) / 2 (sequence) / 3 (addr) are additive, gated by trig_mode. Only
    // previously-FREE dec_cfg[15:12] + mode-reinterpreted 0x48/0x68 fields are used
    // => no selector added, IFACE_BUILD_ID stays 0xc2f6eb5f.
    //
    // mode-2 adjacency window (COLUMN units): ~64x the integer samples-per-bit, a
    // generous single-expression threshold (contiguous data-byte start-idx gaps <=
    // this pass; cross-transmission idle gaps are far larger and reject). This is
    // the documented bench-trim knob (SPEC risk #5) — testgen-exact vs bench-exact
    // are tracked separately. For I2C/SPI dec_spb is the reused gap/idle register,
    // so the window is even more generous there (accepts contiguous, rejects gaps).
    wire [15:0] adj_win = {dec_spb[17:8], 6'b0};   // integer-SPB << 6 (~64x), 16-bit

    dec_trigger u_dec_trigger (
        .clk            (clk),
        .rst            (op_reset),
        .en             (dec_en),
        .emit_stb       (dec_emit_stb),
        .emit_byte      (dec_emit_byte),
        .emit_idx       (dec_emit_idx),
        .emit_flags     (dec_emit_flags8),
        .sel_i2c        (sel_i2c & ~can_ext),
        .sel_spi        (sel_spi & ~can_ext),
        .sel_eth        (sel_eth & ~can_ext),
        .eth_sfd        (eth_sfd_seen),
        .i2c_start      (i2c_start_stb),
        .i2c_stop       (i2c_stop_stb),
        .trig_en        (dec_trigen),
        .trig_mode      (dec_cfg[13:12]),          // 0=byte 1=err 2=seq 3=addr (was FREE)
        .seqlen_cfg     (dec_cfg[15:14]),          // N = seqlen_cfg+1 (was FREE)
        .match_pattern  (dec_match[7:0]),          // mode0/2 seq[0], mode3 addr_field
        .match_mask     (dec_match[15:8]),         // mode0 mask, mode1 err_mask, mode2 seq[3], mode3 addr_mask
        .seq_b1         (dec_tg[7:0]),             // mode2 seq[1] (reuses TESTGEN 0x68)
        .seq_b2         (dec_tg[15:8]),            // mode2 seq[2]
        .adj_win        (adj_win),
        .legacy_trig    (dec_trig_pulse),          // UNTOUCHED per-module mode-0 pulse
        .legacy_matched (dec_matched_sticky),
        .legacy_matched_byte(dec_matched_byte),
        .decode_trig    (dec_trig_final),
        .matched        (dec_matched_out),
        .matched_byte   (dec_matched_byte_out)
    );

    // ---- WIDE-FRAME DRAIN SEQUENCER (ADDITIVE; inert unless dec_wide) ------------
    // In wide mode each FIFO entry is read as 3 consecutive 0x7c words; dec_phase
    // (0,1,2) selects which sub-word the head presents, and the FIFO is popped ONLY on
    // the phase-2 read (dec_pop) — exactly one entry retires per 3 GPMC reads. Phase
    // re-anchors to 0 on op_reset and on every STATUS(0x4c) read (the per-drain sync
    // point). In legacy mode dec_wide=0: dec_phase never advances and dec_pop==dec_rd_done,
    // so 0x7c is byte-for-byte the one-word-per-read pop of today.
    reg [1:0] dec_phase = 2'd0;
    always @(posedge clk) begin
        if (op_reset || dec_status_rd_done) dec_phase <= 2'd0;
        else if (dec_rd_done && dec_wide)   dec_phase <= (dec_phase == 2'd2) ? 2'd0 : (dec_phase + 2'd1);
    end
    wire dec_pop = dec_rd_done & (dec_wide ? (dec_phase == 2'd2) : 1'b1);

    // Lossless byte FIFO (logic registers, ramstyle=logic => 0 M9K). Entry =
    // {flags[7:0], idx[23:0], byte[7:0]}. Drain word packs {flags,data} only.
    wire [7:0]  dfifo_head_byte;
    wire [23:0] dfifo_head_idx;
    wire [7:0]  dfifo_head_flags;
    wire [5:0]  dfifo_fill;       // AW+1 = 6 bits, 0..32
    wire        dfifo_empty, dfifo_full, dfifo_overflow;
    byte_fifo #(.DEPTH(32), .AW(5)) u_dec_fifo (
        .clk         (clk),
        .rst         (op_reset),
        .push        (dec_emit_stb & dec_en),
        .in_byte     (dec_emit_byte),
        .in_idx      (dec_emit_idx),
        .in_flags    (dec_emit_flags8),
        .pop         (dec_pop),
        .head_byte   (dfifo_head_byte),
        .head_idx    (dfifo_head_idx),
        .head_flags  (dfifo_head_flags),
        .fill_count  (dfifo_fill),
        .empty       (dfifo_empty),
        .full        (dfifo_full),
        .overflow    (dfifo_overflow),
        .clr_overflow(op_reset | we_DEC_CFG)   // host clears overflow by rewriting CFG
    );

    // DEC readback words (final-driver override at the tri-state, section 6).
    wire [15:0] dec_status  = {dfifo_overflow, dec_matched_out, ~dfifo_empty,
                               2'b0, {5'd0, dfifo_fill}};       // {ovf,matched,busy,-,fill[10:0]}
    wire [15:0] dec_matched = {6'd0, 2'b00, dec_matched_byte_out}; // matched is data-only => flags 0
    // 0x7c drain word. UART/I2C/SPI: unchanged {--,flags[1:0],byte}. ETH exposes the
    // framer's full 8 flag bits (start[7]/end[6]/fcs[5]/ok[4]/err[3]) in the spare upper
    // byte — additive, and byte-identical to the legacy layout for every non-ETH proto.
    wire [15:0] dec_byte_hd = (sel_eth | can_ext) ? {dfifo_head_flags[7:0], dfifo_head_byte}
                                                   : {6'd0, dfifo_head_flags[1], dfifo_head_flags[0], dfifo_head_byte};
    // Wide-frame sub-words (phase-selected). w0 carries the FULL 8-bit flags (matching
    // the ETH form) so the host gets uniform flags regardless of proto; idx is the
    // byte's start-column span anchor. Legacy path (dec_wide=0) is unchanged below.
    wire [15:0] dec_byte_wide = (dec_phase == 2'd0) ? {dfifo_head_flags[7:0], dfifo_head_byte}
                              : (dec_phase == 2'd1) ? dfifo_head_idx[15:0]
                              :                       {8'h00, dfifo_head_idx[23:16]};
    wire [15:0] dec_byte_out  = dec_wide ? dec_byte_wide : dec_byte_hd;

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
    // DEC read override: the generated read-mux returns default 0x0000 for the
    // hand-decoded DEC selectors 0x4c/0x6c/0x7c, so intercept them at the SINGLE
    // tri-state driver. dec_read is FALSE for every schema selector (rd_sel uses
    // the same masked sel[6:2] as the generated mux), so all schema reads pass
    // through rmux_rdata untouched. Uses rd_sel (unregistered), like the gen mux.
    wire        dec_read  = (~nCS1) & ((rd_sel == 8'h4c) | (rd_sel == 8'h6c) | (rd_sel == 8'h7c));
    wire [15:0] dec_rdata = (rd_sel == 8'h4c) ? dec_status
                          : (rd_sel == 8'h6c) ? dec_matched
                          :                     dec_byte_out;  // 0x7c (wide or legacy)
    assign gpmc_d    = read_active ? (dec_read ? dec_rdata : rmux_rdata) : 16'hzzzz;   // SINGLE tri-state driver
    assign gpmc_wait = 1'b1;                          // held ready (never wedge the bus)

endmodule

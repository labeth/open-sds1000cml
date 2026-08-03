// capsram.v — capture-over-external-SRAM: a DROP-IN replacement for capture.v.
//
// Same functional port contract as capture.v (byte-identical inputs/outputs so
// acq_sram.v instantiates it exactly like `capture`, and spine/drain/adcif/
// envelope are reused verbatim). The ONLY difference from capture.v: the record
// is stored in the EXTERNAL S7A163630M SRAM the vendor way (ADC drives the shared
// DQ bus, the fixed MAX-V sequences the SRAM strobes) instead of the on-chip M9K.
//
// WHAT IS UNCHANGED FROM capture.v (bit-for-bit behaviour, so trig_idx / trig_frac /
// fill / envelope stay correct):
//   * the circular pre/post writer bookkeeping (waddr / wrote_count / post_count),
//   * the exact post-count finalize (post_count == posttrig_work equality),
//   * the trigger accept + sticky-NORM edge + NORM bound,
//   * the first-order Q16 interpolation long-division,
//   * smp_valid = wr_commit (envelope folds exactly the committed samples),
//   * the single registered rd_addr -> rd_data read port (M9K, one clocked block).
//
// WHAT CHANGED:
//   (1) CAPTURE WRITE ENGINE (during ST_FILL): on each committed sample we drive the
//       27 RE-proven SRAM balls (18 ADDRESS = spread(waddr), 6 CONTROL = CS#/WE#/load,
//       3 CLOCK = the write sample clock) + hold D2 low, so the MAX-V latches the
//       ADC-driven DQ into the SRAM. We NEVER drive DQ on write (PATH A shared bus:
//       the ADC is the write-data master). mem[] is NOT written during fill.
//   (2) On finalize (post_full or a triggered halt) we enter ST_DRAIN_SRAM and run
//       the PROVEN sramdump non-contention read (drive ONLY D14, tri-state all 27
//       write balls so the MAX-V holds/advances the address) to slurp rec_len words
//       SRAM -> on-chip mem[]. Only then do we assert `coherent` (deferred), so the
//       2-cycle registered rd_addr->rd_data drain never sees stale mem[].
//   (3) The MAX-V write micro-timing is RUNTIME-SETTABLE via spare CS1 debug
//       selectors (decoded here, NOT in the generated regmux.vh, so the build-ID
//       0xc2f6eb5f / VERSION 0x0052 schema is untouched). The read path reuses the
//       fixed proven sramdump timing (only rd_clkdiv + lane_sel are tunable).
//
// Clean-room: design/spec-derived. Synthesizable Verilog-2001, EP4CE10.

`include "regs.vh"

module capsram (
    input  wire                 clk,

    // ---- capture.v FUNCTIONAL CONTRACT (identical) ------------------------
    input  wire                 arm,           // OP_GO  : clean single-cycle re-arm
    input  wire                 halt,          // OP_HALT: manual freeze
    input  wire                 rst,           // OP_RESET: -> idle, clear
    input  wire [15:0]          pre_work_w,
    input  wire [15:0]          post_work_w,
    input  wire [15:0]          cap_word,      // hi=CH1, lo=CH2 (unused on write; ADC drives DQ)
    input  wire                 cap_tick,      // spine `valid` (decimated)
    input  wire                 mode_norm,     // 0 = AUTO, 1 = NORM
    input  wire                 trig_rise,     // synchronized comparator rising edge
    input  wire [7:0]           trig_level,    // level in CH1 sample units (bench-tunable)
    input  wire [`ADDR_W-1:0]   rd_addr,
    output reg  [15:0]          rd_data,
    output wire                 filling,
    output wire                 smp_valid,
    output reg                  r_valid,
    output reg                  r_trig,
    output reg                  r_done,
    output reg                  coherent,
    output wire [10:0]          fill_out,
    output wire [`ADDR_W-1:0]   trig_idx,
    output reg  [15:0]          trig_frac,
    output wire [15:0]          rec_len,
    output reg                  frame_done,

    // ---- SRAM PHYSICAL INTERFACE (new; top-level tri-states via wr_oe) ------
    output wire [17:0]          sram_addr,     // 18 ADDRESS balls (spread(waddr))
    output wire [5:0]           sram_ctrl,     // 6 CONTROL balls (CS#/WE#/load, idle-HIGH)
    output wire                 sram_wclk,     // write sample clock (fans to F2/J2/K2)
    output wire                 wclk_oe,        // tri-state enable for F2/J2/K2 (clk_mode: report says free-run every phase)
    output wire                 draining,       // 1 => ST_DRAIN_SRAM (for ADC-standby-on-drain override)
    output wire [6:0]           adc_ctl_ovr,    // 7-bit ADC mode-control override {F1,L4,T2,T7,G1,G2,K1}
    output wire                 adc_ctl_ovr_en, // 1 => apply adc_ctl_ovr while draining (AD9288 S1/S2 standby -> release DQ)
    output wire                 wr_oe,          // 1 => drive the 27 write balls (ST_FILL only)
    output wire                 d2,             // nCSO MAX-V mode lever (static-low default)
    output reg                  sck_rd,         // D14 read clock (only net driven on drain)
    input  wire [21:0]          dq,             // SRAM DQ read candidates (proven sramdump order)
    output wire [17:0]          dq_wr,          // TEST-WRITE data on the 18 DEDICATED sram_dq balls during ST_FILL (= cell address). NO ADC contention: separate from the 4 ADC-input shared balls (dqv[0]/[3]/[14]/[15] = A13/B12/G15/G16) the FPGA cannot drive — exclude those 4 bit positions from any write-verify.
    output wire                 dq_wr_oe,       // 1 => drive DQ with dq_wr (ST_FILL only); Hi-Z on drain so the SRAM drives it
    input  wire                 p6,             // MAX-V status mirror (input only)

    // ---- FREE CS1 DEBUG SELECTORS (decoded here, outside regmux.vh) ---------
    input  wire                 we_commit,      // GPMC write accepted (synchronized)
    input  wire                 cs1_low,        // CS1 selected (write plane)
    input  wire [7:0]           wr_sel,         // selector of the accepted write
    input  wire [7:0]           rd_sel,         // selector of the current read
    input  wire [15:0]          d_q2,           // registered write data word
    output wire                 dbg_rd_hit,     // 1 => dbg_rdata owns gpmc_d this read
    output reg  [15:0]          dbg_rdata
);

    // ---- sized geometry constants ---------------------------------------------
    localparam [`ADDR_W-1:0] REC_LAST    = `REC_DEPTH - 1;   // 20479
    localparam [15:0]        REC_DEPTH16 = `REC_DEPTH;        // 20480

    // ---- FSM: added ST_DRAIN_SRAM (SRAM->mem slurp) between FILL and HALT -----
    localparam [1:0] ST_IDLE = 2'd0, ST_FILL = 2'd1, ST_DRAIN_SRAM = 2'd2, ST_HALT = 2'd3;
    reg [1:0] state = ST_IDLE;

    // ---- capture bookkeeping (UNCHANGED from capture.v) -----------------------
    reg [`ADDR_W-1:0] waddr        = {`ADDR_W{1'b0}};
    reg [15:0]        wrote_count  = 16'd0;
    reg [15:0]        post_count   = 16'd0;
    reg [15:0]        pretrig_work = 16'd0;
    reg [15:0]        posttrig_work= 16'd0;
    reg               triggered    = 1'b0;
    reg               comp_pending = 1'b0;
    reg               fill_frozen  = 1'b0;
    reg [10:0]        fill_frozen_val = 11'd0;
    reg [`ADDR_W-1:0] trig_idx_r   = {`ADDR_W{1'b0}};
    reg [15:0]        rec_len_r    = 16'd0;
    reg [7:0]         prev_trig_ch = 8'd0;

    assign filling  = (state == ST_FILL);
    assign draining       = (state == ST_DRAIN_SRAM);
    assign adc_ctl_ovr    = adc_ctl_ovr_r;
    assign adc_ctl_ovr_en = adc_ovr_en_r;
    assign fill_out = fill_frozen ? fill_frozen_val : wrote_count[10:0];
    assign trig_idx = trig_idx_r;
    assign rec_len  = rec_len_r;

    // ---- trigger accept (UNCHANGED) -------------------------------------------
    wire pretrig_ok = (wrote_count >= pretrig_work);
    wire [15:0] norm_bound = REC_DEPTH16 - posttrig_work;
    wire        norm_full  = mode_norm && (wrote_count >= norm_bound);
    wire trig_cond  = (!mode_norm) ? 1'b1 : (trig_rise | comp_pending | norm_full);
    wire trig_fire  = filling && cap_tick && !triggered && pretrig_ok && trig_cond;

    // ---- exact post-count finalize (UNCHANGED) --------------------------------
    wire post_full  = triggered && (post_count == posttrig_work);
    wire wr_commit  = filling && cap_tick && !post_full;   // atomic single-cycle write
    assign smp_valid = wr_commit;

    // =======================================================================
    // RUNTIME WRITE-TUNING KNOBS — decoded from FREE reachable CS1 selectors
    // (multiples of 4 in 0x00..0x7c that the schema does NOT use). NOT added to
    // regmux.vh, so IFACE_BUILD_ID / VERSION stay bit-identical and the app never
    // touches these selectors.
    //   0x48 DBG_WDIV    : [15:0]  write SRAM-CLK divider
    //   0x4c DBG_WPHASE  : [3:0]=we_phase  [7:4]=load_phase (assert-window widths)
    //   0x68 DBG_WSTROBE : [2:0]=load_sel [6:4]=we_sel [13:8]=low_mask (over ctrl[5:0])
    //   0x6c DBG_WMISC   : [0]=eng_enable [1]=d2_wr [2]=d2_rd [3]=d2_idle
    //   0x0c DBG_RDDIV   : [15:0]  drain D14 pulse divider (proven sramdump = 25)
    //   0x08 DBG_MAP     : [4:0]=val [9:5]=idx [11:10]=tbl (0=addr order,1=lane_sel)
    // =======================================================================
    reg [15:0] clkdiv    = 16'd25;
    reg [3:0]  we_phase  = 4'd2;
    reg [3:0]  load_phase= 4'd2;
    reg [2:0]  load_sel  = 3'd3;          // ctrl[3] = N5 (load/ADSC# default)
    reg [2:0]  we_sel    = 3'd2;          // ctrl[2] = M6 (WE# default)
    reg [5:0]  low_mask  = 6'b000011;     // ctrl[0],ctrl[1] = L2,N1 = CS# held LOW
    reg        eng_enable= 1'b1;          // master enable for the SRAM write drive
    reg        d2_wr     = 1'b0;
    reg        d2_rd     = 1'b0;
    reg        d2_idle   = 1'b0;
    reg [1:0]  clk_mode  = 2'd0;          // F2/J2/K2 drive: 0=gated to FILL (orig), 1=free-run ALWAYS, 2=FILL+DRAIN
    // ---- research-derived multi-variant knobs (0x58 DBG_ADV) ----
    reg [6:0]  adc_ctl_ovr_r = 7'b1111000; // default = normal (F1/L4/T2/T7=1, G1/G2/K1=0); sweep for AD9288 standby
    reg        adc_ovr_en_r  = 1'b0;        // apply override while draining (release shared DQ bus)
    reg        we_level      = 1'b0;        // 0=WE# pulsed per write (orig), 1=WE# held LOW as write-mode level over FILL
    reg        cap_dly       = 1'b0;        // 0=capture DQ this edge, 1=capture DQ delayed 1 clk (SPB +1 pipeline)
    // read-handshake knobs (0x58 [12:10]): read-only skips FILL; drain P6 wait/polarity
    reg        read_only     = 1'b0;        // arm -> go straight to ST_DRAIN_SRAM (read vendor-written SRAM, no fill/overwrite)
                                             //   (2026-08-03: temporarily defaulted 1 for the factory-prime read test — see
                                             //    CONTENT_DIFFERENTIAL.md; that test proved our drain can't read a known
                                             //    factory-written constant, so reverted to the normal fill-capable default.)
    reg        drain_p6_wait = 1'b0;        // hold slurp until P6==drain_p6_pol (MAX-V read grant) before reading
    reg        drain_p6_pol  = 1'b0;        // P6 grant polarity for the drain handshake
    reg        drain_pend    = 1'b0;        // in ST_DRAIN_SRAM, waiting for the P6 read grant
    reg        read_sync     = 1'b0;        // 1 => slurp captures on the free-run F2/J2/K2 edge (MAX-V walks the read addr on it), D14=F2/J2/K2
    // ---- VENDOR-STYLE READ (0x58 bit14, 2026-08-03 decompile-grounded) ----
    // Decompile verdict corrections to the old D14-slurp/tri-state model:
    //   * D14 is the ADC sample clock, NOT the SRAM read clock -> clock the read on F2/J2/K2.
    //   * D2 is STATIC LOW (MAX-V CE) through capture AND drain -> hold d2_rd=0.
    //   * the address counter is CYCLONE-owned and must KEEP WALKING during read (do NOT tri-state);
    //     MAX-V latches the presented address (ADSC#) + asserts OE# when CS#=low & WE#=high (read).
    // vread=1 makes the drain drive addr(walking)+ctrl(CS# low, WE# HIGH)+F2/J2/K2, capture adc_lane DQ.
    reg        vread         = 1'b0;
    reg [15:0] rd_clkdiv = 16'd25;
    reg [4:0]  amap [0:17];               // ADDRESS ball <- waddr18[amap[i]] (order sweep)
    reg [4:0]  lmap [0:15];               // word bit  <- dq[lmap[j]]         (lane_sel sweep)

    integer ii;
    initial begin
        for (ii = 0; ii < 18; ii = ii + 1) amap[ii] = ii[4:0];   // identity default
        for (ii = 0; ii < 16; ii = ii + 1) lmap[ii] = ii[4:0];   // word = dq[15:0] default
    end

    always @(posedge clk) begin
        if (we_commit && cs1_low) case (wr_sel)
            8'h48: clkdiv     <= d_q2;
            8'h4c: begin we_phase <= d_q2[3:0]; load_phase <= d_q2[7:4]; end
            8'h68: begin load_sel <= d_q2[2:0]; we_sel <= d_q2[6:4]; low_mask <= d_q2[13:8]; end
            8'h6c: begin eng_enable <= d_q2[0]; d2_wr <= d_q2[1]; d2_rd <= d_q2[2]; d2_idle <= d_q2[3]; clk_mode <= d_q2[5:4]; end
            8'h58: begin adc_ctl_ovr_r <= d_q2[6:0]; adc_ovr_en_r <= d_q2[7]; we_level <= d_q2[8]; cap_dly <= d_q2[9];
                         read_only <= d_q2[10]; drain_p6_wait <= d_q2[11]; drain_p6_pol <= d_q2[12]; read_sync <= d_q2[13];
                         vread <= d_q2[14]; end
            8'h0c: rd_clkdiv <= d_q2;
            8'h08: begin
                if (d_q2[11:10] == 2'b00)      amap[d_q2[9:5]] <= d_q2[4:0];
                else if (d_q2[11:10] == 2'b01) lmap[d_q2[8:5]] <= d_q2[4:0];
            end
            default: ;
        endcase
    end

    // =======================================================================
    // CAPTURE WRITE ENGINE — drives the 27 balls so the MAX-V latches the ADC
    // sample. Free-running divided SRAM clock + address = spread(waddr) held
    // through the write, CS# held LOW, WE#/load pulsed per committed sample.
    // (sramrw / sramgold2 brute-forceable-role idiom, reused for the write path.)
    // =======================================================================
    reg [15:0]        wdv          = 16'd0;
    reg               sck_wr       = 1'b0;
    reg [3:0]         we_timer     = 4'd0;
    reg [3:0]         load_timer   = 4'd0;
    reg [`ADDR_W-1:0] wsample_addr = {`ADDR_W{1'b0}};   // address of the sample in flight

    always @(posedge clk) begin
        // FREE-RUNNING divided SRAM clock — toggles in EVERY state (report §4: F2/J2/K2
        // free-run as the SRAM clock in every phase; the OLD design gated it to ST_FILL,
        // starving the MAX-V's synchronous write/read FSM). Drive/tri-state is clk_mode.
        if (wdv >= clkdiv) begin wdv <= 16'd0; sck_wr <= ~sck_wr; end
        else                     wdv <= wdv + 16'd1;
        // per-sample strobe pulse windows (active while timer != 0)
        if (wr_commit) begin
            we_timer     <= we_phase;
            load_timer   <= load_phase;
            wsample_addr <= waddr;               // the cell this sample lands in
        end else begin
            if (we_timer   != 4'd0) we_timer   <= we_timer   - 4'd1;
            if (load_timer != 4'd0) load_timer <= load_timer - 4'd1;
        end
    end

    // VENDOR-STYLE READ: during drain with vread=1 the Cyclone keeps driving the
    // address counter (walking = slurp_addr) and control balls in READ posture
    // (all WE/load strobes HIGH, CS# low), so the MAX-V can latch+assert OE#.
    wire        reading     = (state == ST_DRAIN_SRAM) && vread;
    wire        we_active   = reading ? 1'b0 :
                              (we_level ? filling : (we_timer != 4'd0));   // read=WE# high; else level/pulse
    wire        load_active = reading ? 1'b0 : (load_timer != 4'd0);       // read=load(ADSC-req) high
    wire [17:0] waddr18     = reading ? {3'b000, slurp_addr}               // read walks the drain counter
                                      : {3'b000, wsample_addr};            // A18 strap is off-FPGA (not driven)

    // ADDRESS balls: programmable bit-order spread of the write/read pointer.
    genvar ga;
    generate for (ga = 0; ga < 18; ga = ga + 1) begin: addrdrv
        assign sram_addr[ga] = waddr18[amap[ga]];
    end endgenerate

    // CONTROL balls: WE#/load strobes (active-low) take priority, then CS# held
    // LOW (low_mask), else idle-HIGH.
    genvar gc;
    generate for (gc = 0; gc < 6; gc = gc + 1) begin: ctrldrv
        assign sram_ctrl[gc] = (we_sel   == gc[2:0]) ? ~we_active   :
                               (load_sel == gc[2:0]) ? ~load_active :
                               low_mask[gc]          ? 1'b0         : 1'b1;
    end endgenerate

    assign sram_wclk = sck_wr;
    // F2/J2/K2 tri-state enable by clk_mode: 0=only FILL (orig), 1=ALWAYS free-run,
    // 2=FILL+DRAIN (give the MAX-V read FSM a clock too). eng_enable still gates it.
    assign wclk_oe   = eng_enable && ( (clk_mode == 2'd1) ? 1'b1 :
                                       (clk_mode == 2'd2) ? ((state == ST_FILL) || (state == ST_DRAIN_SRAM)) :
                                                            (state == ST_FILL) );
    assign wr_oe     = ((state == ST_FILL) || reading) && eng_enable;   // drive addr/ctrl in FILL and vread-drain; Hi-Z else
    // TEST WRITE: drive the DQ bus with the write ADDRESS (data==addr ramp) so a drain
    // read-back that returns the ramp PROVES distinct-address->distinct-data through the
    // external SRAM, independent of the ADC. Enabled only during ST_FILL (Hi-Z on drain).
    assign dq_wr     = {3'b000, wsample_addr};
    assign dq_wr_oe  = (state == ST_FILL) && eng_enable;
    assign d2        = (state == ST_FILL)       ? d2_wr :
                       (state == ST_DRAIN_SRAM) ? d2_rd : d2_idle;

    // =======================================================================
    // DRAIN READ ENGINE — PROVEN sramdump: drive ONLY D14, capture DQ on the
    // sck-high edge; here retargeted to write mem[] (rec_len words), FIXED timing.
    // =======================================================================
    reg [`ADDR_W-1:0] slurp_addr = {`ADDR_W{1'b0}};
    reg [15:0]        rd_dv      = 16'd0;
    reg               slurp_run  = 1'b0;
    reg               slurp_done = 1'b0;
    reg [21:0]        dq_lat     = 22'd0;    // last latched candidate vector (lane confirm)

    // one captured word per read tick. read_sync => capture on the free-run F2/J2/K2 rising
    // edge (the clock the MAX-V walks its read address on); else the original D14 timing.
    reg  sck_wr_d = 1'b0;
    always @(posedge clk) sck_wr_d <= sck_wr;
    wire sck_wr_rise = sck_wr & ~sck_wr_d;
    wire slurp_tick = (state == ST_DRAIN_SRAM) && slurp_run &&
                      ((read_sync || vread) ? sck_wr_rise : ((rd_dv >= rd_clkdiv) && sck_rd));

    // word assembly: programmable lane_sel over the DQ candidate vector, with an
    // optional 1-clk capture delay (SPB read pipeline is +1: Q(A) lands the edge AFTER).
    reg  [21:0] dq_d1 = 22'd0;
    always @(posedge clk) dq_d1 <= dq;
    wire [21:0] dq_sel = cap_dly ? dq_d1 : dq;
    wire [15:0] rd_word;
    genvar gl;
    generate for (gl = 0; gl < 16; gl = gl + 1) begin: lanemap
        assign rd_word[gl] = dq_sel[lmap[gl]];
    end endgenerate

    // ---- record M9K: single write / registered read, one clocked block --------
    (* ramstyle = "M9K" *) reg [15:0] mem [0:`REC_DEPTH-1];
    always @(posedge clk) begin
        if (slurp_tick) mem[slurp_addr] <= rd_word;   // fill from SRAM during the slurp
        rd_data <= mem[rd_addr];                       // registered read (drain), unchanged
    end

    // ---- interpolation divider (UNCHANGED from capture.v) --------------------
    wire [7:0] s_k       = cap_word[15:8];
    wire [7:0] s_km1     = prev_trig_ch;
    wire       rise_dir  = (s_k >= s_km1);
    wire [7:0] den_mag   = rise_dir ? (s_k - s_km1) : (s_km1 - s_k);
    wire       lvl_up    = (trig_level >= s_km1);
    wire [7:0] num_mag   = lvl_up ? (trig_level - s_km1) : (s_km1 - trig_level);
    wire       same_dir  = (rise_dir == lvl_up);

    reg        div_busy = 1'b0;
    reg [4:0]  div_cnt  = 5'd0;
    reg [8:0]  div_rem  = 9'd0;
    reg [15:0] div_q    = 16'd0;
    reg [7:0]  div_den  = 8'd0;
    wire [8:0] rem_sh   = {div_rem[7:0], 1'b0};
    wire       q_bit    = (rem_sh >= {1'b0, div_den});
    wire [8:0] rem_nx   = q_bit ? (rem_sh - {1'b0, div_den}) : rem_sh;

    // ======================================================================
    // MAIN FSM — capture datapath, then the SRAM->mem slurp, then opcode
    // overrides LAST (last-assignment priority: arm/halt/reset win the edge).
    // ======================================================================
    always @(posedge clk) begin
        frame_done <= 1'b0;

        // ---- long-division step (interpolation) — UNCHANGED ----------------
        if (div_busy) begin
            div_rem <= rem_nx;
            div_q   <= {div_q[14:0], q_bit};
            if (div_cnt == 5'd1) begin
                div_busy  <= 1'b0;
                trig_frac <= {div_q[14:0], q_bit};
            end
            div_cnt <= div_cnt - 1'b1;
        end

        // ================================================================
        // FILL datapath — bookkeeping UNCHANGED; finalize now enters
        // ST_DRAIN_SRAM (coherent/r_valid/r_done DEFERRED to slurp done).
        // ================================================================
        if (filling) begin
            if (wr_commit) begin
                waddr        <= (waddr == REC_LAST) ? {`ADDR_W{1'b0}} : (waddr + 1'b1);
                prev_trig_ch <= s_k;
                if (wrote_count != REC_DEPTH16) wrote_count <= wrote_count + 1'b1;
                if (triggered && !trig_fire) post_count <= post_count + 1'b1;
            end

            if (trig_rise && !triggered) comp_pending <= 1'b1;

            if (trig_fire) begin
                triggered  <= 1'b1;
                r_trig     <= 1'b1;
                comp_pending <= 1'b0;
                trig_idx_r <= waddr;
                post_count <= 16'd1;
                if (den_mag == 8'd0 || !same_dir) trig_frac <= 16'h0000;
                else if (num_mag >= den_mag)      trig_frac <= 16'hFFFF;
                else begin
                    div_busy <= 1'b1; div_cnt <= 5'd16;
                    div_rem  <= {1'b0, num_mag}; div_q <= 16'd0; div_den <= den_mag;
                end
            end

            // exact finalize -> start the SRAM->mem slurp (coherent deferred).
            if (post_full) begin
                state           <= ST_DRAIN_SRAM;
                fill_frozen     <= 1'b1;
                fill_frozen_val <= wrote_count[10:0];
                rec_len_r       <= wrote_count;
                frame_done      <= 1'b1;             // envelope flush (may precede slurp)
                slurp_addr <= {`ADDR_W{1'b0}}; rd_dv <= 16'd0;
                sck_rd <= 1'b0; slurp_run <= 1'b0; slurp_done <= 1'b0;
                drain_pend <= 1'b1;   // wait for the MAX-V read grant (P6) before slurping
            end
        end

        // ---- drain P6 read-handshake: D2=d2_rd is asserted while in ST_DRAIN_SRAM;
        //      start the slurp only once the MAX-V grants (P6==pol), or immediately if wait off.
        if (state == ST_DRAIN_SRAM && drain_pend) begin
            if (!drain_p6_wait || (p6 == drain_p6_pol)) begin
                drain_pend <= 1'b0; slurp_run <= 1'b1;
            end
        end

        // ================================================================
        // SRAM -> mem SLURP (proven sramdump D14 read). Advance one word per
        // D14 period; capture at the sck-high->low edge into mem[] (mem block).
        // ================================================================
        if (state == ST_DRAIN_SRAM && slurp_run) begin
            if (read_sync || vread) begin
                sck_rd <= sck_wr;                    // D14 tracks F2/J2/K2 (vread: read clocked on F2/J2/K2)
                if (slurp_tick) begin                // = sck_wr rising edge
                    dq_lat <= dq;
                    if (slurp_addr >= (rec_len_r[`ADDR_W-1:0] - 1'b1)) begin
                        slurp_run <= 1'b0; slurp_done <= 1'b1;
                    end else slurp_addr <= slurp_addr + 1'b1;
                end
            end else if (rd_dv >= rd_clkdiv) begin
                rd_dv  <= 16'd0;
                sck_rd <= ~sck_rd;
                if (sck_rd) begin                    // sck-high -> capture this period
                    dq_lat <= dq;
                    if (slurp_addr >= (rec_len_r[`ADDR_W-1:0] - 1'b1)) begin
                        slurp_run <= 1'b0; slurp_done <= 1'b1;
                    end else begin
                        slurp_addr <= slurp_addr + 1'b1;
                    end
                end
            end else rd_dv <= rd_dv + 16'd1;
        end

        // slurp finished -> now the record is coherent; open the drain.
        if (state == ST_DRAIN_SRAM && slurp_done && !slurp_run) begin
            state      <= ST_HALT;
            coherent   <= 1'b1;
            r_valid    <= 1'b1;
            r_done     <= 1'b1;
            slurp_done <= 1'b0;
        end

        // ================================================================
        // OPCODE overrides (decoded LAST for last-assignment priority).
        // ================================================================
        if (rst) begin
            state <= ST_IDLE;
            waddr <= {`ADDR_W{1'b0}};
            wrote_count <= 16'd0; post_count <= 16'd0;
            triggered <= 1'b0; comp_pending <= 1'b0;
            coherent <= 1'b0; r_valid <= 1'b0; r_trig <= 1'b0; r_done <= 1'b0;
            fill_frozen <= 1'b0; trig_frac <= 16'h0000; trig_idx_r <= {`ADDR_W{1'b0}};
            prev_trig_ch <= 8'd0; div_busy <= 1'b0; rec_len_r <= 16'd0;
            slurp_run <= 1'b0; slurp_done <= 1'b0;
        end else if (arm) begin
            state <= read_only ? ST_DRAIN_SRAM : ST_FILL;   // read-only: skip FILL, read the SRAM as-is
            waddr <= {`ADDR_W{1'b0}};
            wrote_count <= 16'd0; post_count <= 16'd0;
            triggered <= 1'b0; comp_pending <= 1'b0;
            coherent <= 1'b0; r_valid <= 1'b0; r_trig <= 1'b0; r_done <= 1'b0;
            fill_frozen <= 1'b0; trig_frac <= 16'h0000; trig_idx_r <= {`ADDR_W{1'b0}};
            prev_trig_ch <= 8'd0; div_busy <= 1'b0;
            rec_len_r <= read_only ? REC_DEPTH16 : 16'd0;   // slurp a full record in read-only
            slurp_addr <= {`ADDR_W{1'b0}}; rd_dv <= 16'd0; sck_rd <= 1'b0;
            slurp_run <= 1'b0; slurp_done <= 1'b0;
            drain_pend <= read_only;   // read-only arms the drain P6 handshake immediately
            pretrig_work  <= pre_work_w;
            posttrig_work <= post_work_w;
        end else if (halt) begin
            if (state == ST_FILL && triggered) begin
                // finalize a triggered frame -> slurp SRAM->mem, then freeze.
                state           <= ST_DRAIN_SRAM;
                fill_frozen     <= 1'b1;
                fill_frozen_val <= wrote_count[10:0];
                rec_len_r       <= wrote_count;
                frame_done      <= 1'b1;
                slurp_addr <= {`ADDR_W{1'b0}}; rd_dv <= 16'd0;
                sck_rd <= 1'b0; slurp_run <= 1'b1; slurp_done <= 1'b0;
            end else if (state == ST_FILL && !triggered) begin
                // no trigger to anchor -> abandon; never expose a partial.
                state    <= ST_IDLE;
                coherent <= 1'b0;
            end
            // halt while already draining/halted: ignore (record stays coherent).
        end
    end

    // =======================================================================
    // FREE DEBUG READ PORT (bring-up visibility; app never reads these).
    //   0x00 DBG_ID     : identity tag
    //   0x04 DBG_RAW_HI : {p6, ..., dq_lat[21:16]}  (upper candidate lanes)
    //   0x1c DBG_RAW_LO : dq_lat[15:0]              (lower candidate lanes)
    //   0x7c DBG_STATUS : engine state + slurp progress + strobe peek
    // =======================================================================
    always @* begin
        dbg_rdata = 16'h0000;
        case (rd_sel)
            8'h00: dbg_rdata = 16'h5CA0;                       // "SCAP" id
            8'h04: dbg_rdata = {p6, 9'd0, dq_lat[21:16]};
            8'h1c: dbg_rdata = dq_lat[15:0];
            8'h7c: dbg_rdata = {slurp_addr[7:0], sck_rd, sck_wr, eng_enable,
                                slurp_done, slurp_run, coherent, state};
            default: dbg_rdata = 16'h0000;
        endcase
    end
    assign dbg_rd_hit = (rd_sel == 8'h00) || (rd_sel == 8'h04)
                     || (rd_sel == 8'h1c) || (rd_sel == 8'h7c);

endmodule

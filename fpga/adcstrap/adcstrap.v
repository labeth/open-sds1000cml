// adcstrap.v — ADC STATIC-CONTROL STRAP kit (takeover plan `14` S2.1 + S2.2 pre-check).
//
// WHY THIS KIT EXISTS
//   The seven AD9288-class static mode-control balls — F1 L4 T2 T7 G1 G2 K1 — are
//   hardwired constants in every shipping fabric (`adc_ctl_hi = 4'b1111;
//   adc_ctl_lo = 3'b000;`, fpga/standard/adcif.v:95-96). Sweeping them therefore
//   costs one Quartus build per state: the 128 states of takeover `14` S2.2c are
//   128 compiles. This kit makes the seven balls a RUNTIME REGISTER, so the whole
//   sweep is 128 register writes.
//
//   Homed per takeover `14` A-13 route 1 ("do the characterisation in a dedicated
//   diagnostic bitstream — use this by default"): standard/acq.v has 32 of 32 CS1
//   selectors in use, and the two dead-but-decoded homes A-13 offers in `standard`
//   (PRETRIG_HI 0x34 / POSTTRIG_HI 0x3c) are BOTH written to zero by gpmc_probe's
//   own `rawcap` sequence (tools/gpmc_probe/main.go: w(0x34,0); w(0x3c,0)), which
//   would silently cancel the override on every capture. A kit owns its own map.
//
// SAFETY — the override is SELF-LIMITING BY CONSTRUCTION (takeover `14` S2.1)
//   The reference implementation (sramcap/capsram.v:72-73,158,195 +
//   sramcap/acq_sram.v:236-243) gates the override on `cap_draining`; the plan is
//   explicit that this gate is a safety feature bounding how long the converters
//   can be held off, and that a free-running override can strand the ADC in a state
//   the host can no longer write out of (the recorded crash hung fpga_reload with
//   no route to host). Here the gate is replaced by a SELF-CLEARING ONE-SHOT:
//     * a write to CTL_OVR with bit[7]=1 applies the pattern for OVR_WIN * 4096
//       `clk` cycles and then REVERTS TO THE FACTORY HOLD IN HARDWARE;
//     * the window is 16 bits of 4096 clk => hard ceiling 2^28 clk ~ 3.4 s at the
//       nominal ~80 MHz C2 (~6.4 s at the pessimistic 42 MHz of `18` C1) — the
//       ceiling is a REGISTER WIDTH, not a software convention;
//     * there is NO latching mode. Per S2.1 that is added only after the one-shot
//       has demonstrated a clean round trip on the instrument.
//     * every strap is driven to a STATIC level. The recorded firmware crash came
//       from a TOGGLING beacon on F1 G1 G2 K1 T2 T7 (fpga-specs/53 §5.5); nothing
//       in this kit toggles a strap.
//
// BIT-ORDER — one ball, one named port. NO INDEX TO PERMUTE.
//   capsram documents adc_ctl_ovr[6:0] = {F1,L4,T2,T7,G1,G2,K1} but wires it as
//   adc_ctl_hi[3:0] = eff_ctl[6:3] / adc_ctl_lo[2:0] = eff_ctl[2:0] against a QSF
//   that assigns adc_ctl_hi[0]=F1 .. [3]=T7 and adc_ctl_lo[0]=G1 .. [2]=K1 — i.e.
//   BOTH nibbles are bit-reversed relative to the documented names. The factory
//   hold 7'b1111000 is symmetric under that reversal, which is why it never showed.
//   Any NON-default pattern driven through capsram's map lands on the wrong balls.
//   This kit uses seven scalar ports named for their balls, so the QSF says
//   `PIN_F1 -to strap_f1` and no index can silently permute the sweep's labels.
//
// BIDIR PRE-CHECK (takeover `14` S2.2)
//   L4, T2 and T7 are census-direction BIDIR, not output (fpga-specs/23 §2.5). The
//   valid pre-check is a PULL-UP A/B: build the ball tri-stated with
//   WEAK_PULL_UP_RESISTOR ON and again with it OFF, and read the ball. A net that
//   FOLLOWS the pull-up is free; a net that STAYS PUT in both variants is held by
//   something external and must not be driven.
//   ⚠ A "SAMPLE with the output disabled and see whether the level is one we are
//   not sourcing" test is NOT VALID: an undriven CMOS mode input with no board
//   resistor floats to an indeterminate level, so that test returns a random answer
//   and would condemn a perfectly free ball. Only the A/B discriminates.
//   Two macro-selected variants (STRAP_PRECHECK_PU1 / _PU0) turn L4/T2/T7 into
//   tri-stated inputs; the QSF supplies ON / OFF. F1 G1 G2 K1 stay driven at the
//   factory hold so only the three balls under test change posture.
//   The primary sensor is JTAG SAMPLE (open-sds1102cml boundary-scan kit). This
//   fabric ALSO reports the three levels over GPMC at STRAP_IN as {now, and, or}
//   accumulated over the capture window — an independent second sensor that, unlike
//   a single SAMPLE snapshot, distinguishes "sits at a clean level" (and==or) from
//   "floating and noisy" (and=0, or=1).
//
// ID — the loaded image identifies itself (takeover PHASE0 precondition P2)
//   CS1 0x10 (and 0x00) reads:
//     0x57A0  adcstrap        — the seven straps DRIVEN, override live
//     0x57A1  adcstrap_pu1    — L4/T2/T7 tri-stated inputs, WEAK_PULL_UP ON
//     0x57A2  adcstrap_pu0    — L4/T2/T7 tri-stated inputs, WEAK_PULL_UP OFF
//   The pull-up posture is a CRAM/QSF property invisible to RTL, so the three
//   variants would otherwise be indistinguishable on the instrument — exactly the
//   trap the deploy slot fell into. One read of 0x10 names the image.
//
// REGISTER MAP (CS1, mult-of-4 selectors, decoded on sel[6:2] only — the
//   HW-verified scheme: A1/A2/A8 are unusable, so every selector is a multiple of 4)
//   R 0x00  ID          = `KITID          (alias of 0x10; a CS-base prefetch is harmless)
//   R 0x10  ID          = `KITID
//   R 0x14  REV         = 0x0001
//   R 0x18  CAPS        {precheck, 7'd0, 8'd51}   51 = lanes covered
//   W 0x20  ARM         strobe: clear buffer + wptr + full + AND/OR accumulators, record
//   RW0x24  ENC_DIV     [15:0] ENCODE half-period-1 in clk  (ENCODE = clk/(2*(DIV+1)))
//   RW0x28  DECIM       [15:0] store every (DECIM+1)-th ENCODE rise
//   RW0x2c  BANK        [1:0] time-series slice: 0=lane[15:0] 1=[31:16] 2=[47:32] 3={13'd0,[50:48]}
//   R 0x30  STATUS      {full, ovr_active, 4'd0, wptr[9:0]}
//   W 0x34  RDADDR      [9:0] read index into the 1024-deep buffer
//   R 0x38  RDDATA      buf[RDADDR]   (registered — updates 1 clk after RDADDR)
//   RW0x40  CTL_OVR     {7'd0, autoarm[8], en[7], pat[6:0]}  <- THE S2.1 REGISTER
//                       pat = {F1,L4,T2,T7,G1,G2,K1}; en=1 (re)starts the one-shot,
//                       en=0 cancels it NOW. autoarm=1 also pulses ARM in the same
//                       cycle, so one write both applies the state and starts the
//                       capture inside the window.
//   RW0x44  OVR_WIN     [15:0] one-shot window in units of 4096 clk (0 => 1 unit)
//   R 0x48  OVR_LEFT    [15:0] window units remaining (0 => reverted)
//   R 0x4c  STRAP_IN    {7'd0, now[2:0], and[2:0], or[2:0]}  each = {L4,T2,T7}
//                       (PRECHECK builds; 16'h0000 in the driven build)
//   W 0x54  OVR_CLR     strobe: revert to the factory hold immediately
//   R 0x3c  OVR_STAT    {ovr_active, 1'b0, pat[6:0], eff[6:0]}  eff = what the balls carry
//   R 0x60/0x64/0x68/0x6c  LANE_AND[15:0]/[31:16]/[47:32]/{13'd0,[50:48]}
//   R 0x70/0x74/0x78/0x7c  LANE_OR [15:0]/[31:16]/[47:32]/{13'd0,[50:48]}
//
//   The AND/OR accumulators are the point of the kit for a 128-state sweep: the
//   S2.2 predicate ("a coherent group of >= 8 lanes goes constant and reversibly
//   returns") is computed IN FABRIC over the whole record, so scoring one strap
//   state costs 8 register reads instead of a 1024-word dump. Per lane:
//     AND=1            -> constant 1 for the whole window
//     OR =0            -> constant 0 for the whole window
//     AND=0 and OR=1   -> live (toggled)
//   ⚠ constant-1 alone does NOT prove Hi-Z: an ADC still driving code 0xFF reads 1
//   on every data lane too (takeover `14` A-3). The rail A/B (offset DAC / GND
//   coupling) is what discriminates, and it is a run-time procedure, not a fabric
//   feature. The time-series buffer is kept so any hit can be re-examined in full.
//
// SAFETY / posture
//   Driven balls: 8 ENCODE (K8 K9 K10 L8 L9 L10 M7 M8) + the C14/D14 differential
//   ENCODE + the strap set + gpmc_d (CS1 reads only) + gpmc_wait. Every one is a
//   ball the factory itself drives; all are capped to MINIMUM CURRENT in the QSF.
//   A11 is read as adc_lane[13] (the proven fpga/rawcap posture) rather than driven
//   as a third ENCODE: if a converter is clocked only from A11 it stays frozen in
//   EVERY state including the enable=0 control, so it cannot masquerade as a strap
//   effect — the control capture pins it down. Volatile CRAM: a Shelly mains cycle
//   restores the factory image.
//
// Clean-room: design/spec-derived, never vendor RTL. Synthesizable Verilog-2001,
// EP4CE10F17C8.

`ifdef STRAP_PRECHECK_PU1
  `ifndef STRAP_PRECHECK
    `define STRAP_PRECHECK
  `endif
  `define KITID 16'h57A1
`endif
`ifdef STRAP_PRECHECK_PU0
  `ifndef STRAP_PRECHECK
    `define STRAP_PRECHECK
  `endif
  `define KITID 16'h57A2
`endif
`ifndef KITID
  `define KITID 16'h57A0
`endif

module adcstrap (
    input  wire        clk,             // ball C2 — free-running fabric reference (~80 MHz, UNMEASURED: `18` C1)

    // ---- ADC data lanes: the full 51-ball set, INPUT ONLY, weak pull-up ----
    //      index -> ball order is byte-identical to standard/acq.qsf, except that
    //      index 13 is ball A11 here (acq.qsf spends A11 on a third ENCODE and
    //      leaves adc_lane[13] unplaced). Keeping the same indices means a capture
    //      from this kit is directly comparable to a `standard` raw capture.
    input  wire [50:0] adc_lane,

    // ---- ADC ENCODE drive (the bench-cracked converting recipe) ----
    output wire [7:0]  adc_enc,         // K8 K9 K10 L8 L9 L10 M7 M8 — common clock
    output wire        adc_enc_c14,     // C14 \ differential sample clock; leaving these
    output wire        adc_enc_d14,     // D14 / floating was the recorded "ADC dead" regression

    // ---- the seven static mode-control balls, one named port each ----
    output wire        strap_f1,        // F1  (factory hold 1)
`ifdef STRAP_PRECHECK
    input  wire        strap_l4,        // L4  census BIDIR — tri-stated input for the pull-up A/B
    input  wire        strap_t2,        // T2  census BIDIR — tri-stated input for the pull-up A/B
    input  wire        strap_t7,        // T7  census BIDIR — tri-stated input for the pull-up A/B
`else
    output wire        strap_l4,        // L4  (factory hold 1)
    output wire        strap_t2,        // T2  (factory hold 1)
    output wire        strap_t7,        // T7  (factory hold 1)
`endif
    output wire        strap_g1,        // G1  (factory hold 0)
    output wire        strap_g2,        // G2  (factory hold 0)
    output wire        strap_k1,        // K1  (factory hold 0)

    // ---- GPMC slave (CS1 only; CS3 is decoded by the MAX V, bench-proven) ----
    input  wire        nCS1,
    input  wire        nOE,
    input  wire        nWE,
    input  wire [6:0]  sel,
    inout  wire [15:0] gpmc_d,
    output wire        gpmc_wait
);
    // =======================================================================
    // 1) GPMC slave — identical to the HW-verified acq / rawcap scheme
    // =======================================================================
    reg [2:0]  cs1_q = 3'b111, oe_q = 3'b111, we_q = 3'b111;
    reg [6:0]  sel_q1 = 7'd0, sel_q2 = 7'd0;
    reg [15:0] d_q1 = 16'd0, d_q2 = 16'd0;
    always @(posedge clk) begin
        cs1_q <= {cs1_q[1:0], nCS1};
        oe_q  <= {oe_q[1:0],  nOE};
        we_q  <= {we_q[1:0],  nWE};
        sel_q1 <= sel;      sel_q2 <= sel_q1;
        d_q1   <= gpmc_d;   d_q2   <= d_q1;
    end
    wire       cs1_low   = (cs1_q[2] == 1'b0);
    wire       we_commit = (we_q[2] == 1'b0) && (we_q[1] == 1'b1);   // nWE rising
    // bits 0/1/7 forced 0: A1 (M2, carries a ~50% clock), A2 (D1, floats high) and
    // A8 are unusable, so only sel[6:2] = A3..A7 decode.
    wire [7:0] wr_sel    = {1'b0, sel_q2[6:2], 2'b00};
    wire [7:0] rd_sel    = {1'b0, sel[6:2],    2'b00};

    // =======================================================================
    // 2) Host-writable state
    // =======================================================================
    localparam [6:0] FACTORY_HOLD = 7'b1111000;   // {F1,L4,T2,T7,G1,G2,K1} = 1111 000

    reg [15:0] enc_div    = 16'd3;        // ENCODE = clk/(2*4) ~ 10 MHz at 80 MHz
    reg [15:0] decim      = 16'd40;       // store every 41st ENCODE rise
    reg [1:0]  bank       = 2'd0;
    reg [9:0]  rdaddr     = 10'd0;
    reg        arm        = 1'b0;         // 1-cycle pulse

    reg [6:0]  ovr_pat    = FACTORY_HOLD; // the pattern the one-shot applies
    reg        ovr_active = 1'b0;         // 1 => the balls carry ovr_pat
    reg [15:0] ovr_win    = 16'd2000;     // window, units of 4096 clk (~102 ms at 80 MHz)
    reg [15:0] ovr_left   = 16'd0;        // units remaining
    reg [11:0] ovr_tick   = 12'd0;        // 0..4095 sub-counter

    wire       w_ctl_ovr  = we_commit && cs1_low && (wr_sel == 8'h40);
    wire       w_ovr_clr  = we_commit && cs1_low && (wr_sel == 8'h54);
    wire       ovr_start  = w_ctl_ovr && d_q2[7];     // en=1  -> (re)start the one-shot
    wire       ovr_stop   = (w_ctl_ovr && !d_q2[7]) || w_ovr_clr;

    always @(posedge clk) begin
        arm <= 1'b0;
        if (we_commit && cs1_low) begin
            case (wr_sel)
                8'h20: arm     <= 1'b1;
                8'h24: enc_div <= d_q2;
                8'h28: decim   <= d_q2;
                8'h2c: bank    <= d_q2[1:0];
                8'h34: rdaddr  <= d_q2[9:0];
                8'h44: ovr_win <= d_q2;
                default: ;
            endcase
        end
        // ---- the self-clearing one-shot ----------------------------------
        // Priority: an explicit stop always wins; a start latches the pattern and
        // reloads the window; otherwise the window counts down and, at zero,
        // REVERTS TO THE FACTORY HOLD IN HARDWARE with no host action.
        if (ovr_stop) begin
            ovr_active <= 1'b0;
            ovr_left   <= 16'd0;
            ovr_tick   <= 12'd0;
        end else if (ovr_start) begin
            ovr_pat    <= d_q2[6:0];
            ovr_active <= 1'b1;
            ovr_left   <= (ovr_win == 16'd0) ? 16'd1 : ovr_win;
            ovr_tick   <= 12'd0;
            if (d_q2[8]) arm <= 1'b1;     // autoarm: capture inside the window
        end else if (ovr_active) begin
            if (ovr_tick == 12'd4095) begin
                ovr_tick <= 12'd0;
                if (ovr_left <= 16'd1) begin
                    ovr_left   <= 16'd0;
                    ovr_active <= 1'b0;   // <-- the revert
                end else begin
                    ovr_left <= ovr_left - 16'd1;
                end
            end else begin
                ovr_tick <= ovr_tick + 12'd1;
            end
        end
    end

    // ---- effective strap levels: override while active, factory hold otherwise --
    wire [6:0] eff_ctl = ovr_active ? ovr_pat : FACTORY_HOLD;
    assign strap_f1 = eff_ctl[6];
`ifndef STRAP_PRECHECK
    assign strap_l4 = eff_ctl[5];
    assign strap_t2 = eff_ctl[4];
    assign strap_t7 = eff_ctl[3];
`endif
    assign strap_g1 = eff_ctl[2];
    assign strap_g2 = eff_ctl[1];
    assign strap_k1 = eff_ctl[0];

    // =======================================================================
    // 3) ENCODE generation (rawcap's proven scheme)
    // =======================================================================
    reg        enc_clk = 1'b0, enc_prev = 1'b0;
    reg [15:0] dv = 16'd0;
    always @(posedge clk) begin
        enc_prev <= enc_clk;
        if (dv >= enc_div) begin dv <= 16'd0; enc_clk <= ~enc_clk; end
        else                     dv <= dv + 16'd1;
    end
    wire enc_rise = enc_clk & ~enc_prev;
    assign adc_enc     = {8{enc_clk}};
    assign adc_enc_c14 =  enc_clk;
    assign adc_enc_d14 = ~enc_clk;

    // =======================================================================
    // 4) Bidir pre-check sensor (PRECHECK builds only)
    // =======================================================================
    // Two-FF synchronize the three asynchronous ball levels, then accumulate them
    // over the same window as the lane accumulators. {L4,T2,T7}.
`ifdef STRAP_PRECHECK
    reg [2:0] sp_q1 = 3'd0, sp_q2 = 3'd0;
    always @(posedge clk) begin
        sp_q1 <= {strap_l4, strap_t2, strap_t7};
        sp_q2 <= sp_q1;
    end
    wire [2:0] strap_now = sp_q2;
`else
    wire [2:0] strap_now = 3'd0;
`endif

    // =======================================================================
    // 5) Decimated time-series recorder + per-lane AND/OR accumulators
    // =======================================================================
    reg [15:0] buf_mem [0:1023];
    reg [9:0]  wptr  = 10'd0;
    reg        full  = 1'b0;
    reg [15:0] dcnt  = 16'd0;
    reg [50:0] lr    = 51'd0;             // lanes registered once
    reg [50:0] lane_and = {51{1'b1}};
    reg [50:0] lane_or  = 51'd0;
    reg [2:0]  strap_and = 3'b111;
    reg [2:0]  strap_or  = 3'b000;

    reg [15:0] slice;
    always @* begin
        case (bank)
            2'd0:    slice = lr[15:0];
            2'd1:    slice = lr[31:16];
            2'd2:    slice = lr[47:32];
            default: slice = {13'd0, lr[50:48]};
        endcase
    end

    always @(posedge clk) begin
        lr <= adc_lane;
        if (arm) begin
            wptr      <= 10'd0;
            full      <= 1'b0;
            dcnt      <= 16'd0;
            lane_and  <= {51{1'b1}};
            lane_or   <= 51'd0;
            strap_and <= 3'b111;
            strap_or  <= 3'b000;
        end else if (!full && enc_rise) begin
            if (dcnt >= decim) begin
                dcnt <= 16'd0;
                buf_mem[wptr] <= slice;
                lane_and  <= lane_and  & lr;
                lane_or   <= lane_or   | lr;
                strap_and <= strap_and & strap_now;
                strap_or  <= strap_or  | strap_now;
                if (wptr == 10'd1023) full <= 1'b1;
                else                  wptr <= wptr + 10'd1;
            end else begin
                dcnt <= dcnt + 16'd1;
            end
        end
    end

    // Registered read port so the 16 Kbit buffer infers M9K block RAM rather than
    // 16,384 logic-cell flip-flops (an async read on a 1024-deep array does not fit
    // an EP4CE10). RDDATA is valid one clk after the RDADDR write — microseconds
    // before the host's next GPMC read.
    reg [15:0] rd_q = 16'd0;
    always @(posedge clk) rd_q <= buf_mem[rdaddr];

    // =======================================================================
    // 6) Read mux
    // =======================================================================
`ifdef STRAP_PRECHECK
    localparam PRECHECK_BIT = 1'b1;
`else
    localparam PRECHECK_BIT = 1'b0;
`endif

    reg [15:0] rdata;
    always @* begin
        case (rd_sel)
            8'h00:   rdata = `KITID;
            8'h10:   rdata = `KITID;
            8'h14:   rdata = 16'h0001;                       // REV
            8'h18:   rdata = {PRECHECK_BIT, 7'd0, 8'd51};    // CAPS: lanes covered
            8'h24:   rdata = enc_div;
            8'h28:   rdata = decim;
            8'h2c:   rdata = {14'd0, bank};
            8'h30:   rdata = {full, ovr_active, 4'd0, wptr};
            8'h38:   rdata = rd_q;
            8'h3c:   rdata = {ovr_active, 1'b0, ovr_pat, eff_ctl};
            8'h40:   rdata = {7'd0, 1'b0, ovr_active, ovr_pat};
            8'h44:   rdata = ovr_win;
            8'h48:   rdata = ovr_left;
            8'h4c:   rdata = {7'd0, strap_now, strap_and, strap_or};
            8'h60:   rdata = lane_and[15:0];
            8'h64:   rdata = lane_and[31:16];
            8'h68:   rdata = lane_and[47:32];
            8'h6c:   rdata = {13'd0, lane_and[50:48]};
            8'h70:   rdata = lane_or[15:0];
            8'h74:   rdata = lane_or[31:16];
            8'h78:   rdata = lane_or[47:32];
            8'h7c:   rdata = {13'd0, lane_or[50:48]};
            default: rdata = 16'h0000;
        endcase
    end

    wire read_active = (~nCS1) & (~nOE);
    assign gpmc_d    = read_active ? rdata : 16'hzzzz;
    assign gpmc_wait = 1'b1;

endmodule

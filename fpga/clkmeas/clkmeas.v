// =============================================================================
// clkmeas.v -- the absolute clock-measurement fabric.
//
// Owner: takeover plan 18-clocks-and-timebase.md, steps C1.1 / C1.2 / C1.3
//        (= master-plan Phase 1 step 1.4, blocker CB-4).
//
// WHAT THIS IS FOR
//   Neither reference clock on this board has ever been edge-counted. Three
//   arithmetic paths in the corpus give ~80, ~100 and ~42 MHz for ball C2, and
//   every ns figure, every sample-rate claim and every SDC constraint we own
//   scales with it. This fabric answers it by TRUE EDGE COUNT, which cannot
//   alias, instead of by sampling, which can (a sampler clocked from C2 reads a
//   C2-coherent clock as a constant -- that is exactly how ball R4 was called
//   "static" and then measured a rock-steady 25.000 MHz).
//
// THE ONE PROPERTY THAT MATTERS: THE LATCH IS ATOMIC.
//   A torn read across a free-running 32-bit counter is the classic way this
//   measurement goes silently wrong. Here every counter lives in ITS OWN clock
//   domain and captures all 32 of its bits into a shadow register on ONE edge of
//   ITS OWN clock. Nothing samples a moving counter from another domain; the
//   host only ever reads a shadow, and a shadow is static between latches.
//   The latch REQUEST crosses domains (as a toggle, 2-FF synchronised); the
//   captured VALUE never does.
//
//   Two hardware self-checks are built in, so the host does not have to trust
//   this comment:
//     * DOUBLE SHADOW. One latch fills shadow A on the capture edge and
//       shadow B on the NEXT edge of the same clock. B - A is therefore
//       EXACTLY 1 when the counter is running and EXACTLY 0 when it is frozen,
//       by construction -- the "every pair differs by 0 or 1, never more"
//       predicate of C1.1, measurable in one latch instead of twenty.
//     * FREEZE. CTRL.run = 0 stops every counter. Two successive latches with
//       run = 0 must return IDENTICAL A and B words for every domain. That is
//       C1.1's atomicity predicate, and it is only meaningful because the
//       counters can actually be stopped.
//
// WHAT IT COUNTS (15 independent domains, one 32-bit counter each)
//     0  clk          ball C2    the fabric reference             (CB-4, D1)
//     1  mclk_in      ball M2    dedicated PLL-INCLK reference    (CB-4, D3)
//     2  ball_e1      ball E1  \
//     3  ball_m1      ball M1   |  the six "dead" PLL-INCLK balls. The DDR scan
//     4  ball_e15     ball E15  |  that called them static was a SAMPLER; a free
//     5  ball_e16     ball E16  |  32-bit counter integrates over an arbitrarily
//     6  ball_m15     ball M15  |  long gate and cannot alias. A hit here is a
//     7  ball_m16     ball M16 /   SECOND PLL-capable reference (18 CLK-8).
//     8  ball_k2      ball K2  \   the four SRAM-family balls. K2 = 50.000 MHz
//     9  ball_r4      ball R4   |  and R4 = 25.000 MHz under the FACTORY fabric;
//    10  ball_f2      ball F2   |  under ours they are plain inputs, so a
//    11  ball_j2      ball J2  /   non-zero count means an EXTERNAL driver (D2).
//    12  trig_sense   ball A12   the trigger comparator, usable as a clock to
//                                ~1 MHz -- the gate input that touches no BGA.
//    13  pll_c4       u_m2pll  c4 = mclk_in x 5/80 = f_M2/16 exactly.
//                              16 x count(13) MUST equal count(1). An in-fabric
//                              cross-check of the M2 counter that needs no
//                              external standard at all.
//    14  pll200_c0    u_pll200 c0 = mclk_in x 5/4. count(14)/count(1) MUST be
//                              1.25. Second independent M2 witness.
//
// WHAT IT DRIVES
//   gpmc_d (only on a CS1 read outside the panel window) and gpmc_wait. That is
//   ALL. Every other ball is an input or is reserved AS INPUT TRI-STATED. In
//   particular this fabric drives NO ENCODE, NO ADC controls and NO SRAM pins,
//   so it cannot fight the MAX V or the converters for anything.
//   The panel-window exclusion on the read driver is the same fix step 0.5
//   applied to acq.v: selectors 0x64/0x68 may be answered by another device
//   under resolution R2, and driving them would be a bus fight.
//
// SAFETY / REVERSIBILITY
//   Volatile SRAM configuration only. A Shelly mains cycle restores the factory
//   image from NAND. Nothing here writes EPCS, NAND, firmdata0 or calibration.
//
// HOST REGISTER MAP  (GPMC CS1; selector = {1'b0, sel_hi[4:0], 2'b00}, i.e. the
// 32 multiples of 4 from 0x00 to 0x7C -- A1/A2 do not reach this device, CB-3)
//
//   0x00  R   ID          0xC1EA   <-- THE FABRIC DISCRIMINATOR. Read it and log
//                                      it every session (precondition P2).
//                                      0x5CA0 = sramcap, 0xC1EA = clkmeas.
//   0x10  R   ID          0xC1EA
//   0x14  R   VERSION     0x0001
//   0x18  R   NDOM        number of counted domains (15)
//   0x1C  R   STATUS      {seq[7:0], 4'b0, gate_en, run, pll200_lk, m2pll_lk}
//   0x20  RW  CTRL        [0] run (reset 1)   [1] a12_gate_en (reset 0)
//   0x24  W   CLEAR       any write zeroes every counter (value ignored)
//   0x28  W   LATCH       any write captures every counter (value ignored)
//   0x2C  RW  GATE_N_LO   A12-gate divisor, low  16 bits (must be >= 1)
//   0x30  RW  GATE_N_HI   A12-gate divisor, high 16 bits
//   0x34  RW  IDX         [3:0] domain, [4] 0 = shadow A, 1 = shadow B
//   0x38  R   DATA_LO     shadow[IDX][15:0]
//   0x3C  R   DATA_HI     shadow[IDX][31:16]
//   0x40  R   ACK         bit g = 1 <=> domain g captured the CURRENT latch.
//                         A domain whose clock never ticks NEVER acks -- so a
//                         0 here is positive evidence that the ball is dead,
//                         not a hang. Do not wait on it.
//   0x48  R   C2_LO   ) direct, index-free readback of shadow A for the two
//   0x4C  R   C2_HI   ) domains step 1.4 actually needs. Use THESE for C2/M2:
//   0x50  R   M2_LO   ) they cannot be desynchronised from IDX.
//   0x54  R   M2_HI   )
//   0x64,0x68           NOT DECODED and NOT DRIVEN (panel window).
//
// HOST PROTOCOL
//   Host-gated absolute count (C1.2):
//       write CTRL = 0x0000 (run=0) ; write CLEAR ; write CTRL = 0x0001 (run=1)
//       busy-wait the gate T ; write CTRL = 0x0000 (run=0) ; write LATCH
//       read 0x48/0x4C (C2) and 0x50/0x54 (M2) and, via IDX, the rest.
//     Defining the gate by run's rising and falling edge is better than
//     CLEAR->LATCH: the 2-FF synchroniser delay is the same on both edges and
//     cancels, so the only gate error left is the host's own busy-wait.
//   Atomicity self-check (C1.1 pass predicate):
//       with run=0: LATCH, read all; LATCH, read all -> must be IDENTICAL.
//   Cross-domain latch check (C1.1 "0 or 1"):
//       with run=1: LATCH, then read shadow A and shadow B of each domain
//       (IDX bit 4) -> B - A must be exactly 1 for every LIVE domain and
//       exactly 0 for every dead one. Never anything else.
//   IDX RACE -- MANDATORY. A GPMC write is only committed into the clk domain
//   ~4 clk after nWE rises, and the read path decodes the selector
//   COMBINATIONALLY. A read of 0x38 issued immediately after a write of 0x34
//   can therefore return the PREVIOUS domain's word with no error indication.
//   ALWAYS read 0x34 back and confirm it equals what you wrote BEFORE reading
//   0x38/0x3C. (0x48-0x54 are immune -- they need no IDX.)
//   In A12_GATE mode a latch can fire between your two DATA reads: read STATUS
//   (0x1C) before and after and re-read if seq changed.
// =============================================================================


// -----------------------------------------------------------------------------
// cnt32 -- one counted domain: a 32-bit free-running edge counter plus an atomic
// double shadow, entirely inside the domain's own clock.
//
// The counter is SEGMENTED (4 x 8 bits with pre-computed wrap enables) rather
// than a flat 32-bit ripple-carry add. This is not decoration: M2's candidate
// range runs to 266 MHz, and a flat 32-bit carry chain on an EP4CE10 -8 part is
// marginal there. Undercounting from a missed carry would be invisible in the
// result -- it would just look like a slower clock. The segmented form keeps the
// longest combinational path at one 8-bit carry chain and two 4-LUT levels.
//
// Correctness of the segmentation (why the enables are one cycle early):
//   e1 is registered as (q[7:0] == 8'hFE), so on the NEXT edge q[7:0] rolls
//   FF -> 00 and q[15:8] increments -- exactly when byte 0 wraps. f1/f2 hold
//   (q[15:8] == FF) and (q[15:8] == FF && q[23:16] == FF) from the previous
//   cycle; those bytes only change on a wrap, i.e. at least 253 cycles before
//   the next time e2/e3 are evaluated, so the registered copy is never stale
//   when it is used.
//
// run_a / clr_a / lat_a arrive from the clk domain and are each 2-FF
// synchronised HERE, in the counter's own domain. lat_a and clr_a are TOGGLES
// (level changes), not pulses, so a domain slower than clk cannot miss one.
// -----------------------------------------------------------------------------
module cnt32 (
    input  wire        c,       // this domain's clock -- the signal under test
    input  wire        run_a,   // async level : count enable            (clk dom)
    input  wire        clr_a,   // async toggle: zero the counter        (clk dom)
    input  wire        lat_a,   // async toggle: capture into A then B   (clk dom)
    output reg  [31:0] sa,      // shadow A: q at the capture edge
    output reg  [31:0] sb,      // shadow B: q one domain-clock later
    output reg         ack      // mirrors lat_a's polarity once captured
);
    reg [31:0] q     = 32'd0;
    reg        e1    = 1'b0, e2 = 1'b0, e3 = 1'b0;   // wrap enables (1 cycle early)
    reg        f1    = 1'b0, f2 = 1'b0;              // registered partial compares
    reg [2:0]  run_s = 3'b000, clr_s = 3'b000, lat_s = 3'b000;
    reg        bpend = 1'b0;

    // ack starts at 1 while lat_a starts at 0, so "done" (ack == lat_a, computed
    // in the clk domain) is FALSE at power-up -- a domain must actually capture
    // before it is reported as having captured.
    initial begin sa = 32'd0; sb = 32'd0; ack = 1'b1; end

    // lat_s[0]/clr_s[0]/run_s[0] are the (possibly metastable) first stage;
    // [1] and [2] are the two clean stages the edge detect uses.
    wire clr_ev = clr_s[2] ^ clr_s[1];
    wire lat_ev = lat_s[2] ^ lat_s[1];

    always @(posedge c) begin
        run_s <= {run_s[1:0], run_a};
        clr_s <= {clr_s[1:0], clr_a};
        lat_s <= {lat_s[1:0], lat_a};

        if (clr_ev) begin
            q  <= 32'd0;
            e1 <= 1'b0; e2 <= 1'b0; e3 <= 1'b0;
            f1 <= 1'b0; f2 <= 1'b0;
        end else if (run_s[2]) begin
            q[7:0]           <= q[7:0]   + 8'd1;
            if (e1) q[15:8]  <= q[15:8]  + 8'd1;
            if (e2) q[23:16] <= q[23:16] + 8'd1;
            if (e3) q[31:24] <= q[31:24] + 8'd1;
            e1 <= (q[7:0]  == 8'hFE);
            e2 <= (q[7:0]  == 8'hFE) & f1;
            e3 <= (q[7:0]  == 8'hFE) & f2;
            f1 <= (q[15:8] == 8'hFF);
            f2 <= (q[15:8] == 8'hFF) & (q[23:16] == 8'hFF);
        end

        // THE ATOMIC LATCH. All 32 bits of q are captured on one edge of c.
        // sb is captured on the very next edge of c, so sb - sa is 1 (running)
        // or 0 (frozen) by construction and nothing else is possible.
        if (lat_ev) begin
            sa    <= q;
            bpend <= 1'b1;
            ack   <= lat_s[1];   // the NEW polarity: this is the capture receipt
        end else if (bpend) begin
            sb    <= q;
            bpend <= 1'b0;
        end
    end
endmodule


module clkmeas (
    // --- the two references under test -------------------------------------
    input  wire        clk,          // ball C2  -- fabric reference, host domain
    input  wire        mclk_in,      // ball M2  -- dedicated PLL-INCLK
    // --- the trigger comparator (also the A12 gate source) ------------------
    input  wire        trig_sense,   // ball A12
    // --- the six "dead" PLL-INCLK balls -------------------------------------
    input  wire        ball_e1,      // ball E1
    input  wire        ball_m1,      // ball M1
    input  wire        ball_e15,     // ball E15
    input  wire        ball_e16,     // ball E16
    input  wire        ball_m15,     // ball M15
    input  wire        ball_m16,     // ball M16
    // --- the four SRAM-family balls that carry rates under the factory image -
    input  wire        ball_k2,      // ball K2   (50.000 MHz, factory)
    input  wire        ball_r4,      // ball R4   (25.000 MHz, factory; bidir)
    input  wire        ball_f2,      // ball F2
    input  wire        ball_j2,      // ball J2
    // --- GPMC CS1 slave ------------------------------------------------------
    input  wire        nCS1,
    input  wire        nOE,
    input  wire        nWE,
    input  wire  [4:0] sel_hi,       // GPMC A3..A7 == selector bits [6:2]
    inout  wire [15:0] gpmc_d,
    output wire        gpmc_wait
);
    localparam integer NDOM  = 15;   // counted domains
    localparam [15:0]  NDOM16 = NDOM; // same value, sized for the read mux
    localparam integer NSLOT = 16;   // readback slots (IDX[3:0]); 15 is padding

    // =========================================================================
    // GPMC front end -- identical in structure to the HW-verified slave in
    // sramla.v / acq.v: 3-FF synchronise nCS1 and nWE, pipeline sel and the
    // write data two deep so they are stable at the commit edge, and commit on
    // the RISING edge of nWE while nCS1 is low.
    // =========================================================================
    reg  [2:0]  cs1_q  = 3'b111, we_q = 3'b111;
    reg  [4:0]  sel_q1 = 5'd0,   sel_q2 = 5'd0;
    reg  [15:0] d_q1   = 16'd0,  d_q2   = 16'd0;
    always @(posedge clk) begin
        cs1_q  <= {cs1_q[1:0], nCS1};
        we_q   <= {we_q[1:0],  nWE};
        sel_q1 <= sel_hi;  sel_q2 <= sel_q1;
        d_q1   <= gpmc_d;  d_q2   <= d_q1;
    end
    wire       cs1_low   = (cs1_q[2] == 1'b0);
    wire       we_commit = (we_q[2] == 1'b0) && (we_q[1] == 1'b1);
    wire       wr_hit    = we_commit & cs1_low;
    wire [7:0] wr_sel    = {1'b0, sel_q2, 2'b00};
    wire [7:0] rd_sel    = {1'b0, sel_hi, 2'b00};

    // =========================================================================
    // Control registers (clk domain)
    // =========================================================================
    reg        run_r   = 1'b1;    // counters free-run from configuration
    reg        gate_en = 1'b0;
    reg        clr_tog = 1'b0;
    reg        lat_tog = 1'b0;
    reg [31:0] gate_n  = 32'd10000;
    reg [4:0]  idx     = 5'd0;
    reg [7:0]  seq     = 8'd0;

    wire host_latch = wr_hit & (wr_sel == 8'h28);
    wire gate_edge;                       // from the A12 gate, below
    wire latch_ev   = host_latch | gate_edge;

    always @(posedge clk) begin
        if (wr_hit) begin
            case (wr_sel)
                8'h20: begin run_r <= d_q2[0]; gate_en <= d_q2[1]; end
                8'h24: clr_tog          <= ~clr_tog;
                8'h2C: gate_n[15:0]     <= d_q2;
                8'h30: gate_n[31:16]    <= d_q2;
                8'h34: idx              <= d_q2[4:0];
                default: ;
            endcase
        end
        if (latch_ev) begin
            lat_tog <= ~lat_tog;
            seq     <= seq + 8'd1;
        end
    end

    // =========================================================================
    // The two PLLs.
    //
    // Both are instantiated on mclk_in with the SAME input ratios as the shipped
    // acq.v fabric, so their `locked` bits are directly comparable with the
    // Job 6 [BENCH] result and with the 120.02-260.08 MHz / 80.01-173.4 MHz
    // windows the corpus quotes. u_m2pll's c4 output (unused in acq.v) is
    // configured here as x5/80 = f_M2/16 and is COUNTED, which turns "locked
    // asserted" into a measured ratio: 16 x count(pll_c4) must equal count(M2).
    // =========================================================================
    wire [5:0] m2pll_out;     // 6 wide: the altpll formal port width (c0..c5)
    wire       m2pll_locked;
    altpll #(
        .bandwidth_type("AUTO"),
        .clk0_divide_by(2),  .clk0_duty_cycle(50), .clk0_multiply_by(5), .clk0_phase_shift("0"),
        .clk1_divide_by(5),  .clk1_duty_cycle(50), .clk1_multiply_by(5), .clk1_phase_shift("1562"),
        .clk2_divide_by(5),  .clk2_duty_cycle(50), .clk2_multiply_by(5), .clk2_phase_shift("3125"),
        .clk3_divide_by(5),  .clk3_duty_cycle(50), .clk3_multiply_by(5), .clk3_phase_shift("4687"),
        .clk4_divide_by(80), .clk4_duty_cycle(50), .clk4_multiply_by(5), .clk4_phase_shift("0"),
        // acq.v compensates on CLK0; here c0..c3 are unused and pruned, so CLK0 is
        // not available to compensate against. This changes the PHASE compensation
        // only -- M, N and the VCO, which are what set the 120.02-260.08 MHz lock
        // window this design exists to test, are unchanged.
        .compensate_clock("CLK4"), .inclk0_input_frequency(6250),
        .intended_device_family("Cyclone IV E"), .operation_mode("NORMAL"), .pll_type("AUTO"),
        .port_clk0("PORT_USED"), .port_clk1("PORT_USED"), .port_clk2("PORT_USED"),
        .port_clk3("PORT_USED"), .port_clk4("PORT_USED"),
        .port_inclk0("PORT_USED"), .port_locked("PORT_USED")
    ) u_m2pll ( .inclk({1'b0, mclk_in}), .clk(m2pll_out), .locked(m2pll_locked) );

    wire [5:0] pll200_out;
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

    wire pll_c4    = m2pll_out[4];    // f_M2 x 5/80  = f_M2/16
    wire pll200_c0 = pll200_out[0];   // f_M2 x 5/4

    reg [1:0] lk_m2 = 2'b00, lk_200 = 2'b00;
    always @(posedge clk) begin
        lk_m2  <= {lk_m2[0],  m2pll_locked};
        lk_200 <= {lk_200[0], pll200_locked};
    end

    // =========================================================================
    // The 15 counted domains
    // =========================================================================
    wire [NDOM-1:0] dclk = { pll200_c0, pll_c4, trig_sense,
                             ball_j2, ball_f2, ball_r4, ball_k2,
                             ball_m16, ball_m15, ball_e16, ball_e15,
                             ball_m1, ball_e1,
                             mclk_in, clk };

    wire [32*NSLOT-1:0] sa_flat, sb_flat;
    wire [NSLOT-1:0]    ackv;

    genvar g;
    generate
        for (g = 0; g < NDOM; g = g + 1) begin : dom
            cnt32 u (
                .c(dclk[g]), .run_a(run_r), .clr_a(clr_tog), .lat_a(lat_tog),
                .sa(sa_flat[32*g +: 32]), .sb(sb_flat[32*g +: 32]), .ack(ackv[g])
            );
        end
        for (g = NDOM; g < NSLOT; g = g + 1) begin : pad
            assign sa_flat[32*g +: 32] = 32'd0;
            assign sb_flat[32*g +: 32] = 32'd0;
            assign ackv[g]             = 1'b0;
        end
    endgenerate

    // Capture receipts, synchronised back into clk. done[g] is high once domain
    // g has captured the CURRENT latch request. A dead ball never sets it --
    // that is the intended reading, not a fault, so nothing in the host protocol
    // may block on it.
    reg [NSLOT-1:0] ack_s1 = {NSLOT{1'b0}}, ack_s2 = {NSLOT{1'b0}};
    always @(posedge clk) begin
        ack_s1 <= ackv;
        ack_s2 <= ack_s1;
    end
    wire [NSLOT-1:0] done = ~(ack_s2 ^ {NSLOT{lat_tog}});
    wire [15:0] done_m = done & 16'h7FFF;      // mask the one padding slot

    // =========================================================================
    // A12 gate (C1.4): latch on every N-th rising edge of the comparator.
    // Runs in the A12 domain. gate_n is quasi-static -- write it BEFORE arming
    // gate_en, never while the gate is running.
    // =========================================================================
    wire [31:0] gate_n_m1 = gate_n - 32'd1;    // gate_n must be >= 1
    reg [31:0] a12_gcnt   = 32'd0;
    reg        a12_tog    = 1'b0;
    reg [2:0]  gen_s      = 3'b000;
    always @(posedge trig_sense) begin
        gen_s <= {gen_s[1:0], gate_en};
        if (~gen_s[2]) begin
            a12_gcnt <= 32'd0;
        end else if (a12_gcnt >= gate_n_m1) begin
            a12_gcnt <= 32'd0;
            a12_tog  <= ~a12_tog;
        end else begin
            a12_gcnt <= a12_gcnt + 32'd1;
        end
    end
    reg [2:0] gt_s = 3'b000;
    always @(posedge clk) gt_s <= {gt_s[1:0], a12_tog};
    assign gate_edge = gt_s[2] ^ gt_s[1];

    // =========================================================================
    // Readback
    // =========================================================================
    wire [31:0] shadow = idx[4] ? sb_flat[{idx[3:0], 5'd0} +: 32]
                                : sa_flat[{idx[3:0], 5'd0} +: 32];

    reg [15:0] rdata;
    always @* begin
        case (rd_sel)
            8'h00: rdata = 16'hC1EA;                       // fabric discriminator
            8'h10: rdata = 16'hC1EA;
            8'h14: rdata = 16'h0001;                       // design version
            8'h18: rdata = NDOM16;
            8'h1C: rdata = {seq, 4'd0, gate_en, run_r, lk_200[1], lk_m2[1]};
            8'h20: rdata = {14'd0, gate_en, run_r};
            8'h2C: rdata = gate_n[15:0];
            8'h30: rdata = gate_n[31:16];
            8'h34: rdata = {11'd0, idx};
            8'h38: rdata = shadow[15:0];
            8'h3C: rdata = shadow[31:16];
            8'h40: rdata = done_m;
            8'h48: rdata = sa_flat[0*32      +: 16];       // C2 shadow A, low
            8'h4C: rdata = sa_flat[0*32 + 16 +: 16];       // C2 shadow A, high
            8'h50: rdata = sa_flat[1*32      +: 16];       // M2 shadow A, low
            8'h54: rdata = sa_flat[1*32 + 16 +: 16];       // M2 shadow A, high
            default: rdata = 16'h0000;
        endcase
    end

    // The panel window (0x64 / 0x68) is never decoded and never driven -- the
    // same qualification step 0.5 added to acq.v, for the same reason: another
    // device may answer those selectors and a second driver is a bus fight.
    wire panel_window = (rd_sel == 8'h64) | (rd_sel == 8'h68);
    wire read_active  = (~nCS1) & (~nOE) & (~panel_window);

    assign gpmc_d    = read_active ? rdata : 16'hzzzz;   // single tri-state driver
    assign gpmc_wait = 1'b1;                             // held ready, never wedge
endmodule

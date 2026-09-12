// pll_m2.v -- the acq2 clock tree from ball M2 ([BENCH] ~160 MHz continuous, owned-fpga 670b873).
//
//   PLL A (M2 -> 200 MHz):  c0 = 0 deg  c1 = 90  c2 = 180  c3 = 270   (the four encode phases,
//                           01-CONTRACT s2.1: five pairs on four output-clock codes)
//                           c4 = capture clock, 200 MHz, DYNAMICALLY phase-shiftable in VCO/8
//                           steps (600 MHz VCO -> 208 ps, 24 steps per period) = DIAG PHASE_SEL.CAP_STEP
//   PLL B (M2 -> bus):      c0 = 100 MHz (SRAM-class clock, F2/J2 option), c1 = 50 MHz (K2 forward
//                           [BENCH] 50.000 MHz, the 27-ball bus register clock)
//
// Both altplls are instantiated directly with parameters (no megawizard files). The prior's
// u_pll200 (owned-fpga acq.v, 5f8bb21) is the reference for the 160->200 M/N choice; the fitter
// realises 200 = 160 x 5/4 as M=15 N=4 VCO 600 MHz (fpga-specs/04 s5.3), which is what the
// 208 ps step assumes. inclk0_input_frequency is in ps (6250 = 160 MHz).
//
// Dynamic phase shift protocol (Cyclone IV E handbook, "PLL dynamic phase shifting"):
//   phasecounterselect (3 bits: 2..6 = c0..c4) and phaseupdown stable >= 1 scanclk before
//   phasestep rises; phasestep high >= 2 scanclk cycles; phasedone drops and returns high when
//   the shift is applied; the next step may start only after phasedone is high again.
// The step engine below walks c4 until step_applied == step_target (up or down). A step that
// never reports phasedone within 256 scanclk cycles is counted as applied and sets step_err
// (sticky until the target changes) -- printed in DBG, never absorbed.
//
// SIM (`ifdef SIM): a behavioural stand-in generates the clocks with fixed 5/10/20 ns periods
// (ignoring the actual mclk_in rate), lock after 1 us, and shifts cap_clk by 208 ps per step.

`timescale 1ns/1ps

module pll_m2 (
    input  wire       mclk_in,          // ball M2
    output wire       ph0,              // 200 MHz phases
    output wire       ph90,
    output wire       ph180,
    output wire       ph270,
    output wire       cap_clk,          // 200 MHz capture clock (phase-shiftable)
    output wire       clk100,
    output wire       clk50,
    output wire       locked_a,         // raw PLL lock outputs (async; synchronize before use)
    output wire       locked_b,
    // dynamic phase shift of cap_clk (scanclk domain; scanclk <= 100 MHz)
    input  wire       scanclk,
    input  wire [7:0] step_target,
    output reg  [7:0] step_applied,
    output wire       step_busy,
    output reg        step_err
);
    // ------------------------------------------------------------------
    // step engine (scanclk domain)
    // ------------------------------------------------------------------
    localparam [2:0] S_IDLE = 3'd0, S_SETUP = 3'd1, S_STEP = 3'd2, S_WAIT_LOW = 3'd3,
                     S_WAIT_HIGH = 3'd4, S_APPLY = 3'd5;
    localparam [2:0] CSEL_C4 = 3'd6;          // phasecounterselect code for counter c4

    reg [2:0] st         = S_IDLE;
    reg       phasestep  = 1'b0;
    reg       phaseupdown= 1'b1;
    reg [2:0] csel       = 3'd0;
    reg [7:0] tmo        = 8'd0;
    reg [2:0] hold       = 3'd0;
    wire      phasedone_raw;
    reg [1:0] pd_s       = 2'b11;             // phasedone synchronized (idles high)
    reg [7:0] target_q   = 8'd0;
    initial step_applied = 8'd0;
    initial step_err     = 1'b0;

    assign step_busy = (st != S_IDLE);

    always @(posedge scanclk) begin
        pd_s     <= {pd_s[0], phasedone_raw};
        target_q <= step_target;
        if (target_q != step_target) step_err <= 1'b0;   // a new request clears the sticky error
        case (st)
            S_IDLE: if (target_q != step_applied) begin
                        phaseupdown <= (target_q > step_applied);
                        csel        <= CSEL_C4;
                        hold        <= 3'd0;
                        st          <= S_SETUP;
                    end
            S_SETUP: begin                                  // select/updown settled one cycle
                        hold <= hold + 3'd1;
                        if (hold == 3'd1) begin phasestep <= 1'b1; hold <= 3'd0; st <= S_STEP; end
                     end
            S_STEP: begin                                   // phasestep high for 4 scanclk cycles
                        hold <= hold + 3'd1;
                        if (hold == 3'd3) begin phasestep <= 1'b0; tmo <= 8'd0; st <= S_WAIT_LOW; end
                    end
            S_WAIT_LOW: begin                               // expect phasedone to drop
                        tmo <= tmo + 8'd1;
                        if (pd_s[1] == 1'b0) st <= S_WAIT_HIGH;
                        else if (tmo == 8'hff) begin step_err <= 1'b1; st <= S_APPLY; end
                    end
            S_WAIT_HIGH: begin                              // ... and return high
                        tmo <= tmo + 8'd1;
                        if (pd_s[1] == 1'b1) st <= S_APPLY;
                        else if (tmo == 8'hff) begin step_err <= 1'b1; st <= S_APPLY; end
                    end
            S_APPLY: begin
                        step_applied <= phaseupdown ? (step_applied + 8'd1) : (step_applied - 8'd1);
                        st <= S_IDLE;
                    end
            default: st <= S_IDLE;
        endcase
    end

`ifdef SIM
    // ------------------------------------------------------------------
    // behavioural stand-in: fixed periods, phase offsets, lock after 1 us
    // ------------------------------------------------------------------
    reg c0 = 1'b0, c1 = 1'b0, c2 = 1'b0, c3 = 1'b0, b0 = 1'b0, b1 = 1'b0;
    reg lk_a = 1'b0, lk_b = 1'b0;
    reg capq = 1'b0;
    reg pd_model = 1'b1;
    initial begin
        #1000.0; lk_a = 1'b1; lk_b = 1'b1;
    end
    always #2.5 c0 = ~c0;
    initial begin #1.25;  forever #2.5 c1 = ~c1; end
    initial begin #2.5;   forever #2.5 c2 = ~c2; end
    initial begin #3.75;  forever #2.5 c3 = ~c3; end
    always #5.0  b0 = ~b0;
    always #10.0 b1 = ~b1;
    // cap_clk = c0 delayed by step_applied x 208 ps (transport delay, tracks step changes)
    always @(c0) capq <= #(0.208 * step_applied) c0;
    // phasedone: drops ~2 scanclk after phasestep rises, returns after ~4 more
    always @(posedge phasestep) begin
        repeat (2) @(posedge scanclk);
        pd_model = 1'b0;
        repeat (4) @(posedge scanclk);
        pd_model = 1'b1;
    end
    assign ph0 = c0; assign ph90 = c1; assign ph180 = c2; assign ph270 = c3;
    assign cap_clk = capq;
    assign clk100 = b0; assign clk50 = b1;
    assign locked_a = lk_a; assign locked_b = lk_b;
    assign phasedone_raw = pd_model;
`else
    wire [4:0] a_out;
    wire [4:0] b_out;
    altpll #(
        .bandwidth_type           ("AUTO"),
        .clk0_divide_by(4), .clk0_duty_cycle(50), .clk0_multiply_by(5), .clk0_phase_shift("0"),
        .clk1_divide_by(4), .clk1_duty_cycle(50), .clk1_multiply_by(5), .clk1_phase_shift("1250"),
        .clk2_divide_by(4), .clk2_duty_cycle(50), .clk2_multiply_by(5), .clk2_phase_shift("2500"),
        .clk3_divide_by(4), .clk3_duty_cycle(50), .clk3_multiply_by(5), .clk3_phase_shift("3750"),
        .clk4_divide_by(4), .clk4_duty_cycle(50), .clk4_multiply_by(5), .clk4_phase_shift("0"),
        .compensate_clock         ("CLK0"),
        .inclk0_input_frequency   (6250),
        .intended_device_family   ("Cyclone IV E"),
        .lpm_hint                 ("CBX_MODULE_PREFIX=pll_a"),
        .lpm_type                 ("altpll"),
        .operation_mode           ("NORMAL"),
        .pll_type                 ("AUTO"),
        .port_activeclock("PORT_UNUSED"), .port_areset("PORT_UNUSED"), .port_clkbad0("PORT_UNUSED"),
        .port_clkbad1("PORT_UNUSED"), .port_clkloss("PORT_UNUSED"), .port_clkswitch("PORT_UNUSED"),
        .port_configupdate("PORT_UNUSED"), .port_fbin("PORT_UNUSED"), .port_inclk0("PORT_USED"),
        .port_inclk1("PORT_UNUSED"), .port_locked("PORT_USED"), .port_pfdena("PORT_UNUSED"),
        .port_phasecounterselect("PORT_USED"), .port_phasedone("PORT_USED"),
        .port_phasestep("PORT_USED"), .port_phaseupdown("PORT_USED"), .port_pllena("PORT_UNUSED"),
        .port_scanaclr("PORT_UNUSED"), .port_scanclk("PORT_USED"), .port_scanclkena("PORT_UNUSED"),
        .port_scandata("PORT_UNUSED"), .port_scandataout("PORT_UNUSED"), .port_scandone("PORT_UNUSED"),
        .port_scanread("PORT_UNUSED"), .port_scanwrite("PORT_UNUSED"),
        .port_clk0("PORT_USED"), .port_clk1("PORT_USED"), .port_clk2("PORT_USED"),
        .port_clk3("PORT_USED"), .port_clk4("PORT_USED"), .port_clk5("PORT_UNUSED"),
        .port_clkena0("PORT_UNUSED"), .port_clkena1("PORT_UNUSED"), .port_clkena2("PORT_UNUSED"),
        .port_clkena3("PORT_UNUSED"), .port_clkena4("PORT_UNUSED"), .port_clkena5("PORT_UNUSED"),
        .port_extclk0("PORT_UNUSED"), .port_extclk1("PORT_UNUSED"), .port_extclk2("PORT_UNUSED"),
        .port_extclk3("PORT_UNUSED"),
        .self_reset_on_loss_lock  ("OFF"),
        .width_clock              (5),
        .width_phasecounterselect (3)
    ) u_pll_a (
        .inclk              ({1'b0, mclk_in}),
        .clk                (a_out),
        .locked             (locked_a),
        .scanclk            (scanclk),
        .phasecounterselect (csel),
        .phaseupdown        (phaseupdown),
        .phasestep          (phasestep),
        .phasedone          (phasedone_raw),
        .areset(1'b0), .clkena({6{1'b1}}), .clkswitch(1'b0), .configupdate(1'b0),
        .extclkena({4{1'b1}}), .fbin(1'b1), .pfdena(1'b1), .pllena(1'b1),
        .scanaclr(1'b0), .scanclkena(1'b1), .scandata(1'b0), .scanread(1'b0), .scanwrite(1'b0),
        .activeclock(), .clkbad(), .clkloss(), .enable0(), .enable1(), .extclk(),
        .fbmimicbidir(), .fbout(), .scandataout(), .scandone(), .sclkout0(), .sclkout1(),
        .vcooverrange(), .vcounderrange()
    );
    assign ph0 = a_out[0]; assign ph90 = a_out[1]; assign ph180 = a_out[2]; assign ph270 = a_out[3];
    assign cap_clk = a_out[4];

    altpll #(
        .bandwidth_type           ("AUTO"),
        .clk0_divide_by(8),  .clk0_duty_cycle(50), .clk0_multiply_by(5), .clk0_phase_shift("0"),
        .clk1_divide_by(16), .clk1_duty_cycle(50), .clk1_multiply_by(5), .clk1_phase_shift("0"),
        .compensate_clock         ("CLK0"),
        .inclk0_input_frequency   (6250),
        .intended_device_family   ("Cyclone IV E"),
        .lpm_hint                 ("CBX_MODULE_PREFIX=pll_b"),
        .lpm_type                 ("altpll"),
        .operation_mode           ("NORMAL"),
        .pll_type                 ("AUTO"),
        .port_activeclock("PORT_UNUSED"), .port_areset("PORT_UNUSED"), .port_clkbad0("PORT_UNUSED"),
        .port_clkbad1("PORT_UNUSED"), .port_clkloss("PORT_UNUSED"), .port_clkswitch("PORT_UNUSED"),
        .port_configupdate("PORT_UNUSED"), .port_fbin("PORT_UNUSED"), .port_inclk0("PORT_USED"),
        .port_inclk1("PORT_UNUSED"), .port_locked("PORT_USED"), .port_pfdena("PORT_UNUSED"),
        .port_phasecounterselect("PORT_UNUSED"), .port_phasedone("PORT_UNUSED"),
        .port_phasestep("PORT_UNUSED"), .port_phaseupdown("PORT_UNUSED"), .port_pllena("PORT_UNUSED"),
        .port_scanaclr("PORT_UNUSED"), .port_scanclk("PORT_UNUSED"), .port_scanclkena("PORT_UNUSED"),
        .port_scandata("PORT_UNUSED"), .port_scandataout("PORT_UNUSED"), .port_scandone("PORT_UNUSED"),
        .port_scanread("PORT_UNUSED"), .port_scanwrite("PORT_UNUSED"),
        .port_clk0("PORT_USED"), .port_clk1("PORT_USED"), .port_clk2("PORT_UNUSED"),
        .port_clk3("PORT_UNUSED"), .port_clk4("PORT_UNUSED"), .port_clk5("PORT_UNUSED"),
        .port_clkena0("PORT_UNUSED"), .port_clkena1("PORT_UNUSED"), .port_clkena2("PORT_UNUSED"),
        .port_clkena3("PORT_UNUSED"), .port_clkena4("PORT_UNUSED"), .port_clkena5("PORT_UNUSED"),
        .port_extclk0("PORT_UNUSED"), .port_extclk1("PORT_UNUSED"), .port_extclk2("PORT_UNUSED"),
        .port_extclk3("PORT_UNUSED"),
        .self_reset_on_loss_lock  ("OFF"),
        .width_clock              (5)
    ) u_pll_b (
        .inclk              ({1'b0, mclk_in}),
        .clk                (b_out),
        .locked             (locked_b),
        .areset(1'b0), .clkena({6{1'b1}}), .clkswitch(1'b0), .configupdate(1'b0),
        .extclkena({4{1'b1}}), .fbin(1'b1), .pfdena(1'b1), .pllena(1'b1),
        .phasecounterselect(4'b0000), .phasestep(1'b0), .phaseupdown(1'b0), .scanclk(1'b0),
        .scanaclr(1'b0), .scanclkena(1'b1), .scandata(1'b0), .scanread(1'b0), .scanwrite(1'b0),
        .activeclock(), .clkbad(), .clkloss(), .enable0(), .enable1(), .extclk(),
        .fbmimicbidir(), .fbout(), .phasedone(), .scandataout(), .scandone(), .sclkout0(), .sclkout1(),
        .vcooverrange(), .vcounderrange()
    );
    assign clk100 = b_out[0];
    assign clk50  = b_out[1];
`endif
endmodule

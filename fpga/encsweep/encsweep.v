// encsweep.v — ENCODE-ball discovery diagnostic for the SDS1000CML Cyclone IV.
//
// The owned acquisition fabric needs to DRIVE the ADC ENCODE clock, but which Cyclone
// output ball reaches the converters' ENCODE input is unknown (boundary-scan can't tell
// a driven output from an input). This diagnostic finds it WITHOUT a Quartus rebuild per
// candidate: it reuses the HW-verified GPMC register slave (A3..A7-only decode, clk=C2)
// to (a) drive a ~MHz clock onto a register-SELECTED candidate ball, one at a time, and
// (b) watch a sticky per-lane change-detector on the 40 ADC data lanes. Sweep ENC_SEL
// over the candidates via register writes; the candidate that makes the ADC lanes TOGGLE
// (converting) is an ENCODE clock the fabric must drive.
//
//   NOTE: needs the ADC to have something to digitize — a signal on the analog input, or
//   just converter thermal noise (LSB dither toggles once the part is actually clocked).
//
// Register map (CS1, mult-of-4 selectors so decode uses only verified lines A3..A7):
//   0x10 R  ID          = 0xE5CE (handshake / addressing sanity)
//   0x20 RW ENC_SEL     [4:0]=candidate index, [8]=drive enable
//   0x24 RW ENC_DIV     [15:0] half-period in clk cycles (enc_clk = clk/(2*(DIV+1)))
//   0x28 W  CHG_RST     (strobe) latch the ADC reference + clear the change bitmap
//   0x40 R  CHG_LO      ADC change bitmap [15:0]  (bit i set => lane i toggled since RST)
//   0x44 R  CHG_MID     ADC change bitmap [31:16]
//   0x48 R  CHG_HI      ADC change bitmap [39:32]
//
// SAFETY: only gpmc_d (on a CS1 read), gpmc_wait, and the ONE selected enc_cand ball are
// driven (min current, qsf); all others Hi-Z / reserved inputs. Reboot after (volatile CRAM).
// Clean-room, synthesizable Verilog-2001.

module encsweep #(
    parameter integer NCAND = 16
)(
    input  wire        clk,                  // C2, ~80 MHz (verified)
    input  wire [23:0] adc_ch1,              // ADC data lanes (verified 36 + candidates)
    input  wire [15:0] adc_ch2,
    output wire [NCAND-1:0] enc_cand,        // candidate ENCODE outputs (one driven at a time)

    input  wire        nCS1,
    input  wire        nOE,
    input  wire        nWE,
    input  wire [6:0]  sel,
    inout  wire [15:0] gpmc_d,
    output wire        gpmc_wait
);
    // ---- GPMC synchronizers (identical scheme to the verified acq.v slave) ----
    reg [2:0]  cs1_q = 3'b111, oe_q = 3'b111, we_q = 3'b111;
    reg [6:0]  sel_q1 = 7'd0, sel_q2 = 7'd0;
    reg [15:0] d_q1 = 16'd0, d_q2 = 16'd0;
    always @(posedge clk) begin
        cs1_q <= {cs1_q[1:0], nCS1};
        oe_q  <= {oe_q[1:0],  nOE};
        we_q  <= {we_q[1:0],  nWE};
        sel_q1 <= sel; sel_q2 <= sel_q1;
        d_q1 <= gpmc_d; d_q2 <= d_q1;
    end
    wire       cs1_low   = (cs1_q[2] == 1'b0);
    wire       we_commit = (we_q[2] == 1'b0) && (we_q[1] == 1'b1);
    wire [7:0] wr_sel    = {1'b0, sel_q2[6:2], 2'b00};   // mask bits 0/1/7 -> A3..A7 only
    wire [7:0] rd_sel    = {1'b0, sel[6:2],   2'b00};

    // ---- register file ----
    reg [4:0]  enc_sel  = 5'd0;
    reg        enc_en   = 1'b0;
    reg [15:0] enc_div  = 16'd7;      // clk/(2*8) ~ 5 MHz default ENCODE
    reg        chg_rst  = 1'b0;
    always @(posedge clk) begin
        chg_rst <= 1'b0;
        if (we_commit && cs1_low) begin
            case (wr_sel)
                8'h20: begin enc_sel <= d_q2[4:0]; enc_en <= d_q2[8]; end
                8'h24: enc_div <= d_q2;
                8'h28: chg_rst <= 1'b1;
                default: ;
            endcase
        end
    end

    // ---- ENCODE clock divider ----
    reg        enc_clk = 1'b0;
    reg [15:0] dv = 16'd0;
    always @(posedge clk) begin
        if (dv >= enc_div) begin dv <= 16'd0; enc_clk <= ~enc_clk; end
        else                     dv <= dv + 16'd1;
    end

    // ---- drive enc_clk onto the selected candidate; every other candidate Hi-Z ----
    genvar g;
    generate for (g = 0; g < NCAND; g = g + 1) begin : gcand
        assign enc_cand[g] = (enc_en && (enc_sel == g[4:0])) ? enc_clk : 1'bz;
    end endgenerate

    // ---- sticky per-lane ADC change detector ----
    wire [39:0] adc = {adc_ch2, adc_ch1};
    reg  [39:0] adc_r = 40'd0, adc_ref = 40'd0, chg = 40'd0;
    always @(posedge clk) begin
        adc_r <= adc;
        if (chg_rst) begin adc_ref <= adc_r; chg <= 40'd0; end
        else               chg <= chg | (adc_r ^ adc_ref);
    end

    // ---- read mux ----
    reg [15:0] rdata;
    always @* begin
        case (rd_sel)
            8'h10:   rdata = 16'hE5CE;
            8'h20:   rdata = {7'd0, enc_en, 3'd0, enc_sel};
            8'h24:   rdata = enc_div;
            8'h40:   rdata = chg[15:0];
            8'h44:   rdata = chg[31:16];
            8'h48:   rdata = {8'd0, chg[39:32]};
            default: rdata = 16'h0000;
        endcase
    end
    wire read_active = (~nCS1) & (~nOE);
    assign gpmc_d    = read_active ? rdata : 16'hzzzz;
    assign gpmc_wait = 1'b1;

endmodule

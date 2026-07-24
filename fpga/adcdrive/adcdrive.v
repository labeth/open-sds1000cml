// adcdrive.v — "shotgun" ADC-drive replication diagnostic for the SDS1000CML Cyclone IV.
//
// Goal: make the 3 AD9288s CONVERT under our OWN fabric, without a schematic or physical
// access, by REPLICATING the factory's ADC drive wholesale. From boundary-scan of the
// running factory (which converts correctly) we know exactly which balls the Cyclone
// DRIVES (control cells) and their behaviour:
//   * 32 bottom-cluster DRIVEN-TOGGLING outputs  -> the ENCODE (ENCA/ENCB) + phase/clock
//     pool. We drive ALL of them with one common ~MHz clock (a common phase still makes
//     every AD9288 core convert; phase only sets the interleave, not whether it converts).
//   * 7 DRIVEN-STATIC controls held at their factory values (F1/L4/T2/T7=1, G1/G2/K1=0)
//     -> the S1/S2/DFS/mode pool, so the parts leave power-down.
// Then a sticky change-detector on the 33 ADC data-lane INPUTS reports conversion:
// with a live input signal, converting cores make the data lanes toggle.
//
// If the ADC converts under this shotgun, the drive model is proven and the ENCODE can
// be isolated by binary-searching the driven subset (future variants). AD9288: ENCODE
// single-ended, 1 MSPS min -> ENC_DIV keeps the clock >=~1 MHz.
//
// Register map (CS1, mult-of-4 selectors, A3..A7-only decode — HW-verified scheme):
//   0x10 R  ID       = 0xADC0
//   0x24 RW ENC_DIV  [15:0] half-period in clk cycles (clk/(2*(DIV+1)))
//   0x28 W  CHG_RST  (strobe) latch ADC reference + clear the change bitmap
//   0x40 R  CHG_LO   data-lane change bitmap [15:0]
//   0x44 R  CHG_MID  [31:16]
//   0x48 R  CHG_HI   [32]
//
// SAFETY: driven balls (32 shot + 7 hold + gpmc_d/gpmc_wait) are ALL Cyclone outputs the
// factory itself drives (no board contention) and capped to MINIMUM CURRENT in the qsf;
// reboot after (volatile CRAM). Clean-room, synthesizable Verilog-2001.

module adcdrive (
    input  wire        clk,             // C2 ~80 MHz
    input  wire [32:0] adc_lane,        // 33 ADC data-lane inputs (change detector)
    output wire [31:0] shot,            // 32 shotgun clock outputs (all = enc_clk)
    output wire [3:0]  hold_hi,         // driven-static HIGH controls (F1 L4 T2 T7)
    output wire [2:0]  hold_lo,         // driven-static LOW  controls (G1 G2 K1)

    input  wire        nCS1,
    input  wire        nOE,
    input  wire        nWE,
    input  wire [6:0]  sel,
    inout  wire [15:0] gpmc_d,
    output wire        gpmc_wait
);
    // ---- GPMC slave (identical to the HW-verified acq/encsweep scheme) ----
    reg [2:0]  cs1_q = 3'b111, oe_q = 3'b111, we_q = 3'b111;
    reg [6:0]  sel_q1 = 7'd0, sel_q2 = 7'd0;
    reg [15:0] d_q1 = 16'd0, d_q2 = 16'd0;
    always @(posedge clk) begin
        cs1_q <= {cs1_q[1:0], nCS1}; oe_q <= {oe_q[1:0], nOE}; we_q <= {we_q[1:0], nWE};
        sel_q1 <= sel; sel_q2 <= sel_q1; d_q1 <= gpmc_d; d_q2 <= d_q1;
    end
    wire       cs1_low   = (cs1_q[2] == 1'b0);
    wire       we_commit = (we_q[2] == 1'b0) && (we_q[1] == 1'b1);
    wire [7:0] wr_sel    = {1'b0, sel_q2[6:2], 2'b00};
    wire [7:0] rd_sel    = {1'b0, sel[6:2],   2'b00};

    reg [15:0] enc_div = 16'd3;          // clk/(2*4)=~10 MHz default
    reg        chg_rst = 1'b0;
    always @(posedge clk) begin
        chg_rst <= 1'b0;
        if (we_commit && cs1_low) begin
            case (wr_sel)
                8'h24: enc_div <= d_q2;
                8'h28: chg_rst <= 1'b1;
                default: ;
            endcase
        end
    end

    // ---- common shotgun clock ----
    reg        enc_clk = 1'b0;
    reg [15:0] dv = 16'd0;
    always @(posedge clk) begin
        if (dv >= enc_div) begin dv <= 16'd0; enc_clk <= ~enc_clk; end
        else                     dv <= dv + 16'd1;
    end
    assign shot    = {32{enc_clk}};
    assign hold_hi = 4'b1111;
    assign hold_lo = 3'b000;

    // ---- sticky change detector on the ADC data lanes ----
    reg [32:0] lr = 33'd0, lref = 33'd0, chg = 33'd0;
    always @(posedge clk) begin
        lr <= adc_lane;
        if (chg_rst) begin lref <= lr; chg <= 33'd0; end
        else               chg <= chg | (lr ^ lref);
    end

    // ---- read mux ----
    reg [15:0] rdata;
    always @* begin
        case (rd_sel)
            8'h10:   rdata = 16'hADC0;
            8'h24:   rdata = enc_div;
            8'h40:   rdata = chg[15:0];
            8'h44:   rdata = chg[31:16];
            8'h48:   rdata = {15'd0, chg[32]};
            default: rdata = 16'h0000;
        endcase
    end
    wire read_active = (~nCS1) & (~nOE);
    assign gpmc_d    = read_active ? rdata : 16'hzzzz;
    assign gpmc_wait = 1'b1;

endmodule

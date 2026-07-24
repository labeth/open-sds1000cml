// rawcap.v — raw ADC-lane TIME-SERIES recorder (bit-order discriminator diagnostic).
//
// Purpose: derive the per-bit order / core grouping of the ADC data lanes, which the
// offset-DAC sweep provably CANNOT (its analog gain rails the ADC in <2 offset LSBs, so
// every CH1 lane flips together — no ranking). The order needs a COHERENT TIME-SERIES:
// with a slowly-moving input (a triangle), consecutive samples differ by a small code
// delta, so LSBs toggle nearly every sample and MSBs rarely. Ranking lanes by toggle-rate
// (and clustering lanes that move together) recovers the byte(s) and their bit order.
//
// This fabric DRIVES the proven converting ADC set (8 ENCODE clocks + 7 held controls,
// identical to adcif) and, once ARMed, records a DECIMATED stream of a selectable 16-lane
// slice into a 256-deep buffer, then freezes. The buffer is read back over GPMC with plain
// COMBINATIONAL reads (write RDADDR, read RDDATA) — no BURST, no app, so a single
// gpmc_probe read with factory timing works exactly like the HW-verified register reads.
//
// Register map (CS1, mult-of-4 selectors, A3..A7-only decode — HW-verified scheme):
//   0x10 R  ID       = 0xADC1
//   0x20 W  ARM      (strobe) reset wptr/full, start recording
//   0x24 RW ENC_DIV  [15:0] ENCODE half-period in clk cycles (clk/(2*(DIV+1)))
//   0x28 RW DECIM    [15:0] store every (DECIM+1)th ENCODE sample (tunes code-step/sample)
//   0x2C RW BANK     bit0: 0 = store adc_lane[15:0], 1 = store adc_lane[31:16]
//   0x30 R  STATUS   {full, 6'd0, wptr[8:0]}
//   0x34 W  RDADDR   [8:0] read index into the buffer
//   0x38 R  RDDATA   = buf[RDADDR]  (combinational)
//
// SAFETY: driven balls (8 ENCODE + 7 controls + gpmc_d/gpmc_wait) are all Cyclone outputs
// the factory itself drives (no board contention), capped to MINIMUM CURRENT in the qsf.
// Reboot after (volatile CRAM). Clean-room, synthesizable Verilog-2001, EP4CE10.

module rawcap (
    input  wire        clk,             // C2 ~80 MHz reference
    input  wire [32:0] adc_lane,        // 33 ADC data-lane inputs
    output wire [7:0]  adc_enc,         // 8 ENCODE clock outputs (common clock)
    output wire [3:0]  adc_ctl_hi,      // held-HIGH controls (F1 L4 T2 T7)
    output wire [2:0]  adc_ctl_lo,      // held-LOW  controls (G1 G2 K1)

    input  wire        nCS1,
    input  wire        nOE,
    input  wire        nWE,
    input  wire [6:0]  sel,
    inout  wire [15:0] gpmc_d,
    output wire        gpmc_wait
);
    // ---- GPMC slave (identical to the HW-verified acq/adcdrive scheme) ----
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

    reg [15:0] enc_div = 16'd3;      // clk/(2*4) ~ 10 MHz ENCODE
    reg [15:0] decim   = 16'd40;     // store every 41st sample
    reg        bank    = 1'b0;
    reg [8:0]  rdaddr  = 9'd0;
    reg        arm     = 1'b0;
    always @(posedge clk) begin
        arm <= 1'b0;
        if (we_commit && cs1_low) begin
            case (wr_sel)
                8'h20: arm     <= 1'b1;
                8'h24: enc_div <= d_q2;
                8'h28: decim   <= d_q2;
                8'h2C: bank    <= d_q2[0];
                8'h34: rdaddr  <= d_q2[8:0];
                default: ;
            endcase
        end
    end

    // ---- ENCODE clock + held controls (bring the AD9288s out of power-down) ----
    reg        enc_clk = 1'b0, enc_prev = 1'b0;
    reg [15:0] dv = 16'd0;
    always @(posedge clk) begin
        enc_prev <= enc_clk;
        if (dv >= enc_div) begin dv <= 16'd0; enc_clk <= ~enc_clk; end
        else                     dv <= dv + 16'd1;
    end
    wire enc_rise = enc_clk & ~enc_prev;
    assign adc_enc    = {8{enc_clk}};
    assign adc_ctl_hi = 4'b1111;
    assign adc_ctl_lo = 3'b000;

    // ---- decimated time-series recorder ----
    reg [15:0] buf_mem [0:255];
    reg [8:0]  wptr = 9'd0;
    reg        full = 1'b0;
    reg [15:0] dcnt = 16'd0;
    reg [32:0] lr = 33'd0;
    wire [15:0] slice = bank ? adc_lane[31:16] : adc_lane[15:0];
    always @(posedge clk) begin
        lr <= adc_lane;                       // register the lanes once
        if (arm) begin
            wptr <= 9'd0; full <= 1'b0; dcnt <= 16'd0;
        end else if (!full && enc_rise) begin
            if (dcnt >= decim) begin
                dcnt <= 16'd0;
                buf_mem[wptr] <= (bank ? lr[31:16] : lr[15:0]);
                if (wptr == 9'd255) full <= 1'b1;
                else                wptr <= wptr + 9'd1;
            end else begin
                dcnt <= dcnt + 16'd1;
            end
        end
    end
    wire [15:0] rddata = buf_mem[rdaddr];

    // ---- read mux ----
    reg [15:0] rdata;
    always @* begin
        case (rd_sel)
            8'h10:   rdata = 16'hADC1;
            8'h24:   rdata = enc_div;
            8'h28:   rdata = decim;
            8'h2C:   rdata = {15'd0, bank};
            8'h30:   rdata = {full, 6'd0, wptr};
            8'h38:   rdata = rddata;
            default: rdata = 16'h0000;
        endcase
    end
    wire read_active = (~nCS1) & (~nOE);
    assign gpmc_d    = read_active ? rdata : 16'hzzzz;
    assign gpmc_wait = 1'b1;

endmodule

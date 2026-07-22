// dac.v — trigger-level DAC serializer (3-wire, MSB-first), tri-stated until load.
//
// The CPU writes a 16-bit level code over CS3 (LVL_A/B_LO then LVL_A/B_HI); acq.v
// assembles the code and pulses `dac_load`. On a load this shifts the 16 bits out
// MSB-first at clk/(2*DAC_DIV): frame low (`dac_sync`=0) for the shift, idle high
// otherwise. The exact DAC part / framing is HW-discovered — DAC_DIV and the framing
// are CANDIDATE placeholders resolved on the bench.
//
// SAFETY: the three DAC balls are Hi-Z until the FIRST level load (`dac_active`
// sticky), so a mis-mapped candidate ball cannot contend before we deliberately drive
// a level. This is the only place these balls are ever driven.
//
// Clean-room: design/spec-derived. Synthesizable Verilog-2001, EP4CE10.

module dac #(
    parameter integer DAC_DIV = 8      // clk cycles per DAC bit-clock half (CANDIDATE)
)(
    input  wire        clk,
    input  wire        dac_load,       // 1-cycle: new code ready to serialize
    input  wire [15:0] dac_code,       // full 16-bit level code (assembled in acq.v)

    output wire        dac_sync,       // frame / load, active-low
    output wire        dac_sclk,       // serial bit clock
    output wire        dac_sdi         // serial data, MSB first
);

    reg [15:0] dac_shift = 16'h0000;
    reg [4:0]  dac_bits  = 5'd0;
    reg [7:0]  dac_presc = 8'd0;
    reg        dac_busy  = 1'b0;
    reg        dac_active= 1'b0;       // sticky: set on first load -> start driving
    reg        dac_sclk_r= 1'b0;
    reg        dac_sync_r= 1'b1;       // idle high (deasserted)
    reg        dac_sdi_r = 1'b0;

    always @(posedge clk) begin
        if (dac_load && !dac_busy) begin
            dac_busy   <= 1'b1;
            dac_active <= 1'b1;                 // first write -> enable the drivers
            dac_shift  <= dac_code;
            dac_bits   <= 5'd16;
            dac_presc  <= 8'd0;
            dac_sclk_r <= 1'b0;
            dac_sync_r <= 1'b0;                 // assert frame
            dac_sdi_r  <= dac_code[15];         // present MSB
        end else if (dac_busy) begin
            if (dac_presc >= (DAC_DIV[7:0] - 8'd1)) begin
                dac_presc  <= 8'd0;
                dac_sclk_r <= ~dac_sclk_r;
                if (dac_sclk_r) begin           // falling edge: advance to next bit
                    if (dac_bits <= 5'd1) begin
                        dac_busy   <= 1'b0;
                        dac_sync_r <= 1'b1;     // deassert frame
                        dac_sclk_r <= 1'b0;
                    end else begin
                        dac_bits  <= dac_bits - 1'b1;
                        dac_shift <= {dac_shift[14:0], 1'b0};
                        dac_sdi_r <= dac_shift[14];
                    end
                end
            end else begin
                dac_presc <= dac_presc + 8'd1;
            end
        end
    end

    // Hi-Z until the first level load; single driver on each DAC ball.
    assign dac_sync = dac_active ? dac_sync_r : 1'bz;
    assign dac_sclk = dac_active ? dac_sclk_r : 1'bz;
    assign dac_sdi  = dac_active ? dac_sdi_r  : 1'bz;

endmodule

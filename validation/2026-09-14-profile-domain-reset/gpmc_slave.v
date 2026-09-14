// gpmc_slave.v -- 16-bit asynchronous GPMC slave on the C2 clock (the [BENCH]-verified shape of
// owned-fpga fpga/standard/acq.v s1, commits 8d3a2de / 9e207e0; re-implemented here).
//
//   * nCS1 / nOE / nWE are 3-flop synchronized; sel and gpmc_d (write data) 2-flop, so at the
//     commit cycle sel_q2 / d_q2 are the values sampled on the same edge that first saw nWE high
//     (the GPMC holds address and data past the nWE rise to the end of the cycle).
//   * sel is the full 7-bit selector: bit b = GPMC A(b+1). Bits 6:2 are the A3..A7 address balls,
//     bit 1 = GPMC A2 = ball A2 and bit 0 = GPMC A1 = ball B1 (DEFINITIVE, reports/2026-09-05-R0:
//     12/12 SNOOP_SEL writes) -- the top concatenates {sel[6:2], gpmc_a2, gpmc_b1}. 128 selectors
//     (schema v3, SEL_MASK 0x7f); regmux.vh masks bit 7 (06-TIERS s2).
//   * we_commit: one pulse on the nWE rising edge while nCS1 is low.  wr_sel is the selector as
//     it reaches the fabric ({0, sel}); wr_aux = {B1, A2} = {sel[0], sel[1]} sampled with it,
//     kept for SNOOP_SEL bits 8/7 (they duplicate SEL[1:0] since v3).
//   * rd_pop: one pulse on the nOE rising edge while nCS1 is low, for pop-on-read selectors.
//     rd_sel is the LIVE selector while a read is in progress (the read mux must answer within
//     the access) and the selector HELD from the last read otherwise, so the pop strobe -- which
//     fires ~3 clocks after nOE rises -- still compares against the address of the read that just
//     ended even if the GPMC has already changed the address lines.
//   * gpmc_d is driven by exactly ONE tri-state driver, only while nCS1 & nOE are both low (the AD
//     bus is shared with the NAND rootfs, 05-WORKPLAN s4.4). Read data is combinational from the
//     live selector (all seven bits are in the 30 ns read through-path budget, default.sdc).

`timescale 1ns/1ps

module gpmc_slave #(parameter QUALIFIED_READ=0) (
    input  wire        clk,
    input  wire        nCS1,
    input  wire        nOE,
    input  wire        nWE,
    input  wire [6:0]  sel,          // {A7..A3, A2 (ball A2), A1 (ball B1)}
    inout  wire [15:0] gpmc_d,

    output wire        we_commit,
    output wire [7:0]  wr_sel,
    output wire [15:0] wr_data,
    output wire [1:0]  wr_aux,       // {B1, A2} levels at the commit = {wr_sel[0], wr_sel[1]}
    output wire        rd_pop,
    output wire [7:0]  rd_sel,
    input  wire [15:0] rdata,
    output wire        drive_active   // the data bus is being driven (DBG)
);
    reg [2:0]  cs1_q  = 3'b111, oe_q = 3'b111, we_q = 3'b111;
    reg [6:0]  sel_q1 = 7'h00, sel_q2 = 7'h00;
    reg [15:0] d_q1   = 16'h0000, d_q2 = 16'h0000;
    reg [6:0]  sel_hold = 7'h00;
    reg [2:0] read_q = 0;

    always @(posedge clk) begin
        read_q <= {read_q[1:0], !nCS1 && !nOE};
        cs1_q  <= {cs1_q[1:0], nCS1};
        oe_q   <= {oe_q[1:0],  nOE};
        we_q   <= {we_q[1:0],  nWE};
        sel_q1 <= sel;      sel_q2 <= sel_q1;
        d_q1   <= gpmc_d;   d_q2   <= d_q1;          // meaningful only during a write
        if (!nCS1 && !nOE) sel_hold <= sel;          // address of the read in progress
    end

    wire cs1_low = (cs1_q[2] == 1'b0);
    assign we_commit = cs1_low && (we_q[2] == 1'b0) && (we_q[1] == 1'b1);   // nWE rising
    assign wr_sel    = {1'b0, sel_q2};
    assign wr_data   = d_q2;
    assign wr_aux    = {sel_q2[0], sel_q2[1]};                              // {B1, A2}
    // Count the end of a selected read even if CS releases before OE.
    assign rd_pop    = QUALIFIED_READ ? (read_q[2] && !read_q[1]) : cs1_low && (oe_q[2] == 1'b0) && (oe_q[1] == 1'b1);   // nOE rising

    wire read_now = (~nCS1) & (~nOE);
    assign rd_sel       = read_now ? {1'b0, sel} : {1'b0, sel_hold};
    assign drive_active = read_now;
    assign gpmc_d       = read_now ? rdata : 16'hzzzz;    // the single tri-state driver
endmodule

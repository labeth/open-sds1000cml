// drain.v — the single auto-inc BURST port (1-D DMA source) + BURST_REMAIN.
//
// A single fixed-address raw-record readout: successive `SEL_BURST reads walk the
// frozen record 0,1,..,rec_len-1 in order — the 1-D stride a GPMC prefetch / EDMA
// descriptor chases at one fixed byte address. This replaces the proving-ground
// 5-port round-robin: one address, one pointer, one pop per nOE-rise, HALT-gated
// (advances only while `coherent`, which is only asserted after the record freezes).
//
// `rec_len` is the frozen captured length (pre+post) from capture, so a drain returns
// exactly the captured window; the pointer saturates at rec_len so REMAIN reaches 0
// and the address clamps at the last valid word (a flat dead tail past the end).
//
// BURST_REMAIN = {READY[15], REMAIN[14:0]} : READY=1 while a coherent record is open,
// REMAIN = words still to drain (live count) — the flow-control the app's self-paced
// EDMA / prefetch drain gates on.
//
// The record data itself comes from capture's registered read port; drain only owns
// the address + the remaining count. acq.v gates the data word on `coherent`.
//
// Clean-room: design/spec-derived. Synthesizable Verilog-2001, EP4CE10.

`include "regs.vh"

module drain (
    input  wire                 clk,
    input  wire                 arm,          // OP_GO / OP_RESET reset the pointer
    input  wire                 rst,
    input  wire                 coherent,     // frozen record present (HALT-gated pops)
    input  wire [15:0]          rec_len,      // frozen captured length (pre+post)
    input  wire                 burst_rd_done,// one pop per nOE-rise on `SEL_BURST

    output wire [`ADDR_W-1:0]   burst_addr,       // record M9K read address
    output wire [15:0]          rdata_burst_remain// {READY, REMAIN}
);

    reg [`ADDR_W-1:0] burst_ptr = {`ADDR_W{1'b0}};   // 0 .. rec_len

    wire [`ADDR_W-1:0] rec_len_a  = rec_len[`ADDR_W-1:0];
    wire               at_end     = (burst_ptr >= rec_len_a) || (rec_len == 16'd0);
    // address clamps at the last valid word (guarded when rec_len==0).
    assign burst_addr = at_end ? ((rec_len == 16'd0) ? {`ADDR_W{1'b0}} : (rec_len_a - 1'b1))
                               : burst_ptr;
    // words remaining (saturates to 0 once the pointer reaches the end).
    wire [15:0] remain = at_end ? 16'd0 : (rec_len - {1'b0, burst_ptr});

    assign rdata_burst_remain = coherent ? {1'b1, remain[14:0]} : 16'h0000;

    always @(posedge clk) begin
        if (rst || arm)
            burst_ptr <= {`ADDR_W{1'b0}};
        else if (coherent && burst_rd_done && !at_end)
            burst_ptr <= burst_ptr + 1'b1;       // one pop / nOE-rise, saturating
    end

endmodule

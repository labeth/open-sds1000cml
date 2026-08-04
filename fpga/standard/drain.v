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
    // stream mode: live circular drain chasing the capture write pointer.
    input  wire                 stream_on,
    input  wire [`ADDR_W-1:0]   wr_ptr,       // capture's live write pointer (stream mode)

    output wire [`ADDR_W-1:0]   burst_addr,       // record M9K read address
    output wire [15:0]          rdata_burst_remain// {READY, REMAIN}
);

    reg [`ADDR_W-1:0] burst_ptr = {`ADDR_W{1'b0}};   // 0 .. rec_len (or ring in stream mode)

    wire [`ADDR_W-1:0] rec_len_a  = rec_len[`ADDR_W-1:0];
    wire               at_end     = (burst_ptr >= rec_len_a) || (rec_len == 16'd0);

    // stream mode: words available = (wr_ptr - burst_ptr) mod REC_DEPTH; ptr wraps at REC_LAST.
    localparam [`ADDR_W-1:0] REC_LAST = `REC_DEPTH - 1;
    wire [`ADDR_W-1:0] avail_s = (wr_ptr >= burst_ptr) ? (wr_ptr - burst_ptr)
                                                       : (`REC_DEPTH - burst_ptr + wr_ptr);
    wire               have_s  = (avail_s != {`ADDR_W{1'b0}});

    // address: stream = live ptr (unclamped, wraps); triggered = clamp at last valid word.
    assign burst_addr = stream_on ? burst_ptr
                      : at_end ? ((rec_len == 16'd0) ? {`ADDR_W{1'b0}} : (rec_len_a - 1'b1))
                               : burst_ptr;
    wire [15:0] remain = stream_on ? {1'b0, avail_s[14:0]}
                       : (at_end ? 16'd0 : (rec_len - {1'b0, burst_ptr}));
    // READY: always open in stream mode; else gated on a frozen coherent record.
    assign rdata_burst_remain = (stream_on || coherent) ? {1'b1, remain[14:0]} : 16'h0000;

    always @(posedge clk) begin
        if (rst || arm)
            burst_ptr <= {`ADDR_W{1'b0}};
        else if (stream_on) begin
            // Advance UNCONDITIONALLY on every pop (like the proven-clean frozen path). The
            // software drainer reads exactly `avail` words (BURST_REMAIN 0x44) per poll, so it
            // never pops past the writer. The old `have_s` gate put a wide combinational
            // subtract (wr_ptr-burst_ptr) in the pop-enable at the SAME edge as burst_rd_done;
            // tight back-to-back EDMA nOE pulses mis-sampled it -> dup+skip. Removing it makes
            // the EDMA stream drain byte-clean; have_s survives only in the REMAIN report below.
            if (burst_rd_done)
                burst_ptr <= (burst_ptr == REC_LAST) ? {`ADDR_W{1'b0}} : (burst_ptr + 1'b1);
        end else if (coherent && burst_rd_done && !at_end)
            burst_ptr <= burst_ptr + 1'b1;       // one pop / nOE-rise, saturating
    end

endmodule

// byte_fifo.v — lossless logic-register FIFO of decoded bytes.
//
// Entry = { flags[7:0], idx[23:0], byte[7:0] }  (40 bits).
// Depth is a parameter (default 32). Implemented in LOGIC REGISTERS ONLY —
// the acquisition FPGA (EP4CE10F17C8) has 46/46 M9K blocks consumed, so the
// mem array carries (* ramstyle = "logic" *) to forbid RAM inference. A
// 32x40 array = 1280 FFs, trivially fits in fabric.
//
// Lossless / sticky overflow: a push while full does NOT corrupt existing
// entries and does NOT silently drop them; instead it is refused (the new
// byte is the one lost — the OLD data is preserved) and the sticky `overflow`
// flag is raised. This is the fail-visible policy the decoder contract wants:
// the host sees overflow!=0 and knows the stream outran the drain. Cleared by
// clr_overflow. A simultaneous push+pop while full is safe (one slot frees as
// one fills — no overflow, no loss).
//
// Read side: head_* are COMBINATIONAL taps of the entry at the read pointer,
// valid whenever !empty. A one-cycle `pop` strobe retires the head. This maps
// directly onto a GPMC auto-inc spare-selector drain: the parent asserts `pop`
// on the nOE-rising of the LAST packed read word (see read packing in
// interface_notes). fill_count is the exact occupancy 0..DEPTH.

module byte_fifo #(
    parameter integer DEPTH = 32,
    parameter integer AW    = 5    // ceil(log2(DEPTH)); must satisfy (1<<AW) >= DEPTH
) (
    input  wire        clk,
    input  wire        rst,          // synchronous, active-high: clears ptrs+overflow

    // ---- write (decoder strobe) ----
    input  wire        push,         // 1-cycle: enqueue {in_flags,in_idx,in_byte}
    input  wire [7:0]  in_byte,
    input  wire [23:0] in_idx,
    input  wire [7:0]  in_flags,

    // ---- read (GPMC auto-inc drain) ----
    input  wire        pop,          // 1-cycle: retire current head (ignored if empty)
    output wire [7:0]  head_byte,
    output wire [23:0] head_idx,
    output wire [7:0]  head_flags,

    // ---- status ----
    output wire [AW:0] fill_count,   // exact occupancy, 0..DEPTH
    output wire        empty,
    output wire        full,
    output reg         overflow,     // sticky: set on push-while-full (no pop)
    input  wire        clr_overflow  // 1-cycle: clear sticky overflow
);

    // Entry storage — forced to logic (no M9K). Split arrays keep the async
    // read mux small and make intent obvious to the fitter.
    (* ramstyle = "logic" *) reg [7:0]  mem_byte  [0:DEPTH-1];
    (* ramstyle = "logic" *) reg [23:0] mem_idx   [0:DEPTH-1];
    (* ramstyle = "logic" *) reg [7:0]  mem_flags [0:DEPTH-1];

    reg [AW-1:0] wptr;               // next write slot
    reg [AW-1:0] rptr;               // current head slot
    reg [AW:0]   count;              // occupancy 0..DEPTH (needs AW+1 bits)

    assign empty      = (count == 0);
    assign full       = (count == DEPTH[AW:0]);
    assign fill_count = count;

    // Combinational head taps (valid when !empty).
    assign head_byte  = mem_byte [rptr];
    assign head_idx   = mem_idx  [rptr];
    assign head_flags = mem_flags[rptr];

    wire pop_ok  = pop  && !empty;
    // Room exists if not full, OR a pop this same cycle frees a slot.
    wire push_ok = push && (!full || pop_ok);
    wire ovf_set = push && full && !pop_ok;

    always @(posedge clk) begin
        if (rst) begin
            wptr     <= {AW{1'b0}};
            rptr     <= {AW{1'b0}};
            count    <= {(AW+1){1'b0}};
            overflow <= 1'b0;
        end else begin
            if (push_ok) begin
                mem_byte [wptr] <= in_byte;
                mem_idx  [wptr] <= in_idx;
                mem_flags[wptr] <= in_flags;
                wptr <= (wptr == DEPTH[AW-1:0]-1'b1) ? {AW{1'b0}} : wptr + 1'b1;
            end
            if (pop_ok) begin
                rptr <= (rptr == DEPTH[AW-1:0]-1'b1) ? {AW{1'b0}} : rptr + 1'b1;
            end

            // net occupancy change: +push_ok -pop_ok
            case ({push_ok, pop_ok})
                2'b10:   count <= count + 1'b1;
                2'b01:   count <= count - 1'b1;
                default: count <= count;   // 2'b00 or 2'b11 (simultaneous)
            endcase

            // sticky overflow
            if (clr_overflow) overflow <= 1'b0;
            else if (ovf_set) overflow <= 1'b1;
        end
    end

endmodule

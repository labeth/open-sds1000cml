// capture.v — circular pre/post-trigger writer + the record M9K + trigger accept
//             + the interpolating timestamp + the static-freeze guarantee.
//
// Geometry comes from the generated regs.vh (`REC_DEPTH / `ADDR_W / `PRETRIG_MAX):
// ONE source of truth, so the RTL and the app can never disagree. No module
// parameter shadows the schema.
//
// EXACT PRE/POST WINDOW (fixes the proving-ground over-capture MEDIUM)
//   The write is single-cycle and atomic: on a committed sample the word lands at
//   mem[waddr] and the pointer advances in the SAME edge (no registered-wren tail,
//   no +1 offset). `post_count` is incremented on that same commit, and the frame
//   finalizes when post_count == posttrig_work EXACTLY (equality, not >=), with the
//   would-be extra write suppressed on the finalizing edge. Total post-trigger
//   writes therefore equal the programmed post depth with zero slop.
//
//   INVARIANT (sim gate — "capture"): the drained physical array holds exactly
//   pretrig_work pre-trigger samples, then the trigger sample at index pretrig_work,
//   then posttrig_work-1 post-trigger samples; pre+post <= REC_DEPTH-2. The arm-time
//   joint clamp (done in acq.v) guarantees pre+post <= REC_DEPTH-MARGIN (MARGIN=2),
//   so a full window can never wrap and clobber a still-needed pre-trigger cell.
//     Sim assertion: capture a known ramp; the trigger sample sits at drained index
//     == pretrig_work AND the oldest pre-trigger sample (index 0) is intact.
//
// STATIC FREEZE (DESIGN.md sec.7)
//   After finalize, state = ST_HALT: filling stops, nothing writes `mem`, and the
//   single registered read port is CPU-paced by the drain. The record is a genuine
//   immutable freeze, so a DMA drain and a concurrent LCD blit see a coherent record.
//
// M9K INFERENCE (load-bearing — do NOT break)
//   `mem` is the canonical single-write / registered-read simple-dual-port template
//   Quartus maps to block RAM: one write port, one registered read port, NO initial
//   contents, NO read-enable gate. Read and write are in one clocked block. IF a
//   future tap must re-read the frozen M9K it MUST honor the 2-cycle registered read
//   latency (registered address AND registered data) — a FETCH->SETTLE->ACC pipeline
//   or a prefetch — and prime raddr at sweep start. (This is the exact latency the
//   proving-ground reducer ignored; the envelope here avoids it by folding live.)
//
// Clean-room: design/spec-derived. Synthesizable Verilog-2001, EP4CE10.

`include "regs.vh"

module capture (
    input  wire                 clk,

    // control pulses (mutually exclusive; decoded from OPCODE in acq.v, run-gated).
    input  wire                 arm,           // OP_GO  : clean single-cycle re-arm
    input  wire                 halt,          // OP_HALT: manual freeze
    input  wire                 rst,           // OP_RESET: -> idle, clear

    // stream mode: continuous ring — never finalize/halt, waddr wraps forever, drain
    // reads live behind wr_ptr. Additive: stream_on=0 => the triggered path is unchanged.
    input  wire                 stream_on,

    // arm-time working depths (already field- and joint-clamped in acq.v).
    input  wire [15:0]          pre_work_w,
    input  wire [15:0]          post_work_w,

    // canonical stream from the spine.
    input  wire [15:0]          cap_word,      // hi=CH1, lo=CH2
    input  wire                 cap_tick,      // spine `valid` (decimated)

    // trigger sense.
    input  wire                 mode_norm,     // 0 = AUTO, 1 = NORM
    input  wire                 trig_rise,     // synchronized comparator rising edge
    input  wire [7:0]           trig_level,    // level in CH1 sample units (bench-tunable)

    // record read port (address driven by the drain; data consumed by acq.v).
    input  wire [`ADDR_W-1:0]   rd_addr,
    output reg  [15:0]          rd_data,

    // stream / status exports.
    output wire                 filling,       // FILL state (feeds spine decimator gate)
    output wire                 smp_valid,     // committed-sample strobe (envelope fold enable)
    output reg                  r_valid,       // STATUS_A VALID
    output reg                  r_trig,        // STATUS_A TRIG
    output reg                  r_done,        // STATUS_A DONE
    output reg                  coherent,      // frozen coherent record present (drain open)
    output wire [10:0]          fill_out,      // FILL COUNT (freezes at halt)
    output wire [`ADDR_W-1:0]   trig_idx,      // TRIGPOS.IDX (physical index of trigger sample)
    output reg  [15:0]          trig_frac,     // TRIGPOS.FRAC (Q16 sub-sample interpolation)
    output wire [15:0]          rec_len,       // frozen captured length (for BURST remain)
    output reg                  frame_done,    // 1-cycle pulse at finalize (envelope flush)
    output wire [`ADDR_W-1:0]   wr_ptr         // stream mode: live circular write pointer
);

    // ---- sized geometry constants (never part-select an int macro) -----------
    localparam [`ADDR_W-1:0] REC_LAST    = `REC_DEPTH - 1;   // 20479
    localparam [15:0]        REC_DEPTH16 = `REC_DEPTH;        // 20480

    // ---- FSM ------------------------------------------------------------------
    localparam [1:0] ST_IDLE = 2'd0, ST_FILL = 2'd1, ST_HALT = 2'd2;
    reg [1:0] state = ST_IDLE;

    reg [`ADDR_W-1:0] waddr        = {`ADDR_W{1'b0}};
    reg [15:0]        wrote_count  = 16'd0;   // committed samples this frame (saturates)
    reg [15:0]        post_count   = 16'd0;   // committed post-trigger samples (trigger incl.)
    reg [15:0]        pretrig_work = 16'd0;   // latched @ arm
    reg [15:0]        posttrig_work= 16'd0;   // latched @ arm
    reg               triggered    = 1'b0;
    reg               comp_pending = 1'b0;    // sticky comparator edge (NORM), cleared on accept
    reg               fill_frozen  = 1'b0;
    reg [10:0]        fill_frozen_val = 11'd0;
    reg [`ADDR_W-1:0] trig_idx_r   = {`ADDR_W{1'b0}};
    reg [15:0]        rec_len_r    = 16'd0;
    reg [7:0]         prev_trig_ch = 8'd0;    // last committed CH1 sample (interp s[k-1])

    assign filling  = (state == ST_FILL);
    assign fill_out = fill_frozen ? fill_frozen_val : wrote_count[10:0];
    assign trig_idx = trig_idx_r;
    assign rec_len  = rec_len_r;

    // ---- trigger accept -------------------------------------------------------
    //   pretrig_ok uses the PRE-increment wrote_count, so the earliest accept writes
    //   physical index == pretrig_work (proof of the drained-index invariant).
    wire pretrig_ok = (wrote_count >= pretrig_work);
    // NORM bound (fix): if the comparator edge never arrives, force the trigger before
    // the circular writer could wrap past a still-needed pre-trigger cell. At
    // wrote_count == REC_DEPTH-posttrig_work the post-fill lands exactly at REC_DEPTH,
    // so the linear drain (0..rec_len-1) stays chronologically coherent and trig_idx is
    // accurate -- a late NORM edge can never corrupt the record. (The live envelope's
    // column split is exact for the AUTO/prompt window; for a late-NORM longer record it
    // reduces the leading window -- the raw BURST record is always the coherent ground
    // truth, and the CPU does final content-centring.)
    wire [15:0] norm_bound = REC_DEPTH16 - posttrig_work;
    wire        norm_full  = mode_norm && (wrote_count >= norm_bound);
    wire trig_cond  = (!mode_norm) ? 1'b1 : (trig_rise | comp_pending | norm_full);
    wire trig_fire  = filling && cap_tick && !triggered && pretrig_ok && trig_cond;

    // ---- exact post-count finalize -------------------------------------------
    //   post_full is registered (post_count) -> becomes true the edge AFTER the last
    //   post write. On that edge the would-be extra write is suppressed and we halt.
    wire post_full  = triggered && (post_count == posttrig_work);
    // stream mode commits every decimated tick (no post-count suppression, never finalizes).
    wire wr_commit  = filling && cap_tick && (stream_on || !post_full);
    assign smp_valid = wr_commit;                          // envelope folds exactly these
    assign wr_ptr    = waddr;                               // live write pointer for the ring drain

    // ---- record M9K: single write / registered read, one clocked block --------
    (* ramstyle = "M9K" *) reg [15:0] mem [0:`REC_DEPTH-1];
    always @(posedge clk) begin
        if (wr_commit) mem[waddr] <= cap_word;   // single write port (capture only)
        rd_data <= mem[rd_addr];                 // single registered read port (drain)
    end

    // ---- interpolation divider (first-order Q16 fraction) --------------------
    //   frac = (lvl - s[k-1]) / (s[k] - s[k-1]) computed by a 16-step long division
    //   kicked at trig_fire; the whole post-trigger fill provides ample cycles. The
    //   CH1 sample is the trigger channel; `trig_level` is its level in sample units
    //   (a bench-tunable placeholder for the DAC-code -> sample-code calibration).
    wire [7:0] s_k       = cap_word[15:8];                       // s[k]   (this sample)
    wire [7:0] s_km1     = prev_trig_ch;                         // s[k-1] (previous)
    wire       rise_dir  = (s_k >= s_km1);
    wire [7:0] den_mag   = rise_dir ? (s_k - s_km1) : (s_km1 - s_k);
    wire       lvl_up    = (trig_level >= s_km1);
    wire [7:0] num_mag   = lvl_up ? (trig_level - s_km1) : (s_km1 - trig_level);
    wire       same_dir  = (rise_dir == lvl_up);                 // level on the crossing side

    reg        div_busy = 1'b0;
    reg [4:0]  div_cnt  = 5'd0;
    reg [8:0]  div_rem  = 9'd0;
    reg [15:0] div_q    = 16'd0;
    reg [7:0]  div_den  = 8'd0;
    wire [8:0] rem_sh   = {div_rem[7:0], 1'b0};                  // remainder << 1
    wire       q_bit    = (rem_sh >= {1'b0, div_den});
    wire [8:0] rem_nx   = q_bit ? (rem_sh - {1'b0, div_den}) : rem_sh;

    // ======================================================================
    // MAIN FSM — capture datapath first, then opcode overrides LAST so an
    // arm/halt/reset wins over the fill datapath on the same clock edge.
    // ======================================================================
    always @(posedge clk) begin
        frame_done <= 1'b0;   // 1-cycle pulse default

        // ---- long-division step (interpolation) --------------------------
        if (div_busy) begin
            div_rem <= rem_nx;
            div_q   <= {div_q[14:0], q_bit};
            if (div_cnt == 5'd1) begin
                div_busy  <= 1'b0;
                trig_frac <= {div_q[14:0], q_bit};   // final 16 fractional bits
            end
            div_cnt <= div_cnt - 1'b1;
        end

        // ================================================================
        // FILL datapath (C2): commit the sample, wrap the pointer, count
        // fill + post, accept the trigger, finalize on exact post depth.
        // ================================================================
        if (filling) begin
            if (wr_commit) begin
                waddr        <= (waddr == REC_LAST) ? {`ADDR_W{1'b0}} : (waddr + 1'b1);
                prev_trig_ch <= s_k;
                if (wrote_count != REC_DEPTH16) wrote_count <= wrote_count + 1'b1;
                // post-trigger count: trigger sample is post write #1 (set at accept).
                if (triggered && !trig_fire) post_count <= post_count + 1'b1;
            end

            if (trig_rise && !triggered) comp_pending <= 1'b1;   // sticky NORM edge

            if (trig_fire) begin
                triggered  <= 1'b1;
                r_trig     <= 1'b1;
                comp_pending <= 1'b0;
                trig_idx_r <= waddr;          // SAME waddr that writes the trigger sample
                post_count <= 16'd1;          // trigger sample counts as post write #1
                // kick / resolve the interpolation fraction.
                if (den_mag == 8'd0 || !same_dir) trig_frac <= 16'h0000;
                else if (num_mag >= den_mag)      trig_frac <= 16'hFFFF;
                else begin
                    div_busy <= 1'b1; div_cnt <= 5'd16;
                    div_rem  <= {1'b0, num_mag}; div_q <= 16'd0; div_den <= den_mag;
                end
            end

            // exact finalize -> HALT + static freeze; envelope is already live.
            // stream mode NEVER finalizes: stays in ST_FILL, waddr wraps continuously.
            if (post_full && !stream_on) begin
                state           <= ST_HALT;
                fill_frozen     <= 1'b1;
                fill_frozen_val <= wrote_count[10:0];
                rec_len_r       <= wrote_count;   // frozen captured length
                coherent        <= 1'b1;
                r_valid         <= 1'b1;
                r_done          <= 1'b1;
                frame_done      <= 1'b1;
            end
        end

        // ================================================================
        // OPCODE overrides (decoded LAST for last-assignment priority).
        // ================================================================
        if (rst) begin
            state <= ST_IDLE;
            waddr <= {`ADDR_W{1'b0}};
            wrote_count <= 16'd0; post_count <= 16'd0;
            triggered <= 1'b0; comp_pending <= 1'b0;
            coherent <= 1'b0; r_valid <= 1'b0; r_trig <= 1'b0; r_done <= 1'b0;
            fill_frozen <= 1'b0; trig_frac <= 16'h0000; trig_idx_r <= {`ADDR_W{1'b0}};
            prev_trig_ch <= 8'd0; div_busy <= 1'b0; rec_len_r <= 16'd0;
        end else if (arm) begin
            state <= ST_FILL;
            waddr <= {`ADDR_W{1'b0}};
            wrote_count <= 16'd0; post_count <= 16'd0;
            triggered <= 1'b0; comp_pending <= 1'b0;
            coherent <= 1'b0; r_valid <= 1'b0; r_trig <= 1'b0; r_done <= 1'b0;
            fill_frozen <= 1'b0; trig_frac <= 16'h0000; trig_idx_r <= {`ADDR_W{1'b0}};
            prev_trig_ch <= 8'd0; div_busy <= 1'b0; rec_len_r <= 16'd0;
            pretrig_work  <= pre_work_w;    // latch the clamped working depths
            posttrig_work <= post_work_w;
        end else if (halt) begin
            if (triggered) begin
                // finalize a triggered frame (partial post window) -> freeze + flush.
                state           <= ST_HALT;
                fill_frozen     <= 1'b1;
                fill_frozen_val <= wrote_count[10:0];
                rec_len_r       <= wrote_count;
                coherent        <= 1'b1;
                r_valid         <= 1'b1;
                r_done          <= 1'b1;
                frame_done      <= 1'b1;
            end else begin
                // no trigger to anchor -> abandon; never expose a partial as coherent.
                state    <= ST_IDLE;
                coherent <= 1'b0;
            end
        end
    end

endmodule

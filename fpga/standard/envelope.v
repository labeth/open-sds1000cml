// envelope.v — LIVE-STREAM min/max reducer + the envelope result FIFO.
//
// LIVE STREAM (fixes the proving-ground read-latency BLOCKER)
//   The proving-ground reducer swept the FROZEN record M9K post-halt with a one-cycle
//   FETCH->ACC FSM, but that M9K has a registered read ADDRESS *and* registered read
//   DATA (2-cycle latency), so it folded mem[k-1]: it corrupted column 0 and NEVER
//   folded the last sample. This module eliminates the class entirely: it computes
//   per-column per-channel min/max ON THE CANONICAL STREAM as each sample is committed
//   during FILL (`smp_valid` from capture, which is exactly capture's wr_commit). There
//   is NO post-halt re-read of the record M9K at all — the envelope is ready the instant
//   the record freezes, so a slow/envelope band drains only ENV_DATA (O(columns)).
//
//   SIM GATE ("envelope"): column 0 folds sample 0; the last sample is folded; the
//   column count is exact.
//     * column 0 folds sample 0 — col_open starts 0 at arm, so the first sample seeds
//       (not stale-folds) column 0's running min/max.
//     * last sample folded — a divider-free Bresenham accumulator keyed on the write
//       count closes exactly `cols_work` columns over N = pre+post samples, with the
//       last close landing on the last sample. A manual early HALT flushes the open
//       partial column (frame_done) so the last written sample is still folded.
//     * column count exact — sum(cols_work) over N samples is a multiple of N, so
//       exactly cols_work columns close (== programmed ENV_COLS, clamped to [1,N]).
//
// OVERFLOW is first-class (C3, set-and-skip)
//   A closed column latches into a 6-word emit and is drained into the FIFO. If a new
//   column closes while the emit is still busy, or the FIFO lacks room for a full
//   record, the OVERFLOW bit sets and that column is skipped — never a silent drop.
//   For display column counts (~256-1000) columns are ~N/cols apart (tens of clocks),
//   so overflow cannot occur in normal use; it only guards a pathological ENV_COLS.
//
// M9K INFERENCE — `envmem` is the same single-write / registered-read template; the
//   reducer writes packed 40-bit records as 3 words each (CH1 then CH2), the CPU reads
//   them back via the ENV_DATA auto-inc port. After a frame it too is a static freeze.
//
// Clean-room: design/spec-derived. Synthesizable Verilog-2001, EP4CE10.

module envelope (
    input  wire        clk,

    // control.
    input  wire        arm,          // OP_GO : reset fold + FIFO, latch N and cols_work
    input  wire        rst,          // OP_RESET : clear all
    input  wire        env_reset,    // ENV_RESET strobe : clear the FIFO only
    input  wire [15:0] pre_work_w,   // clamped working depths (from acq.v)
    input  wire [15:0] post_work_w,
    input  wire [15:0] env_cols,     // requested display column count

    // live stream (folded exactly on committed samples).
    input  wire [15:0] cap_word,     // hi=CH1, lo=CH2
    input  wire        smp_valid,    // == capture wr_commit
    input  wire        frame_done,   // finalize -> flush the open partial column

    // read side (auto-inc ENV_DATA + level-status ENV_COUNT).
    input  wire        coherent,     // gate ENV_DATA (read-after-halt)
    input  wire        env_rd_active,
    input  wire        env_rd_done,
    output wire [15:0] rdata_env_data,
    output wire [15:0] rdata_env_count
);

    // ---- envelope FIFO geometry (2048 x 16 = ~4 M9K) -------------------------
    localparam integer ENV_MEM_DEPTH = 2048;
    localparam integer ENV_ADDR_W    = 11;
    localparam [ENV_ADDR_W-1:0] ENV_LAST = ENV_MEM_DEPTH - 1;      // 2047
    localparam [ENV_ADDR_W:0]   ENV_ROOM = ENV_MEM_DEPTH - 6;      // room for a full column

    // ---- fold state ----------------------------------------------------------
    reg  [7:0]  min1 = 8'hFF, max1 = 8'h00, min2 = 8'hFF, max2 = 8'h00;
    reg         col_open = 1'b0;                 // >=1 sample folded since last close
    reg  [16:0] acc      = 17'd0;                // Bresenham accumulator
    reg  [15:0] Nreg     = 16'd1;                // total samples N (latched @ arm)
    reg  [15:0] cols_work= 16'd1;                // clamped column count (latched @ arm)
    reg  [15:0] col_idx  = 16'd0;                // current column index

    wire [7:0]  c1 = cap_word[15:8];
    wire [7:0]  c2 = cap_word[7:0];
    wire [7:0]  n1min = col_open ? ((c1 < min1) ? c1 : min1) : c1;
    wire [7:0]  n1max = col_open ? ((c1 > max1) ? c1 : max1) : c1;
    wire [7:0]  n2min = col_open ? ((c2 < min2) ? c2 : min2) : c2;
    wire [7:0]  n2max = col_open ? ((c2 > max2) ? c2 : max2) : c2;
    wire [16:0] acc_nx = acc + {1'b0, cols_work};
    wire        close  = (acc_nx >= {1'b0, Nreg});     // this sample closes a column

    // ---- emit request (a closed column, or a partial column flushed at halt) --
    wire        close_req = smp_valid && close;
    wire        flush_req = frame_done && col_open && !close_req;
    wire        want_emit = close_req | flush_req;
    wire [7:0]  e1min = close_req ? n1min : min1;
    wire [7:0]  e1max = close_req ? n1max : max1;
    wire [7:0]  e2min = close_req ? n2min : min2;
    wire [7:0]  e2max = close_req ? n2max : max2;

    // ---- emit FSM (6 words: CH1 record then CH2 record) ----------------------
    reg         emit_busy = 1'b0;
    reg  [2:0]  emit_wcnt = 3'd0;
    reg  [7:0]  h_min1, h_max1, h_min2, h_max2;
    reg  [15:0] h_col;
    reg  [ENV_ADDR_W:0]   env_wptr  = {(ENV_ADDR_W+1){1'b0}};   // 0..2048
    reg  [ENV_ADDR_W-1:0] env_rptr  = {ENV_ADDR_W{1'b0}};
    reg  [14:0] env_rec_count = 15'd0;
    reg         env_overflow  = 1'b0;
    wire        env_room = (env_wptr <= ENV_ROOM);

    // ---- envelope FIFO M9K: single write / registered read -------------------
    (* ramstyle = "M9K" *) reg [15:0] envmem [0:ENV_MEM_DEPTH-1];
    reg  [15:0] env_rd_data = 16'h0000;
    reg  [ENV_ADDR_W-1:0] env_raddr = {ENV_ADDR_W{1'b0}};

    reg  [15:0] env_wdata;
    always @* begin
        case (emit_wcnt)
            3'd0:    env_wdata = h_col;                          // record[15:0]  = col
            3'd1:    env_wdata = {h_max1, h_min1};               // record[31:16] = {max,min} CH1
            3'd2:    env_wdata = {8'd0, env_overflow, 3'd0, 4'd0};// record[47:32] = {0,ovf,rsvd,ch=0}
            3'd3:    env_wdata = h_col;
            3'd4:    env_wdata = {h_max2, h_min2};               // CH2 {max,min}
            default: env_wdata = {8'd0, env_overflow, 3'd0, 4'd1};// ch=1
        endcase
    end

    always @(posedge clk) begin
        if (emit_busy) envmem[env_wptr[ENV_ADDR_W-1:0]] <= env_wdata;  // single write port
        env_rd_data <= envmem[env_raddr];                             // single registered read
    end

    // ======================================================================
    // fold + emit control
    // ======================================================================
    always @(posedge clk) begin

        // ---- fold the live sample / advance the column ----
        if (smp_valid) begin
            if (close) begin
                col_idx  <= col_idx + 1'b1;
                col_open <= 1'b0;
                min1 <= 8'hFF; max1 <= 8'h00; min2 <= 8'hFF; max2 <= 8'h00;
                acc  <= acc_nx - {1'b0, Nreg};
            end else begin
                min1 <= n1min; max1 <= n1max; min2 <= n2min; max2 <= n2max;
                col_open <= 1'b1;
                acc  <= acc_nx;
            end
        end else if (flush_req) begin
            col_idx  <= col_idx + 1'b1;
            col_open <= 1'b0;
            min1 <= 8'hFF; max1 <= 8'h00; min2 <= 8'hFF; max2 <= 8'h00;
        end

        // ---- start an emit for a closed/flushed column ----
        if (want_emit) begin
            if (!emit_busy && env_room) begin
                h_min1 <= e1min; h_max1 <= e1max;
                h_min2 <= e2min; h_max2 <= e2max;
                h_col  <= col_idx;
                emit_busy <= 1'b1; emit_wcnt <= 3'd0;
            end else begin
                env_overflow <= 1'b1;             // C3: set-and-skip, never a silent drop
            end
        end

        // ---- drain the 6-word emit into the FIFO (aligned to env_wptr) ----
        if (emit_busy) begin
            env_wptr <= env_wptr + 1'b1;
            if (emit_wcnt == 3'd2 || emit_wcnt == 3'd5)
                env_rec_count <= env_rec_count + 1'b1;   // one full record stored
            if (emit_wcnt == 3'd5)
                emit_busy <= 1'b0;
            emit_wcnt <= emit_wcnt + 1'b1;
        end

        // ---- CPU read pointer (auto-inc, read-after-halt) ----
        if (env_rd_active) env_raddr <= env_rptr;
        if (env_rd_done && coherent && (env_rptr != ENV_LAST))
            env_rptr <= env_rptr + 1'b1;

        // ---- control overrides ----
        if (rst || arm) begin
            col_open <= 1'b0; acc <= 17'd0; col_idx <= 16'd0;
            min1 <= 8'hFF; max1 <= 8'h00; min2 <= 8'hFF; max2 <= 8'h00;
            env_wptr <= {(ENV_ADDR_W+1){1'b0}}; env_rptr <= {ENV_ADDR_W{1'b0}};
            env_rec_count <= 15'd0; env_overflow <= 1'b0;
            emit_busy <= 1'b0; emit_wcnt <= 3'd0;
            if (arm) begin
                // N = pre+post (>=1); columns clamped to [1, N].
                Nreg      <= (pre_work_w + post_work_w == 16'd0) ? 16'd1 : (pre_work_w + post_work_w);
                cols_work <= (env_cols == 16'd0)                 ? 16'd1 :
                             (env_cols >  (pre_work_w + post_work_w)) ? (pre_work_w + post_work_w) :
                             env_cols;
            end
        end else if (env_reset) begin
            // clear the FIFO only (fold state untouched).
            env_wptr <= {(ENV_ADDR_W+1){1'b0}}; env_rptr <= {ENV_ADDR_W{1'b0}};
            env_rec_count <= 15'd0; env_overflow <= 1'b0;
            emit_busy <= 1'b0; emit_wcnt <= 3'd0;
        end
    end

    assign rdata_env_data  = coherent ? env_rd_data : 16'h0000;
    assign rdata_env_count = {env_overflow, env_rec_count};

endmodule

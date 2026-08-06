// il_capture.v — dual-clock PRE-TRIGGER RING interleave capture buffer
// =========================================================================
// Part of the Cyclone IV E (EP4CE10F17C8) time-interleave acquisition fabric.
//
// PURPOSE
//   Capture the phase-interleaved ADC core bytes into a dual-clock M9K RING at
//   the fast 200 MHz capture clock (cap_clk). The ring writes CONTINUOUSLY, so
//   pre-trigger history is always present; on the data crossing (trig rising)
//   it captures POST more entries and FREEZES, then streams the window out at
//   the 80 MHz drain clock (clk) CENTERED on the crossing (PRE = N-POST before,
//   POST after) into the existing byte-exact drain path.
//
//   Two channel formats, latched at arm:
//     chan_sel==0 : C1, 3 cores  -> entry {c1a,c1b,c1c,8'b0}; readout 2 words/entry
//     chan_sel==1 : C2, 2 cores  -> entry {c2a,c2b,16'b0};    readout 1 word/entry
//
// TRIGGER
//   trig_en=1 : after arm, keep writing the ring while ARMED; on a trig rising
//               edge (or a ~84 ms timeout) latch trig_addr, write POST more
//               entries, freeze, and read out from (trig_addr-PRE). This lands
//               the window ON the crossing with real pre-history.
//   trig_en=0 : fire immediately on arm (POST entries after the arm point).
//
// CDC (dual-clock, no lockstep; 200:80 non-integer)
//   arm crosses clk->cap_clk via a TOGGLE synchronizer; fill-done (freeze)
//   crosses cap_clk->clk via a toggle + edge detect. Readout runs only after
//   FREEZE, so the RAM ports never touch overlapping live addresses. trig_addr
//   is stable once frozen, so the clk readout reads it directly.
//
// ADDITIVE / GATED: il_en==0 => never arms, ring idle, outputs quiet.
// =========================================================================

module il_capture #(
    parameter integer N    = 256,   // ring depth in entries
    parameter integer AW   = 8,     // = clog2(N)
    parameter integer POST = 192    // post-trigger entries; PRE = N-POST = 64
)(
    input  wire        clk,        // 80 MHz  drain / readout domain
    input  wire        cap_clk,    // 200 MHz fast fill domain
    input  wire        arm,        // 1-cycle pulse, clk domain
    input  wire        il_en,      // interleave enable (gate)
    input  wire        chan_sel,   // 0 = C1 (3-wide), 1 = C2 (2-wide)
    input  wire        trig,       // data crossing (core MSB); async, synced in cap_clk
    input  wire        trig_en,    // 1 = wait for a trig rising edge (pre-trigger), 0 = immediate
    input  wire [7:0]  c1a, c1b, c1c,
    input  wire [7:0]  c2a, c2b,
    output reg  [15:0] il_word,
    output reg         il_tick,
    output wire        il_busy
);
    localparam [AW-1:0] PRE = N - POST;   // pre-trigger entries

    (* ramstyle = "M9K" *) reg [31:0] mem [0:N-1];
    reg  [31:0] rdata;

    // ---- clk-domain control / readout ----
    reg         active, arm_tgl, chan_sel_l;
    reg         fd_meta, fd_s2, fd_s3;
    localparam [2:0] S_IDLE=3'd0, S_REQ=3'd1, S_WAIT=3'd2, S_W1=3'd3, S_NEXT=3'd4;
    reg  [2:0]      rstate;
    reg  [AW-1:0]   raddr, rcnt;

    // ---- cap_clk-domain ring/fill ----
    reg         arm_meta, arm_s2, arm_s3;
    reg         trig_meta, trig_s2, trig_s3;
    reg  [AW-1:0] wa;
    reg         armed, posting, held;
    reg  [AW-1:0] postcnt, trig_addr;
    reg  [23:0] tocnt;
    reg         fdone_tgl;
    // ---- RETIMED fill tap (200 MHz timing closure) --------------------------
    // The ring write data (the c1a/c1b/c1c phased captures) reaches this M9K
    // from PLL phases that leave only a fraction of a 5 ns period of setup to the
    // cap_clk200 write edge (a real sub-cycle Group-B phase transfer). Registering
    // the write DATA and ADDRESS TOGETHER one cap cycle moves the tight endpoint
    // from the M9K write port (large synchronous setup) to a fabric FF (small
    // setup, placed adjacent), closing the path. Data and address are delayed by
    // the SAME one cycle, so entry[A] still holds wdata sampled when wa==A -> the
    // frozen window is BIT-IDENTICAL; only the physical write lands 1 cap cycle
    // later, hidden by the freeze->drain CDC latency. wr_en_q flushes the final
    // entry on the ring->held transition.
    reg  [31:0]   wdata_q;
    reg  [AW-1:0] wa_q;
    reg           wr_en_q;

    initial begin
        active=1'b0; arm_tgl=1'b0; chan_sel_l=1'b0;
        fd_meta=1'b0; fd_s2=1'b0; fd_s3=1'b0;
        rstate=S_IDLE; raddr={AW{1'b0}}; rcnt={AW{1'b0}};
        il_word=16'd0; il_tick=1'b0; rdata=32'd0;
        arm_meta=1'b0; arm_s2=1'b0; arm_s3=1'b0;
        trig_meta=1'b0; trig_s2=1'b0; trig_s3=1'b0;
        wa={AW{1'b0}}; armed=1'b0; posting=1'b0; held=1'b0;
        postcnt={AW{1'b0}}; trig_addr={AW{1'b0}}; tocnt=24'd0; fdone_tgl=1'b0;
        wdata_q=32'd0; wa_q={AW{1'b0}}; wr_en_q=1'b0;
    end

    assign il_busy = active;
    wire [31:0] wdata = chan_sel_l ? {c2a, c2b, 16'b0} : {c1a, c1b, c1c, 8'b0};
    wire trig_rise_cap = trig_s2 & ~trig_s3;

    // ================= cap_clk domain: continuous ring + pre-trigger freeze =================
    always @(posedge cap_clk) begin
        arm_meta  <= arm_tgl;  arm_s2  <= arm_meta;  arm_s3  <= arm_s2;
        trig_meta <= trig;     trig_s2 <= trig_meta; trig_s3 <= trig_s2;
        // ---- retimed fill tap: register {data,addr,we} then write M9K ----
        // Data + address delayed together => entry[A] == wdata@(wa==A), identical
        // ring content; wr_en_q = (previous cycle was writing) flushes the last
        // pre-history entry on the ring->held transition.
        wdata_q <= wdata;
        wa_q    <= wa;
        wr_en_q <= ~held;
        if (wr_en_q) mem[wa_q] <= wdata_q;    // continuous ring write (pipelined 1 cap cycle)
        if (held) begin
            if (arm_s2 ^ arm_s3) begin held <= 1'b0; armed <= 1'b1; tocnt <= 24'd0; end // re-arm resumes ring
        end else begin
            wa      <= wa + 1'b1;
            if (!armed && !posting) begin
                if (arm_s2 ^ arm_s3) begin armed <= 1'b1; tocnt <= 24'd0; end
            end else if (armed) begin
                tocnt <= tocnt + 24'd1;
                if ((trig_en ? trig_rise_cap : 1'b1) || (&tocnt)) begin
                    armed <= 1'b0; posting <= 1'b1; postcnt <= POST[AW-1:0]; trig_addr <= wa;
                end
            end else if (posting) begin
                if (postcnt == {AW{1'b0}}) begin posting <= 1'b0; held <= 1'b1; fdone_tgl <= ~fdone_tgl; end
                else postcnt <= postcnt - 1'b1;
            end
        end
    end

    // RAM read port (clk)
    always @(posedge clk) rdata <= mem[raddr];

    // ================= clk domain: arm-accept + readout (centered on trig_addr-PRE) =================
    always @(posedge clk) begin
        if (arm && il_en && !active) begin
            active     <= 1'b1;
            arm_tgl    <= ~arm_tgl;
            chan_sel_l <= chan_sel;
        end
        fd_meta <= fdone_tgl; fd_s2 <= fd_meta; fd_s3 <= fd_s2;
        il_tick <= 1'b0;
        case (rstate)
            S_IDLE: if (active && (fd_s2 ^ fd_s3)) begin
                        raddr  <= trig_addr - PRE;   // start PRE entries before the crossing
                        rcnt   <= {AW{1'b0}};
                        rstate <= S_REQ;
                    end
            S_REQ:  rstate <= S_WAIT;
            S_WAIT: begin il_word <= rdata[31:16]; il_tick <= 1'b1;
                          rstate  <= chan_sel_l ? S_NEXT : S_W1; end
            S_W1:   begin il_word <= {rdata[15:8], 8'h00}; il_tick <= 1'b1; rstate <= S_NEXT; end
            S_NEXT: if (rcnt == (N-1)) begin rstate <= S_IDLE; active <= 1'b0; end
                    else begin raddr <= raddr + 1'b1; rcnt <= rcnt + 1'b1; rstate <= S_REQ; end
            default: rstate <= S_IDLE;
        endcase
    end
endmodule

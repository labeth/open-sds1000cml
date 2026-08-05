// sr_accum.v — full-rate in-fabric ETS super-res COMBINE engine
// =========================================================================
// Part of the owned Cyclone IV E (EP4CE10F17C8) acquisition bitstream.
// Branch owned-fpga, TOP = acq.v. ADDITIVE / GATED by combine_en.
//
// WHAT IT DOES
//   In the fast capture clock domain (cap_clk, one phased 200/160 MHz tap),
//   for each fast sample it computes a trigger-referenced BIN INDEX (position
//   in one small record/period) and INTEGER-accumulates per bin per channel:
//     align channel : {cnt, sum, sum2, cntA, sumA}   (72-bit cell)
//     other channel : {cnt, sum}                     (24-bit cell)
//   The odd/even half-stack (cntA/sumA = the "odd pass" half) lets the host
//   Stack.Result() recover the measured sigma / BitsGained UNCHANGED. This is
//   algorithm-EQUIVALENT ETS super-res: the float reference-lock brain stays
//   host-side; the fabric does the integer trigger-referenced fill.
//
//   A freeze-then-snapshot CDC (mirrors il_capture's proven dual-clock pattern)
//   exposes the frozen grid to the 80 MHz drain clock. The drain FSM streams
//   Nbins * 12 little-endian 16-bit words (LSW first) matching the CPU consumer
//   BinGrid wire format:
//     w0 = {8'h0, align.cnt}         w6  = {8'h0, align.cntA}
//     w1 = align.sum[15:0]           w7  = align.sumA[15:0]
//     w2 = align.sum[31:16] (=0)     w8  = align.sumA[31:16] (=0)
//     w3 = align.sum2[15:0]          w9  = {8'h0, other.cnt}
//     w4 = align.sum2[31:16]         w10 = other.sum[15:0]
//     w5 = align.sum2[47:32] (=0)    w11 = other.sum[31:16] (=0)
//
// OVERFLOW GUARD
//   cnt saturates at DMAX (default 255): when a bin reaches DMAX it FREEZES
//   (no further add). This BOUNDS every width so the max-depth totals fit:
//     cnt<=255 (8b)  sum<=255*255=65025 (16b)  sum2<=255*65025<2^24 (24b)
//     cntA<=128 (8b) sumA<=128*255=32640 (16b)
//
// RMW HAZARD-FREEDOM
//   pos is monotonic mod NBINS; a bin is revisited only after NBINS ticks, far
//   longer than the 1-deep RMW pipeline, so a plain read-modify-write has no
//   read-after-write hazard (no forwarding, no stall). Requires NBINS >= 4.
//
// BYTE-IDENTICAL INVARIANT
//   combine_en==0 => this module never arms, never drives bin_word/bin_tick,
//   busy/coherent stay low, memories are untouched. The acq.v drain mux selects
//   the raw record, so the whole datapath is byte-for-byte identical to today.
// =========================================================================
`default_nettype none

module sr_accum #(
    parameter integer NBINS = 256,  // fine bins over one period (= GridL*K)
    parameter integer AW    = 8,    // = clog2(NBINS)
    parameter integer DMAX  = 255,  // cnt saturation depth (overflow guard)
    // WITH_OTHER=1 -> also accumulate the OTHER channel {cnt,sum} (Mean2). Costs a
    // 2nd (256x24 -> 1 M9K) block. The SHIPPING 2-M9K combine build sets this 0
    // (align-only, 256x72 = 2 M9K EXACT); bmem is then never written and the
    // fitter prunes it, and other.cnt/other.sum drain as 0 (host -> Mean2 empty).
    parameter integer WITH_OTHER = 1,
    // FULLSTATS=1 -> 72-bit align cell {sumA,cntA,sum2,sum,cnt} = 2 M9K (256x36 x2):
    //   full byte-exact Result incl. BitsGained. TB-proven; the SRAM-era config.
    // FULLSTATS=0 -> 24-bit align cell {sum,cnt} = 256x24 = 1 M9K: MEAN-ONLY. sum2/sumA/
    //   cntA are computed but NOT stored (new_a[CW-1:0] slices them off) so drain words
    //   3..8 read 0 and the host sets ASum2/ASumA/ACntA=nil -> Mean trace, BitsGained=0.
    //   THIS is the SHIPPING config on the 45/46-full owned device: it fits the single
    //   free M9K (45 + 1 = 46/46). The cnt/sum path (drain words 0..2) is byte-identical
    //   to FULLSTATS=1, so the TB's cnt/sum checks already validate the mean-only drain.
    parameter integer FULLSTATS = 1
)(
    // ---- fast accumulate domain ----
    input  wire        cap_clk,     // 200/160 MHz fast fill clock
    input  wire        combine_en,  // GATE: 0 => module fully idle (byte-identical)
    input  wire        smp_valid,   // 1 = a fast sample is present this tick
    input  wire [7:0]  smp_a,       // align-channel 8-bit ADC code
    input  wire [7:0]  smp_b,       // other-channel 8-bit ADC code
    input  wire        trig,        // data trigger (async; core MSB rising), synced here
    input  wire        trig_en,     // 1 = trigger-referenced pass anchor; 0 = free-run

    // ---- drain / control domain (80 MHz) ----
    input  wire        clk,         // 80 MHz drain / readout clock
    input  wire        arm,         // clk-domain 1-cycle pulse: clear + start accumulate
    input  wire        halt,        // clk-domain 1-cycle pulse: freeze the grid
    input  wire        drain_req,   // clk-domain 1-cycle pulse: stream the frozen grid
    output reg  [15:0] bin_word,    // drained bin word (like il_word)
    output reg         bin_tick,    // 1-cycle strobe with each bin_word
    output wire        busy,        // 1 = clearing or accumulating (not yet frozen)
    output wire        coherent     // 1 = grid frozen and ready to drain
);
    localparam integer WPB = 12;    // words per bin (FULL-BITS layout)
    // align cell width: 72b (full stats, 2 M9K) or 24b (mean-only, 1 M9K). The low 24
    // bits are ALWAYS {sum[23:8], cnt[7:0]} so new_a[CW-1:0] yields the right subset.
    localparam integer CW = (FULLSTATS != 0) ? 72 : 24;

    // ================= memories =================
    // align cell (72b): {sumA[71:56], cntA[55:48], sum2[47:24], sum[23:8], cnt[7:0]}
    // (mean-only CW=24: {sum[23:8], cnt[7:0]} — sum2/sumA/cntA not stored).
    (* ramstyle = "M9K" *) reg [CW-1:0] amem [0:NBINS-1];
    // other cell (24b): {bsum[23:8], bcnt[7:0]}
    (* ramstyle = "M9K" *) reg [23:0] bmem [0:NBINS-1];

    // ================= clk-domain control / CDC =================
    reg        active;              // clk: armed (accumulate window open) until frozen
    reg        arm_tgl, halt_tgl;   // toggles into cap_clk
    reg        fd_meta, fd_s2, fd_s3;
    reg        coh_r;
    reg [2:0]  dstate;
    reg [AW-1:0] draddr;
    reg [3:0]  widx;
    reg [71:0] ra_q;
    reg [23:0] rb_q;
    // ---- single-M9K drain: amem is READ ONLY in the cap_clk domain (RMW read + a
    // frozen-grid read, address-muxed into ONE read port). The clk drain FSM gets each
    // frozen bin cell via a req/ack handshake (dr_*). This keeps amem a single-clock
    // simple-dual-port (1 write + 1 read) => 1 M9K; a clk-domain amem read would force
    // Quartus to DUPLICATE the block (2 M9K) and overflow the 46-M9K device.
    reg [71:0] rd_hold_a;    // cap-side: frozen align cell latched for the clk drain
    reg [23:0] rd_hold_b;    // cap-side: frozen other cell
    reg        dr_req_tgl;   // clk->cap read request (toggle)
    reg [AW-1:0] dr_addr;    // clk->cap read address (held stable while a req is pending)
    reg        dr_ack_m, dr_ack_s2, dr_ack_s3;   // cap->clk ack sync (clk side)
    reg        dr_req_m, dr_req_s2, dr_req_s3;   // clk->cap req sync (cap side)
    reg [AW-1:0] draddr_cs;  // cap-side registered read address
    reg        dr_ack_tgl;   // cap->clk ack (toggle)
    reg [1:0]  drd_cnt;      // cap-side read settle counter

    localparam [2:0] D_IDLE=3'd0, D_REQ=3'd1, D_EMIT=3'd2, D_DONE=3'd3, D_WAIT=3'd5;

    // ================= cap_clk-domain accumulate =================
    reg        arm_meta, arm_s2, arm_s3;
    reg        halt_meta, halt_s2, halt_s3;
    reg        trig_meta, trig_s2, trig_s3;
    reg [1:0]  fstate;
    reg [AW-1:0] csweep;            // clear-sweep address
    reg [AW-1:0] cur_pos;           // current bin position
    reg [31:0] pass_idx;            // pass counter (odd/even parity source)
    reg        fdone_tgl;
    // 1-deep RMW pipeline
    reg        p1_val;
    reg [AW-1:0] p1_pos;
    reg [7:0]  p1_va, p1_vb;
    reg        p1_odd;
    reg [71:0] q_a;
    reg [23:0] q_b;

    localparam [1:0] FI_IDLE=2'd0, FI_CLEAR=2'd1, FI_ACCUM=2'd2, FI_FROZEN=2'd3;

    integer i;
    initial begin
        active=1'b0; arm_tgl=1'b0; halt_tgl=1'b0;
        fd_meta=1'b0; fd_s2=1'b0; fd_s3=1'b0; coh_r=1'b0;
        dstate=D_IDLE; draddr={AW{1'b0}}; widx=4'd0; ra_q=72'd0; rb_q=24'd0;
        rd_hold_a=72'd0; rd_hold_b=24'd0; dr_req_tgl=1'b0; dr_addr={AW{1'b0}};
        dr_ack_m=1'b0; dr_ack_s2=1'b0; dr_ack_s3=1'b0;
        dr_req_m=1'b0; dr_req_s2=1'b0; dr_req_s3=1'b0;
        draddr_cs={AW{1'b0}}; dr_ack_tgl=1'b0; drd_cnt=2'd0;
        bin_word=16'd0; bin_tick=1'b0;
        arm_meta=1'b0; arm_s2=1'b0; arm_s3=1'b0;
        halt_meta=1'b0; halt_s2=1'b0; halt_s3=1'b0;
        trig_meta=1'b0; trig_s2=1'b0; trig_s3=1'b0;
        fstate=FI_IDLE; csweep={AW{1'b0}}; cur_pos={AW{1'b0}}; pass_idx=32'd0;
        fdone_tgl=1'b0;
        p1_val=1'b0; p1_pos={AW{1'b0}}; p1_va=8'd0; p1_vb=8'd0; p1_odd=1'b0;
        q_a=72'd0; q_b=24'd0;
        for (i=0;i<NBINS;i=i+1) begin amem[i]={CW{1'b0}}; bmem[i]=24'd0; end
    end

    assign busy     = (fstate == FI_CLEAR) || (fstate == FI_ACCUM);
    assign coherent = coh_r;

    // ---- unpack old align cell ----
    wire [7:0]  old_cnt  = q_a[7:0];
    wire [15:0] old_sum  = q_a[23:8];
    wire [23:0] old_sum2 = q_a[47:24];
    wire [7:0]  old_cntA = q_a[55:48];
    wire [15:0] old_sumA = q_a[71:56];
    wire        frozen_a = (old_cnt == DMAX[7:0]);
    wire [15:0] vv       = p1_va * p1_va;      // 8x8 -> 16b (<=65025), free DSP
    wire [7:0]  new_cnt  = frozen_a ? old_cnt  : old_cnt  + 8'd1;
    wire [15:0] new_sum  = frozen_a ? old_sum  : old_sum  + {8'd0, p1_va};
    wire [23:0] new_sum2 = frozen_a ? old_sum2 : old_sum2 + {8'd0, vv};
    wire        addA     = ~frozen_a & p1_odd;
    wire [7:0]  new_cntA = addA ? old_cntA + 8'd1 : old_cntA;
    wire [15:0] new_sumA = addA ? old_sumA + {8'd0, p1_va} : old_sumA;
    wire [71:0] new_a    = {new_sumA, new_cntA, new_sum2, new_sum, new_cnt};

    // ---- unpack old other cell ----
    wire [7:0]  old_bcnt = q_b[7:0];
    wire [15:0] old_bsum = q_b[23:8];
    wire        frozen_b = (old_bcnt == DMAX[7:0]);
    wire [7:0]  new_bcnt = frozen_b ? old_bcnt : old_bcnt + 8'd1;
    wire [15:0] new_bsum = frozen_b ? old_bsum : old_bsum + {8'd0, p1_vb};
    wire [23:0] new_b    = {new_bsum, new_bcnt};

    wire        do_accum  = (fstate == FI_ACCUM) & smp_valid & combine_en;
    wire        trig_rise = trig_s2 & ~trig_s3;
    wire        cur_odd   = pass_idx[0];
    wire        wrap      = do_accum & (cur_pos == (NBINS-1));
    // SINGLE amem read port (cap_clk): RMW reads cur_pos while filling; the drain reads
    // the frozen grid at draddr_cs. One read statement => one M9K read port (no dup).
    wire [AW-1:0] rd_addr_b = (fstate == FI_FROZEN) ? draddr_cs : cur_pos;

    always @(posedge cap_clk) begin
        // 3-FF synchronizers
        arm_meta  <= arm_tgl;   arm_s2  <= arm_meta;  arm_s3  <= arm_s2;
        halt_meta <= halt_tgl;  halt_s2 <= halt_meta; halt_s3 <= halt_s2;
        trig_meta <= trig;      trig_s2 <= trig_meta; trig_s3 <= trig_s2;

        // ---- frozen-grid read handshake (clk drain -> cap read -> clk) ----
        dr_req_m <= dr_req_tgl; dr_req_s2 <= dr_req_m; dr_req_s3 <= dr_req_s2;
        if ((dr_req_s2 ^ dr_req_s3) && (fstate == FI_FROZEN)) begin
            draddr_cs <= dr_addr;      // sample the (stable) clk read address
            drd_cnt   <= 2'd2;         // wait 2 cap cycles for q_a to reflect draddr_cs
        end else if (drd_cnt != 2'd0) begin
            drd_cnt <= drd_cnt - 2'd1;
            if (drd_cnt == 2'd1) begin
                rd_hold_a  <= q_a;     // q_a now = amem[draddr_cs]
                rd_hold_b  <= q_b;
                dr_ack_tgl <= ~dr_ack_tgl;
            end
        end

        // registered read + RMW pipeline load (only meaningful in ACCUM)
        q_a    <= amem[rd_addr_b];
        q_b    <= (WITH_OTHER != 0) ? bmem[rd_addr_b] : 24'd0;
        p1_val <= do_accum;
        p1_pos <= cur_pos;
        p1_va  <= smp_a;
        p1_vb  <= smp_b;
        p1_odd <= cur_odd;

        case (fstate)
            FI_IDLE: begin
                if (combine_en && (arm_s2 ^ arm_s3)) begin
                    fstate <= FI_CLEAR; csweep <= {AW{1'b0}};
                end
            end
            FI_CLEAR: begin
                amem[csweep] <= {CW{1'b0}};
                if (WITH_OTHER != 0) bmem[csweep] <= 24'd0;
                if (csweep == (NBINS-1)) begin
                    fstate   <= FI_ACCUM;
                    cur_pos  <= {AW{1'b0}};
                    pass_idx <= 32'd0;
                end else csweep <= csweep + 1'b1;
            end
            FI_ACCUM: begin
                // commit the pipelined RMW (1-cycle latency; hazard-free)
                if (p1_val) begin
                    amem[p1_pos] <= new_a[CW-1:0];
                    if (WITH_OTHER != 0) bmem[p1_pos] <= new_b;
                end
                // advance position / pass parity for NEXT cycle
                if (trig_en & trig_rise) begin
                    cur_pos  <= {AW{1'b0}};
                    pass_idx <= pass_idx + 32'd1;   // new pass -> flip odd
                end else if (do_accum) begin
                    cur_pos <= wrap ? {AW{1'b0}} : (cur_pos + 1'b1);
                    if (~trig_en & wrap) pass_idx <= pass_idx + 32'd1;
                end
                // freeze on halt (commit the in-flight p1 above, then stop)
                if (halt_s2 ^ halt_s3) begin
                    fstate    <= FI_FROZEN;
                    fdone_tgl <= ~fdone_tgl;
                    p1_val    <= 1'b0;
                end
            end
            FI_FROZEN: begin
                // flush any last p1 (from the halt cycle) then hold frozen
                if (p1_val) begin
                    amem[p1_pos] <= new_a[CW-1:0];
                    if (WITH_OTHER != 0) bmem[p1_pos] <= new_b;
                    p1_val <= 1'b0;
                end
                if (combine_en && (arm_s2 ^ arm_s3)) begin
                    fstate <= FI_CLEAR; csweep <= {AW{1'b0}};
                end
            end
            default: fstate <= FI_IDLE;
        endcase

        if (!combine_en) begin
            fstate <= FI_IDLE;
            p1_val <= 1'b0;
            drd_cnt <= 2'd0;
        end
    end

    // ================= clk-domain: arm/halt CDC + drain readout =================
    // word mux for the current bin cell (ra_q / rb_q)
    reg [15:0] wmux;
    always @(*) begin
        case (widx)
            4'd0:  wmux = {8'h00, ra_q[7:0]};      // align.cnt
            4'd1:  wmux = ra_q[23:8];              // align.sum[15:0]
            4'd2:  wmux = 16'h0000;                // align.sum[31:16]
            4'd3:  wmux = ra_q[39:24];             // align.sum2[15:0]
            4'd4:  wmux = {8'h00, ra_q[47:40]};    // align.sum2[31:16]
            4'd5:  wmux = 16'h0000;                // align.sum2[47:32]
            4'd6:  wmux = {8'h00, ra_q[55:48]};    // align.cntA
            4'd7:  wmux = ra_q[71:56];             // align.sumA[15:0]
            4'd8:  wmux = 16'h0000;                // align.sumA[31:16]
            4'd9:  wmux = {8'h00, rb_q[7:0]};      // other.cnt
            4'd10: wmux = rb_q[23:8];              // other.sum[15:0]
            default: wmux = 16'h0000;              // other.sum[31:16]
        endcase
    end

    always @(posedge clk) begin
        fd_meta <= fdone_tgl; fd_s2 <= fd_meta; fd_s3 <= fd_s2;
        dr_ack_m <= dr_ack_tgl; dr_ack_s2 <= dr_ack_m; dr_ack_s3 <= dr_ack_s2;
        bin_tick <= 1'b0;

        // arm accept: clear coherent, pulse arm into cap_clk, open window
        if (arm && combine_en) begin
            arm_tgl <= ~arm_tgl;
            active  <= 1'b1;
            coh_r   <= 1'b0;
        end
        // halt accept: pulse halt into cap_clk
        if (halt && combine_en && active) begin
            halt_tgl <= ~halt_tgl;
        end
        // frozen edge -> grid coherent for drain
        if (active && (fd_s2 ^ fd_s3)) begin
            coh_r  <= 1'b1;
            active <= 1'b0;
        end

        case (dstate)
            D_IDLE: if (drain_req && coh_r) begin
                        draddr <= {AW{1'b0}}; widx <= 4'd0; dstate <= D_REQ;
                    end
            // D_REQ/D_WAIT: fetch bin[draddr] from the cap_clk read port via handshake
            // (no clk-domain amem read -> single-M9K). Toggle the request, wait the ack,
            // then latch the frozen cell that the cap side parked in rd_hold_a/b.
            D_REQ:  begin dr_addr <= draddr; dr_req_tgl <= ~dr_req_tgl; dstate <= D_WAIT; end
            D_WAIT: if (dr_ack_s2 ^ dr_ack_s3) begin
                        ra_q <= rd_hold_a; rb_q <= rd_hold_b;
                        widx <= 4'd0; dstate <= D_EMIT;
                    end
            D_EMIT: begin
                        bin_word <= wmux; bin_tick <= 1'b1;
                        if (widx == (WPB-1)) begin
                            if (draddr == (NBINS-1)) dstate <= D_DONE;
                            else begin draddr <= draddr + 1'b1; dstate <= D_REQ; end
                        end else widx <= widx + 1'b1;
                    end
            D_DONE: dstate <= D_IDLE;
            default: dstate <= D_IDLE;
        endcase

        if (!combine_en) begin
            active <= 1'b0; coh_r <= 1'b0; dstate <= D_IDLE; bin_tick <= 1'b0;
        end
    end
endmodule
`default_nettype wire

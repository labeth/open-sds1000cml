// eth_gearbox.v — 200->80 rate-matching gearbox (interleave -> wide codes-word).
//
// PURPOSE (bounded plumbing, NO new RE): the 600 MSa/s interleave delivers 3
// samples every 200 MHz tick (c1a_p,c1b_p,c1c_p at PLL phases 0/120/240 — the
// per-core skew is BENCH-cal, see acq.v). The 100BASE-TX slicer+CDR
// (eth_slicer_cdr.v) consumes an 8-lane wide codes-word at the 80 MHz fabric
// clock (ball C2). 600/80 = 7.5 samples/clk on average -> an 8-lane word with an
// occasional whole-word gap (0-lane) so the long-run average matches 7.5.
//
// This is a CLOCK-DOMAIN CROSSING: the 200 MHz interleave clock (from mclk_in via
// u_pll200) and the 80 MHz fabric clk (ball C2) are SEPARATE physical oscillators
// -> genuinely ASYNCHRONOUS. Metastable multi-bit pointer tears are therefore a
// real hazard; this module is built so that EVERY cross-domain pointer is a
// UNIT-INCREMENT gray code (the only tear-safe multi-bit CDC).
//
// -------------------------------------------------------------------------
// STRUCTURE — LANES(=8) parallel single-sample async FIFO banks (no rotation)
// -------------------------------------------------------------------------
// Sample index i is routed to bank (i mod LANES). Because we write WR_SAMP(=3)
// CONSECUTIVE samples per tick and 3 <= LANES, the 3 target banks are always
// DISTINCT -> each bank sees AT MOST ONE write per 200 MHz tick -> the per-bank
// WRITE pointer is UNIT-INCREMENT (gray-safe). The READ side pops exactly ONE
// sample from EVERY bank per 80 MHz clock, and ONLY when all LANES banks are
// non-empty; so the emitted word is samples {8K .. 8K+7} — CONTIGUOUS and
// LANES-ALIGNED, lane j = bank j (NO barrel rotation), and the per-bank READ
// pointer is also UNIT-INCREMENT (gray-safe). Every CDC pointer is thus a
// unit-step gray code: NO multi-bit tear is possible on either side.
//
//   * lane 0 of the output word is the EARLIEST sample (codes[SAMPLE_W-1:0]),
//     matching eth_slicer_cdr's "lane 0 = earliest" convention.
//   * nvalid = LANES on a full word, 0 on a gap (in_valid=0). A FLUSH tail can
//     emit a final partial word (nvalid = #leftover < LANES) to close the stream.
//
// RATE / NO LOSS: per bank fill = 600/LANES = 75 MSa/s, drain <= 80 MSa/s (one
// pop per 80 MHz clock) -> drain > fill, occupancy stays near-empty, overflow is
// unreachable in steady state (matches the 640 > 600 headroom). `overflow` is a
// STICKY guard asserted only if a write ever hits a full bank (startup/jitter
// pathology); no sample is silently dropped without raising it.
//
// TRANSPARENCY: the gearbox delivers the golden 600 MSa/s sample sequence to the
// CDR in-order with IDENTICAL 8-sample word boundaries as the direct-fed sim
// (gaps inject no samples and the CDR holds its carried phase across a gap), so
// the recovered NRZI bits are BIT-EXACT vs the direct feed. Proven in
// sim/tb_eth_gearbox.v (arp 550 bits, icmp 630 bits, exact).
//
// -------------------------------------------------------------------------
// INPUT INTERFACE (documented; see interface_notes)
// -------------------------------------------------------------------------
//   WRITE (interleave / 200 MHz) domain:
//     wr_clk, wr_rst (sync, active-high)
//     wr_valid : WR_SAMP fresh samples are presented THIS wr_clk
//     wr_samp  : {s2,s1,s0} packed, s0=[SAMPLE_W-1:0] is the EARLIEST sample
//                (map c1a_p->s0, c1b_p->s1, c1c_p->s2 at the integration site)
//   READ (fabric / 80 MHz) domain:
//     rd_clk, rd_rst (sync, active-high)
//     rd_ready : downstream can accept (tie 1 for eth_slicer_cdr — always ready)
//     flush    : rd-domain; emit a final partial word to drain the tail
//   CONTROL:
//     en       : rd-domain engine gate (from SEL_DEC_CFG). en=0 holds the whole
//                module INERT (both domains cleared, outputs 0, overflow 0),
//                exactly like the sibling eth_* engines (dec_proto==3 select).
//   OUTPUT (rd-domain, registered):
//     codes[LANES*SAMPLE_W-1:0], nvalid[3:0], in_valid, overflow(sticky).
//
// RESOURCES: LANES tiny dual-clock RAM banks (DEPTH=2^DEPTHW each) + unit gray
// pointers + 2-FF synchronizers. 0 PLL, 0 pins, 0 multipliers. Feeds
// eth_slicer_cdr unchanged (same codes/nvalid/in_valid/flush port shape).

module eth_gearbox #(
    parameter integer SAMPLE_W = 12,  // signed sample-code width (golden +/-1000)
    parameter integer LANES    = 8,   // output lanes == #banks (power of two)
    parameter integer WR_SAMP  = 3,   // samples written per wr_clk (interleave: 3)
    parameter integer DEPTHW   = 2    // per-bank FIFO depth = 2^DEPTHW (4; drain>fill
                                      // keeps occupancy ~1-2, so 4 is ample headroom)
) (
    // ---- write (interleave / 200 MHz) domain ----
    input  wire                        wr_clk,
    input  wire                        wr_rst,
    input  wire                        wr_valid,
    input  wire [WR_SAMP*SAMPLE_W-1:0] wr_samp,   // s0=[SAMPLE_W-1:0] earliest

    // ---- read (fabric / 80 MHz) domain ----
    input  wire                        rd_clk,
    input  wire                        rd_rst,
    input  wire                        rd_ready,  // downstream ready (CDR: tie 1)
    input  wire                        flush,     // emit final partial word

    // ---- engine gate (rd-domain) ----
    input  wire                        en,        // en=0 -> module inert

    // ---- wide sample word out (rd-domain, registered) ----
    output reg  [LANES*SAMPLE_W-1:0]   codes,     // lane 0 = earliest
    output reg  [3:0]                  nvalid,    // #valid lanes (0..LANES)
    output reg                         in_valid,  // nvalid != 0
    output reg                         overflow   // sticky: a write hit a full bank
);
    localparam integer AW    = DEPTHW;        // per-bank addr width
    localparam integer DEPTH = (1 << DEPTHW);
    // bank-index width (avoid $clog2 for -g2001 portability)
    localparam integer LW = (LANES <= 2)  ? 1 :
                            (LANES <= 4)  ? 2 :
                            (LANES <= 8)  ? 3 :
                            (LANES <= 16) ? 4 : 5;

    genvar  g;
    integer p;

    // ===================================================================
    // WRITE DOMAIN (200 MHz): round-robin route WR_SAMP samples across banks
    // ===================================================================
    reg  [LW-1:0]        wbank;                 // bank cursor (bank of s0 this tick)
    reg  [LANES-1:0]     bwe;                   // per-bank write request (pre-full)
    reg  [SAMPLE_W-1:0]  bwd [0:LANES-1];       // per-bank write data
    wire [LANES-1:0]     bfull;                 // per-bank full (wr domain)
    reg  [LW:0]          tb;                    // wbank+p before mod

    // enable synced into the write domain (en is an rd-domain control)
    reg en_wr1, en_wr2;
    always @(posedge wr_clk) begin
        en_wr1 <= en; en_wr2 <= en_wr1;
    end
    wire en_wr = en_wr2;

    // ---- RETIMED input register (200 MHz timing margin) ---------------------
    // Register the incoming samples ONE wr_clk before the round-robin routing.
    // This splits the upstream integration-site sub+shift (c1*_e -> +/-scale, in
    // acq.v) from the in-gearbox route-mux: the sub+shift now ends at THIS FF and
    // the route-mux starts from it, so neither shares a 5 ns edge. wbank advances
    // on the SAME delayed valid, so the (wbank,sample) pairing is unchanged ->
    // identical bank contents, +1 wr_clk latency (invisible to the elastic FIFO).
    reg  [WR_SAMP*SAMPLE_W-1:0] wr_samp_i;
    reg                         wr_valid_i;
    always @(posedge wr_clk) begin
        if (wr_rst | ~en_wr) begin wr_samp_i <= {WR_SAMP*SAMPLE_W{1'b0}}; wr_valid_i <= 1'b0; end
        else                 begin wr_samp_i <= wr_samp;                  wr_valid_i <= wr_valid; end
    end
    wire wr_go = en_wr & wr_valid_i;      // routing/cursor track the REGISTERED valid

    always @(posedge wr_clk) begin
        if (wr_rst | ~en_wr) wbank <= {LW{1'b0}};
        else if (wr_go)      wbank <= (wbank + WR_SAMP) % LANES;
    end

    // combinational routing of the WR_SAMP samples to their banks
    always @* begin
        bwe = {LANES{1'b0}};
        tb  = {(LW+1){1'b0}};
        for (p = 0; p < LANES; p = p + 1) bwd[p] = {SAMPLE_W{1'b0}};
        if (wr_go) begin
            for (p = 0; p < WR_SAMP; p = p + 1) begin
                tb         = (wbank + p) % LANES;
                bwe[tb]    = 1'b1;
                bwd[tb]    = wr_samp_i[p*SAMPLE_W +: SAMPLE_W];
            end
        end
    end

    // PIPELINE the routed write one wr_clk: splits the (wbank -> sample-mux ->
    // RAM-write) cone into (mux -> reg) and (reg -> RAM), so the 200 MHz write
    // path closes. +1 wr_clk latency (2.5 ns) — invisible to the elastic FIFO.
    reg  [LANES-1:0]    bwe_q;
    reg  [SAMPLE_W-1:0] bwd_q [0:LANES-1];
    always @(posedge wr_clk) begin
        if (wr_rst | ~en_wr) begin
            bwe_q <= {LANES{1'b0}};
            for (p = 0; p < LANES; p = p + 1) bwd_q[p] <= {SAMPLE_W{1'b0}};
        end else begin
            bwe_q <= bwe;
            for (p = 0; p < LANES; p = p + 1) bwd_q[p] <= bwd[p];
        end
    end

    // sticky overflow: a routed write that hits a full bank (no silent drop).
    // Detection is done PER BANK (short local path, see generate) and only the
    // already-registered per-bank sticky bits are OR-reduced here -> keeps the
    // 200 MHz write path free of the wide full-flag OR cone.
    wire [LANES-1:0] bovf;                       // per-bank sticky (registered)
    reg              ov_wr;
    always @(posedge wr_clk) begin
        if (wr_rst | ~en_wr) ov_wr <= 1'b0;
        else                 ov_wr <= |bovf;
    end

    // ===================================================================
    // READ DOMAIN (80 MHz): pop one from every bank when all non-empty
    // ===================================================================
    wire [LANES-1:0]            bempty;          // per-bank empty (rd domain)
    wire [LANES*SAMPLE_W-1:0]   rdbus;           // per-bank head data (FWFT)
    reg  [LANES-1:0]            rden;            // per-bank pop request
    reg  [3:0]                  nv;
    reg                         do_out;
    reg                         stop;
    integer                     q;

    wire en_rd    = en;                          // en is native rd-domain
    wire allrdy   = (&(~bempty)) & rd_ready & en_rd;

    // decide the pop set + valid count for this rd_clk
    always @* begin
        rden   = {LANES{1'b0}};
        nv     = 4'd0;
        do_out = 1'b0;
        stop   = 1'b0;
        q      = 0;
        if (en_rd) begin
            if (allrdy) begin
                rden   = {LANES{1'b1}};
                nv     = LANES;
                do_out = 1'b1;
            end else if (flush) begin
                // leading contiguous non-empty banks (the tail always fills
                // banks 0..r-1 because the next block would start at bank 0)
                stop = 1'b0;
                for (q = 0; q < LANES; q = q + 1) begin
                    if (!stop && !bempty[q]) begin
                        rden[q] = 1'b1;
                        nv      = nv + 4'd1;
                    end else begin
                        stop = 1'b1;
                    end
                end
                do_out = (nv != 4'd0);
            end
        end
    end

    // registered output word
    always @(posedge rd_clk) begin
        if (rd_rst | ~en_rd) begin
            codes <= {LANES*SAMPLE_W{1'b0}};
            nvalid <= 4'd0; in_valid <= 1'b0;
        end else begin
            for (q = 0; q < LANES; q = q + 1)
                codes[q*SAMPLE_W +: SAMPLE_W] <=
                    (q < nv) ? rdbus[q*SAMPLE_W +: SAMPLE_W] : {SAMPLE_W{1'b0}};
            nvalid   <= nv;
            in_valid <= do_out;
        end
    end

    // sticky overflow synced into the rd domain for the output port
    reg ov_r1, ov_r2;
    always @(posedge rd_clk) begin
        if (rd_rst | ~en_rd) begin ov_r1 <= 1'b0; ov_r2 <= 1'b0; overflow <= 1'b0; end
        else begin ov_r1 <= ov_wr; ov_r2 <= ov_r1; overflow <= ov_r2; end
    end

    // ===================================================================
    // LANES parallel single-sample async FIFO banks (unit-increment gray)
    // ===================================================================
    generate
    for (g = 0; g < LANES; g = g + 1) begin : bank
        reg  [SAMPLE_W-1:0] mem [0:DEPTH-1];
        // write-domain pointers
        reg  [AW:0] wbin, wgray;
        reg  [AW:0] rq1, rq2;          // read gray synced into wr domain
        reg         bovf_r;            // per-bank sticky overflow (local path)
        // read-domain pointers
        reg  [AW:0] rbin, rgray;
        reg  [AW:0] wq1, wq2;          // write gray synced into rd domain

        wire [AW:0] wbin_nxt  = wbin + 1'b1;
        wire [AW:0] wgray_nxt = wbin_nxt ^ (wbin_nxt >> 1);
        wire [AW:0] rbin_nxt  = rbin + 1'b1;
        wire [AW:0] rgray_nxt = rbin_nxt ^ (rbin_nxt >> 1);

        // ---- write side (wr_clk) ----
        always @(posedge wr_clk) begin
            if (wr_rst | ~en_wr) begin
                wbin <= {(AW+1){1'b0}}; wgray <= {(AW+1){1'b0}}; bovf_r <= 1'b0;
            end else if (bwe_q[g]) begin
                if (bfull[g]) begin
                    bovf_r <= 1'b1;            // dropped write -> raise sticky
                end else begin
                    mem[wbin[AW-1:0]] <= bwd_q[g];
                    wbin  <= wbin_nxt;
                    wgray <= wgray_nxt;
                end
            end
        end
        assign bovf[g] = bovf_r;
        // read-gray -> wr domain (2FF)
        always @(posedge wr_clk) begin
            if (wr_rst | ~en_wr) begin rq1 <= 0; rq2 <= 0; end
            else begin rq1 <= rgray; rq2 <= rq1; end
        end
        // full: next-write-gray equals read-gray with top two bits inverted
        assign bfull[g] = (wgray_nxt ==
                           {~rq2[AW:AW-1], rq2[AW-2:0]});

        // ---- read side (rd_clk) ----
        always @(posedge rd_clk) begin
            if (rd_rst | ~en_rd) begin
                rbin <= {(AW+1){1'b0}}; rgray <= {(AW+1){1'b0}};
            end else if (rden[g]) begin
                rbin  <= rbin_nxt;
                rgray <= rgray_nxt;
            end
        end
        // write-gray -> rd domain (2FF)
        always @(posedge rd_clk) begin
            if (rd_rst | ~en_rd) begin wq1 <= 0; wq2 <= 0; end
            else begin wq1 <= wgray; wq2 <= wq1; end
        end
        assign bempty[g] = (rgray == wq2);

        // FWFT head data
        assign rdbus[g*SAMPLE_W +: SAMPLE_W] = mem[rbin[AW-1:0]];
    end
    endgenerate
endmodule

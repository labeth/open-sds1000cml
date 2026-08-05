// eth_4b5b2.v — 100BASE-TX 4B/5B align + decode, 2-bit/clk UNROLL (RX stage F).
//
// Line-rate sibling of eth_4b5b.v.  IDENTICAL alignment / Table-24-1 decode /
// J-K-T-R handling, but consumes a VARIABLE 0..2 recovered bits per clock (the
// eth_descramble2 / eth_slicer_cdr burst handshake) so the tail sustains
// 2 bits/clk x 80 MHz = 160 Mbit/s >= the 125 Mbit line rate.
//
// ORACLE: app/internal/eth100tx decode.go align5B + eth100tx.go data4b5b/rev4b5b.
// Nibble/code-group-exact vs vectors/{arp,icmp}.mii_nibbles / .code_groups.
//
// UNROLL MODEL — "step the 1-bit engine twice, queue the emit events":
//   The 1-bit engine (eth_4b5b.v) does ONE recovered bit per clock and emits at
//   most one code-group event.  Here a pure-combinational STEP (f4step) is the
//   1-bit engine's exact body; we apply it up to twice per clock.  Two steps can
//   produce TWO emit events in one clock — the ONLY case is the /J/K/ SSD, whose
//   J and K land one bit apart.  A small emit FIFO (EVQ_D deep) absorbs that:
//   push up to 2 events, pop exactly 1 per clock.  Because real code groups are
//   5 bits apart (>=3 clocks at 2 bits/clk) the queue is momentary (max depth 2
//   at the SSD) and drains immediately, so:
//     * output is AT MOST 1 nibble / clk  -> the framer (fed <=0.4 nibbles/clk)
//       needs NO unroll and never sees two nibbles in a clock;
//     * event ORDER is preserved -> byte-exact with the 1-bit engine and golden.
//   Descrambled-bit consumption is up to 2/clk -> the tail never underruns.
//
// SSD / ESD (unchanged): /J/K/ replaces preamble octet 0x55 -> J and K each emit
// data nibble 0x5 and are flagged control; /T/ then /R/ ESD sets eof, no data
// nibbles for I/T/R/H/Q.  Bit order MSB-first (first-received bit = code bit4).
//
// GATING: rst OR !en -> fully inert (no events, no strobes).  0 M9K, 0 PLL, 0 pins.

module eth_4b5b2 #(
    parameter integer EVQ_D = 4          // emit-FIFO depth (>=2; 4 gives margin)
) (
    input  wire        clk,
    input  wire        rst,        // synchronous, active-high: clears to inert HUNT
    input  wire        en,         // gate; when 0 the engine is fully inert

    // ---- recovered descrambled bit burst in (up to 2/clk) ----
    input  wire        in_valid,   // 1 = in_nbits bits valid this clk
    input  wire [1:0]  in_nbits,   // 0..2 valid bits
    input  wire [1:0]  in_bits,    // bit0 = earliest descrambled NRZ bit

    // ---- aligned 5-bit code-group stream (registered, <=1 group/clk) ----
    output reg         cg_stb,     // 1-clk: cg_* valid this cycle
    output reg  [4:0]  cg_code,    // raw 5-bit code group, bit4..bit0
    output reg         cg_ctrl,    // 1 = control symbol, 0 = data
    output reg  [2:0]  cg_sym,     // control-symbol id (SYM_*), valid when cg_ctrl
    output reg         cg_err,     // 1 = invalid code group (unknown 5B code)

    // ---- MII data nibble stream (data groups + J/K -> 0x5) ----
    output reg  [3:0]  nibble,
    output reg         nibble_stb, // 1-clk: nibble valid this cycle (<=1/clk)

    // ---- frame delimiters ----
    output reg         sof,        // 1-clk: /J/K/ SSD aligned (start of stream)
    output reg         eof,        // 1-clk: /T/R/ ESD seen (end of stream)
    output reg         locked,     // level: code-group alignment currently held
    output reg         ovf         // sticky: emit FIFO overflow (should never fire)
);
    // ---- control-symbol ids ------------------------------------------------
    localparam [2:0] SYM_DATA = 3'd0, SYM_I = 3'd1, SYM_J = 3'd2, SYM_K = 3'd3,
                     SYM_T    = 3'd4, SYM_R = 3'd5, SYM_H = 3'd6, SYM_Q = 3'd7;
    localparam [4:0] C_I = 5'b11111, C_J = 5'b11000, C_K = 5'b10001,
                     C_T = 5'b01101, C_R = 5'b00111, C_H = 5'b00100, C_Q = 5'b00000;
    localparam [9:0] JK_SSD = {C_J, C_K};   // 10'b1100010001

    // ---- 5B -> {ctrl,isdata,err,sym,nibble} (IEEE 802.3 Table 24-1) --------
    function [9:0] dec5b;
        input [4:0] c;
        begin
            case (c)
                C_I: dec5b = {1'b1, 1'b0, 1'b0, SYM_I, 4'h0};
                C_J: dec5b = {1'b1, 1'b1, 1'b0, SYM_J, 4'h5};
                C_K: dec5b = {1'b1, 1'b1, 1'b0, SYM_K, 4'h5};
                C_T: dec5b = {1'b1, 1'b0, 1'b0, SYM_T, 4'h0};
                C_R: dec5b = {1'b1, 1'b0, 1'b0, SYM_R, 4'h0};
                C_H: dec5b = {1'b1, 1'b0, 1'b0, SYM_H, 4'h0};
                C_Q: dec5b = {1'b1, 1'b0, 1'b0, SYM_Q, 4'h0};
                5'b11110: dec5b = {1'b0, 1'b1, 1'b0, SYM_DATA, 4'h0};
                5'b01001: dec5b = {1'b0, 1'b1, 1'b0, SYM_DATA, 4'h1};
                5'b10100: dec5b = {1'b0, 1'b1, 1'b0, SYM_DATA, 4'h2};
                5'b10101: dec5b = {1'b0, 1'b1, 1'b0, SYM_DATA, 4'h3};
                5'b01010: dec5b = {1'b0, 1'b1, 1'b0, SYM_DATA, 4'h4};
                5'b01011: dec5b = {1'b0, 1'b1, 1'b0, SYM_DATA, 4'h5};
                5'b01110: dec5b = {1'b0, 1'b1, 1'b0, SYM_DATA, 4'h6};
                5'b01111: dec5b = {1'b0, 1'b1, 1'b0, SYM_DATA, 4'h7};
                5'b10010: dec5b = {1'b0, 1'b1, 1'b0, SYM_DATA, 4'h8};
                5'b10011: dec5b = {1'b0, 1'b1, 1'b0, SYM_DATA, 4'h9};
                5'b10110: dec5b = {1'b0, 1'b1, 1'b0, SYM_DATA, 4'hA};
                5'b10111: dec5b = {1'b0, 1'b1, 1'b0, SYM_DATA, 4'hB};
                5'b11010: dec5b = {1'b0, 1'b1, 1'b0, SYM_DATA, 4'hC};
                5'b11011: dec5b = {1'b0, 1'b1, 1'b0, SYM_DATA, 4'hD};
                5'b11100: dec5b = {1'b0, 1'b1, 1'b0, SYM_DATA, 4'hE};
                5'b11101: dec5b = {1'b0, 1'b1, 1'b0, SYM_DATA, 4'hF};
                default:  dec5b = {1'b0, 1'b0, 1'b1, SYM_DATA, 4'h0}; // invalid
            endcase
        end
    endfunction

    localparam ST_HUNT = 1'b0, ST_LOCK = 1'b1;

    // ---- registered engine state (carried across clocks) ------------------
    reg        st;
    reg [9:0]  sr;        // HUNT shift register (last 10 bits, MSB=oldest)
    reg [4:0]  grp;       // LOCK 5-bit accumulator (MSB-first)
    reg [2:0]  cnt;       // bits collected into grp (0..4)
    reg        emitK;     // pending K emission the step after SSD match
    reg        armT;      // previous data-stream group decoded as /T/ (ESD1)
    reg        lk;        // alignment held

    // ---- one-bit 4B5B STEP (pure combinational; the 1-bit engine's body) ---
    // in  : current state {st,sr,grp,cnt,emitK,armT,lk} + one descrambled bit.
    // out : packed {nstate[21:0], event[17:0]}
    //   nstate [21]=st [20:11]=sr [10:6]=grp [5:3]=cnt [2]=emitK [1]=armT [0]=lk
    //   event  [17]=cg_stb [16:12]=cg_code [11]=cg_ctrl [10:8]=cg_sym [7]=cg_err
    //          [6:3]=nibble [2]=nibble_stb [1]=sof [0]=eof
    function [39:0] f4step;
        input [21:0] s;
        input        b;
        reg        i_st;   reg [9:0] i_sr; reg [4:0] i_grp; reg [2:0] i_cnt;
        reg        i_emK;  reg i_armT;     reg i_lk;
        reg        n_st;   reg [9:0] n_sr; reg [4:0] n_grp; reg [2:0] n_cnt;
        reg        n_emK;  reg n_armT;     reg n_lk;
        reg [4:0]  grp_done; reg [9:0] d; reg [9:0] nsr_hunt;
        reg        e_cgstb; reg [4:0] e_cgcode; reg e_cgctrl; reg [2:0] e_cgsym;
        reg        e_cgerr; reg [3:0] e_nib; reg e_nibstb; reg e_sof; reg e_eof;
        begin
            i_st = s[21]; i_sr = s[20:11]; i_grp = s[10:6]; i_cnt = s[5:3];
            i_emK = s[2]; i_armT = s[1]; i_lk = s[0];
            // defaults: hold state, no event
            n_st = i_st; n_sr = i_sr; n_grp = i_grp; n_cnt = i_cnt;
            n_emK = i_emK; n_armT = i_armT; n_lk = i_lk;
            e_cgstb = 1'b0; e_cgcode = 5'd0; e_cgctrl = 1'b0; e_cgsym = SYM_DATA;
            e_cgerr = 1'b0; e_nib = 4'd0; e_nibstb = 1'b0; e_sof = 1'b0; e_eof = 1'b0;
            grp_done = 5'd0; d = 10'd0; nsr_hunt = 10'd0;  // defaults (no latch)

            if (i_st == ST_HUNT) begin
                nsr_hunt = {i_sr[8:0], b};
                n_sr = nsr_hunt;
                if (nsr_hunt == JK_SSD) begin
                    n_st = ST_LOCK; n_lk = 1'b1; n_emK = 1'b1; n_armT = 1'b0;
                    n_grp = 5'd0; n_cnt = 3'd0;
                    e_cgstb = 1'b1; e_cgcode = C_J; e_cgctrl = 1'b1; e_cgsym = SYM_J;
                    e_nib = 4'h5; e_nibstb = 1'b1; e_sof = 1'b1;
                end
            end else begin // ST_LOCK
                if (i_emK) begin
                    n_emK = 1'b0;
                    e_cgstb = 1'b1; e_cgcode = C_K; e_cgctrl = 1'b1; e_cgsym = SYM_K;
                    e_nib = 4'h5; e_nibstb = 1'b1;
                    n_grp = {4'b0, b}; n_cnt = 3'd1;
                end else if (i_cnt == 3'd4) begin
                    grp_done = {i_grp[3:0], b};
                    d = dec5b(grp_done);
                    e_cgstb = 1'b1; e_cgcode = grp_done; e_cgctrl = d[9];
                    e_cgsym = d[6:4]; e_cgerr = d[7]; e_nib = d[3:0]; e_nibstb = d[8];
                    n_cnt = 3'd0; n_grp = 5'd0;
                    if (d[9] && (d[6:4] == SYM_T)) begin
                        n_armT = 1'b1;
                    end else if (d[9] && (d[6:4] == SYM_R) && i_armT) begin
                        n_armT = 1'b0; e_eof = 1'b1;
                        n_st = ST_HUNT; n_lk = 1'b0; n_sr = 10'd0;
                    end else n_armT = 1'b0;
                end else begin
                    n_grp = {i_grp[3:0], b}; n_cnt = i_cnt + 3'd1;
                end
            end
            f4step = {n_st, n_sr, n_grp, n_cnt, n_emK, n_armT, n_lk,
                      e_cgstb, e_cgcode, e_cgctrl, e_cgsym, e_cgerr,
                      e_nib, e_nibstb, e_sof, e_eof};
        end
    endfunction

    // ---- emit FIFO (event = 18 bits) --------------------------------------
    localparam integer AW = (EVQ_D <= 2)  ? 1 :
                            (EVQ_D <= 4)  ? 2 :
                            (EVQ_D <= 8)  ? 3 : 4;
    reg [17:0]   evq [0:EVQ_D-1];
    reg [AW-1:0] q_head, q_tail;   // wrap mod EVQ_D (EVQ_D is a power of two)
    reg [AW:0]   q_cnt;            // occupancy 0..EVQ_D

    // per-clock working values
    reg [39:0]   r0, r1;
    reg [21:0]   cs;
    reg          ev0_v, ev1_v;
    reg [17:0]   ev0, ev1;
    reg [AW-1:0] th, tl;
    reg [AW:0]   cq;
    reg [17:0]   fe;
    integer      ii;

    always @(posedge clk) begin
        if (rst || !en) begin
            st <= ST_HUNT; sr <= 10'd0; grp <= 5'd0; cnt <= 3'd0;
            emitK <= 1'b0; armT <= 1'b0; lk <= 1'b0;
            q_head <= {AW{1'b0}}; q_tail <= {AW{1'b0}}; q_cnt <= {(AW+1){1'b0}};
            cg_stb <= 1'b0; cg_code <= 5'd0; cg_ctrl <= 1'b0; cg_sym <= SYM_DATA;
            cg_err <= 1'b0; nibble <= 4'd0; nibble_stb <= 1'b0;
            sof <= 1'b0; eof <= 1'b0; locked <= 1'b0; ovf <= 1'b0;
            for (ii = 0; ii < EVQ_D; ii = ii + 1) evq[ii] <= 18'd0;
        end else begin
            // default output strobes low each clock
            cg_stb <= 1'b0; nibble_stb <= 1'b0; sof <= 1'b0; eof <= 1'b0;

            // ---- step the 1-bit engine up to twice; collect emit events ----
            cs = {st, sr, grp, cnt, emitK, armT, lk};
            ev0_v = 1'b0; ev1_v = 1'b0; ev0 = 18'd0; ev1 = 18'd0;
            if (en && in_valid && (in_nbits != 2'd0)) begin
                r0    = f4step(cs, in_bits[0]);
                ev0   = r0[17:0];
                ev0_v = r0[17];        // event present iff cg_stb
                if (in_nbits >= 2'd2) begin
                    r1    = f4step(r0[39:18], in_bits[1]);
                    ev1   = r1[17:0];
                    ev1_v = r1[17];
                    {st, sr, grp, cnt, emitK, armT, lk} <= r1[39:18];
                end else begin
                    {st, sr, grp, cnt, emitK, armT, lk} <= r0[39:18];
                end
            end
            // level `locked` follows engine alignment (mux of the applied step)
            locked <= (en && in_valid && (in_nbits >= 2'd2)) ? r1[18] :
                      (en && in_valid && (in_nbits == 2'd1)) ? r0[18] : lk;

            // ---- emit FIFO: pop 1 (register outputs), push up to 2 ----------
            th = q_head; tl = q_tail; cq = q_cnt;
            if (cq != {(AW+1){1'b0}}) begin  // FIFO non-empty -> pop front
                fe = evq[th];
                cg_stb     <= fe[17];
                cg_code    <= fe[16:12];
                cg_ctrl    <= fe[11];
                cg_sym     <= fe[10:8];
                cg_err     <= fe[7];
                nibble     <= fe[6:3];
                nibble_stb <= fe[2];
                sof        <= fe[1];
                eof        <= fe[0];
                th = th + 1'b1; cq = cq - 1'b1;
            end
            if (ev0_v) begin evq[tl] <= ev0; tl = tl + 1'b1; cq = cq + 1'b1; end
            if (ev1_v) begin evq[tl] <= ev1; tl = tl + 1'b1; cq = cq + 1'b1; end
            q_head <= th; q_tail <= tl; q_cnt <= cq;
            if (cq > EVQ_D[AW:0]) ovf <= 1'b1;   // never fires on valid streams
        end
    end

endmodule

// eth_descramble2.v — 100BASE-TX self-synchronising descrambler, 2-bit/clk UNROLL.
//
// Line-rate sibling of eth_descramble.v.  IDENTICAL FSM / recurrence / idle-lock,
// but consumes a VARIABLE 0..2 recovered bits per clock (the eth_slicer_cdr burst
// handshake) and advances the x^11+x^9+1 LFSR by up to 2 per clock so the tail
// sustains 2 bits/clk x 80 MHz = 160 Mbit/s >= the 125 Mbit line rate.
//
// GOLDEN-MODEL ORACLE: app/internal/eth100tx/decode.go func descramble()
//   (vectors/<case>.scrambled_bits -> vectors/<case>.plain_bits).
// The unroll is BIT-EXACT with the 1-bit/clk eth_descramble.v (and thus the
// golden model): processing 2 bits in one clock is EXACTLY equivalent to
// processing them one-at-a-time — the per-bit combinational "step" (dstep) is
// the same primitive the 1-bit engine registers each cycle, chained twice here.
//
// KEYSTREAM / IDLE-LOCK (unchanged from eth_descramble.v):
//   key[n] = key[n-9] ^ key[n-11];  plain[n] = scrambled[n] ^ key[n].
//   HUNT   : load SEED_LEN idle-exposed key bits (in_bit^1) into the LFSR.
//   VERIFY : free-run; require VERIFY_LEN descrambled bits all-ones, else re-hunt.
//   LOCKED : LFSR free-runs; `locked` sticky; plain = in ^ key.
//   Lock lands after SEED_LEN+VERIFY_LEN = 44 processed bits (LockOffset==0),
//   identical to the 1-bit engine.
//
// ---- 2-WIDE STREAMING HANDSHAKE (matches eth_slicer_cdr out_bits/out_cnt) ----
//   in_valid          : 1 = this clock carries in_nbits recovered bits
//   in_nbits[1:0]     : 0..2 valid bits this clock (== CDR out_cnt, capped 2)
//   in_bits[1:0]      : bit 0 = EARLIEST, bit 1 = later (== CDR out_bits[1:0])
//   out_valid         : 1 = out_bits[out_nbits-1:0] valid (registered, 1-clk lat)
//   out_nbits[1:0]    : 0..2 descrambled bits out this clock (== in_nbits, delayed)
//   out_bits[1:0]     : bit 0 = earliest descrambled bit
//   locked            : idle-lock held (level, sticky until rst/!en)
// Descramble is 1:1, so out_nbits equals the in_nbits presented one clock earlier.
//
// GATING: rst OR !en holds the module inert (locked=0, out_valid=0) — the fabric
// inertness contract.  0 M9K, 0 PLL, 0 pins.

module eth_descramble2 #(
    parameter integer SEED_LEN   = 11,  // idle bits to seed the LFSR (need)
    parameter integer VERIFY_LEN = 33   // idle bits that must descramble to 1
) (
    input  wire clk,
    input  wire rst,        // synchronous, active-high: full reset to HUNT
    input  wire en,         // engine gate; en=0 holds the module inert

    // ---- scrambled bit burst in (up to 2/clk) ----
    input  wire       in_valid,   // 1 = in_nbits bits valid this clk
    input  wire [1:0] in_nbits,   // 0..2 valid bits
    input  wire [1:0] in_bits,    // bit0 = earliest scrambled NRZ bit

    // ---- descrambled bit burst out (registered, 1-clk latency) ----
    output reg        out_valid,  // 1 = out_bits valid
    output reg [1:0]  out_nbits,  // 0..2 descrambled bits out
    output reg [1:0]  out_bits,   // bit0 = earliest descrambled (plain) NRZ bit
    output reg        locked      // 1 once idle-lock verified; sticky until rst/!en
);
    // FSM states (identical encoding to eth_descramble.v).
    localparam [1:0] S_HUNT   = 2'd0,
                     S_VERIFY = 2'd1,
                     S_LOCKED = 2'd2;

    // Registered engine state (carried across clocks).
    reg [1:0]  state;
    reg [10:0] lfsr;   // lfsr[0]=key[n-1] ... lfsr[10]=key[n-11]; key[n]=lfsr[8]^lfsr[10]
    reg [5:0]  cnt;    // seed/verify progress

    // ---- one-bit descramble STEP (pure combinational; the 1-bit engine's body).
    // Input : current {state,lfsr,cnt,locked} + one scrambled bit.
    // Output: packed {nstate[1:0], nlfsr[10:0], ncnt[5:0], nlocked, out_bit}.
    //   [20:19]=nstate [18:8]=nlfsr [7:2]=ncnt [1]=nlocked [0]=out_bit
    function [20:0] dstep;
        input [1:0]  st;
        input [10:0] lf;
        input [5:0]  ct;
        input        lk;
        input        b;
        reg [1:0]  nst;
        reg [10:0] nlf;
        reg [5:0]  nct;
        reg        nlk;
        reg        ob;
        reg        kb, pb, sb;
        begin
            nst = st; nlf = lf; nct = ct; nlk = lk;
            kb = 1'b0; pb = 1'b0; sb = 1'b0; ob = 1'b0;  // defaults (no latch)
            case (st)
                S_HUNT: begin
                    sb  = b ^ 1'b1;      // idle plaintext = 1 -> key exposed
                    kb  = sb;
                    pb  = b ^ kb;        // = 1 during idle
                    ob  = pb;
                    nlf = {lf[9:0], sb};
                    nlk = 1'b0;
                    if (ct == SEED_LEN[5:0] - 6'd1) begin
                        nst = S_VERIFY; nct = 6'd0;
                    end else nct = ct + 6'd1;
                end
                S_VERIFY: begin
                    kb  = lf[8] ^ lf[10];
                    pb  = b ^ kb;
                    ob  = pb;
                    nlf = {lf[9:0], kb};
                    if (pb != 1'b1) begin
                        nst = S_HUNT; nct = 6'd0;   // bad phase -> re-hunt
                    end else if (ct == VERIFY_LEN[5:0] - 6'd1) begin
                        nst = S_LOCKED; nlk = 1'b1;
                    end else nct = ct + 6'd1;
                end
                S_LOCKED: begin
                    kb  = lf[8] ^ lf[10];
                    pb  = b ^ kb;
                    ob  = pb;
                    nlf = {lf[9:0], kb};
                    nlk = 1'b1;
                end
                default: begin nst = S_HUNT; ob = 1'b1; end  // unreachable
            endcase
            dstep = {nst, nlf, nct, nlk, ob};
        end
    endfunction

    // per-clock working values
    reg [20:0] r0, r1;
    reg [1:0]  fs_state;   // final state after this clock's bits
    reg [10:0] fs_lfsr;
    reg [5:0]  fs_cnt;
    reg        fs_locked;
    reg        b0out, b1out;
    reg        proc;

    always @(posedge clk) begin
        if (rst || !en) begin
            state     <= S_HUNT;
            lfsr      <= 11'd0;
            cnt       <= 6'd0;
            out_valid <= 1'b0;
            out_nbits <= 2'd0;
            out_bits  <= 2'd0;
            locked    <= 1'b0;
        end else begin
            proc = in_valid && (in_nbits != 2'd0);
            if (!proc) begin
                out_valid <= 1'b0;
                out_nbits <= 2'd0;
                // state / lfsr / cnt / locked unchanged (idle gap transparent)
            end else begin
                // step bit 0 (earliest)
                r0        = dstep(state, lfsr, cnt, locked, in_bits[0]);
                b0out     = r0[0];
                if (in_nbits >= 2'd2) begin
                    // step bit 1 from the post-bit-0 state
                    r1       = dstep(r0[20:19], r0[18:8], r0[7:2], r0[1], in_bits[1]);
                    b1out    = r1[0];
                    fs_state  = r1[20:19];
                    fs_lfsr   = r1[18:8];
                    fs_cnt    = r1[7:2];
                    fs_locked = r1[1];
                    out_bits  <= {b1out, b0out};
                    out_nbits <= 2'd2;
                end else begin
                    fs_state  = r0[20:19];
                    fs_lfsr   = r0[18:8];
                    fs_cnt    = r0[7:2];
                    fs_locked = r0[1];
                    out_bits  <= {1'b0, b0out};
                    out_nbits <= 2'd1;
                end
                state     <= fs_state;
                lfsr      <= fs_lfsr;
                cnt       <= fs_cnt;
                locked    <= fs_locked;
                out_valid <= 1'b1;
            end
        end
    end

endmodule

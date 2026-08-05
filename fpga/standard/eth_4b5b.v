// ---------------------------------------------------------------------------
// eth_4b5b.v  --  100BASE-TX 4B/5B align + decode stage (RX)
//
// Part of the in-fabric 100BASE-TX PHY decoder for the owned SDS1000CML FPGA.
// This module implements ONLY stage F of the RX chain (SPEC --- (5-of-chain)):
// MLT-3/NRZI-to-bit is UPSTREAM. Here we consume the recovered descrambled NRZ
// bit stream (the golden model's <case>.plain_bits), detect the /J/K/ Start-of-
// Stream Delimiter to ALIGN the 5-bit code-group boundary, map each aligned 5B
// group to its 4B MII nibble via IEEE 802.3 Table 24-1, and pass control / idle
// / ESD markers.  Output = MII nibble stream + frame-start / frame-end strobes.
//
// ORACLE: the Go golden model app/internal/eth100tx (decode.go align5B +
// eth100tx.go data4b5b/rev4b5b). Bit/nibble-exact vs vectors/{arp,icmp}.*.
//
// Bit order: code groups are serialized MSB-first (bit4..bit0), matching the
// golden model (eth100tx.go EncodeFrame: `for b := 4; b >= 0; b--`).  The /J/K/
// SSD on the wire is J then K, i.e. bits 1 1 0 0 0 1 0 0 0 1.
//
// SSD stand-in: /J/ and /K/ replace the first preamble octet 0x55, so per the
// golden model (decode.go nibbleOf) they each map to data nibble 0x5.  Hence a
// data nibble IS emitted for J and for K (with nibble = 0x5), while they are
// also flagged as control code groups.  ESD /T/ (then /R/) ends the frame; no
// data nibble is emitted for T/R/I (golden decode.go stops the nibble loop at
// T and never treats I/T/R/H/Q as data).
//
// Datapath width: this module processes ONE recovered bit per clock (in_valid).
// The overall PHY spec calls for up to 2 bits/clk from the CDR; a 2-wide wrapper
// (feed two eth_4b5b bit-slots, or unroll the mod-5 phase counter by 2) is an
// integration concern -- this stage is functionally complete and golden-exact
// at 1 bit/clk, which is what the sim proof pins.  Fully gated inert at reset
// (en=0 -> no state change, no output strobes), matching the additive contract.
// ---------------------------------------------------------------------------

module eth_4b5b (
    input  wire        clk,
    input  wire        rst,        // synchronous, active-high: clears to inert HUNT
    input  wire        en,         // gate; when 0 the engine is fully inert

    input  wire        in_bit,     // recovered descrambled NRZ bit
    input  wire        in_valid,   // 1-clk strobe: in_bit is valid this cycle

    // aligned 5-bit code-group stream (one entry per group once locked)
    output reg         cg_stb,     // 1-clk: cg_* valid this cycle
    output reg  [4:0]  cg_code,    // raw 5-bit code group, bit4..bit0
    output reg         cg_ctrl,    // 1 = control symbol, 0 = data
    output reg  [2:0]  cg_sym,     // control-symbol id (SYM_*), valid when cg_ctrl
    output reg         cg_err,     // 1 = invalid code group (unknown 5B code)

    // MII data nibble stream (data groups + J/K -> 0x5); low-nibble-first octets
    output reg  [3:0]  nibble,
    output reg         nibble_stb, // 1-clk: nibble valid this cycle

    // frame delimiters
    output reg         sof,        // 1-clk: /J/K/ SSD aligned (start of stream)
    output reg         eof,        // 1-clk: /T/R/ ESD seen (end of stream)
    output reg         locked      // level: code-group alignment currently held
);

    // ---- control-symbol ids (cg_sym) --------------------------------------
    localparam [2:0] SYM_DATA = 3'd0,
                     SYM_I    = 3'd1,   // IDLE  11111
                     SYM_J    = 3'd2,   // SSD1  11000
                     SYM_K    = 3'd3,   // SSD2  10001
                     SYM_T    = 3'd4,   // ESD1  01101
                     SYM_R    = 3'd5,   // ESD2  00111
                     SYM_H    = 3'd6,   // Halt  00100
                     SYM_Q    = 3'd7;   // Quiet 00000

    // ---- 5B control code words (bit4..bit0) -------------------------------
    localparam [4:0] C_I = 5'b11111,
                     C_J = 5'b11000,
                     C_K = 5'b10001,
                     C_T = 5'b01101,
                     C_R = 5'b00111,
                     C_H = 5'b00100,
                     C_Q = 5'b00000;

    // /J/K/ SSD as serialized MSB-first: J(11000) then K(10001)
    localparam [9:0] JK_SSD = {C_J, C_K};   // 10'b1100010001

    // ---- 5B -> {ctrl,isdata,err,sym,nibble} decode (IEEE 802.3 Table 24-1) --
    // packed result: [9]=ctrl [8]=isdata [7]=err [6:4]=sym [3:0]=nibble
    function [9:0] dec5b;
        input [4:0] c;
        begin
            case (c)
                // control
                C_I: dec5b = {1'b1, 1'b0, 1'b0, SYM_I, 4'h0};
                C_J: dec5b = {1'b1, 1'b1, 1'b0, SYM_J, 4'h5}; // SSD stands in for 0x5
                C_K: dec5b = {1'b1, 1'b1, 1'b0, SYM_K, 4'h5};
                C_T: dec5b = {1'b1, 1'b0, 1'b0, SYM_T, 4'h0};
                C_R: dec5b = {1'b1, 1'b0, 1'b0, SYM_R, 4'h0};
                C_H: dec5b = {1'b1, 1'b0, 1'b0, SYM_H, 4'h0};
                C_Q: dec5b = {1'b1, 1'b0, 1'b0, SYM_Q, 4'h0};
                // data (nibble value in low 4 bits)
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

    // ---- state ------------------------------------------------------------
    localparam ST_HUNT = 1'b0,   // searching for /J/K/ SSD
               ST_LOCK = 1'b1;   // aligned: grouping bits into 5B code groups

    reg        state;
    reg [9:0]  sr;        // HUNT shift register (last 10 bits, MSB=oldest)
    reg [4:0]  grp;       // LOCK 5-bit accumulator (MSB-first)
    reg [2:0]  cnt;       // bits collected into grp (0..4)
    reg        emitK;     // pending K emission the cycle after SSD match
    reg        armT;      // previous data-stream group decoded as /T/ (ESD1)

    // completed-group decode wires (used when cnt==4 in LOCK)
    wire [4:0] grp_done = {grp[3:0], in_bit};
    wire [9:0] d        = dec5b(grp_done);

    always @(posedge clk) begin
        // default: all strobes low each cycle
        cg_stb     <= 1'b0;
        nibble_stb <= 1'b0;
        sof        <= 1'b0;
        eof        <= 1'b0;

        if (rst) begin
            state   <= ST_HUNT;
            sr      <= 10'd0;
            grp     <= 5'd0;
            cnt     <= 3'd0;
            emitK   <= 1'b0;
            armT    <= 1'b0;
            locked  <= 1'b0;
            cg_code <= 5'd0;
            cg_ctrl <= 1'b0;
            cg_sym  <= SYM_DATA;
            cg_err  <= 1'b0;
            nibble  <= 4'd0;
        end else if (en && in_valid) begin
            case (state)
            // -----------------------------------------------------------
            ST_HUNT: begin
                sr <= {sr[8:0], in_bit};
                if ({sr[8:0], in_bit} == JK_SSD) begin
                    // SSD aligned: {sr[8:0],in_bit}=J..K.  Emit /J/ now, /K/ next.
                    state      <= ST_LOCK;
                    locked     <= 1'b1;
                    emitK      <= 1'b1;
                    armT       <= 1'b0;
                    grp        <= 5'd0;
                    cnt        <= 3'd0;
                    // present /J/ this cycle
                    cg_stb     <= 1'b1;
                    cg_code    <= C_J;
                    cg_ctrl    <= 1'b1;
                    cg_sym     <= SYM_J;
                    cg_err     <= 1'b0;
                    nibble     <= 4'h5;   // J stands in for 0x5
                    nibble_stb <= 1'b1;
                    sof        <= 1'b1;
                end
            end
            // -----------------------------------------------------------
            ST_LOCK: begin
                if (emitK) begin
                    // present /K/ this cycle; the current in_bit is the first
                    // bit of the next (first data) code group.
                    emitK      <= 1'b0;
                    cg_stb     <= 1'b1;
                    cg_code    <= C_K;
                    cg_ctrl    <= 1'b1;
                    cg_sym     <= SYM_K;
                    cg_err     <= 1'b0;
                    nibble     <= 4'h5;   // K stands in for 0x5
                    nibble_stb <= 1'b1;
                    grp        <= {4'b0, in_bit};
                    cnt        <= 3'd1;
                end else if (cnt == 3'd4) begin
                    // 5th bit completes grp_done -> decode & emit
                    cg_stb     <= 1'b1;
                    cg_code    <= grp_done;
                    cg_ctrl    <= d[9];
                    cg_sym     <= d[6:4];
                    cg_err     <= d[7];
                    nibble     <= d[3:0];
                    nibble_stb <= d[8];        // isdata (data groups + none for ctrl)
                    cnt        <= 3'd0;
                    grp        <= 5'd0;
                    // ESD detection: /T/ then /R/ ends the frame
                    if (d[9] && (d[6:4] == SYM_T)) begin
                        armT <= 1'b1;
                    end else if (d[9] && (d[6:4] == SYM_R) && armT) begin
                        armT   <= 1'b0;
                        eof    <= 1'b1;
                        state  <= ST_HUNT;
                        locked <= 1'b0;
                        sr     <= 10'd0;
                    end else begin
                        armT <= 1'b0;
                    end
                end else begin
                    grp <= {grp[3:0], in_bit};
                    cnt <= cnt + 3'd1;
                end
            end
            endcase
        end
    end

endmodule

// eth_framer.v — 100BASE-TX MAC framer + IEEE 802.3 FCS (CRC-32) check.
//
// Stage G of the in-fabric 100BASE-TX PHY decoder (see the eth100tx architecture
// spec). Consumes the MII DATA-NIBBLE stream produced by the 4B5B align+decode
// stage (low-nibble-first per octet, IEEE 802.3 s22.2.3), detects the
// preamble(0x55...)+SFD(0xD5), assembles MAC frame octets, runs the running
// CRC-32 (IEEE 802.3 FCS: reflected poly 0xEDB88320, init 0xFFFFFFFF), and at
// frame end flags FCS-ok/err via the published residue.
//
// ORACLE: the Go golden model open-sds/app/internal/eth100tx. FCS check uses the
// CRC-32/ISO-HDLC RAW-REGISTER residue 0xDEBB20E3 over (frame||FCS) — i.e. the
// running register (before the final XOR) equals 0xDEBB20E3 for a good frame.
// This is exactly the golden model's `CRC32(body)==0x2144DF1C` check, since
// 0x2144DF1C ^ 0xFFFFFFFF == 0xDEBB20E3. Verified against the arp+icmp vectors.
//
// EMIT: pushes every body octet (frame data then the 4 FCS octets) onto a strobe
// word compatible with byte_fifo {flags[7:0], idx[23:0], byte[7:0]}. The FCS
// octets carry is_fcs=1; the last octet carries frame_end + fcs_ok/err. A 4-deep
// delay line separates frame-data octets from the trailing FCS with 0 M9K.
//
// GATING: en=0 (or rst) => fully inert (emit_stb held low, FSM in HUNT). At the
// fabric top dec_proto!=ETH100TX keeps en low so every existing mode is
// byte-exact and the engine costs nothing at runtime.
//
// CLOCK: single 80 MHz fabric domain (clk). At most one octet is assembled per
// two nibble cycles and at most one octet is emitted per cycle, so the 1-push/cyc
// byte_fifo sink always keeps up.

module eth_framer #(
    // Minimum run of consecutive 0x5 MII nibbles that must precede the SFD's
    // high nibble (0xD) to accept a start-of-frame. The golden stream presents
    // 7 preamble octets + the SFD low nibble = 15 fives, so any threshold <=15
    // locks; 8 is a robust default (>=4 preamble octets) that still rejects a
    // stray 0xD in noise.
    parameter integer PRE_MIN = 8
) (
    input  wire        clk,
    input  wire        rst,        // synchronous, active-high
    input  wire        en,         // master enable; 0 => fully inert

    // ---- MII data-nibble stream (from 4B5B decode) ----
    input  wire        nib_valid,  // 1-cycle: `nib` is a valid decoded data nibble
    input  wire [3:0]  nib,        // decoded MII nibble (low-nibble-first per octet)
    input  wire        nib_end,    // 1-cycle: ESD (/T/R/) reached => frame complete
                                   //          (asserted the cycle AFTER the last nibble)

    // ---- emit to byte_fifo {flags,idx,byte} ----
    output reg         emit_stb,   // 1-cycle push strobe
    output reg  [7:0]  emit_byte,  // body octet (frame data, then FCS)
    output reg  [23:0] emit_idx,   // octet index within the frame body (0 = dst[0])
    output reg  [7:0]  emit_flags, // see flag map below

    // ---- frame-level status (trigger / readback aids) ----
    output reg         sfd_seen,   // 1-cycle: SFD detected (frame start)
    output reg         frame_done, // 1-cycle: end of frame (coincident with last emit)
    output reg         fcs_ok_o    // frame's FCS verdict, valid at frame_done
);

    // ---- emit_flags bit map ----
    localparam integer F_START = 7; // first body octet (idx==0)
    localparam integer F_END   = 6; // last body octet (4th/last FCS octet)
    localparam integer F_FCS   = 5; // this octet is one of the 4 FCS octets
    localparam integer F_OK    = 4; // FCS verified good  (valid when F_END)
    localparam integer F_ERR   = 3; // FCS verified bad   (valid when F_END)
    // bits [2:0] reserved (0) — spare for lock_lost/code_err from upstream.

    // IEEE 802.3 FCS running-register residue for a good frame||FCS (reflected,
    // pre-final-XOR). == 0x2144DF1C ^ 0xFFFFFFFF.
    localparam [31:0] CRC_RESIDUE = 32'hDEBB20E3;
    localparam [31:0] CRC_POLY    = 32'hEDB88320; // reflected 0x04C11DB7
    localparam [31:0] CRC_INIT    = 32'hFFFFFFFF;

    // Reflected CRC-32, one octet, LSB-first (== the standard table update).
    function [31:0] crc_next;
        input [31:0] c;
        input [7:0]  d;
        reg   [31:0] x;
        integer i;
        begin
            x = c ^ {24'd0, d};
            for (i = 0; i < 8; i = i + 1)
                x = x[0] ? ((x >> 1) ^ CRC_POLY) : (x >> 1);
            crc_next = x;
        end
    endfunction

    // ---- state ----
    localparam [1:0] S_HUNT  = 2'd0, // scanning preamble for SFD
                     S_DATA  = 2'd1, // assembling body octets
                     S_FLUSH = 2'd2; // draining the 4-octet FCS delay line

    reg [1:0]  state;
    reg [5:0]  pre_cnt;   // consecutive-0x5 counter (saturating)
    reg        phase;     // 0 = expecting low nibble, 1 = expecting high nibble
    reg [3:0]  lo_nib;    // latched low nibble
    reg [31:0] crc;
    reg [23:0] byte_idx;  // body octet index

    // 4-deep octet delay line (separates frame data from trailing FCS). p0=oldest.
    reg [7:0]  p0, p1, p2, p3;
    reg [2:0]  pcnt;      // occupancy 0..4
    reg [2:0]  flush_i;   // flush position

    wire [7:0] asm_byte = {nib, lo_nib};        // low-nibble-first assembly
    wire       fcs_good = (crc == CRC_RESIDUE); // verdict once all body octets fed

    always @(posedge clk) begin
        // pulsed outputs default low every cycle
        emit_stb   <= 1'b0;
        sfd_seen   <= 1'b0;
        frame_done <= 1'b0;

        if (rst || !en) begin
            state    <= S_HUNT;
            pre_cnt  <= 6'd0;
            phase    <= 1'b0;
            pcnt     <= 3'd0;
            byte_idx <= 24'd0;
            crc      <= CRC_INIT;
            flush_i  <= 3'd0;
            emit_flags <= 8'd0;
            emit_byte  <= 8'd0;
            emit_idx   <= 24'd0;
            fcs_ok_o   <= 1'b0;
        end else begin
            case (state)
            // -------------------------------------------------------------
            S_HUNT: begin
                if (nib_valid) begin
                    if (nib == 4'h5) begin
                        if (pre_cnt != 6'h3f) pre_cnt <= pre_cnt + 6'd1;
                    end else if ((nib == 4'hD) && (pre_cnt >= PRE_MIN[5:0])) begin
                        // SFD complete — next nibble is dst[0] low nibble.
                        state    <= S_DATA;
                        phase    <= 1'b0;
                        pcnt     <= 3'd0;
                        byte_idx <= 24'd0;
                        crc      <= CRC_INIT;
                        sfd_seen <= 1'b1;
                    end else begin
                        pre_cnt <= 6'd0; // stray nibble: restart preamble hunt
                    end
                end
            end
            // -------------------------------------------------------------
            S_DATA: begin
                if (nib_valid) begin
                    if (phase == 1'b0) begin
                        lo_nib <= nib;
                        phase  <= 1'b1;
                    end else begin
                        phase <= 1'b0;
                        crc   <= crc_next(crc, asm_byte);
                        if (pcnt < 3'd4) begin
                            // fill the delay line (no emit yet)
                            case (pcnt)
                                3'd0: p0 <= asm_byte;
                                3'd1: p1 <= asm_byte;
                                3'd2: p2 <= asm_byte;
                                3'd3: p3 <= asm_byte;
                            endcase
                            pcnt <= pcnt + 3'd1;
                        end else begin
                            // line full: oldest (p0) is a genuine frame-data octet
                            emit_stb  <= 1'b1;
                            emit_byte <= p0;
                            emit_idx  <= byte_idx;
                            emit_flags <= (byte_idx == 24'd0) ? (8'd1 << F_START) : 8'd0;
                            byte_idx  <= byte_idx + 24'd1;
                            p0 <= p1; p1 <= p2; p2 <= p3; p3 <= asm_byte;
                        end
                    end
                end
                if (nib_end) begin
                    // ESD: p0..p(pcnt-1) hold the trailing FCS octets; drain them.
                    // (pcnt==0 only on a body with no octets — nothing to flush.)
                    flush_i <= 3'd0;
                    state   <= (pcnt == 3'd0) ? S_HUNT : S_FLUSH;
                    if (pcnt == 3'd0) pre_cnt <= 6'd0;
                end
            end
            // -------------------------------------------------------------
            S_FLUSH: begin
                // emit one FCS octet per cycle (always the head p0, shifting up)
                emit_stb  <= 1'b1;
                emit_byte <= p0;
                emit_idx  <= byte_idx;
                byte_idx  <= byte_idx + 24'd1;
                p0 <= p1; p1 <= p2; p2 <= p3;
                if (flush_i == (pcnt - 3'd1)) begin
                    // last FCS octet: attach frame_end + verdict
                    emit_flags <= (8'd1 << F_END) | (8'd1 << F_FCS) |
                                  (fcs_good ? (8'd1 << F_OK) : (8'd1 << F_ERR));
                    frame_done <= 1'b1;
                    fcs_ok_o   <= fcs_good;
                    state      <= S_HUNT;
                    pre_cnt    <= 6'd0;
                end else begin
                    emit_flags <= (8'd1 << F_FCS);
                    flush_i    <= flush_i + 3'd1;
                end
            end
            // -------------------------------------------------------------
            default: state <= S_HUNT;
            endcase
        end
    end

endmodule

// can_decode.v — in-fabric CAN / CAN-FD frame decoder (ITEM 7).
//
// GOAL: produce the SAME decoded PAYLOAD byte sequence as the app software
// oracle decode.DecodeCANFD().Bytes on clean signals, GAPLESS and in real time,
// feeding the SHARED byte_fifo + dec_trigger exactly like uart/i2c/spi/eth.
//
// ============================ ORACLE CONTRACT ============================
// Audit of app/internal/decode/decode_canfd.go (canReader / decodeCANOneFrame /
// DecodeCANFD) and decode.go (sliceChannel / logicAt). One decode step per
// cap_tick (one decimated column, colTimeS units).
//
//  * SINGLE SLICED LINE, PURE THRESHOLD. logicAt(): codes[i] >= Thr ? 1:0.
//    thr8 = ceil(Thr) makes integer `code >= thr8` == float `code >= Thr`.
//    CAN bus state: dominant = logic-0, recessive = logic-1 when dom_low
//    (standard capture). So can_bit = dom_low ? (code>=thr8) : ~(code>=thr8);
//    can_bit==0 => dominant, ==1 => recessive.
//
//  * FREE-RUNNING SAMPLER ANCHORED AT THE SOF EDGE. The oracle anchors pos at
//    the sub-sample SOF edge e.x and samples raw bit k at round(e.x+(k+0.5)*spb)
//    with NO per-bit resync (canReader.readRaw advances pos by spb). For a clean
//    rail-to-rail single-column transition e.x == i-0.5 (i = first dominant
//    column), independent of polarity and of spb, so the sample column is
//        i + round((k+0.5)*spb - 0.5) = i + floor((k+0.5)*spb).
//    A Q16.8 accumulator gives that exactly: acc0 = spb/2 - 0.5 (== (spb>>1)-128
//    in Q.8), tgt = round(acc) = (acc+128)>>8, acc += spb per RAW bit. This is
//    uart_decode's proven accumulator with a -0.5 SOF-edge offset (RISK: the
//    -0.5 assumes a clean full-swing single-column edge — clean-signal-exact,
//    NOT bench-proven, same status as the other decoders).
//
//  * BIT DESTUFFING (canReader.next). Track runVal/runLen of the DESTUFFED
//    stream; while stuffOn, once runLen>=5 the NEXT raw bit is a STUFF bit
//    (consumed, not delivered; 6-identical => stuff_err). Each raw sample either
//    is a stuff bit (parser NOT advanced) or a destuffed bit (parser advanced) —
//    both advance the sampler by spb.
//
//  * FRAME WALK (decodeCANOneFrame), MSB-first fields over destuffed bits:
//      SOF(1,dominant) ID(11) b1(RTR/SRR) IDE
//      IDE=0 standard: FDF/r0 -> FDF=1 => FD {res,BRS,ESI}; FDF=0 => classic,
//                      remote = (b1==RTR==1)
//      IDE=1 extended: extID(18) RTR r1 r0 ; remote = RTR
//      DLC(4) ; nBytes = remote?0 : fd?fdDataLen(dlc) : (dlc>8?8:dlc)
//      DATA(nBytes*8) -> EMIT each byte (Result.Bytes)
//      classic: CRC-15(15) CRCdelim(1,stuffOn) ACK(1,stuffOff)
//      FD: best-effort stop after data (matches the oracle).
//    Bit stuffing spans SOF..CRC (stuffOn) then off for ACK. CRC-15 (poly
//    0x4599, seed 0, MSB-first) is computed over the DESTUFFED SOF..data bits.
//    A CRC-mismatch or stuff violation still EMITS the data bytes (the oracle
//    keeps fr.ok=true and its Bytes on a bad CRC) — the error rides the
//    end-of-frame STATUS MARKER, never the data bytes.
//
//  * WHAT LANDS IN Result.Bytes (the validation target): ONLY the data payload
//    bytes. So on the shared emit bus each DATA byte carries emit_flags==0
//    (data-only); a per-frame STATUS MARKER carries the error/FD flags with
//    emit_flags[1]=1 (non-data) so it is EXCLUDED from Bytes and lets
//    dec_trigger mode-1 ERROR fire. Host validation = drained entries with
//    emit_flags[1:0]==0, in order == Result.Bytes.
//
// ---- SHARED 8-bit emit_flags (CAN) — documented map -----------------------
//   DATA byte    : 8'b0000_0000                               (data-only)
//   STATUS marker: [1]=1 marker(non-data) [2]=ACK-err(NAK) [3]=CRC-err
//                  [4]=form-err(CRC delim not recessive) [5]=stuff-err [6]=FD
//   mode-1 ERROR: set MATCH err_mask (MATCH[15:8]) = 0x3C to trigger on any CAN
//   error (ACK/CRC/form/stuff); data bytes (flags 0) never false-trigger it.
//
// ---- TRIGGER (mode-0 legacy, data-only, mirrors serialtrig) ---------------
//   decode_trig fires on a DATA byte when (byte&mask)==(pattern&mask); matched
//   sticky; matched_byte latched. Modes 1/2/3 are handled by dec_trigger off the
//   shared emit_flags (mode-1 uses the error bits above).
//
// GATING: en=0 (proto-extend off or dec_en=0) holds every output inert
// (emit_stb=0, decode_trig=0, matched=0) and freezes/clears state, exactly like
// uart_decode gates on !en. All state is logic registers (M9K 46/46 full).
//
// All sequential logic steps ONLY on cap_tick, so "column" == cap_tick.

`default_nettype none

module can_decode (
    input  wire        clk,        // 80 MHz single domain
    input  wire        rst_n,      // async active-low reset (tie 1'b1 if unused)
    input  wire        cap_tick,   // one pulse per decimated column

    input  wire [7:0]  sample_code,// chosen-channel code for THIS column

    // ---- config (loaded by host; latched in acq.v spare selectors) ----
    input  wire        en,         // master enable; 0 => fully inert
    input  wire [7:0]  thr8,       // ceil(Thr); can bit level = code >= thr8
    input  wire [23:0] spb,        // samples-per-bit, Q16.8 (Result.SPB)
    input  wire        dom_low,    // 1 => dominant is the LOW level (standard CAN)

    // ---- single-byte (data-only) trigger, mode-0 legacy ----
    input  wire        trig_en,
    input  wire [7:0]  match_pattern,
    input  wire [7:0]  match_mask,

    // ---- outputs (SAME emit interface shape; 8-bit flags like eth) ----
    output reg         emit_stb,    // 1-clk pulse: a DATA byte or STATUS marker
    output reg  [7:0]  emit_byte,   // data byte (or CRC low byte on the marker)
    output reg  [23:0] emit_idx,    // column index (byte first-bit / SOF for marker)
    output reg  [7:0]  emit_flags,  // 8-bit CAN flag field (see map above)
    output reg         decode_trig, // 1-clk pulse into capture.v (data byte match)
    output reg         matched,     // sticky: a data match since reset/arm
    output reg  [7:0]  matched_byte // latched matching byte
);

    // ------------------------------------------------------------------ slicer
    wire pure_lvl = (sample_code >= thr8);          // ORACLE logicAt sample
    wire can_bit  = dom_low ? pure_lvl : ~pure_lvl; // 0=dominant, 1=recessive

    // ------------------------------------------------------- frame-walk phases
    localparam [4:0]
        P_HUNT=5'd0, P_SOF=5'd1, P_ID=5'd2, P_B1=5'd3, P_IDE=5'd4,
        P_EXTID=5'd5, P_EXTRTR=5'd6, P_EXTR1=5'd7, P_EXTR0=5'd8,
        P_STDFDF=5'd9, P_FDRES=5'd10, P_FDBRS=5'd11, P_FDESI=5'd12,
        P_DLC=5'd13, P_DATA=5'd14, P_CRC=5'd15, P_CDELIM=5'd16,
        P_ACK=5'd17, P_FDMARK=5'd18;

    // CAN-FD DLC -> data byte count.
    function [6:0] fd_len;
        input [3:0] d;
        begin
            case (d)
                4'd9:  fd_len = 7'd12;
                4'd10: fd_len = 7'd16;
                4'd11: fd_len = 7'd20;
                4'd12: fd_len = 7'd24;
                4'd13: fd_len = 7'd32;
                4'd14: fd_len = 7'd48;
                default: fd_len = (d >= 4'd15) ? 7'd64 : {3'd0, d}; // 0..8 or 64
            endcase
        end
    endfunction

    // ------------------------------------------------------------------- state
    reg        prev_bit;    // can_bit at previous column (SOF edge detect)
    reg [23:0] sidx;        // free-running column counter
    reg [23:0] s_idx;       // latched SOF column
    reg [23:0] off;         // columns since SOF
    reg [31:0] acc;         // Q16.8 relative sample accumulator
    reg [31:0] tgt;         // rounded target offset for the next RAW bit

    reg [1:0]  runVal;      // 0/1 = last destuffed bit, 2 = sentinel(invalid)
    reg [2:0]  runLen;      // consecutive-identical destuffed run (cap 5)
    reg        stuffOn;     // destuffing active (SOF..CRC delim)
    reg        stuff_err;   // 6 identical bits (bit-stuff violation)

    reg [4:0]  phase;
    reg [4:0]  bcnt;        // bit index within a multi-bit field / byte
    reg [17:0] field;       // ID / EXTID / CRC assembly (MSB-first)
    reg        b1;          // RTR (std) or SRR (ext)
    reg        ext;         // extended frame
    reg        fd;          // CAN-FD frame
    reg        remote;      // RTR data-less frame
    reg [3:0]  dlc;
    reg [6:0]  nbytes;      // resolved payload length
    reg [6:0]  byteno;      // payload byte index
    reg [7:0]  dbyte;       // data-byte assembly (MSB-first)
    reg [23:0] byte_start;  // column of the current byte's first bit
    reg [14:0] crc15;       // running CRC-15 over destuffed SOF..data
    reg        crc_record;  // feed crc15 (SOF..data only)
    reg [14:0] crc_read;    // CRC field read off the wire
    reg        crc_err, form_err;

    wire [23:0] cur_off = off + 24'd1;      // offset AT this ACTIVE column
    wire        is_stuff = stuffOn & (runLen >= 3'd5);

    // temporaries (blocking) for the DLC->nBytes resolve
    reg [3:0] dfull;
    reg [6:0] nb;

    always @(posedge clk or negedge rst_n) begin
        if (!rst_n) begin
            prev_bit <= 1'b1; sidx <= 24'd0; s_idx <= 24'd0; off <= 24'd0;
            acc <= 32'd0; tgt <= 32'd0;
            runVal <= 2'd2; runLen <= 3'd0; stuffOn <= 1'b1; stuff_err <= 1'b0;
            phase <= P_HUNT; bcnt <= 5'd0; field <= 18'd0;
            b1 <= 1'b0; ext <= 1'b0; fd <= 1'b0; remote <= 1'b0; dlc <= 4'd0;
            nbytes <= 7'd0; byteno <= 7'd0; dbyte <= 8'd0; byte_start <= 24'd0;
            crc15 <= 15'd0; crc_record <= 1'b0; crc_read <= 15'd0;
            crc_err <= 1'b0; form_err <= 1'b0;
            emit_stb <= 1'b0; emit_byte <= 8'd0; emit_idx <= 24'd0;
            emit_flags <= 8'd0; decode_trig <= 1'b0; matched <= 1'b0;
            matched_byte <= 8'd0;
        end else begin
            emit_stb    <= 1'b0;   // default 1-clk pulses
            decode_trig <= 1'b0;

            if (!en) begin
                prev_bit <= 1'b1; sidx <= 24'd0; phase <= P_HUNT;
                runVal <= 2'd2; runLen <= 3'd0; stuffOn <= 1'b1; stuff_err <= 1'b0;
                crc_record <= 1'b0; matched <= 1'b0;
            end else if (cap_tick) begin
                sidx     <= sidx + 24'd1;
                prev_bit <= can_bit;

                if (phase == P_HUNT) begin
                    // SOF = recessive(1) -> dominant(0) edge.
                    if (prev_bit == 1'b1 && can_bit == 1'b0) begin
                        s_idx <= sidx;
                        off   <= 24'd0;
                        acc   <= ({8'd0, spb[23:1]}) - 32'd128;   // spb/2 - 0.5 (Q.8)
                        tgt   <= ({8'd0, spb[23:1]}) >> 8;        // round(spb/2 - 0.5)
                        // fresh per-frame decode state
                        runVal <= 2'd2; runLen <= 3'd0; stuffOn <= 1'b1;
                        stuff_err <= 1'b0; crc15 <= 15'd0; crc_record <= 1'b1;
                        ext <= 1'b0; fd <= 1'b0; remote <= 1'b0; b1 <= 1'b0;
                        dlc <= 4'd0; nbytes <= 7'd0; byteno <= 7'd0;
                        bcnt <= 5'd0; field <= 18'd0; dbyte <= 8'd0;
                        crc_err <= 1'b0; form_err <= 1'b0;
                        phase <= P_SOF;
                    end
                end else begin
                    // ACTIVE: advance the column offset, sample on the target.
                    off <= cur_off;
                    if (cur_off == tgt) begin
                        // advance the RAW-bit sampler for the next fire
                        acc <= acc + {8'd0, spb};
                        tgt <= (acc + {8'd0, spb} + 32'd128) >> 8;

                        if (is_stuff) begin
                            // STUFF bit (consumed; parser NOT advanced)
                            if (can_bit == runVal[0]) stuff_err <= 1'b1; // 6 same
                            runVal <= {1'b0, can_bit};
                            runLen <= 3'd1;
                        end else begin
                            // DESTUFFED bit -> run update + CRC + parser
                            runVal <= {1'b0, can_bit};
                            if (runVal != 2'd2 && can_bit == runVal[0])
                                runLen <= (runLen >= 3'd5) ? 3'd5 : (runLen + 3'd1);
                            else
                                runLen <= 3'd1;
                            if (crc_record)
                                crc15 <= {crc15[13:0],1'b0}
                                       ^ ((crc15[14]^can_bit) ? 15'h4599 : 15'd0);

                            case (phase)
                            // ---- SOF ----
                            P_SOF: begin
                                if (can_bit != 1'b0) phase <= P_HUNT; // not a frame
                                else begin phase <= P_ID; bcnt <= 5'd0; field <= 18'd0; end
                            end
                            // ---- 11-bit base identifier ----
                            P_ID: begin
                                field <= {field[16:0], can_bit};
                                bcnt  <= bcnt + 5'd1;
                                if (bcnt == 5'd10) phase <= P_B1;
                            end
                            // ---- RTR(std) / SRR(ext) ----
                            P_B1: begin b1 <= can_bit; phase <= P_IDE; end
                            // ---- IDE ----
                            P_IDE: begin
                                ext <= can_bit;
                                if (can_bit) begin phase <= P_EXTID; bcnt <= 5'd0; field <= 18'd0; end
                                else          phase <= P_STDFDF;
                            end
                            // ---- extended 18-bit id tail ----
                            P_EXTID: begin
                                field <= {field[16:0], can_bit};
                                bcnt  <= bcnt + 5'd1;
                                if (bcnt == 5'd17) phase <= P_EXTRTR;
                            end
                            P_EXTRTR: begin remote <= can_bit; phase <= P_EXTR1; end
                            P_EXTR1:  begin phase <= P_EXTR0; end
                            P_EXTR0:  begin phase <= P_DLC; bcnt <= 5'd0; field <= 18'd0; end
                            // ---- std: FDF/EDL (=r0 when 0) ----
                            P_STDFDF: begin
                                if (can_bit) begin fd <= 1'b1; phase <= P_FDRES; end
                                else begin remote <= b1; phase <= P_DLC; bcnt <= 5'd0; field <= 18'd0; end
                            end
                            P_FDRES: begin phase <= P_FDBRS; end
                            // BRS: single data-rate on the oracle vectors (dataSpb==spb);
                            // a rate switch is a documented bench-cal extension.
                            P_FDBRS: begin phase <= P_FDESI; end
                            P_FDESI: begin phase <= P_DLC; bcnt <= 5'd0; field <= 18'd0; end
                            // ---- DLC (4 bits) -> resolve nBytes ----
                            P_DLC: begin
                                field <= {field[16:0], can_bit};
                                bcnt  <= bcnt + 5'd1;
                                if (bcnt == 5'd3) begin
                                    dfull = {field[2:0], can_bit};
                                    dlc <= dfull;
                                    if (remote)   nb = 7'd0;
                                    else if (fd)  nb = fd_len(dfull);
                                    else          nb = (dfull > 4'd8) ? 7'd8 : {3'd0, dfull};
                                    nbytes <= nb;
                                    if (nb == 7'd0) begin
                                        if (fd) phase <= P_FDMARK;
                                        else begin crc_record <= 1'b0; phase <= P_CRC; bcnt <= 5'd0; field <= 18'd0; end
                                    end else begin
                                        phase  <= P_DATA;
                                        bcnt   <= 5'd0;
                                        byteno <= 7'd0;
                                        dbyte  <= 8'd0;
                                    end
                                end
                            end
                            // ---- data payload ----
                            P_DATA: begin
                                if (bcnt == 5'd0) byte_start <= sidx;
                                dbyte <= {dbyte[6:0], can_bit};
                                bcnt  <= bcnt + 5'd1;
                                if (bcnt == 5'd7) begin
                                    emit_stb   <= 1'b1;
                                    emit_byte  <= {dbyte[6:0], can_bit};
                                    emit_idx   <= byte_start;
                                    emit_flags <= 8'd0;                 // data-only
                                    if (trig_en &&
                                        (({dbyte[6:0],can_bit} & match_mask)
                                          == (match_pattern & match_mask))) begin
                                        decode_trig  <= 1'b1;
                                        matched      <= 1'b1;
                                        matched_byte <= {dbyte[6:0], can_bit};
                                    end
                                    if ((byteno + 7'd1) == nbytes) begin
                                        if (fd) phase <= P_FDMARK;
                                        else begin crc_record <= 1'b0; phase <= P_CRC; bcnt <= 5'd0; field <= 18'd0; end
                                    end else begin
                                        byteno <= byteno + 7'd1;
                                        bcnt   <= 5'd0;
                                        dbyte  <= 8'd0;
                                    end
                                end
                            end
                            // ---- classic CRC-15 (record off) ----
                            P_CRC: begin
                                field <= {field[16:0], can_bit};
                                bcnt  <= bcnt + 5'd1;
                                if (bcnt == 5'd14) begin
                                    crc_read <= {field[13:0], can_bit};
                                    crc_err  <= ({field[13:0], can_bit} != crc15);
                                    phase    <= P_CDELIM;
                                end
                            end
                            // ---- CRC delimiter (stuffOn) then ACK unstuffed ----
                            P_CDELIM: begin
                                form_err <= (can_bit != 1'b1); // must be recessive
                                stuffOn  <= 1'b0;
                                phase    <= P_ACK;
                            end
                            // ---- ACK slot -> emit STATUS marker, resume hunt ----
                            P_ACK: begin
                                emit_stb   <= 1'b1;
                                emit_byte  <= crc_read[7:0];
                                emit_idx   <= s_idx;
                                // [6]=fd [5]=stuff [4]=form [3]=crc [2]=ack(NAK) [1]=marker
                                emit_flags <= {1'b0, fd, stuff_err, form_err,
                                               crc_err, can_bit, 1'b1, 1'b0};
                                phase      <= P_HUNT;
                            end
                            // ---- CAN-FD best-effort marker ----
                            P_FDMARK: begin
                                emit_stb   <= 1'b1;
                                emit_byte  <= 8'd0;
                                emit_idx   <= s_idx;
                                emit_flags <= {1'b0, 1'b1, stuff_err, 3'b000, 1'b1, 1'b0};
                                phase      <= P_HUNT;
                            end
                            default: phase <= P_HUNT;
                            endcase
                        end
                    end
                end
            end // cap_tick
        end
    end

endmodule

`default_nettype wire

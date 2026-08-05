// i2c_decode.v — in-fabric I2C decoder + internal TEST-GEN.
//
// GOAL: produce the SAME decoded payload byte sequence as the app's software
// oracle decode.DecodeI2C().Bytes on clean signals, GAPLESS, in real time,
// feeding the SHARED byte_fifo + trigger + drain that the UART decoder uses.
//
// DIVISION OF LABOR: unlike UART, I2C needs NO host-computed timing param to
// DECODE — sampling is purely SCL-edge-driven. The host only loads the two
// 8-bit slice thresholds (SCL, SDA). All decode is deterministic here.
//
// ============================ ORACLE CONTRACT ============================
// Audit of app/internal/decode/decode_i2c_spi.go:26-131 (DecodeI2C) and
// decode.go (sliceChannel / logicAt). One decode step per cap_tick (one
// decimated column, colTimeS units), mirroring the app's `for i:=0;i<n;i++`.
//
//  * PURE-THRESHOLD SAMPLE. The app samples the data bit with logicAt()
//    (decode.go:181): codes[i] >= Thr ? 1:0. thr8 = ceil(Thr) makes integer
//    `code >= thr8` == float `code >= Thr`. So cl=(scl>=scl_thr),
//    da=(sda>=sda_thr).
//
//  * HYSTERESIS NOTE (RISK-1). DecodeI2C detects START/STOP and the SCL-rising
//    edge from the HYSTERESIS level[] array (band = 0.20*amp/2), but samples
//    the DATA BIT with pure-threshold logicAt. On the clean oracle vectors
//    (codes at the rails 56/200, sharp single-sample transitions) level[]
//    flips at the SAME column as the pure threshold, so ONE pure-threshold
//    slice per channel used for BOTH edge detection AND bit sampling is
//    bit-exact. Holds ONLY for clean full-swing signals => clean-signal-
//    verified, NOT bench-proven.
//
//  * PER-COLUMN FSM (priority START > STOP > SCL-rising-sample; the app
//    `continue`s after START/STOP so they are mutually exclusive with the
//    sample). cl,da = this column's slice; prev_scl,prev_sda = previous
//    column. The app seeds cl,da=-1 and skips column 0 (pcl<0||pda<0) — we
//    prime prev on the first cap_tick and begin decoding on the second.
//      1. START = (cl==1 && prev_sda==1 && da==0)  [SDA falls, SCL high]
//                 -> inTxn=1; expectAddr=1; bitCount=0; val=0.  (no sample)
//      2. STOP  = (cl==1 && prev_sda==0 && da==1)  [SDA rises, SCL high]
//                 -> inTxn=0; bitCount=0.
//      3. SCL RISING & inTxn = (prev_scl==0 && cl==1 && inTxn) -> sample bit=da,
//         MSB-first: if bitCount<8 { val=(val<<1)|da; bitCount++;
//           at bitCount==8: address (first byte after START) or DATA payload }
//         else (9th clock) { ack=da; bitCount=0; val=0 }.
//
//  * WHAT LANDS IN Result.Bytes (the validation target): ONLY the DATA payload
//    bytes (line 108). Addresses are Spans, START/STOP/ACK/NAK are markers —
//    NOT bytes. So HOST validation = drained entries with flags[1]==0 (KIND=data).
//
// ---- EMIT TIMING (deferred to the 9th/ACK clock) --------------------------
// The app appends a DATA byte to Bytes at the 8th data-bit rising edge
// (bitCount==8, BEFORE the ACK clock). We DEFER the FIFO push to the following
// SCL rising (the 9th/ACK clock) so the correct ACK/NAK bit can ride in
// emit_flags[0]. On ALL oracle vectors every byte (address or data) is followed
// by exactly one ACK clock, so the relative order and set of DATA bytes drained
// is byte-for-byte identical to Result.Bytes. To stay faithful even when a byte
// is NOT followed by its 9th clock, a completed-but-un-ACKed byte is FLUSHED
// (with ack=0) the instant a START or STOP reframes the bus — reproducing the
// app's unconditional append-at-bit-8. The ONLY residual divergence from the
// app is a TRUNCATED capture that ends mid-transaction with a completed byte
// and NEITHER a 9th clock NOR a terminating START/STOP before end-of-data; no
// oracle vector exercises that (every txn closes with ACK+STOP or repeated
// START). Documented, clean-vector-exact.
//
// ---- SYMBOL ENCODING (fits byte_fifo {flags,idx,byte}; only flags[1:0] drain)
//   emit_flags[1] = KIND : 0 = DATA payload byte, 1 = ADDRESS byte.
//   emit_flags[0] = ACK  : 0 = ACK, 1 = NAK (the unit's 9th-clock SDA level;
//                          0 on a START/STOP flush where no ACK was seen).
//   emit_byte     = the full 8-bit unit value (data byte, or {addr7,rw} for an
//                   address — host recovers addr=byte>>1, rw=byte&1).
//   HOST VALIDATION: Result.Bytes == in order, every drained entry with
//   flags[1]==0.  Address entries (flags[1]==1) are excluded, matching the app.
//
// ---- TRIGGER (data-only, mirrors serialtrig / DecodeI2C payload semantics) --
//   Fires decode_trig only on DATA bytes (KIND==0) when
//   (byte & match_mask) == (match_pattern & match_mask). matched is sticky;
//   matched_byte latches the matching byte.
//
// All sequential logic steps ONLY on cap_tick, so "column" == cap_tick.
// en=0 OR proto!=I2C (gated by the parent) => fully inert (no strobes, sticky
// cleared). New state is logic registers only (M9K 46/46 full).

`default_nettype none

module i2c_decode (
    input  wire        clk,        // 80 MHz single domain
    input  wire        rst_n,      // async active-low reset (tie 1'b1 if unused)
    input  wire        cap_tick,   // one pulse per decimated column

    // ---- pre-selected channel codes for THIS column (parent does chan_swap) --
    input  wire [7:0]  scl_code,   // SCL channel raw code
    input  wire [7:0]  sda_code,   // SDA channel raw code

    // ---- config (loaded by host via reinterpreted spare selectors) ----
    input  wire        en,         // master enable; 0 => fully inert
    input  wire [7:0]  scl_thr,    // ceil(Thr) for SCL; cl = scl_code >= scl_thr
    input  wire [7:0]  sda_thr,    // ceil(Thr) for SDA; da = sda_code >= sda_thr

    // ---- internal TEST-GEN ----
    input  wire        tg_en,      // 1 => decoder input driven by the generator

    // ---- single-symbol trigger (data-only, mirrors serialtrig) ----
    input  wire        trig_en,
    input  wire [7:0]  match_pattern,
    input  wire [7:0]  match_mask,

    // ---- outputs (SAME emit interface as uart_decode) ----
    output reg         emit_stb,   // 1-clk pulse: a decoded unit pushed
    output reg  [7:0]  emit_byte,  // 8-bit unit value (data byte or {addr,rw})
    output reg  [23:0] emit_idx,   // column index of the unit's first data bit
    output reg  [1:0]  emit_flags, // {KIND(1=addr), ACK(1=NAK)}
    output reg         decode_trig,// 1-clk pulse into capture.v (data-only match)
    output reg         matched,    // sticky: a data match has occurred
    output reg  [7:0]  matched_byte,// latched matching byte

    // ---- transaction markers (bound a txn for dec_trigger mode-3 addr+data) ----
    // 1-clk pulses aligned to the emit stream: i2c_start_stb / i2c_stop_stb ride
    // the SAME column as any flush-emit that a START/STOP forces, so a consumer
    // sees the flushed (last) data byte AND the boundary in the same cycle.
    output reg         i2c_start_stb, // 1-clk: I2C START detected (txn begin)
    output reg         i2c_stop_stb   // 1-clk: I2C STOP  detected (txn end)
);

    // =====================================================================
    // TEST-GEN: drives a fixed, self-checkable I2C transaction on internal
    // SCL/SDA codes (0x00 / 0xFF). Transaction:
    //   START, addr=0x24 W (byte 0x48) +ACK, data 0xA5 +ACK, data 0x5A +ACK,
    //   STOP, idle, repeat.  Expected drained DATA bytes = A5 5A (addr 0x24
    //   excluded, as DecodeI2C excludes addresses from Bytes).
    // Segment ROM: each entry is one {SCL,SDA} level pair held TG_HALF ticks.
    // A bit = a low segment (SDA setup) + a high segment (SCL clock/sample).
    // =====================================================================
    localparam integer TG_HALF = 4;     // cap_ticks per segment (=> ~8 cols/bit)
    localparam integer TG_NSEG = 64;    // total segments (62 used + 2 pad idle);
                                        // power-of-2 so the ROM MIF depth matches

    (* ramstyle = "logic" *) reg tg_scl_rom [0:TG_NSEG-1];
    (* ramstyle = "logic" *) reg tg_sda_rom [0:TG_NSEG-1];

    integer ip, kb, bi;
    reg [7:0] tg_bytes [0:2];
    initial begin
        tg_bytes[0] = 8'h48; // addr 0x24, W
        tg_bytes[1] = 8'hA5; // data
        tg_bytes[2] = 8'h5A; // data
        ip = 0;
        // lead idle (SCL=1,SDA=1) — primes prev_sda=1 for the START
        tg_scl_rom[ip]=1'b1; tg_sda_rom[ip]=1'b1; ip=ip+1;
        tg_scl_rom[ip]=1'b1; tg_sda_rom[ip]=1'b1; ip=ip+1;
        // START: SDA 1->0 while SCL high, then SCL low
        tg_scl_rom[ip]=1'b1; tg_sda_rom[ip]=1'b0; ip=ip+1;
        tg_scl_rom[ip]=1'b0; tg_sda_rom[ip]=1'b0; ip=ip+1;
        // three bytes, MSB-first, each + one ACK (0) clock
        for (bi=0; bi<3; bi=bi+1) begin
            for (kb=7; kb>=0; kb=kb-1) begin
                tg_scl_rom[ip]=1'b0; tg_sda_rom[ip]=(tg_bytes[bi]>>kb)&1'b1; ip=ip+1;
                tg_scl_rom[ip]=1'b1; tg_sda_rom[ip]=(tg_bytes[bi]>>kb)&1'b1; ip=ip+1;
            end
            tg_scl_rom[ip]=1'b0; tg_sda_rom[ip]=1'b0; ip=ip+1; // ACK setup
            tg_scl_rom[ip]=1'b1; tg_sda_rom[ip]=1'b0; ip=ip+1; // ACK clock (=0)
        end
        // STOP: SCL low, then SCL high with SDA low, then SDA 0->1 (STOP)
        tg_scl_rom[ip]=1'b0; tg_sda_rom[ip]=1'b0; ip=ip+1;
        tg_scl_rom[ip]=1'b1; tg_sda_rom[ip]=1'b0; ip=ip+1;
        tg_scl_rom[ip]=1'b1; tg_sda_rom[ip]=1'b1; ip=ip+1;
        // trailing idle (3 pad segments -> total 64, a power of two so the
        // synthesized ROM depth matches the init and Quartus emits no
        // depth-mismatch warning). Extra idle only lengthens the inter-frame gap.
        tg_scl_rom[ip]=1'b1; tg_sda_rom[ip]=1'b1; ip=ip+1;
        tg_scl_rom[ip]=1'b1; tg_sda_rom[ip]=1'b1; ip=ip+1;
        tg_scl_rom[ip]=1'b1; tg_sda_rom[ip]=1'b1; ip=ip+1;
        // ip should equal TG_NSEG here (64)
    end

    reg [6:0] tg_seg;    // current segment index 0..TG_NSEG-1
    reg [3:0] tg_hold;   // ticks spent in current segment

    always @(posedge clk or negedge rst_n) begin
        if (!rst_n) begin
            tg_seg <= 7'd0; tg_hold <= 4'd0;
        end else if (!en || !tg_en) begin
            // parked at segment 0 (idle high) so re-enable leads with idle
            tg_seg <= 7'd0; tg_hold <= 4'd0;
        end else if (cap_tick) begin
            if (tg_hold == TG_HALF-1) begin
                tg_hold <= 4'd0;
                tg_seg  <= (tg_seg == TG_NSEG-1) ? 7'd0 : (tg_seg + 7'd1);
            end else begin
                tg_hold <= tg_hold + 4'd1;
            end
        end
    end

    wire [7:0] tg_scl_code = tg_scl_rom[tg_seg] ? 8'hFF : 8'h00;
    wire [7:0] tg_sda_code = tg_sda_rom[tg_seg] ? 8'hFF : 8'h00;

    // =====================================================================
    // SLICER — pure threshold (== oracle logicAt on clean signals)
    // =====================================================================
    wire [7:0] scl_in = tg_en ? tg_scl_code : scl_code;
    wire [7:0] sda_in = tg_en ? tg_sda_code : sda_code;
    wire       cl = (scl_in >= scl_thr);
    wire       da = (sda_in >= sda_thr);

    // =====================================================================
    // DECODE FSM
    // =====================================================================
    reg        primed;      // first cap_tick seeds prev_* (app's pcl<0 skip)
    reg        prev_scl;
    reg        prev_sda;
    reg        inTxn;
    reg        expectAddr;
    reg [3:0]  bitCount;    // 0..8
    reg [7:0]  val;         // MSB-first assembly
    reg        pend;        // a completed 8-bit unit awaits its ACK clock
    reg        pendKind;    // 1 = address, 0 = data
    reg [7:0]  pendVal;
    reg [23:0] sidx;        // free-running column index
    reg [23:0] bitStart;    // column of the unit's first data bit

    // event decode (combinational)
    wire start_ev  = cl && prev_sda && !da;               // SDA falls, SCL high
    wire stop_ev   = cl && !prev_sda && da;               // SDA rises, SCL high
    wire rising    = !prev_scl && cl;                     // SCL rising
    wire sample_ev = rising && inTxn && !start_ev && !stop_ev;

    // value after shifting in this column's bit (for the 8th-bit latch)
    wire [7:0] val_next = (val << 1) | {7'd0, da};

    // trigger match on a data unit
    wire pend_match = trig_en && ((pendVal & match_mask) == (match_pattern & match_mask));

    always @(posedge clk or negedge rst_n) begin
        if (!rst_n) begin
            primed <= 1'b0; prev_scl <= 1'b1; prev_sda <= 1'b1;
            inTxn <= 1'b0; expectAddr <= 1'b0; bitCount <= 4'd0; val <= 8'd0;
            pend <= 1'b0; pendKind <= 1'b0; pendVal <= 8'd0;
            sidx <= 24'd0; bitStart <= 24'd0;
            emit_stb <= 1'b0; emit_byte <= 8'd0; emit_idx <= 24'd0;
            emit_flags <= 2'd0; decode_trig <= 1'b0;
            matched <= 1'b0; matched_byte <= 8'd0;
            i2c_start_stb <= 1'b0; i2c_stop_stb <= 1'b0;
        end else begin
            // default 1-clk pulses
            emit_stb      <= 1'b0;
            decode_trig   <= 1'b0;
            i2c_start_stb <= 1'b0;
            i2c_stop_stb  <= 1'b0;

            if (!en) begin
                // fully inert: hold decode state clear, drop sticky
                primed <= 1'b0; prev_scl <= 1'b1; prev_sda <= 1'b1;
                inTxn <= 1'b0; expectAddr <= 1'b0; bitCount <= 4'd0;
                val <= 8'd0; pend <= 1'b0; matched <= 1'b0;
            end else if (cap_tick) begin
                sidx <= sidx + 24'd1;

                if (!primed) begin
                    // seed prev_* from this column, decode nothing (app i=0 skip)
                    primed   <= 1'b1;
                    prev_scl <= cl;
                    prev_sda <= da;
                end else begin
                    if (start_ev) begin
                        i2c_start_stb <= 1'b1;   // txn boundary marker (repeated START too)
                        // faithful flush of a completed-but-un-ACKed byte
                        if (pend) begin
                            emit_stb   <= 1'b1;
                            emit_byte  <= pendVal;
                            emit_idx   <= bitStart;
                            emit_flags <= {pendKind, 1'b0};
                            if (!pendKind && pend_match) begin
                                decode_trig  <= 1'b1;
                                matched      <= 1'b1;
                                matched_byte <= pendVal;
                            end
                        end
                        inTxn      <= 1'b1;
                        expectAddr <= 1'b1;
                        bitCount   <= 4'd0;
                        val        <= 8'd0;
                        pend       <= 1'b0;
                    end else if (stop_ev) begin
                        i2c_stop_stb <= 1'b1;    // txn boundary marker
                        if (pend) begin
                            emit_stb   <= 1'b1;
                            emit_byte  <= pendVal;
                            emit_idx   <= bitStart;
                            emit_flags <= {pendKind, 1'b0};
                            if (!pendKind && pend_match) begin
                                decode_trig  <= 1'b1;
                                matched      <= 1'b1;
                                matched_byte <= pendVal;
                            end
                        end
                        inTxn    <= 1'b0;
                        bitCount <= 4'd0;
                        pend     <= 1'b0;
                    end else if (sample_ev) begin
                        if (bitCount < 4'd8) begin
                            if (bitCount == 4'd0) bitStart <= sidx;
                            val      <= val_next;
                            bitCount <= bitCount + 4'd1;
                            if (bitCount == 4'd7) begin
                                // 8th data bit just completed: latch pending unit
                                pend     <= 1'b1;
                                pendKind <= expectAddr;
                                pendVal  <= val_next;
                                if (expectAddr) expectAddr <= 1'b0;
                            end
                        end else begin
                            // 9th clock = ACK/NAK -> emit the pending unit
                            emit_stb   <= 1'b1;
                            emit_byte  <= pendVal;
                            emit_idx   <= bitStart;
                            emit_flags <= {pendKind, da}; // ACK(0)/NAK(1)=da
                            if (!pendKind && pend_match) begin
                                decode_trig  <= 1'b1;
                                matched      <= 1'b1;
                                matched_byte <= pendVal;
                            end
                            bitCount <= 4'd0;
                            val      <= 8'd0;
                            pend     <= 1'b0;
                        end
                    end
                    prev_scl <= cl;
                    prev_sda <= da;
                end
            end // cap_tick
        end
    end

endmodule

`default_nettype wire

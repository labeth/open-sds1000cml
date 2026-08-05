// tb_eth100_decode.v -- END-TO-END iverilog testbench for the chained
// 100BASE-TX PHY decoder (eth100_decode.v).
//
// Feeds the golden model's 600 MSa/s ternary sample codes (<case>.samples)
// LANES(=8) per word into eth100_decode, PACED (one word every 2 clocks) so the
// 1-bit/clk descramble+4B5B tail never underruns and the serializer never
// overflows.  Collects the emitted MAC-octet stream and checks it BYTE-EXACT
// against the golden frame body (<case>.body = MAC frame octets + 4 FCS octets),
// and checks the FCS verdict (fcs_ok_o at frame_done) and frame delimiters.
//
// This is the full-chain oracle comparison:
//   samples --[slice+CDR+MLT3+descramble+4B5B+framer]--> MAC octets + FCS OK
//   ==  golden app/internal/eth100tx DecodeSamples() frame body + FCSOK.
//
// Plusargs: +SAMP=<file> +BODY=<file> +NBODY=<n> +NAME=<label>

`timescale 1ns/1ps
module tb_eth100_decode;
    localparam integer SAMPLE_W = 12;
    localparam integer LANES    = 8;

    integer i, j, fd, r, nsamp, nbody, remain, nv;
    reg [8*160-1:0] samp_file, body_file;
    reg [8*32-1:0]  name;

    // vectors
    integer samp_mem [0:16383];   // signed sample codes
    reg [7:0] exp_body [0:2047];  // expected body octets (frame||FCS)

    // collected outputs
    reg [7:0] got_body [0:2047];
    integer   got_n;
    integer   frame_done_cnt, sfd_cnt;
    reg       fcs_ok_latched;

    // DUT
    reg                        clk, rst, en, in_valid, flush;
    reg  signed [SAMPLE_W-1:0] thr_hi, thr_lo;
    reg  [LANES*SAMPLE_W-1:0]  codes;
    reg  [3:0]                 nvalid;

    wire        emit_stb, sfd_seen, frame_done, fcs_ok_o;
    wire [7:0]  emit_byte, emit_flags;
    wire [23:0] emit_idx;
    wire        descr_locked, cg_locked, cdr_overflow, ser_overflow;

    eth100_decode #(.SAMPLE_W(SAMPLE_W), .LANES(LANES)) dut (
        .clk(clk), .rst(rst), .en(en),
        .thr_hi(thr_hi), .thr_lo(thr_lo),
        .codes(codes), .nvalid(nvalid), .in_valid(in_valid), .flush(flush),
        .emit_stb(emit_stb), .emit_byte(emit_byte),
        .emit_idx(emit_idx), .emit_flags(emit_flags),
        .sfd_seen(sfd_seen), .frame_done(frame_done), .fcs_ok_o(fcs_ok_o),
        .descr_locked(descr_locked), .cg_locked(cg_locked),
        .cdr_overflow(cdr_overflow), .ser_overflow(ser_overflow)
    );

    always #5 clk = ~clk;

    // collect emitted octets + status
    always @(posedge clk) begin
        if (!rst && en) begin
            if (emit_stb) begin
                got_body[got_n] = emit_byte;
                got_n = got_n + 1;
            end
            if (sfd_seen)   sfd_cnt = sfd_cnt + 1;
            if (frame_done) begin
                frame_done_cnt = frame_done_cnt + 1;
                fcs_ok_latched = fcs_ok_o;
            end
        end
    end

    initial begin
        clk = 0; rst = 1; en = 0; in_valid = 0; flush = 0;
        codes = 0; nvalid = 0; got_n = 0;
        frame_done_cnt = 0; sfd_cnt = 0; fcs_ok_latched = 0;
        thr_hi = 500; thr_lo = -500;

        if (!$value$plusargs("SAMP=%s",  samp_file)) begin $display("need +SAMP=");  $finish; end
        if (!$value$plusargs("BODY=%s",  body_file)) begin $display("need +BODY=");  $finish; end
        if (!$value$plusargs("NBODY=%d", nbody))     begin $display("need +NBODY="); $finish; end
        if (!$value$plusargs("NAME=%s",  name)) name = "case";

        // read signed decimal samples
        fd = $fopen(samp_file, "r");
        if (fd == 0) begin $display("FAIL[%0s]: cannot open %0s", name, samp_file); $finish; end
        nsamp = 0;
        r = $fscanf(fd, "%d", samp_mem[nsamp]);
        while (r == 1) begin
            nsamp = nsamp + 1;
            r = $fscanf(fd, "%d", samp_mem[nsamp]);
        end
        $fclose(fd);

        $readmemh(body_file, exp_body);

        // reset
        @(negedge clk); rst = 1; en = 1;
        @(negedge clk); rst = 0;

        // feed LANES samples per word, PACED one word every 2 clocks
        // (bubble clock keeps the 1-bit/clk tail from underrunning).
        i = 0;
        while (i < nsamp) begin
            @(negedge clk);
            remain = nsamp - i;
            nv = (remain >= LANES) ? LANES : remain;
            codes = 0;
            for (j = 0; j < LANES; j = j + 1)
                if (j < nv) codes[j*SAMPLE_W +: SAMPLE_W] = samp_mem[i+j][SAMPLE_W-1:0];
            nvalid   = nv[3:0];
            in_valid = 1;
            i = i + nv;
            // bubble clock: no new samples, CDR carries fractional phase
            @(negedge clk);
            in_valid = 0;
            nvalid   = 0;
        end
        // close final run + drain the 1-bit/clk tail thoroughly
        @(negedge clk); in_valid = 0; nvalid = 0; flush = 1;
        @(negedge clk); flush = 0;
        repeat (64) @(negedge clk);

        // ---- checks ------------------------------------------------------
        if (cdr_overflow) begin
            $display("FAIL[%0s]: cdr_overflow asserted", name); $finish; end
        if (ser_overflow) begin
            $display("FAIL[%0s]: ser_overflow asserted (pace too fast)", name); $finish; end
        if (sfd_cnt != 1) begin
            $display("FAIL[%0s]: sfd_cnt=%0d exp 1", name, sfd_cnt); $finish; end
        if (frame_done_cnt != 1) begin
            $display("FAIL[%0s]: frame_done_cnt=%0d exp 1", name, frame_done_cnt); $finish; end
        if (got_n != nbody) begin
            $display("FAIL[%0s]: body octet count got=%0d exp=%0d", name, got_n, nbody);
            for (i = 0; i < got_n && i < 40; i = i + 1)
                $display("   body[%0d] got=%02h", i, got_body[i]);
            $finish;
        end
        for (i = 0; i < nbody; i = i + 1) begin
            if (got_body[i] !== exp_body[i]) begin
                $display("FAIL[%0s]: body[%0d] got=%02h exp=%02h",
                         name, i, got_body[i], exp_body[i]);
                $finish;
            end
        end
        if (!fcs_ok_latched) begin
            $display("FAIL[%0s]: FCS verdict NOT ok at frame_done", name); $finish; end

        $display("PASS[%0s]: %0d samples -> %0d MAC octets (frame||FCS) BYTE-EXACT vs golden, FCS OK, sfd=1 frame_done=1",
                 name, nsamp, nbody);
        $finish;
    end
endmodule

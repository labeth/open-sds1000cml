// ENGMODEL-OWNER-UNIT: FU-RTL-COMMON-TESTS
// tb_sync.v -- sync_pulse delivers one pulse per input pulse both ways; sync_word only ever
// shows coherent, recent samples (counter mod 20480 -- a non-power-of-two wrap); stretch3 edges.
`timescale 1ns/1ps
// TRLC-LINKS: REQ-SDS-192
module tb_sync;
    reg clk_a = 0; always #18.5 clk_a = ~clk_a;   // 27 MHz-ish
    reg clk_b = 0; always #2.5  clk_b = ~clk_b;   // 200 MHz
    integer errors = 0;
    task check(input cond, input [8*48-1:0] msg); begin if (!cond) begin errors = errors + 1; $display("FAIL: %0s", msg); end end endtask

    // ---- sync_pulse slow -> fast and fast -> slow ----
    reg pa = 0, pb = 0; wire pa_in_b, pb_in_a; integer na = 0, nb = 0;
    sync_pulse u_ab (.clk_src(clk_a), .pulse_src(pa), .clk_dst(clk_b), .pulse_dst(pa_in_b));
    sync_pulse u_ba (.clk_src(clk_b), .pulse_src(pb), .clk_dst(clk_a), .pulse_dst(pb_in_a));
    always @(posedge clk_b) if (pa_in_b) nb = nb + 1;
    always @(posedge clk_a) if (pb_in_a) na = na + 1;

    // ---- sync_word: b (fast) counter mod 20480 -> a (slow) ----
    reg [14:0] cnt = 0; wire [14:0] q;
    always @(posedge clk_b) cnt <= (cnt == 15'd20479) ? 15'd0 : cnt + 15'd1;
    sync_word #(.W(15)) u_w (.clk_src(clk_b), .d_src(cnt), .clk_dst(clk_a), .q_dst(q));
    integer nq = 0, bad = 0; reg [14:0] q_prev = 0; reg [15:0] lag;
    always @(posedge clk_a) begin
        if (q != q_prev) begin
            nq = nq + 1;
            lag = (cnt >= q) ? (cnt - q) : (cnt + 15'd20480 - q);
            if (lag > 16'd200) bad = bad + 1;       // must be a RECENT sample (< ~8 slow cycles)
            q_prev = q;
        end
    end

    // ---- stretch3 ----
    reg din = 0; wire lvl, rise, fall; integer nr = 0, nf = 0;
    stretch3 u_s (.clk(clk_b), .d(din), .level(lvl), .rise(rise), .fall(fall));
    always @(posedge clk_b) begin if (rise) nr = nr + 1; if (fall) nf = nf + 1; end

    integer i;
    initial begin
        #100;
        for (i = 0; i < 20; i = i + 1) begin @(posedge clk_a); pa <= 1; @(posedge clk_a); pa <= 0; repeat (5) @(posedge clk_a); end
        for (i = 0; i < 20; i = i + 1) begin @(posedge clk_b); pb <= 1; @(posedge clk_b); pb <= 0; repeat (40) @(posedge clk_b); end
        for (i = 0; i < 7; i = i + 1) begin #33 din = 1; #41 din = 0; end
        #2000;
        check(nb == 20, "sync_pulse slow->fast: 20 pulses");
        check(na == 20, "sync_pulse fast->slow: 20 pulses");
        check(nq > 100, "sync_word delivered many updates");
        check(bad == 0, "sync_word every value recent and coherent");
        check(nr == 7 && nf == 7, "stretch3 rise/fall counts");
        if (errors == 0) $display("PASS tb_sync"); else $display("FAIL tb_sync: %0d errors", errors);
        $finish;
    end
endmodule

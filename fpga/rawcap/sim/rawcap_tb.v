// rawcap_tb.v — self-checking bench for the raw-lane time-series recorder.
//   Drives the HW-verified GPMC slave protocol, configures ENC_DIV/DECIM, ARMs, feeds a
//   known incrementing pattern on adc_lane[15:0], waits for STATUS.full, then reads the
//   buffer back over GPMC and checks it captured the decimated sequence in order.
`timescale 1ns/1ps
module rawcap_tb;
    reg         clk = 1'b0, nCS1 = 1'b1, nOE = 1'b1, nWE = 1'b1;
    reg  [6:0]  sel = 7'd0;
    reg  [32:0] adc_lane = 33'd0;
    reg  [15:0] d_drive = 16'd0;
    reg         drive_en = 1'b0;
    wire [15:0] gpmc_d = drive_en ? d_drive : 16'hzzzz;
    wire        gpmc_wait;
    wire [7:0]  adc_enc;
    wire [3:0]  adc_ctl_hi;
    wire [2:0]  adc_ctl_lo;
    integer     errors = 0;

    rawcap dut (
        .clk(clk), .adc_lane(adc_lane), .adc_enc(adc_enc),
        .adc_ctl_hi(adc_ctl_hi), .adc_ctl_lo(adc_ctl_lo),
        .nCS1(nCS1), .nOE(nOE), .nWE(nWE), .sel(sel),
        .gpmc_d(gpmc_d), .gpmc_wait(gpmc_wait));

    always #5 clk = ~clk;   // 100 MHz

    // selector -> A3..A7 encoding: sel[6:2] = addr>>2 ... but here addr are the byte offsets
    // (0x20,0x24,...) and the fabric decodes {1'b0,sel[6:2],2'b00}. So sel = addr>>0 with
    // bits[1:0]=0; drive sel = addr[6:0].
    task gpmc_write(input [7:0] addr, input [15:0] data);
        begin
            @(negedge clk); nCS1=1'b0; sel=addr[6:0]; d_drive=data; drive_en=1'b1; nWE=1'b0;
            repeat (4) @(negedge clk);
            nWE=1'b1;
            repeat (4) @(negedge clk);
            drive_en=1'b0; nCS1=1'b1; @(negedge clk);
        end
    endtask
    task gpmc_read(input [7:0] addr, output [15:0] val);
        begin
            @(negedge clk); nCS1=1'b0; nOE=1'b0; sel=addr[6:0]; drive_en=1'b0;
            repeat (2) @(negedge clk); #1 val=gpmc_d;
            @(negedge clk); nCS1=1'b1; nOE=1'b1;
        end
    endtask
    task check(input [15:0] got, input [15:0] exp, input [255:0] name);
        begin
            if (got!==exp) begin $display("FAIL %0s: got %h exp %h", name, got, exp); errors=errors+1; end
            else $display("ok   %0s = %h", name, got);
        end
    endtask

    // background feeder: ramp adc_lane +1/clk once enabled (avoids fork/join_none)
    reg feeding = 1'b0;
    always @(posedge clk) if (feeding) adc_lane <= adc_lane + 33'd1;

    reg [15:0] v; integer k; reg [15:0] seq;
    initial begin
        repeat (6) @(negedge clk);
        // ID
        gpmc_read(8'h10, v); check(v, 16'hADC1, "ID");
        // small ENC_DIV (fast enc), DECIM=0 (store every enc sample), bank 0
        gpmc_write(8'h24, 16'd0);   gpmc_read(8'h24, v); check(v, 16'd0, "ENC_DIV writeback");
        gpmc_write(8'h28, 16'd0);   gpmc_read(8'h28, v); check(v, 16'd0, "DECIM writeback");

        // enc period = 2*(0+1)=2 clks -> a new sample every 2 clks. Ramp adc_lane +1/clk so
        // consecutive stored samples are exactly 2 apart. Start feeding, then ARM.
        feeding = 1'b1;
        gpmc_write(8'h20, 16'h0001);   // ARM

        // wait for full
        for (k = 0; k < 4000; k = k + 1) begin
            gpmc_read(8'h30, v);
            if (v[15]) begin $display("ok   full asserted, wptr=%0d", v[8:0]); k = 4000; end
        end
        gpmc_read(8'h30, v);
        if (!v[15]) begin $display("FAIL never filled"); errors=errors+1; end

        // read back three consecutive cells; input ramps +1/clk, enc period = 2 clks,
        // DECIM=0 -> stored samples are exactly 2 apart (mod 2^16).
        gpmc_write(8'h34, 16'd10); gpmc_read(8'h38, v); seq = v;
        gpmc_write(8'h34, 16'd11); gpmc_read(8'h38, v); check(v - seq, 16'd2, "buf[11]-buf[10] spacing");
        gpmc_write(8'h34, 16'd12); gpmc_read(8'h38, v); check(v - seq, 16'd4, "buf[12]-buf[10] spacing");
        gpmc_write(8'h34, 16'd11); gpmc_read(8'h38, v); check(v - seq, 16'd2, "re-read buf[11] stable");

        if (errors==0) $display("=== RAWCAP_TB PASS ===");
        else           $display("=== RAWCAP_TB FAIL (%0d errors) ===", errors);
        $finish;
    end
endmodule

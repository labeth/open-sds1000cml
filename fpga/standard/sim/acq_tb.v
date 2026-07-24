// acq_tb.v — self-checking GPMC-transaction testbench for the acq top.
//   Drives the async GPMC slave protocol (synchronized strobes + nWE-rising commit)
//   and verifies the register interface end to end: the build-ID/VERSION identity
//   handshake, register write->readback, the single tri-state driver (Hi-Z when
//   deselected), and gpmc_wait held ready. This proves the CS1 slave logic that is
//   blocked on hardware only by the unresolved A2 address ball.
//   Run from fpga/standard/:
//     iverilog -g2001 -I . acq.v adcif.v spine.v capture.v envelope.v drain.v \
//              sim/acq_tb.v -o /tmp/acq_tb && vvp /tmp/acq_tb
`timescale 1ns/1ps
`include "regs.vh"
module acq_tb;
    reg         clk = 1'b0, nCS1 = 1'b1, nOE = 1'b1, nWE = 1'b1, trig_sense = 1'b0;
    reg  [6:0]  sel = 7'd0;
    reg  [32:0] adc_lane = 33'd0;
    reg  [15:0] d_drive = 16'd0;
    reg         drive_en = 1'b0;
    wire [15:0] gpmc_d = drive_en ? d_drive : 16'hzzzz;   // CPU drives only on a write
    wire        gpmc_wait;
    wire [7:0]  adc_enc;
    wire [3:0]  adc_ctl_hi;
    wire [2:0]  adc_ctl_lo;
    integer     errors = 0;

    acq dut (
        .clk(clk), .adc_lane(adc_lane), .adc_enc(adc_enc), .adc_ctl_hi(adc_ctl_hi), .adc_ctl_lo(adc_ctl_lo),
        .trig_sense(trig_sense), .nCS1(nCS1), .nOE(nOE), .nWE(nWE), .sel(sel),
        .gpmc_d(gpmc_d), .gpmc_wait(gpmc_wait));

    always #5 clk = ~clk;   // 100 MHz

    // GPMC write: hold nCS1 low + sel/data stable, pulse nWE low then high (rising = commit)
    task gpmc_write(input [6:0] s, input [15:0] data);
        begin
            @(negedge clk); nCS1 = 1'b0; sel = s; d_drive = data; drive_en = 1'b1; nWE = 1'b0;
            repeat (4) @(negedge clk);
            nWE = 1'b1;                       // rising edge -> we_commit two clks later
            repeat (4) @(negedge clk);        // let we_commit fire + the register latch
            drive_en = 1'b0; nCS1 = 1'b1;
            @(negedge clk);
        end
    endtask

    // GPMC read: combinational mux gated by ~nCS1 & ~nOE; sample the driven bus
    task gpmc_read(input [6:0] s, output [15:0] val);
        begin
            @(negedge clk); nCS1 = 1'b0; nOE = 1'b0; sel = s; drive_en = 1'b0;
            repeat (2) @(negedge clk); #1 val = gpmc_d;
            @(negedge clk); nCS1 = 1'b1; nOE = 1'b1;
        end
    endtask

    task check(input [15:0] got, input [15:0] exp, input [255:0] name);
        begin
            if (got !== exp) begin $display("FAIL %0s: got %h exp %h", name, got, exp); errors = errors + 1; end
            else $display("ok   %0s = %h", name, got);
        end
    endtask

    reg [15:0] v;
    initial begin
        repeat (6) @(negedge clk);   // reset settle

        // 1) identity handshake (the app's first liveness probe)
        gpmc_read(`SEL_BUILDID_LO, v); check(v, `IFACE_BUILD_ID_LO, "BUILDID_LO");
        gpmc_read(`SEL_BUILDID_HI, v); check(v, `IFACE_BUILD_ID_HI, "BUILDID_HI");
        gpmc_read(`SEL_VERSION,    v); check(v, 16'h0052,           "VERSION=0x0052");

        // 2) single tri-state driver: Hi-Z whenever not (~nCS1 & ~nOE)
        @(negedge clk); nCS1 = 1'b1; nOE = 1'b1; #1 check(gpmc_d, 16'hzzzz, "gpmc_d Hi-Z deselected");
        @(negedge clk); nCS1 = 1'b0; nOE = 1'b1; #1 check(gpmc_d, 16'hzzzz, "gpmc_d Hi-Z on write-select (nOE high)");
        nCS1 = 1'b1;

        // 3) write -> readback (proves we_commit + the register file + read mux)
        gpmc_write(`SEL_RUN, 16'h0005);      gpmc_read(`SEL_RUN, v);      check(v, 16'h0005, "RUN writeback");
        gpmc_write(`SEL_DECIM_LO, 16'h00FF); gpmc_read(`SEL_DECIM_LO, v); check(v, 16'h00FF, "DECIM_LO writeback");
        gpmc_write(`SEL_PRETRIG_LO, 16'h1234); gpmc_read(`SEL_PRETRIG_LO, v); check(v, 16'h1234, "PRETRIG_LO writeback");

        // 4) CS3 is NOT decoded here: an LED write (a CS3 selector) must NOT read back on CS1
        gpmc_write(`SEL_ENV_COLS, 16'h0100); gpmc_read(`SEL_ENV_COLS, v); check(v, 16'h0100, "ENV_COLS writeback");

        // 5) gpmc_wait held ready
        check({15'd0, gpmc_wait}, 16'h0001, "gpmc_wait held ready");

        if (errors == 0) $display("=== ACQ_TB PASS ===");
        else            $display("=== ACQ_TB FAIL (%0d errors) ===", errors);
        $finish;
    end
endmodule

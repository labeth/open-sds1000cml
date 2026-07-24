// adcif_tb.v — self-checking testbench for the ADC front-end (adcif.v).
//   Verifies: (1) the de-interleave packs {CH1[7:0],CH2[7:0]} from the data lanes through
//   the 2-stage pipeline; (2) the held mode controls are correct (ctl_hi=1s, ctl_lo=0s);
//   (3) the 8 ENCODE outputs all carry the divided clock and toggle. Run via sim/run.sh
//   (lives in sim/, NOT globbed into the Quartus build).
`timescale 1ns/1ps
module adcif_tb;
    reg         clk = 1'b0;
    reg  [32:0] adc_lane = 33'd0;
    wire [7:0]  adc_enc;
    wire [3:0]  adc_ctl_hi;
    wire [2:0]  adc_ctl_lo;
    wire [15:0] samp;
    integer     errors = 0;

    // ENCODE_DIV=2 -> enc_clk = clk/4, so it toggles within a few cycles for the test
    adcif #(.ENCODE_DIV(2)) dut (
        .clk(clk), .adc_lane(adc_lane),
        .adc_enc(adc_enc), .adc_ctl_hi(adc_ctl_hi), .adc_ctl_lo(adc_ctl_lo), .samp(samp));

    always #5 clk = ~clk;   // 100 MHz

    task expect_samp(input [15:0] exp, input [255:0] name);
        begin
            if (samp !== exp) begin $display("FAIL %0s: samp=%h expected %h", name, samp, exp); errors = errors + 1; end
            else $display("ok   %0s: samp=%h", name, samp);
        end
    endtask
    task settle; begin @(posedge clk); @(posedge clk); @(posedge clk); #1; end endtask

    // Channel-split masks (rawcap-diagnostic-derived): the lanes adcif routes to each byte.
    // CH1 = {0,1,3,5,6,9,11,12}, CH2 = {18,20,22,23,24,28,30,31} (disjoint sets).
    localparam [32:0] CH1_MASK = (33'd1<<0)|(33'd1<<1)|(33'd1<<3)|(33'd1<<5)|
                                 (33'd1<<6)|(33'd1<<9)|(33'd1<<11)|(33'd1<<12);
    localparam [32:0] CH2_MASK = (33'd1<<18)|(33'd1<<20)|(33'd1<<22)|(33'd1<<23)|
                                 (33'd1<<24)|(33'd1<<28)|(33'd1<<30)|(33'd1<<31);

    integer i; reg saw0, saw1;
    initial begin
        // all CH1 lanes high, all CH2 lanes low -> hi byte 0xFF, lo byte 0x00
        adc_lane = CH1_MASK;
        settle; expect_samp(16'hFF00, "CH1 lanes -> hi=FF, CH2 lanes clear -> lo=00");
        // all CH2 lanes high, all CH1 lanes low -> hi 0x00, lo 0xFF
        adc_lane = CH2_MASK;
        settle; expect_samp(16'h00FF, "CH2 lanes -> lo=FF, CH1 lanes clear -> hi=00");
        // both channels' lanes high -> 0xFFFF
        adc_lane = CH1_MASK | CH2_MASK;
        settle; expect_samp(16'hFFFF, "both channel lane sets -> FFFF");
        // lanes NOT in either channel set must not leak into samp
        adc_lane = ~(CH1_MASK | CH2_MASK) & 33'h1FFFFFFFF;
        settle; expect_samp(16'h0000, "non-channel lanes masked out");

        // held mode controls
        #1 if (adc_ctl_hi !== 4'b1111) begin $display("FAIL ctl_hi=%b", adc_ctl_hi); errors=errors+1; end
           else $display("ok   adc_ctl_hi held HIGH");
        if (adc_ctl_lo !== 3'b000) begin $display("FAIL ctl_lo=%b", adc_ctl_lo); errors=errors+1; end
           else $display("ok   adc_ctl_lo held LOW");

        // all 8 ENCODE outputs identical, and the clock actually toggles over time
        saw0 = 1'b0; saw1 = 1'b0;
        for (i = 0; i < 40; i = i + 1) begin
            @(posedge clk); #1;
            if (adc_enc !== {8{adc_enc[0]}}) begin $display("FAIL enc not uniform: %b", adc_enc); errors=errors+1; end
            if (adc_enc[0] === 1'b0) saw0 = 1'b1;
            if (adc_enc[0] === 1'b1) saw1 = 1'b1;
        end
        if (saw0 && saw1) $display("ok   all 8 ENCODE outputs carry the toggling clock");
        else begin $display("FAIL ENCODE did not toggle (saw0=%b saw1=%b)", saw0, saw1); errors=errors+1; end

        if (errors == 0) $display("=== ADCIF_TB PASS ===");
        else            $display("=== ADCIF_TB FAIL (%0d errors) ===", errors);
        $finish;
    end
endmodule

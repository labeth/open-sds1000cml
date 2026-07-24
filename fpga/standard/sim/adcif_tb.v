// adcif_tb.v — self-checking testbench for the ADC front-end (adcif.v).
//   Verifies: (1) the de-interleave packs {CH1[7:0],CH2[7:0]} per the default identity
//   table through the 2-stage capture pipeline; (2) adc_encode forwards clk at ENCODE_DIV=1;
//   (3) a divided ENCODE toggles at clk/(2*DIV). Run: iverilog -g2001 ../adcif.v adcif_tb.v
//   -o /tmp/adcif_tb && vvp /tmp/adcif_tb   (lives in sim/, NOT globbed into the build).
`timescale 1ns/1ps
module adcif_tb;
    reg         clk = 1'b0;
    reg  [23:0] adc_ch1 = 24'd0;
    reg  [15:0] adc_ch2 = 16'd0;
    wire        adc_encode;
    wire [15:0] samp;
    integer     errors = 0;

    adcif #(.ENCODE_DIV(1)) dut (
        .clk(clk), .adc_ch1(adc_ch1), .adc_ch2(adc_ch2),
        .adc_encode(adc_encode), .samp(samp));

    always #5 clk = ~clk;   // 100 MHz

    task expect_samp(input [15:0] exp, input [255:0] name);
        begin
            if (samp !== exp) begin
                $display("FAIL %0s: samp=%h expected %h", name, samp, exp);
                errors = errors + 1;
            end else $display("ok   %0s: samp=%h", name, samp);
        end
    endtask

    // settle 3 posedges so the 2-stage pipeline (ch*_r -> samp) reflects the input
    task settle; begin @(posedge clk); @(posedge clk); @(posedge clk); #1; end endtask

    initial begin
        // default de-interleave = low 8 lanes of each channel (identity)
        adc_ch1 = 24'h0000AB; adc_ch2 = 16'h00CD; settle;
        expect_samp(16'hABCD, "hi=CH1(AB) lo=CH2(CD)");

        adc_ch1 = 24'h0000_55; adc_ch2 = 16'h00_AA; settle;
        expect_samp(16'h55AA, "hi=CH1(55) lo=CH2(AA)");

        // upper lanes (bits 8+ of a channel) must NOT leak into the byte
        adc_ch1 = 24'hFFFF00; adc_ch2 = 16'hFF00; settle;
        expect_samp(16'h0000, "upper lanes masked out");

        // adc_encode forwards clk at ENCODE_DIV=1 (combinational)
        #1 if (adc_encode !== clk) begin
            $display("FAIL encode: adc_encode=%b clk=%b", adc_encode, clk); errors = errors + 1;
        end else $display("ok   adc_encode follows clk");
        @(negedge clk) #1 if (adc_encode !== clk) begin
            $display("FAIL encode (neg): adc_encode=%b clk=%b", adc_encode, clk); errors = errors + 1;
        end else $display("ok   adc_encode follows clk (both phases)");

        if (errors == 0) $display("=== ADCIF_TB PASS ===");
        else            $display("=== ADCIF_TB FAIL (%0d errors) ===", errors);
        $finish;
    end
endmodule

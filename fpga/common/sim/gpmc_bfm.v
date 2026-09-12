// gpmc_bfm.v -- bus-functional model of the AM3352 GPMC as seen by the CS1 slave (sim only).
// Timing in ns: an access is CS low ~100 ns with the strobe low 60-80 ns inside it, address and
// write data held from before CS to after CS; 50 ns cycle gap (the app's EDMA uses a 5-fclk gap).
`timescale 1ns/1ps
module gpmc_bfm (
    output reg        nCS1,
    output reg        nOE,
    output reg        nWE,
    output reg [6:0]  sel,
    inout  wire [15:0] d
);
    reg [15:0] dout  = 16'h0000;
    reg        drive = 1'b0;
    assign d = drive ? dout : 16'hzzzz;
    initial begin nCS1 = 1'b1; nOE = 1'b1; nWE = 1'b1; sel = 7'h00; end

    task write(input [6:0] s, input [15:0] v);
        begin
            sel = s; dout = v; drive = 1'b1;
            #10; nCS1 = 1'b0;
            #20; nWE = 1'b0;
            #60; nWE = 1'b1;
            #20; nCS1 = 1'b1;
            #5;  drive = 1'b0;
            #50;
        end
    endtask

    task read(input [6:0] s, output [15:0] v);
        begin
            sel = s;
            #10; nCS1 = 1'b0;
            #10; nOE = 1'b0;
            #80; v = d;
            #5;  nOE = 1'b1;
            #10; nCS1 = 1'b1;
            #50;
        end
    endtask
endmodule

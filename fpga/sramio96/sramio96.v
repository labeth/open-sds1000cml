// sramio96.v — EXTEST scaffold (ID 0xAD11): 92 SRAM-candidate balls as MAX-CURRENT outputs
// so JTAG-EXTEST drives them strongly for a controllability sweep. (4 ALTERA config pins removed.)
module sramio96 (input wire clk, output wire [91:0] io);
    reg t=0; always @(posedge clk) t<=~t;
    assign io = {92{t}};
endmodule

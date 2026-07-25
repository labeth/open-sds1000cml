// sramfp.v — SRAM control-pin fingerprint probe (ID 0xAD0E).
// Drives D14(clock) + the 5 Cyclone-controllable candidate nets (J2,K2,A11,C14,F2) from registers,
// tri-states everything else, reads DQ live (async, weak-pullup so Hi-Z=all-1s). Lets us apply the
// datasheet fingerprints: OE# = ball that (clock stopped) flips DQ driven<->Hi-Z; ADSC#/ADV# = balls
// whose level changes the clocked read; CE = balls that deselect (DQ->Hi-Z).
module sramfp (
    input wire clk,
    output wire sck_pin,          // D14
    output wire [4:0] ctl5,       // J2 K2 A11 C14 F2
    input  wire [21:0] dq,
    input wire nCS1, input wire nOE, input wire nWE, input wire [6:0] sel,
    inout wire [15:0] gpmc_d, output wire gpmc_wait
);
    reg [2:0] cs1_q=3'b111,we_q=3'b111; reg [6:0] sel_q1=0,sel_q2=0; reg [15:0] d_q1=0,d_q2=0;
    always @(posedge clk) begin
        cs1_q<={cs1_q[1:0],nCS1}; we_q<={we_q[1:0],nWE};
        sel_q1<=sel; sel_q2<=sel_q1; d_q1<=gpmc_d; d_q2<=d_q1;
    end
    wire cs1_low=(cs1_q[2]==0); wire we_commit=(we_q[2]==0)&&(we_q[1]==1);
    wire [7:0] wr_sel={1'b0,sel_q2[6:2],2'b00}; wire [7:0] rd_sel={1'b0,sel[6:2],2'b00};
    reg [4:0] ctl5r=5'h1F; reg d14lvl=0; reg [15:0] clkdiv=16'd25; reg [7:0] npulse=0;
    always @(posedge clk) if (we_commit&&cs1_low) case(wr_sel)
        8'h20: ctl5r<=d_q2[4:0]; 8'h24: d14lvl<=d_q2[0]; 8'h30: clkdiv<=d_q2;
        8'h34: npulse<=d_q2[7:0]; default:;
    endcase
    // clock: if npulse>0, emit that many pulses on D14; else D14 = static d14lvl
    reg sck=0; reg [15:0] dv=0; reg [7:0] np=0; reg pulsing=0;
    wire startp = we_commit && cs1_low && (wr_sel==8'h34);
    always @(posedge clk) begin
        if (startp) begin np<=d_q2[7:0]; pulsing<=(d_q2[7:0]!=0); dv<=0; sck<=0; end
        else if (pulsing) begin
            if (dv>=clkdiv) begin dv<=0; sck<=~sck; if (sck) begin if (np<=1) pulsing<=0; else np<=np-8'd1; end end
            else dv<=dv+16'd1;
        end
    end
    assign sck_pin = pulsing ? sck : d14lvl;
    assign ctl5 = ctl5r;
    reg [21:0] dqlive=0; always @(posedge clk) dqlive<=dq;
    reg [15:0] rdata;
    always @* case(rd_sel)
        8'h10: rdata=16'hAD0E;
        8'h54: rdata=dqlive[15:0]; 8'h58: rdata={10'd0,dqlive[21:16]};
        default: rdata=16'h0000;
    endcase
    wire ra=(~nCS1)&(~nOE); assign gpmc_d=ra?rdata:16'hzzzz; assign gpmc_wait=1'b1;
endmodule

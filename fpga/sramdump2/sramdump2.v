// sramdump2.v — dumper + counter-reset hunt (ID 0xAD0D).
// Like sramdump (drive ONLY D14 clock, tri-state the 29 MAX-V nets, read DQ) but ALSO drives the
// 5 OTHER Cyclone-controllable SRAM nets (J2,K2,A11,C14,F2) from a register, so we can hold each at
// a chosen level (or all high) and test whether any resets/holds the MAX-V read counter -> repeatable
// aligned dumps. ctl5[4:0] = levels for {J2,K2,A11,C14,F2}; default all-high (0x1F) = same as sramdump.
module sramdump2 (
    input wire clk,
    output wire sck_pin,                 // D14 clock
    output wire [4:0] ctl5,              // J2 K2 A11 C14 F2  (Cyclone-controllable nets)
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
    reg [15:0] clkdiv=16'd25; reg [7:0] ncyc=8'd64, ridx=0; reg [4:0] ctl5r=5'h1F;
    wire go = we_commit && cs1_low && (wr_sel==8'h50);
    always @(posedge clk) if (we_commit&&cs1_low) case(wr_sel)
        8'h30: clkdiv<=d_q2; 8'h34: ncyc<=d_q2[7:0]; 8'h4C: ridx<=d_q2[7:0]; 8'h2C: ctl5r<=d_q2[4:0]; default:;
    endcase
    assign ctl5 = ctl5r;
    reg running=0, sck=0, done=0; reg [15:0] dv=0; reg [7:0] cyc=0; reg [21:0] cbuf[0:255];
    always @(posedge clk) begin
        if (go) begin running<=1; cyc<=0; sck<=0; dv<=0; done<=0; end
        else if (running) begin
            if (dv>=clkdiv) begin dv<=0; sck<=~sck;
                if (sck) begin cbuf[cyc]<=dq; if (cyc>=ncyc-8'd1) begin running<=0; done<=1; end else cyc<=cyc+8'd1; end
            end else dv<=dv+16'd1;
        end
    end
    assign sck_pin = sck;
    reg [15:0] rdata;
    always @* case(rd_sel)
        8'h10: rdata=16'hAD0D; 8'h30: rdata={done,7'd0,cyc};
        8'h54: rdata=cbuf[ridx][15:0]; 8'h58: rdata={10'd0,cbuf[ridx][21:16]};
        default: rdata=16'h0000;
    endcase
    wire ra=(~nCS1)&(~nOE); assign gpmc_d=ra?rdata:16'hzzzz; assign gpmc_wait=1'b1;
endmodule

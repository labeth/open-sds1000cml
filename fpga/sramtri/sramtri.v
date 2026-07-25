// sramtri.v — tri-state MAX-V-detector (ID 0xAD0B).
// Drives NOTHING onto the SRAM bus: all 57 SRAM balls (35 addr/ctrl + 22 DQ) are pure inputs,
// weak-pullup OFF, no clock generated. If another device (MAX V U3) actively drives any SRAM
// control net, that ball will hold a firm 0/1 and/or TOGGLE over time. A truly Cyclone-only,
// currently-idle net floats (indeterminate). On GO: snapshot s0, then sample continuously and
// OR every change into `changed`; also keep the latest sample. Readback classifies each ball.
module sramtri (
    input wire clk,
    input  wire [56:0] sb,       // sb[34:0]=sram_ac balls, sb[56:35]=sram_dq balls (all inputs)
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
    wire go = we_commit && cs1_low && (wr_sel==8'h50);

    reg [56:0] cur=0, s0=0, changed=0, ones=0, zeros=0; reg running=0; reg [23:0] n=0; reg [4:0] ridx=0;
    always @(posedge clk) if (we_commit&&cs1_low && wr_sel==8'h4C) ridx<=d_q2[4:0];
    always @(posedge clk) begin
        cur<=sb;
        if (go) begin running<=1; s0<=sb; changed<=57'd0; ones<=57'd0; zeros<=57'd0; n<=0; end
        else if (running) begin
            changed<=changed | (cur ^ s0);
            ones<=ones | cur; zeros<=zeros | (~cur);
            if (n>=24'd1000000) running<=0; else n<=n+1;   // ~sample for a while
        end
    end
    // readback: 0x30..0x3C changed(57b, 4 words); 0x40.. ones; 0x50.. zeros; 0x60 running
    reg [15:0] rdata;
    always @* case(rd_sel)
        8'h10: rdata=16'hAD0B;
        8'h30: rdata=changed[15:0]; 8'h34: rdata=changed[31:16]; 8'h38: rdata=changed[47:32]; 8'h3C: rdata={7'd0,changed[56:48]};
        8'h40: rdata=ones[15:0];    8'h44: rdata=ones[31:16];    8'h48: rdata=ones[47:32];    8'h4C: rdata={7'd0,ones[56:48]};
        8'h50: rdata=zeros[15:0];   8'h54: rdata=zeros[31:16];   8'h58: rdata=zeros[47:32];   8'h5C: rdata={7'd0,zeros[56:48]};
        8'h60: rdata={15'd0,running};
        default: rdata=16'h0000;
    endcase
    wire ra=(~nCS1)&(~nOE); assign gpmc_d=ra?rdata:16'hzzzz; assign gpmc_wait=1'b1;
endmodule

// sramdump.v — non-contending SRAM reader (ID 0xAD0C).
// Drives ONLY D14 (the SRAM clock, the one control net that is Cyclone-controllable). ALL other
// SRAM address/control balls are left UNUSED -> tri-stated (RESERVE_ALL_UNUSED = input tri-state),
// so the MAX V keeps driving the address/control with NO contention from us. We clock D14 to advance
// the (MAX-V-owned) read counter and capture DQ each edge into a buffer -> a sequential SRAM dump.
// Use after the vendor fills the SRAM (fast-timebase acquisition) then STOPs.
module sramdump (
    input wire clk,
    output wire sck_pin,           // D14 = SRAM clock (only driven net)
    input  wire [21:0] dq,         // 22 SRAM DQ candidates (read)
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

    reg [15:0] clkdiv=16'd25; reg [7:0] ncyc=8'd64, ridx=0;
    wire go = we_commit && cs1_low && (wr_sel==8'h50);
    always @(posedge clk) if (we_commit&&cs1_low) case(wr_sel)
        8'h30: clkdiv<=d_q2; 8'h34: ncyc<=d_q2[7:0]; 8'h4C: ridx<=d_q2[7:0]; default:;
    endcase
    reg running=0, sck=0, done=0; reg [15:0] dv=0; reg [7:0] cyc=0;
    reg [21:0] cbuf[0:255];
    always @(posedge clk) begin
        if (go) begin running<=1; cyc<=0; sck<=0; dv<=0; done<=0; end
        else if (running) begin
            if (dv>=clkdiv) begin
                dv<=0; sck<=~sck;
                if (sck) begin cbuf[cyc]<=dq; if (cyc>=ncyc-8'd1) begin running<=0; done<=1; end else cyc<=cyc+8'd1; end
            end else dv<=dv+16'd1;
        end
    end
    assign sck_pin = sck;
    reg [15:0] rdata;
    always @* case(rd_sel)
        8'h10: rdata=16'hAD0C;
        8'h30: rdata={done,7'd0,cyc};
        8'h54: rdata=cbuf[ridx][15:0]; 8'h58: rdata={10'd0,cbuf[ridx][21:16]};
        default: rdata=16'h0000;
    endcase
    wire ra=(~nCS1)&(~nOE); assign gpmc_d=ra?rdata:16'hzzzz; assign gpmc_wait=1'b1;
endmodule

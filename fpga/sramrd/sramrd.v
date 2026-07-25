// sramrd.v — flexible SRAM read-discovery controller (auto-gen, 35 drive / 22 read).
module sramrd (
    input wire clk,
    output wire [34:0] sram_ac,
    inout  wire [21:0] sram_dq,
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
    reg [34:0] pat=0; reg [7:0] clk_sel=8'hFF; reg [15:0] clkdiv=16'd8;
    always @(posedge clk) if (we_commit&&cs1_low) case(wr_sel)
        8'h20: pat[15:0]<=d_q2;
        8'h24: pat[31:16]<=d_q2[15:0];
        8'h28: pat[34:32]<=d_q2[2:0];
        8'h2C: clk_sel<=d_q2[7:0];
        8'h30: clkdiv<=d_q2;
        8'h40: wdata[15:0]<=d_q2;
        8'h44: wdata[21:16]<=d_q2[5:0];
        8'h48: drive_dq<=d_q2[0];
        default:;
    endcase
    reg [21:0] wdata=0; reg drive_dq=0;
    assign sram_dq = drive_dq ? wdata : 22'bz;   // drive DQ for writes; else Hi-Z (read)
    // clock: freerun (0x5c=1) -> continuous clock on clk_sel; else STEP(0x34)=N pulses.
    reg sck=0; reg [15:0] dv=0; reg [15:0] npulse=0; reg freerun=0;
    wire step_wr = we_commit && cs1_low && (wr_sel==8'h34);
    always @(posedge clk) begin
        if (we_commit && cs1_low && wr_sel==8'h5C) freerun<=d_q2[0];
        if (freerun) begin if (dv>=clkdiv) begin dv<=0; sck<=~sck; end else dv<=dv+16'd1; end
        else if (step_wr) begin npulse<=d_q2; dv<=0; end
        else if (npulse!=0) begin
            if (dv>=clkdiv) begin dv<=0; sck<=~sck; npulse<=npulse-16'd1; end else dv<=dv+16'd1;
        end
    end
    assign sram_ac[0] = (clk_sel==8'd0) ? sck : pat[0];
    assign sram_ac[1] = (clk_sel==8'd1) ? sck : pat[1];
    assign sram_ac[2] = (clk_sel==8'd2) ? sck : pat[2];
    assign sram_ac[3] = (clk_sel==8'd3) ? sck : pat[3];
    assign sram_ac[4] = (clk_sel==8'd4) ? sck : pat[4];
    assign sram_ac[5] = (clk_sel==8'd5) ? sck : pat[5];
    assign sram_ac[6] = (clk_sel==8'd6) ? sck : pat[6];
    assign sram_ac[7] = (clk_sel==8'd7) ? sck : pat[7];
    assign sram_ac[8] = (clk_sel==8'd8) ? sck : pat[8];
    assign sram_ac[9] = (clk_sel==8'd9) ? sck : pat[9];
    assign sram_ac[10] = (clk_sel==8'd10) ? sck : pat[10];
    assign sram_ac[11] = (clk_sel==8'd11) ? sck : pat[11];
    assign sram_ac[12] = (clk_sel==8'd12) ? sck : pat[12];
    assign sram_ac[13] = (clk_sel==8'd13) ? sck : pat[13];
    assign sram_ac[14] = (clk_sel==8'd14) ? sck : pat[14];
    assign sram_ac[15] = (clk_sel==8'd15) ? sck : pat[15];
    assign sram_ac[16] = (clk_sel==8'd16) ? sck : pat[16];
    assign sram_ac[17] = (clk_sel==8'd17) ? sck : pat[17];
    assign sram_ac[18] = (clk_sel==8'd18) ? sck : pat[18];
    assign sram_ac[19] = (clk_sel==8'd19) ? sck : pat[19];
    assign sram_ac[20] = (clk_sel==8'd20) ? sck : pat[20];
    assign sram_ac[21] = (clk_sel==8'd21) ? sck : pat[21];
    assign sram_ac[22] = (clk_sel==8'd22) ? sck : pat[22];
    assign sram_ac[23] = (clk_sel==8'd23) ? sck : pat[23];
    assign sram_ac[24] = (clk_sel==8'd24) ? sck : pat[24];
    assign sram_ac[25] = (clk_sel==8'd25) ? sck : pat[25];
    assign sram_ac[26] = (clk_sel==8'd26) ? sck : pat[26];
    assign sram_ac[27] = (clk_sel==8'd27) ? sck : pat[27];
    assign sram_ac[28] = (clk_sel==8'd28) ? sck : pat[28];
    assign sram_ac[29] = (clk_sel==8'd29) ? sck : pat[29];
    assign sram_ac[30] = (clk_sel==8'd30) ? sck : pat[30];
    assign sram_ac[31] = (clk_sel==8'd31) ? sck : pat[31];
    assign sram_ac[32] = (clk_sel==8'd32) ? sck : pat[32];
    assign sram_ac[33] = (clk_sel==8'd33) ? sck : pat[33];
    assign sram_ac[34] = (clk_sel==8'd34) ? sck : pat[34];
    reg [21:0] dl=0;
    always @(posedge clk) dl<=sram_dq;
    reg [15:0] rdata;
    always @* case(rd_sel)
        8'h10: rdata=16'hAD05;
        8'h38: rdata=dl[15:0]; 8'h3C: rdata={10'd0,dl[21:16]};
        default: rdata=16'h0000;
    endcase
    wire ra=(~nCS1)&(~nOE); assign gpmc_d=ra?rdata:16'hzzzz; assign gpmc_wait=1'b1;
endmodule

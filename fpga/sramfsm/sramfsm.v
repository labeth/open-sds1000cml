// sramfsm.v — hardware-timed SRAM read FSM (deterministic load+hold+capture).
module sramfsm (
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
    reg [34:0] pat_load=0, pat_hold=0; reg [7:0] clk_sel=8'hFF; reg [15:0] clkdiv=16'd4;
    reg [7:0] lat=8'd3; reg [21:0] wdata=0; reg drive_dq=0;
    wire go = we_commit&&cs1_low&&(wr_sel==8'h50);
    always @(posedge clk) if (we_commit&&cs1_low) case(wr_sel)
        8'h20: pat_load[15:0]<=d_q2; 8'h24: pat_load[31:16]<=d_q2; 8'h28: pat_load[34:32]<=d_q2[2:0];
        8'h40: pat_hold[15:0]<=d_q2; 8'h44: pat_hold[31:16]<=d_q2; 8'h48: pat_hold[34:32]<=d_q2[2:0];
        8'h2C: clk_sel<=d_q2[7:0]; 8'h30: clkdiv<=d_q2; 8'h4C: lat<=d_q2[7:0];
        8'h54: wdata[15:0]<=d_q2; 8'h58: drive_dq<=d_q2[0];
        default:;
    endcase
    reg [1:0] fsm=0; reg [7:0] cnt=0; reg fclk=0; reg [15:0] fdv=0; reg [21:0] cap=0;
    always @(posedge clk) begin
        case(fsm)
        2'd0: if (go) begin fsm<=1; fclk<=0; fdv<=0; cnt<=0; end
        2'd1: begin
            if (fdv>=clkdiv) begin fdv<=0; fclk<=~fclk; if (fclk) begin fsm<=2; cnt<=0; end end
            else fdv<=fdv+16'd1;
        end
        2'd2: begin
            if (fdv>=clkdiv) begin fdv<=0; fclk<=~fclk;
                if (fclk) begin cnt<=cnt+8'd1; if (cnt>=lat) begin cap<=sram_dq; fsm<=0; end end
            end else fdv<=fdv+16'd1;
        end
        default: fsm<=0;
        endcase
    end
    wire [34:0] curpat = (fsm==2'd1) ? pat_load : pat_hold;
    assign sram_ac[0] = (clk_sel==8'd0) ? fclk : curpat[0];
    assign sram_ac[1] = (clk_sel==8'd1) ? fclk : curpat[1];
    assign sram_ac[2] = (clk_sel==8'd2) ? fclk : curpat[2];
    assign sram_ac[3] = (clk_sel==8'd3) ? fclk : curpat[3];
    assign sram_ac[4] = (clk_sel==8'd4) ? fclk : curpat[4];
    assign sram_ac[5] = (clk_sel==8'd5) ? fclk : curpat[5];
    assign sram_ac[6] = (clk_sel==8'd6) ? fclk : curpat[6];
    assign sram_ac[7] = (clk_sel==8'd7) ? fclk : curpat[7];
    assign sram_ac[8] = (clk_sel==8'd8) ? fclk : curpat[8];
    assign sram_ac[9] = (clk_sel==8'd9) ? fclk : curpat[9];
    assign sram_ac[10] = (clk_sel==8'd10) ? fclk : curpat[10];
    assign sram_ac[11] = (clk_sel==8'd11) ? fclk : curpat[11];
    assign sram_ac[12] = (clk_sel==8'd12) ? fclk : curpat[12];
    assign sram_ac[13] = (clk_sel==8'd13) ? fclk : curpat[13];
    assign sram_ac[14] = (clk_sel==8'd14) ? fclk : curpat[14];
    assign sram_ac[15] = (clk_sel==8'd15) ? fclk : curpat[15];
    assign sram_ac[16] = (clk_sel==8'd16) ? fclk : curpat[16];
    assign sram_ac[17] = (clk_sel==8'd17) ? fclk : curpat[17];
    assign sram_ac[18] = (clk_sel==8'd18) ? fclk : curpat[18];
    assign sram_ac[19] = (clk_sel==8'd19) ? fclk : curpat[19];
    assign sram_ac[20] = (clk_sel==8'd20) ? fclk : curpat[20];
    assign sram_ac[21] = (clk_sel==8'd21) ? fclk : curpat[21];
    assign sram_ac[22] = (clk_sel==8'd22) ? fclk : curpat[22];
    assign sram_ac[23] = (clk_sel==8'd23) ? fclk : curpat[23];
    assign sram_ac[24] = (clk_sel==8'd24) ? fclk : curpat[24];
    assign sram_ac[25] = (clk_sel==8'd25) ? fclk : curpat[25];
    assign sram_ac[26] = (clk_sel==8'd26) ? fclk : curpat[26];
    assign sram_ac[27] = (clk_sel==8'd27) ? fclk : curpat[27];
    assign sram_ac[28] = (clk_sel==8'd28) ? fclk : curpat[28];
    assign sram_ac[29] = (clk_sel==8'd29) ? fclk : curpat[29];
    assign sram_ac[30] = (clk_sel==8'd30) ? fclk : curpat[30];
    assign sram_ac[31] = (clk_sel==8'd31) ? fclk : curpat[31];
    assign sram_ac[32] = (clk_sel==8'd32) ? fclk : curpat[32];
    assign sram_ac[33] = (clk_sel==8'd33) ? fclk : curpat[33];
    assign sram_ac[34] = (clk_sel==8'd34) ? fclk : curpat[34];
    assign sram_dq = drive_dq ? wdata : 22'bz;
    reg [15:0] rdata;
    always @* case(rd_sel)
        8'h10: rdata=16'hAD06;
        8'h30: rdata={13'd0,fsm,1'b0}; 8'h38: rdata=cap[15:0]; 8'h3C: rdata={10'd0,cap[21:16]};
        default: rdata=16'h0000;
    endcase
    wire ra=(~nCS1)&(~nOE); assign gpmc_d=ra?rdata:16'hzzzz; assign gpmc_wait=1'b1;
endmodule

// sramwide.v — SRAM data-bus re-map (ID 0xAD09).
// Hypothesis: the 14 "missing" SRAM DQ bits are among the 21 balls that were INERT when driven
// (a real DQ output ignores an FPGA drive). So: DRIVE clock (D14) + the 8 effective control balls
// in a streaming config, and READ the 21 inert balls + 22 known DQ. During a clocked read stream,
// true data bits VARY; non-data balls stay constant. `changed` = bits that ever moved.
module sramwide (
    input wire clk,
    output wire [7:0] drv,       // 8 effective control balls (driven, static per drvpat)
    output wire sck_pin,         // D14 SRAM clock
    input  wire [42:0] rdc,      // 43 read candidates: 21 inert + 22 known DQ
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

    // drv order: [K2,L1,N1,R6,R7,T3,F7,A11]; default T3(bit5) low, rest high = 0xDF (stream config)
    reg [7:0] drvpat=8'hDF; reg [15:0] clkdiv=16'd25; reg [4:0] ncyc=5'd16;
    wire go = we_commit && cs1_low && (wr_sel==8'h50);
    always @(posedge clk) if (we_commit&&cs1_low) case(wr_sel)
        8'h2C: drvpat<=d_q2[7:0]; 8'h30: clkdiv<=d_q2; 8'h34: ncyc<=d_q2[4:0]; default:;
    endcase
    assign drv = drvpat; assign sck_pin = sck;

    reg running=0, sck=0, done=0; reg [15:0] dv=0; reg [4:0] cyc=0;
    reg [42:0] first=0, changed=0;
    always @(posedge clk) begin
        if (go) begin running<=1'b1; cyc<=0; sck<=0; dv<=0; done<=0; changed<=43'd0; end
        else if (running) begin
            if (dv>=clkdiv) begin
                dv<=0; sck<=~sck;
                if (sck) begin                       // falling edge: sample rdc
                    if (cyc==0) first<=rdc;
                    else changed<=changed | (rdc ^ first);
                    if (cyc>=ncyc-1) begin running<=0; done<=1; end else cyc<=cyc+1;
                end
            end else dv<=dv+1;
        end
    end

    reg [15:0] rdata;
    always @* case(rd_sel)
        8'h10: rdata=16'hAD09;
        8'h38: rdata=changed[15:0]; 8'h3C: rdata=changed[31:16]; 8'h40: rdata={5'd0,changed[42:32]};
        8'h44: rdata=first[15:0];   8'h48: rdata=first[31:16];   8'h4C: rdata={5'd0,first[42:32]};
        8'h30: rdata={done,10'd0,cyc};
        default: rdata=16'h0000;
    endcase
    wire ra=(~nCS1)&(~nOE); assign gpmc_d=ra?rdata:16'hzzzz; assign gpmc_wait=1'b1;
endmodule

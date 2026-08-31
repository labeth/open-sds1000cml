// sramseq.v — Phase-A synchronous SRAM read engine (S7A163630M, 512Kx36 SPB).
// Fixes the two root-cause bugs the datasheet analysis exposed:
//   (1) DQ is captured on the FALLING edge of the SRAM clock (sck), a half period
//       after the rising edge that updates Q, then frozen — no async fast-clock sampling.
//   (2) All 35 driven balls default HIGH (active-low controls deasserted) so an idle
//       or mis-set vector cannot assert GW#/BW# and corrupt the array.
// On GO it runs `ncyc` sck cycles driving pat_load on cyc0 (address load, ADSC# low)
// and pat_hold on cyc>=1 (hold: ADSC# high, ADV# high), capturing DQ each falling edge
// into cbuf[0..15].  buf[1..] constant => deterministic read; period-4 => burst advancing.
module sramseq (
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

    // all balls default HIGH => every active-low control deasserted (safe, no writes)
    reg [34:0] pat_load=35'h7FFFFFFFF, pat_hold=35'h7FFFFFFFF;
    reg [7:0] clk_sel=8'd29;              // D14 = identified SRAM clock ball
    reg [15:0] clkdiv=16'd25;             // sck half-period in internal-clk cycles
    reg [4:0] ncyc=5'd16, ridx=5'd0;
    reg [21:0] wdata=0; reg drive_dq=0;
    reg [34:0] pat_alt=35'd0;             // bits here TOGGLE every sck cycle (Phase-E address alternation)
    wire go = we_commit && cs1_low && (wr_sel==8'h50);
    always @(posedge clk) if (we_commit&&cs1_low) case(wr_sel)
        8'h20: pat_load[15:0]<=d_q2;   8'h24: pat_load[31:16]<=d_q2;  8'h28: pat_load[34:32]<=d_q2[2:0];
        8'h40: pat_hold[15:0]<=d_q2;   8'h44: pat_hold[31:16]<=d_q2;  8'h48: pat_hold[34:32]<=d_q2[2:0];
        8'h60: pat_alt[15:0]<=d_q2;    8'h64: pat_alt[31:16]<=d_q2;   8'h68: pat_alt[34:32]<=d_q2[2:0];
        8'h2C: clk_sel<=d_q2[7:0];     8'h30: clkdiv<=d_q2;           8'h34: ncyc<=d_q2[4:0];
        8'h4C: ridx<=d_q2[4:0];        8'h54: wdata[15:0]<=d_q2;      8'h58: wdata[21:16]<=d_q2[5:0];
        8'h5C: drive_dq<=d_q2[0];      default:;
    endcase

    // sequencer: divide clk to make sck; capture DQ on each falling edge; freeze after ncyc
    reg running=0, sck=0, done=0; reg [15:0] dv=0; reg [4:0] cyc=0;
    reg [21:0] cbuf[0:15];
    always @(posedge clk) begin
        if (go) begin running<=1'b1; cyc<=5'd0; sck<=1'b0; dv<=16'd0; done<=1'b0; end
        else if (running) begin
            if (dv>=clkdiv) begin
                dv<=16'd0; sck<=~sck;
                if (sck) begin                     // 1->0 : falling edge, Q settled
                    cbuf[cyc]<=sram_dq;
                    if (cyc>=ncyc-5'd1) begin running<=1'b0; done<=1'b1; end
                    else cyc<=cyc+5'd1;
                end
            end else dv<=dv+16'd1;
        end
    end

    wire [34:0] pbase = (running && cyc==5'd0) ? pat_load : pat_hold;
    wire [34:0] pnow  = pbase ^ (cyc[0] ? pat_alt : 35'd0);   // toggle alt bits on odd cycles
    genvar i;
    generate for (i=0;i<35;i=i+1) begin: drv
        assign sram_ac[i] = (clk_sel==i) ? sck : pnow[i];
    end endgenerate
    assign sram_dq = drive_dq ? wdata : 22'bz;

    // live async DQ sample (for zero-clock OE test: idle drives pat_hold, DQ is Hi-Z-read)
    reg [21:0] dqlive=0; always @(posedge clk) dqlive<=sram_dq;

    reg [15:0] rdata;
    always @* case(rd_sel)
        8'h10: rdata=16'hAD08;
        8'h30: rdata={done,10'd0,cyc};
        8'h38: rdata=cbuf[ridx][15:0];
        8'h3C: rdata={10'd0,cbuf[ridx][21:16]};
        8'h44: rdata=dqlive[15:0];
        8'h48: rdata={10'd0,dqlive[21:16]};
        default: rdata=16'h0000;
    endcase
    wire ra=(~nCS1)&(~nOE); assign gpmc_d=ra?rdata:16'hzzzz; assign gpmc_wait=1'b1;
endmodule

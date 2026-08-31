// sramgold2.v — parameterized SPB-SRAM write->read self-test on the RUN/STOP+EXTEST-VERIFIED
// Cyclone-driven SRAM address/control bus (ID 0xAD12). Brute-force the control roles from the host
// over GPMC; nbad==0 proves the protocol (write ramp M[i]=pat(i), read back, compare).
//
// sram_ac[23:0] = 24 verified addr/ctrl balls; sram_ac[24] = D14 (SRAM clock).
// sram_dq[15:0] = 16 non-ADC DQ candidates.  pass_pin/done_pin = spare balls for JTAG-SAMPLE readback
//                 (in case GPMC register reads are sel-stuck for custom bitstreams).
//
// GPMC config regs (CS1 writes; sel=byte addr, data=gpmc_d):
//   0x20/24 low_mask[24:0]  balls held static LOW (CS#/OE#/CE candidates)
//   0x40/44 addr_mask[24:0] balls carrying the address counter (spread lsb..msb)
//   0x2C load_sel  ball pulsed LOW at cc0 of each access (ADSC#/ADSP#)
//   0x30 we_sel    ball pulsed LOW during WRITE accesses (GW#/WE#); 0x3F=none
//   0x34 ncnt (<=64)   0x38 clkdiv   0x3C cap(read-capture cycle)   0x4C ridx
//   0x50 GO   bit0=start; bit1: 0=WRITE-ramp, 1=READ-verify
// Readback: 0x10 ID=0xAD12; 0x30 {done,cyc/i}; 0x54 rbuf[ridx]; 0x5C nbad; 0x58 {pass,done,nbad}
module sramgold2 (
    input wire clk,
    output wire [36:0] sram_ac,
    inout  wire [14:0] sram_dq,
    output wire pass_pin, output wire done_pin,
    input wire nCS1, input wire nOE, input wire nWE, input wire [6:0] sel,
    inout wire [15:0] gpmc_d, output wire gpmc_wait
);
    localparam CLK_IDX = 36;
    reg [2:0] cs1_q=3'b111,we_q=3'b111; reg [6:0] sel_q1=0,sel_q2=0; reg [15:0] d_q1=0,d_q2=0;
    always @(posedge clk) begin
        cs1_q<={cs1_q[1:0],nCS1}; we_q<={we_q[1:0],nWE};
        sel_q1<=sel; sel_q2<=sel_q1; d_q1<=gpmc_d; d_q2<=d_q1;
    end
    wire cs1_low=(cs1_q[2]==0); wire we_commit=(we_q[2]==0)&&(we_q[1]==1);
    wire [7:0] wr_sel={1'b0,sel_q2[6:2],2'b00}; wire [7:0] rd_sel={1'b0,sel[6:2],2'b00};

    reg [36:0] low_mask=0, addr_mask=0;
    reg [7:0] load_sel=8'd0, we_sel=8'h3F;
    reg [6:0] ncnt=7'd16, ridx=0; reg [15:0] clkdiv=16'd25; reg [3:0] cap=4'd2;
    wire go = we_commit && cs1_low && (wr_sel==8'h50);
    always @(posedge clk) if (we_commit&&cs1_low) case(wr_sel)
        8'h20: low_mask[15:0]<=d_q2;   8'h24: low_mask[31:16]<=d_q2;
        8'h40: addr_mask[15:0]<=d_q2;  8'h44: addr_mask[31:16]<=d_q2;
        8'h28: low_mask[36:32]<=d_q2[4:0]; 8'h48: addr_mask[36:32]<=d_q2[4:0]; 8'h2C: load_sel<=d_q2[7:0]; 8'h30: we_sel<=d_q2[7:0]; 8'h34: ncnt<=d_q2[6:0];
        8'h38: clkdiv<=d_q2; 8'h3C: cap<=d_q2[3:0]; 8'h4C: ridx<=d_q2[6:0]; default:;
    endcase

    function [36:0] spread(input [19:0] a); integer k,p; begin
        spread=37'd0; p=0;
        for (k=0;k<37;k=k+1) if (addr_mask[k]) begin spread[k]=a[p]; p=p+1; end
    end endfunction

    reg phase=0, running=0, sck=0, done=0; reg [15:0] dv=0;
    reg [3:0] cc=0; reg [6:0] i=0, nbad=0; reg [15:0] rbuf[0:63];
    reg load_low=0, we_low=0, drive_dq=0; reg [15:0] wdata=0, dl=0;
    wire [7:0] pat = {1'b0,i} ^ 8'h5A;
    wire [36:0] apat = spread({13'd0,i});

    genvar g;
    generate for (g=0; g<37; g=g+1) begin: drv
        assign sram_ac[g] = (g==CLK_IDX)  ? sck :
                            (load_sel==g) ? ~load_low :
                            (we_sel==g)   ? ~we_low :
                            low_mask[g]   ? 1'b0 :
                            addr_mask[g]  ? apat[g] : 1'b1;
    end endgenerate
    assign sram_dq = drive_dq ? wdata[14:0] : 15'bz;

    always @(posedge clk) if (running && !sck && dv==0 && cc==cap) dl<=sram_dq;

    always @(posedge clk) begin
        if (go) begin running<=1; phase<=d_q2[1]; i<=0; cc<=0; sck<=0; dv<=0; done<=0; nbad<=0;
                     load_low<=0; we_low<=0; drive_dq<=0; end
        else if (running) begin
            if (dv>=clkdiv) begin
                dv<=0; sck<=~sck;
                if (sck) begin
                    if (cc>=4'd7) begin
                        if (phase==1 && i<64) begin
                            rbuf[i]<=dl;
                            if (dl[7:0]!=pat) nbad<=nbad+1;
                        end
                        if (i>=ncnt-1) begin running<=0; done<=1; end
                        else begin i<=i+1; cc<=0; end
                    end else cc<=cc+1;
                end
            end else dv<=dv+1;
        end
        load_low <= running && (cc==0);
        we_low   <= running && (phase==0) && (cc==0);
        drive_dq <= running && (phase==0) && (cc>=1) && (cc<=2);
        if (phase==0) wdata <= {8'd0, pat};
    end

    assign pass_pin = done && (nbad==0);
    assign done_pin = done;

    reg [15:0] rdata;
    always @* case(rd_sel)
        8'h10: rdata=16'hAD12;
        8'h30: rdata={done,8'd0,i};
        8'h54: rdata=rbuf[ridx];
        8'h5C: rdata={9'd0,nbad};
        8'h58: rdata={pass_pin,done,5'd0,1'b0,nbad[6:0]};
        default: rdata=16'h0000;
    endcase
    wire ra=(~nCS1)&(~nOE); assign gpmc_d=ra?rdata:16'hzzzz; assign gpmc_wait=1'b1;
endmodule

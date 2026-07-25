// sramgold.v — SPB SRAM read/write round-trip controller on the EXTEST-VERIFIED ball map (ID 0xAD10).
//
// sram_ac[24:0] = the 25 EXTEST-verified SRAM address/control balls (A11 B11 C14 F1 F2 G1 G2 J1 J12
//                 J13 J14 J15 J2 K1 K10 K2 K6 K8 K9 L10 L8 L9 M7 M8 P6), sram_ac[25]=D14 (clock).
// sram_dq[21:0] = the 22 verified DQ balls.  All driven at MAXIMUM CURRENT; unused pins tri-stated.
//
// Two oracles, both host-driven, both immune to the async-EXTEST sampling problem (DQ is captured
// synchronously in hardware while sck is low and the SPB pipeline output is stable):
//   READ-ONLY  (GO bit1=1): for i in 0..ncnt-1 present address i, pulse the load strobe, clock `cap`
//                cycles, capture DQ -> rbuf[i].  Host checks determinism (same across runs) AND
//                addressability (rbuf varies with i) -> finds the true ADSC# with zero write risk.
//   WRITE->READ (GO bit1=0 then =1): write M[i]=pat(i), read back, nbad = mismatches. nbad==0 PROVES
//                the full protocol (load+address+write-enable) regardless of address bit-order.
//
// Config regs (GPMC CS1 writes, sel = byte addr; data = gpmc_d):
//   0x20/24 low_mask[25:0]  — balls held statically LOW (chip-select / CE candidates)
//   0x40/44 addr_mask[25:0] — balls carrying the address counter (spread lsb..msb in ball index order)
//   0x2C load_sel  — ball index (0..25) pulsed LOW at cc==0 of every access (ADSC#/ADSP#)
//   0x30 we_sel    — ball index pulsed LOW during WRITE accesses (GW#/BWE#); 0x3F=none
//   0x34 ncnt      — addresses to sweep (<=64)
//   0x38 clkdiv    — sck half period (in 50MHz clks)
//   0x3C cap       — capture cycle: DQ sampled at cc==cap (sweep to find the read pipeline latency)
//   0x4C ridx      — read-buffer index for readback
//   0x50 GO        — bit0=start; bit1: 0=WRITE-ramp phase, 1=READ-verify phase
// Readback: 0x10 ID=0xAD10; 0x30 {done,cyc/i}; 0x54 rbuf[ridx][15:0]; 0x58 rbuf[ridx][21:16];
//           0x5C nbad; 0x60 low16 of last captured; (rbuf holds full 22-bit words)
module sramgold (
    input wire clk,
    output wire [25:0] sram_ac,      // [24:0]=25 ctrl balls, [25]=D14 clock
    inout  wire [21:0] sram_dq,
    input wire nCS1, input wire nOE, input wire nWE, input wire [6:0] sel,
    inout wire [15:0] gpmc_d, output wire gpmc_wait
);
    localparam CLK_IDX = 25;         // D14 = sram_ac[25]
    // ---- GPMC front-end (identical convention to sramctl) ----
    reg [2:0] cs1_q=3'b111,we_q=3'b111; reg [6:0] sel_q1=0,sel_q2=0; reg [15:0] d_q1=0,d_q2=0;
    always @(posedge clk) begin
        cs1_q<={cs1_q[1:0],nCS1}; we_q<={we_q[1:0],nWE};
        sel_q1<=sel; sel_q2<=sel_q1; d_q1<=gpmc_d; d_q2<=d_q1;
    end
    wire cs1_low=(cs1_q[2]==0); wire we_commit=(we_q[2]==0)&&(we_q[1]==1);
    wire [7:0] wr_sel={1'b0,sel_q2[6:2],2'b00}; wire [7:0] rd_sel={1'b0,sel[6:2],2'b00};

    reg [25:0] low_mask=0, addr_mask=0;
    reg [7:0] load_sel=8'd0, we_sel=8'h3F;
    reg [6:0] ncnt=7'd16, ridx=0; reg [15:0] clkdiv=16'd25; reg [3:0] cap=4'd2;
    wire go = we_commit && cs1_low && (wr_sel==8'h50);
    always @(posedge clk) if (we_commit&&cs1_low) case(wr_sel)
        8'h20: low_mask[15:0]<=d_q2;   8'h24: low_mask[25:16]<=d_q2[9:0];
        8'h40: addr_mask[15:0]<=d_q2;  8'h44: addr_mask[25:16]<=d_q2[9:0];
        8'h2C: load_sel<=d_q2[7:0]; 8'h30: we_sel<=d_q2[7:0]; 8'h34: ncnt<=d_q2[6:0];
        8'h38: clkdiv<=d_q2; 8'h3C: cap<=d_q2[3:0]; 8'h4C: ridx<=d_q2[6:0]; default:;
    endcase

    // ---- address spread onto addr_mask balls ----
    function [25:0] spread(input [19:0] a); integer k,p; begin
        spread=26'd0; p=0;
        for (k=0;k<26;k=k+1) if (addr_mask[k]) begin spread[k]=a[p]; p=p+1; end
    end endfunction

    // ---- FSM ----
    reg phase=0, running=0, sck=0, done=0; reg [15:0] dv=0;
    reg [3:0] cc=0; reg [6:0] i=0, nbad=0; reg [21:0] rbuf[0:63];
    reg load_low=0, we_low=0, drive_dq=0; reg [21:0] wdata=0, dl=0;
    wire [7:0] pat = {1'b0,i} ^ 8'h5A;               // nonzero, distinct per cell
    wire [25:0] apat = spread({13'd0,i});

    // per-ball drive: clock, then load strobe, then we strobe, then low_mask, then address, else HIGH
    genvar g;
    generate for (g=0; g<26; g=g+1) begin: drv
        assign sram_ac[g] = (g==CLK_IDX)      ? sck :
                            (load_sel==g)     ? ~load_low :   // active-low pulse
                            (we_sel==g)        ? ~we_low :
                            low_mask[g]        ? 1'b0 :
                            addr_mask[g]       ? apat[g] : 1'b1;
    end endgenerate
    assign sram_dq = drive_dq ? wdata : 22'bz;

    // capture DQ while sck is low and stable, at cc==cap
    always @(posedge clk) if (running && !sck && dv==0 && cc==cap) dl<=sram_dq;

    always @(posedge clk) begin
        if (go) begin running<=1; phase<=d_q2[1]; i<=0; cc<=0; sck<=0; dv<=0; done<=0; nbad<=0;
                     load_low<=0; we_low<=0; drive_dq<=0; end
        else if (running) begin
            if (dv>=clkdiv) begin
                dv<=0; sck<=~sck;
                if (sck) begin
                    // falling edge: advance the access cycle counter, set controls for next cycle
                    if (cc>=4'd7) begin
                        // end of this access: on READ capture already latched at cc==cap
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
        // control levels as a function of cc (combinational-ish via registered cc)
        // cc==0: assert load (and we+data for write); cc>=1: deassert load; write data at cc==1
        load_low <= running && (cc==0);
        we_low   <= running && (phase==0) && (cc==0);
        drive_dq <= running && (phase==0) && (cc>=1) && (cc<=2);
        if (phase==0) wdata <= {14'd0, pat};
    end

    reg [15:0] rdata;
    always @* case(rd_sel)
        8'h10: rdata=16'hAD10;
        8'h30: rdata={done,8'd0,i};
        8'h54: rdata=rbuf[ridx][15:0]; 8'h58: rdata={10'd0,rbuf[ridx][21:16]};
        8'h5C: rdata={9'd0,nbad};
        8'h60: rdata=dl[15:0];
        default: rdata=16'h0000;
    endcase
    wire ra=(~nCS1)&(~nOE); assign gpmc_d=ra?rdata:16'hzzzz; assign gpmc_wait=1'b1;
endmodule

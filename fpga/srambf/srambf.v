// srambf_flex.v (ID 0xBF01) — UNIFIED 64-ball SRAM discovery pool.
// Every pool ball's role is assigned at RUNTIME over GPMC. Any ball NOT given a control/address role
// is automatically the DATA bus: driven with wpat during a WRITE, tri-stated + SAMPLED during a READ.
// So a write->read loopback DISCOVERS the DQ wherever they physically are, with no recompile.
// Roles: clk_sel(CLK) cen_sel(CE#,low-in-op) advld_sel(ADSC#,low-in-op) oe_sel(OE#,read-low/write-high)
//        gw_sel(GW#,write-low/read-high); celow(force0) cehigh(force1) addr_mask(address pattern).
// Reg map (16-bit GPMC words, sel[6:2]<<2 aliasing; writes decode wr_sel, reads rd_sel):
//  W 0x18/1C/20/24 celow[63:0]  0x28/2C/30/34 cehigh[63:0]  0x38/3C/40/44 addr_mask[63:0]
//  W 0x48 clk_sel 0x4C cen_sel 0x50 advld_sel 0x54 oe_sel 0x58 gw_sel  0x5C wpat[15:0]
//  W 0x60/64 waddr[19:0]  0x68 clkdiv  0x6C latency  0x08 hold  0x70 GO(bit1=write,bit2=oe_val)
//  R 0x10 ID=0xBF01  0x14 status={running,captured,9'd0,cc}  0x74/78/7C/04 capdq[63:0]
module srambf (
    input  wire clk,
    input  wire nCS1, input wire nOE, input wire nWE, input wire [6:0] sel,
    inout  wire [15:0] gpmc_d, output wire gpmc_wait,
    inout  wire [63:0] pool
);
    localparam NC=64;
    reg [2:0] cs1_q=3'b111, we_q=3'b111; reg [6:0] sel_q1=0, sel_q2=0; reg [15:0] d_q1=0, d_q2=0;
    always @(posedge clk) begin
        cs1_q<={cs1_q[1:0],nCS1}; we_q<={we_q[1:0],nWE};
        sel_q1<=sel; sel_q2<=sel_q1; d_q1<=gpmc_d; d_q2<=d_q1;
    end
    wire cs1_low=(cs1_q[2]==0); wire we_commit=(we_q[2]==0)&&(we_q[1]==1);
    wire [7:0] wr_sel={1'b0,sel_q2[6:2],2'b00}; wire [7:0] rd_sel={1'b0,sel[6:2],2'b00};

    reg [63:0] celow=0, data_mask=0, addr_mask=0;
    reg [6:0] clk_sel=7'h7F, cen_sel=7'h7F, advld_sel=7'h7F, oe_sel=7'h7F, gw_sel=7'h7F;
    reg [15:0] wpat=0; reg [7:0] clkdiv=8'd0; reg [4:0] latency=5'd3; reg [19:0] waddr=0; reg hold=0;
    always @(posedge clk) if (we_commit&&cs1_low) case(wr_sel)
        8'h18: celow[15:0]<=d_q2;   8'h1C: celow[31:16]<=d_q2;   8'h20: celow[47:32]<=d_q2;   8'h24: celow[63:48]<=d_q2;
        8'h28: data_mask[15:0]<=d_q2;8'h2C:data_mask[31:16]<=d_q2;8'h30:data_mask[47:32]<=d_q2;8'h34:data_mask[63:48]<=d_q2;
        8'h38: addr_mask[15:0]<=d_q2;8'h3C:addr_mask[31:16]<=d_q2;8'h40:addr_mask[47:32]<=d_q2;8'h44:addr_mask[63:48]<=d_q2;
        8'h48: clk_sel<=d_q2[6:0]; 8'h4C: cen_sel<=d_q2[6:0]; 8'h50: advld_sel<=d_q2[6:0]; 8'h54: oe_sel<=d_q2[6:0]; 8'h58: gw_sel<=d_q2[6:0];
        8'h5C: wpat<=d_q2;
        8'h60: waddr[15:0]<=d_q2; 8'h64: waddr[19:16]<=d_q2[3:0];
        8'h68: clkdiv<=d_q2[7:0]; 8'h6C: latency<=d_q2[4:0]; 8'h08: hold<=d_q2[0];
        default:;
    endcase
    wire go = we_commit && cs1_low && (wr_sel==8'h70);
    wire waddr_wr = we_commit && cs1_low && ((wr_sel==8'h60)||(wr_sel==8'h64));

    // spread waddr across the addr_mask balls (lsb..msb)
    function [63:0] spread(input [63:0] mask, input [19:0] a); integer k,p; begin
        spread=64'd0; p=0;
        for (k=0;k<NC;k=k+1) if (mask[k]) begin spread[k]=a[p]; p=p+1; end
    end endfunction
    wire [63:0] apat = spread(addr_mask, waddr);

    // free-running SRAM clock; in HOLD run HCYC edges after each waddr write then FREEZE (static read)
    reg [7:0] dv=0; reg sck=0; reg [6:0] hcnt=7'h7f;
    wire run_clk = (~hold) | (hcnt < 7'd64);
    always @(posedge clk) begin
        if (dv>=clkdiv) begin dv<=0; if (run_clk) sck<=~sck; end else dv<=dv+1;
        if (waddr_wr) hcnt<=0; else if ((dv>=clkdiv) && hold && (hcnt<7'h7f)) hcnt<=hcnt+1;
    end
    wire tick = (dv>=clkdiv) && (sck==1'b1);

    reg running=0, oe_val=0, wmode=0, captured=0; reg [4:0] cc=0; reg [63:0] capdq=0;
    always @(posedge clk) begin
        if (go) begin running<=1; cc<=0; oe_val<=d_q2[2]; wmode<=d_q2[1]; captured<=0; end
        else if (running && tick) begin if (cc>=latency+5'd2) running<=0; else cc<=cc+1; end
        if (running && tick && cc==latency) begin capdq<=pool; captured<=1; end
        if (hold) capdq<=pool;
    end
    wire writing   = running & wmode;
    wire advld_low = running | hold;
    wire cen_low   = running | hold;

    genvar g;
    generate for (g=0; g<NC; g=g+1) begin: drv
        assign pool[g] = ({1'b0,g[6:0]}==clk_sel)   ? sck :
                         ({1'b0,g[6:0]}==cen_sel)   ? ~cen_low :
                         ({1'b0,g[6:0]}==advld_sel) ? ~advld_low :
                         ({1'b0,g[6:0]}==oe_sel)    ? (writing ? 1'b1 : oe_val) :
                         ({1'b0,g[6:0]}==gw_sel)    ? (writing ? 1'b0 : 1'b1) :
                         (data_mask[g] & writing)   ? wpat[g[3:0]] : // DQ: driven on WRITE, Hi-Z(sample) on READ
                         celow[g]                   ? 1'b0 :
                         addr_mask[g]               ? apat[g] :
                         1'bz;  // data_mask@read + unassigned = Hi-Z (pull-up = must-high controls/tied)
    end endgenerate

    reg [15:0] rdata;
    always @* case(rd_sel)
        8'h10: rdata=16'hBF01;
        8'h14: rdata={running,captured,9'd0,cc};
        8'h74: rdata=capdq[15:0]; 8'h78: rdata=capdq[31:16]; 8'h7C: rdata=capdq[47:32]; 8'h04: rdata=capdq[63:48];
        default: rdata=16'h0000;
    endcase
    wire ra=(~nCS1)&(~nOE); assign gpmc_d=ra?rdata:16'hzzzz; assign gpmc_wait=1'b1;
endmodule

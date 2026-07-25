// srambf.v — flexible NoBL SRAM brute-force controller (ID 0xBF01).
// Every control ROLE (clock / CEN / ADV-LD / OE / WE / CE-low / CE-high / address) is assignable at
// RUNTIME over GPMC to any ball in a 43-ball control pool, so the host can brute-force the pin map
// without recompiling. Free-running SRAM clock (C2/2, keeps the NoBL pipeline live). 48 DQ candidates
// are sampled every access; the host detects a working read via OE#-gating (DQ change when OE toggles)
// and address-dependence. Read-only (write comes once the read config is found).
//
// ctrl pool index (0..42): see srambf.qsf.  DQ (0..47): the hum-correlated data candidates.
// GPMC regs (sel[6:2]<<2 aliasing, all multiples of 4):
//  0x20/24/28 celow_mask   0x2C/30/34 cehigh_mask   0x38/3C/40 we_mask   0x44/48/4C addr_mask
//  0x50 clk_sel  0x54 cen_sel  0x58 advld_sel  0x5C oe_sel   (indices 0..42; 0x7F=none)
//  0x60 clkdiv   0x64 latency  0x68 waddr[15:0]  0x6C waddr[19:16]
//  0x70 GO (bit1=rw[unused,read], bit2=oe_val)   read: 0x10 ID=0xBF01  0x14 status
//  0x74/78/7C capdq[47:0]
module srambf (
    input  wire clk,
    input  wire nCS1, input wire nOE, input wire nWE, input wire [6:0] sel,
    inout  wire [15:0] gpmc_d, output wire gpmc_wait,
    inout  wire [42:0] ctrl,          // control/address candidate pool
    inout  wire [47:0] dq             // data candidates (sampled)
);
    localparam NC=43;
    reg [2:0] cs1_q=3'b111, we_q=3'b111; reg [6:0] sel_q1=0, sel_q2=0; reg [15:0] d_q1=0, d_q2=0;
    always @(posedge clk) begin
        cs1_q<={cs1_q[1:0],nCS1}; we_q<={we_q[1:0],nWE};
        sel_q1<=sel; sel_q2<=sel_q1; d_q1<=gpmc_d; d_q2<=d_q1;
    end
    wire cs1_low=(cs1_q[2]==0); wire we_commit=(we_q[2]==0)&&(we_q[1]==1);
    wire [7:0] wr_sel={1'b0,sel_q2[6:2],2'b00}; wire [7:0] rd_sel={1'b0,sel[6:2],2'b00};

    reg [42:0] celow=0, cehigh=0, we_mask=0, addr_mask=0;
    reg [6:0] clk_sel=7'h7F, cen_sel=7'h7F, advld_sel=7'h7F, oe_sel=7'h7F;
    reg [7:0] clkdiv=8'd0; reg [4:0] latency=5'd3; reg [19:0] waddr=0;
    wire go = we_commit && cs1_low && (wr_sel==8'h70);
    always @(posedge clk) if (we_commit&&cs1_low) case(wr_sel)
        8'h20: celow[15:0]<=d_q2;   8'h24: celow[31:16]<=d_q2;   8'h28: celow[42:32]<=d_q2[10:0];
        8'h2C: cehigh[15:0]<=d_q2;  8'h30: cehigh[31:16]<=d_q2;  8'h34: cehigh[42:32]<=d_q2[10:0];
        8'h38: we_mask[15:0]<=d_q2; 8'h3C: we_mask[31:16]<=d_q2; 8'h40: we_mask[42:32]<=d_q2[10:0];
        8'h44: addr_mask[15:0]<=d_q2;8'h48:addr_mask[31:16]<=d_q2;8'h4C:addr_mask[42:32]<=d_q2[10:0];
        8'h50: clk_sel<=d_q2[6:0]; 8'h54: cen_sel<=d_q2[6:0]; 8'h58: advld_sel<=d_q2[6:0]; 8'h5C: oe_sel<=d_q2[6:0];
        8'h60: clkdiv<=d_q2[7:0]; 8'h64: latency<=d_q2[4:0]; 8'h68: waddr[15:0]<=d_q2; 8'h6C: waddr[19:16]<=d_q2[3:0];
        default:;
    endcase

    // spread waddr over addr_mask balls (lsb..msb)
    function [42:0] spread(input [42:0] mask, input [19:0] a); integer k,p; begin
        spread=43'd0; p=0;
        for (k=0;k<NC;k=k+1) if (mask[k]) begin spread[k]=a[p]; p=p+1; end
    end endfunction
    wire [42:0] apat = spread(addr_mask, waddr);

    // free-running SRAM clock (C2 / 2*(clkdiv+1))
    reg [7:0] dv=0; reg sck=0;
    always @(posedge clk) if (dv>=clkdiv) begin dv<=0; sck<=~sck; end else dv<=dv+1;
    wire tick = (dv>=clkdiv) && (sck==1'b1);      // one sram-clock period boundary

    reg running=0, oe_val=0; reg [4:0] cc=0; reg [47:0] capdq=0;
    always @(posedge clk) begin
        if (go) begin running<=1; cc<=0; oe_val<=d_q2[2]; end
        else if (running && tick) begin
            if (cc>=latency+5'd2) running<=0; else cc<=cc+1;
        end
        if (running && tick && cc==latency) capdq<=dq;   // sample DQ at read latency
    end
    wire advld_low = running && (cc==0);              // ADV/LD low to load address at cc0
    wire cen_low   = running;                          // CEN low throughout access

    genvar g;
    generate for (g=0; g<NC; g=g+1) begin: cdrv
        assign ctrl[g] = ({1'b0,g[6:0]}==clk_sel)   ? sck :
                         ({1'b0,g[6:0]}==cen_sel)   ? ~cen_low :
                         ({1'b0,g[6:0]}==advld_sel) ? ~advld_low :
                         ({1'b0,g[6:0]}==oe_sel)    ? oe_val :
                         we_mask[g]                 ? 1'b1 :
                         celow[g]                   ? 1'b0 :
                         cehigh[g]                  ? 1'b1 :
                         addr_mask[g]               ? apat[g] : 1'b1;
    end endgenerate
    assign dq = 48'bz;                                 // always sample (read-only)

    reg [15:0] rdata;
    always @* case(rd_sel)
        8'h10: rdata=16'hBF01;
        8'h14: rdata={running,10'd0,cc};
        8'h74: rdata=capdq[15:0]; 8'h78: rdata=capdq[31:16]; 8'h7C: rdata=capdq[47:32];
        default: rdata=16'h0000;
    endcase
    wire ra=(~nCS1)&(~nOE); assign gpmc_d=ra?rdata:16'hzzzz; assign gpmc_wait=1'b1;
endmodule

// sramx.v — fully-flexible SRAM bus-map DISCOVERY controller (ID 0xAD14).
// Every one of 52 SRAM-candidate balls is assignable at RUNTIME (over GPMC) as clock / load-strobe /
// write-enable / held-low / address / data(DQ), and the FULL 52-bit input state is captured after each
// access. Lets the host search the entire address/data/control map + validate write->read with NO rebuilds.
// bus index map (0..51): 24 RUN/STOP-active balls, then 12 static-control balls, then 15 DQ candidates,
// then D14 clock. (see sramx.qsf for the exact ball per index.)
module sramx (
    input wire clk,
    inout  wire [51:0] bus,
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

    reg [51:0] low_mask=0, addr_mask=0, dq_mask=0;
    reg [7:0] clk_sel=8'd51, load_sel=8'hFF, we_sel=8'hFF;
    reg [19:0] waddr=0; reg [15:0] wdata=0; reg [15:0] clkdiv=16'd25; reg [3:0] cap=4'd4;
    wire go = we_commit && cs1_low && (wr_sel==8'h6C);
    always @(posedge clk) if (we_commit&&cs1_low) case(wr_sel)
        8'h20: low_mask[15:0]<=d_q2; 8'h24: low_mask[31:16]<=d_q2; 8'h28: low_mask[47:32]<=d_q2; 8'h2C: low_mask[51:48]<=d_q2[3:0];
        8'h30: addr_mask[15:0]<=d_q2;8'h34: addr_mask[31:16]<=d_q2;8'h38: addr_mask[47:32]<=d_q2;8'h3C: addr_mask[51:48]<=d_q2[3:0];
        8'h40: dq_mask[15:0]<=d_q2;  8'h44: dq_mask[31:16]<=d_q2;  8'h48: dq_mask[47:32]<=d_q2;  8'h4C: dq_mask[51:48]<=d_q2[3:0];
        8'h50: clk_sel<=d_q2[7:0]; 8'h54: load_sel<=d_q2[7:0]; 8'h58: we_sel<=d_q2[7:0]; 8'h5C: clkdiv<=d_q2;
        8'h18: cap<=d_q2[3:0];
        8'h60: waddr[15:0]<=d_q2; 8'h64: waddr[19:16]<=d_q2[3:0]; 8'h68: wdata<=d_q2; default:;
    endcase

    // spread address over addr_mask balls; spread wdata over dq_mask balls
    function [51:0] spread(input [51:0] mask, input [19:0] a); integer k,p; begin
        spread=52'd0; p=0;
        for (k=0;k<52;k=k+1) if (mask[k]) begin spread[k]=a[p]; p=p+1; end
    end endfunction
    wire [51:0] apat = spread(addr_mask, waddr);
    wire [51:0] wpat = spread(dq_mask, {4'd0,wdata});

    reg rw=0, running=0, sck=0, done=0; reg [15:0] dv=0; reg [3:0] cc=0;
    reg load_low=0, we_low=0, drive_dq=0; reg [51:0] capraw=0;
    genvar g;
    generate for (g=0; g<52; g=g+1) begin: drv
        assign bus[g] = (clk_sel==g)             ? sck :
                        (load_sel==g)            ? ~load_low :
                        (we_sel==g)              ? ~we_low :
                        (dq_mask[g] & drive_dq)  ? wpat[g] :
                        dq_mask[g]               ? 1'bz :
                        low_mask[g]              ? 1'b0 :
                        addr_mask[g]             ? apat[g] : 1'b1;
    end endgenerate
    always @(posedge clk) if (running && !sck && dv==0 && cc==cap) capraw<=bus;

    always @(posedge clk) begin
        if (go) begin running<=1; rw<=d_q2[1]; cc<=0; sck<=0; dv<=0; done<=0; end
        else if (running) begin
            if (dv>=clkdiv) begin dv<=0; sck<=~sck;
                if (sck) begin if (cc>=4'd11) begin running<=0; done<=1; end else cc<=cc+1; end
            end else dv<=dv+1;
        end
        load_low <= running && (cc==0);
        we_low   <= running && (rw==0) && (cc==0);
        drive_dq <= running && (rw==0) && (cc>=1) && (cc<=2);
    end

    reg [15:0] rdata;
    always @* case(rd_sel)
        8'h10: rdata=16'hAD14;
        8'h14: rdata={done,running,14'd0};
        8'h70: rdata=capraw[15:0]; 8'h74: rdata=capraw[31:16]; 8'h78: rdata=capraw[47:32]; 8'h7C: rdata={12'd0,capraw[51:48]};
        default: rdata=16'h0000;
    endcase
    wire ra=(~nCS1)&(~nOE); assign gpmc_d=ra?rdata:16'hzzzz; assign gpmc_wait=1'b1;
endmodule

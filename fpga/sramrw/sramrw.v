// sramrw.v — DIRECT host read/write of the external SRAM over GPMC (ID 0xAD13).
// Full control: host sets addr + data, issues a single WRITE or READ access; result in rdata.
// Roles configurable (from the sramgold2 brute-force: select=low_mask, load_sel=ADSC#, we_sel=WE#,
// clk=D14). sram_ac[0..23]=addr/data candidates, [24..35]=control cluster, [36]=D14 clock.
module sramrw (
    input wire clk,
    output wire [36:0] sram_ac,
    inout  wire [14:0] sram_dq,
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
    reg [7:0] load_sel=8'd24, we_sel=8'd25;
    reg [15:0] clkdiv=16'd25; reg [3:0] cap=4'd2;
    reg [19:0] waddr=0; reg [15:0] wdata=0;
    wire go = we_commit && cs1_low && (wr_sel==8'h6C);
    always @(posedge clk) if (we_commit&&cs1_low) case(wr_sel)
        8'h20: low_mask[15:0]<=d_q2;  8'h24: low_mask[31:16]<=d_q2; 8'h28: low_mask[36:32]<=d_q2[4:0];
        8'h40: addr_mask[15:0]<=d_q2; 8'h44: addr_mask[31:16]<=d_q2;8'h48: addr_mask[36:32]<=d_q2[4:0];
        8'h2C: load_sel<=d_q2[7:0]; 8'h30: we_sel<=d_q2[7:0]; 8'h38: clkdiv<=d_q2; 8'h3C: cap<=d_q2[3:0];
        8'h60: waddr[15:0]<=d_q2; 8'h62: waddr[19:16]<=d_q2[3:0]; 8'h64: wdata<=d_q2; default:;
    endcase

    function [36:0] spread(input [19:0] a); integer k,p; begin
        spread=37'd0; p=0;
        for (k=0;k<37;k=k+1) if (addr_mask[k]) begin spread[k]=a[p]; p=p+1; end
    end endfunction
    wire [36:0] apat = spread(waddr);

    reg rw=0, running=0, sck=0, done=0; reg [15:0] dv=0; reg [3:0] cc=0;
    reg load_low=0, we_low=0, drive_dq=0; reg [14:0] dl=0;
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
        if (go) begin running<=1; rw<=d_q2[1]; cc<=0; sck<=0; dv<=0; done<=0; end
        else if (running) begin
            if (dv>=clkdiv) begin
                dv<=0; sck<=~sck;
                if (sck) begin
                    if (cc>=4'd9) begin running<=0; done<=1; end else cc<=cc+1;
                end
            end else dv<=dv+1;
        end
        load_low <= running && (cc==0);
        we_low   <= running && (rw==0) && (cc==0);
        drive_dq <= running && (rw==0) && (cc>=1) && (cc<=2);
    end

    reg [15:0] rdata;
    always @* case(rd_sel)
        8'h10: rdata=16'hAD13;
        8'h30: rdata={done,running,14'd0};
        8'h70: rdata={1'b0,dl};
        default: rdata=16'h0000;
    endcase
    wire ra=(~nCS1)&(~nOE); assign gpmc_d=ra?rdata:16'hzzzz; assign gpmc_wait=1'b1;
endmodule

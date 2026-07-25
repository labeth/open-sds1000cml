// sramctl.v — parameterized SPB SRAM controller with a WRITE->READ round-trip self-test (ID 0xAD0A).
// Self-validating oracle: write a ramp M[i]=i to N cells, read back; read[i]==i proves the protocol
// (address bit-order is irrelevant — same counter value hits the same cell whatever the permutation).
// Search space collapses to the CONTROL roles: load strobe (ADSC#), write-enable (GW#), CE (low mask).
//
// Config (all via GPMC CS1 writes, latched):
//   0x20/24/28 low_mask[34:0]  — balls held statically LOW (CE/select candidates)
//   0x40/44/48 addr_mask[34:0] — balls that carry the address counter (spread lsb..msb in ball order)
//   0x2C load_sel  — ball index pulsed LOW on each access edge (ADSC#/ADSP#)
//   0x30 we_sel    — ball index pulsed LOW during WRITE edges (GW#); 0x3F = none
//   0x34 ncnt      — number of addresses to round-trip (<=64)
//   0x38 clkdiv    — sck half period
//   0x50 GO        — bit0: 1=start.  bit1: 0=WRITE-ramp phase, 1=READ-verify phase
//   0x4C ridx      — read buffer index
// Readback: 0x10 ID; 0x54 rbuf[ridx][15:0]; 0x58 rbuf[ridx][21:16]; 0x30 {done,cyc}; 0x5C nbad
module sramctl (
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

    reg [34:0] low_mask=0, addr_mask=0;
    reg [7:0] load_sel=8'd0, we_sel=8'h3F, clk_sel=8'd29;
    reg [6:0] ncnt=7'd16, ridx=0; reg [15:0] clkdiv=16'd25;
    wire go = we_commit && cs1_low && (wr_sel==8'h50);
    always @(posedge clk) if (we_commit&&cs1_low) case(wr_sel)
        8'h20: low_mask[15:0]<=d_q2;  8'h24: low_mask[31:16]<=d_q2; 8'h28: low_mask[34:32]<=d_q2[2:0];
        8'h40: addr_mask[15:0]<=d_q2; 8'h44: addr_mask[31:16]<=d_q2;8'h48: addr_mask[34:32]<=d_q2[2:0];
        8'h2C: load_sel<=d_q2[7:0]; 8'h30: we_sel<=d_q2[7:0]; 8'h34: ncnt<=d_q2[6:0];
        8'h38: clkdiv<=d_q2; 8'h4C: ridx<=d_q2[6:0]; default:;
    endcase
    reg phase=0;                                   // 0=write, 1=read (from GO bit1)

    // FSM: for each address i in 0..ncnt-1, run one access (3 sck edges: setup/load, access, capture)
    reg running=0, sck=0, done=0; reg [15:0] dv=0; reg [1:0] st=0; reg [6:0] i=0; reg [6:0] nbad=0;
    reg [21:0] rbuf[0:63];
    reg [19:0] addr;
    // spread addr onto addr_mask balls (ball k gets the p-th addr bit where p=popcount of mask below k)
    function [34:0] spread(input [19:0] a); integer k,p; begin
        spread=35'd0; p=0;
        for (k=0;k<35;k=k+1) if (addr_mask[k]) begin spread[k]=a[p]; p=p+1; end
    end endfunction
    wire [34:0] apat = spread(i);                  // address balls set to counter value i
    // base drive: all HIGH, then low_mask low, then address balls, then strobes pulsed by FSM
    reg load_low=0, we_low=0;
    wire [34:0] base = (~low_mask) & (~apat) ;     // 1 where high-driven
    // final drive per ball
    genvar g;
    generate for (g=0; g<35; g=g+1) begin: drv
        assign sram_ac[g] = (clk_sel==g) ? sck :
                            (load_sel==g) ? ~load_low :          // strobe active-low when load_low
                            (we_sel==g)   ? ~we_low :
                            low_mask[g]   ? 1'b0 :
                            addr_mask[g]  ? apat[g] : 1'b1;
    end endgenerate
    reg drive_dq=0; reg [21:0] wdata=0;
    assign sram_dq = drive_dq ? wdata : 22'bz;
    reg [21:0] dl=0; always @(posedge clk) if (sck && dv>=clkdiv) dl<=sram_dq;  // capture at falling

    always @(posedge clk) begin
        if (go) begin running<=1; phase<=d_q2[1]; i<=0; st<=0; sck<=0; dv<=0; done<=0; nbad<=0;
                     load_low<=0; we_low<=0; drive_dq<=0; end
        else if (running) begin
            if (dv>=clkdiv) begin
                dv<=0; sck<=~sck;
                if (~sck) begin
                    // rising edge just happened conceptually; set up per-state controls before it
                end else begin
                    // falling edge: advance state machine
                    case (st)
                        2'd0: begin load_low<=0; we_low<=0; drive_dq<=0; st<=1; end
                        2'd1: begin // load address i (pulse load), and for write also assert we + data
                            load_low<=1;
                            if (phase==0) begin we_low<=1; drive_dq<=1; wdata<={15'd0,i[6:0]}; end
                            st<=2; end
                        2'd2: begin load_low<=0; we_low<=0; // access/pipeline
                            st<=3; end
                        2'd3: begin // capture (read) or finish cell (write)
                            drive_dq<=0;
                            if (phase==1) begin
                                rbuf[i]<=dl;
                                if (dl[6:0]!=i[6:0]) nbad<=nbad+1;   // round-trip check on low byte
                            end
                            if (i>=ncnt-1) begin running<=0; done<=1; end else i<=i+1;
                            st<=0; end
                    endcase
                end
            end else dv<=dv+1;
        end
    end

    reg [15:0] rdata;
    always @* case(rd_sel)
        8'h10: rdata=16'hAD0A;
        8'h30: rdata={done,8'd0,i};
        8'h54: rdata=rbuf[ridx][15:0]; 8'h58: rdata={10'd0,rbuf[ridx][21:16]};
        8'h5C: rdata={9'd0,nbad};
        default: rdata=16'h0000;
    endcase
    wire ra=(~nCS1)&(~nOE); assign gpmc_d=ra?rdata:16'hzzzz; assign gpmc_wait=1'b1;
endmodule

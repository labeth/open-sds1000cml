// sramz.v — SRAM read/write controller with a FREE-RUNNING fast clock (ID 0xAD20).
// Root-cause fix: prior controllers clocked the SBSRAM at ~C2/50 (~1-2 MHz) and EXTEST at <1 kHz —
// far below what a 512K x36 pipelined SBSRAM needs to output valid data. Here sram_clk free-runs at
// C2/2 (continuous, keeps any DLL locked) and the FSM is paced by it. Ball ROLES come from the JTAG
// EXTEST RUN-duty characterization (see tools/jtag/sram_re/FINDINGS.md):
//   address = ~50%-duty AC balls ; ADSC#=M6 ; OE#=N6 ; WE-group=J6 P2 R4 R7 R5 ; strobe=G5 P1 T4 N3 ;
//   select=P6 ; sram_clk=D14 ; DQ = 40 non-AC/STATIC/ADC/GPMC candidates (round-trip finds the real ones).
// Runtime-config (clkdiv, read-latency, which DQ driven) so the protocol can be tuned without recompiling.
module sramz (
    input  wire clk,                       // C2 GPMC clock
    input  wire nCS1, input wire nOE, input wire nWE, input wire [6:0] sel,
    inout  wire [15:0] gpmc_d, output wire gpmc_wait,
    output wire [16:0] sram_a,             // address
    output wire sram_adsc, output wire sram_oe,
    output wire [4:0] sram_we,             // write-enable group (all asserted together)
    output wire [3:0] sram_strobe,         // strobe group
    output wire sram_sel,                  // select/CS (active low)
    output wire sram_clk,                  // free-running SRAM clock (D14)
    output wire [6:0] mode_hi,             // const-1 mode balls
    output wire [3:0] mode_lo,             // const-0 control balls
    inout  wire [39:0] dq                  // 40 DQ candidates (bidirectional)
);
    // ---- GPMC register interface (same decode as sramx: sel[6:2],2'b00 aliasing) ----
    reg [2:0] cs1_q=3'b111, we_q=3'b111; reg [6:0] sel_q1=0, sel_q2=0; reg [15:0] d_q1=0, d_q2=0;
    always @(posedge clk) begin
        cs1_q<={cs1_q[1:0],nCS1}; we_q<={we_q[1:0],nWE};
        sel_q1<=sel; sel_q2<=sel_q1; d_q1<=gpmc_d; d_q2<=d_q1;
    end
    wire cs1_low=(cs1_q[2]==0); wire we_commit=(we_q[2]==0)&&(we_q[1]==1);
    wire [7:0] wr_sel={1'b0,sel_q2[6:2],2'b00}; wire [7:0] rd_sel={1'b0,sel[6:2],2'b00};

    reg [16:0] waddr=0; reg [39:0] wdata=0; reg [39:0] drive_mask=~40'd0;
    reg [7:0] clkdiv=8'd0; reg [3:0] rlat=4'd3; reg [3:0] wdcyc=4'd2; reg [3:0] ncyc=4'd8;
    wire go = we_commit && cs1_low && (wr_sel==8'h50);
    always @(posedge clk) if (we_commit&&cs1_low) case(wr_sel)
        8'h20: waddr[15:0]<=d_q2; 8'h24: waddr[16]<=d_q2[0];
        8'h28: wdata[15:0]<=d_q2; 8'h2C: wdata[31:16]<=d_q2; 8'h30: wdata[39:32]<=d_q2[7:0];
        8'h34: drive_mask[15:0]<=d_q2; 8'h38: drive_mask[31:16]<=d_q2; 8'h3C: drive_mask[39:32]<=d_q2[7:0];
        8'h40: clkdiv<=d_q2[7:0]; 8'h44: rlat<=d_q2[3:0]; 8'h48: wdcyc<=d_q2[3:0]; 8'h4C: ncyc<=d_q2[3:0];
        default:;
    endcase

    // ---- free-running SRAM clock: sram_clk = C2 divided by 2*(clkdiv+1). clkdiv=0 => C2/2 ----
    reg [7:0] dv=0; reg sclk=0;
    always @(posedge clk) begin
        if (dv>=clkdiv) begin dv<=0; sclk<=~sclk; end else dv<=dv+1;
    end
    wire tick = (dv==0) && (sclk==1'b1);   // one C2 pulse just before each sram_clk RISING edge (sclk 1->0 next... )
    // We advance the access FSM once per sram_clk period, on the C2 edge that ENDS the high phase.
    wire adv = (dv>=clkdiv) && (sclk==1'b1);

    reg running=0, rw=0; reg [3:0] cc=0; reg [39:0] capdq=0;
    always @(posedge clk) begin
        if (go) begin running<=1; rw<=d_q2[1]; cc<=0; end
        else if (running && adv) begin
            if (cc>=ncyc) running<=0; else cc<=cc+1;
        end
        // capture DQ at read-latency cycle (sample near end of high phase)
        if (running && rw && adv && cc==rlat) capdq<=dq;
    end

    // ---- SRAM control choreography (combinational from cc) ----
    wire acc = running;
    assign sram_clk   = sclk;
    assign sram_sel   = ~acc;                                  // select low during access
    assign sram_adsc  = ~(acc && cc==0);                       // ADSC# low at cc0 (load address)
    assign sram_oe    = ~(acc && rw==1'b1);                    // OE# low during reads
    assign sram_we    = {5{~(acc && rw==1'b0 && cc==0)}};      // WE# low at cc0 during writes
    assign sram_strobe= {4{~(acc && cc==0)}};                  // strobe low at cc0
    assign sram_a     = waddr;
    // drive DQ during write data cycles (cc 1..wdcyc), else tri-state (read/idle)
    wire drv = acc && (rw==1'b0) && (cc>=4'd1) && (cc<=wdcyc);
    genvar g;
    generate for (g=0;g<40;g=g+1) begin: dqd
        assign dq[g] = (drv && drive_mask[g]) ? wdata[g] : 1'bz;
    end endgenerate
    assign mode_hi = 7'h7F;   // const-1 mode balls
    assign mode_lo = 4'h0;    // const-0 control balls

    // ---- readback ----
    reg [15:0] rdata;
    always @* case(rd_sel)
        8'h10: rdata=16'hAD20;
        8'h14: rdata={running,3'd0,cc,4'd0,rw,3'd0};
        8'h60: rdata=capdq[15:0]; 8'h64: rdata=capdq[31:16]; 8'h68: rdata={8'd0,capdq[39:32]};
        8'h6C: rdata=waddr[15:0];
        default: rdata=16'h0000;
    endcase
    wire ra=(~nCS1)&(~nOE); assign gpmc_d=ra?rdata:16'hzzzz; assign gpmc_wait=1'b1;
endmodule

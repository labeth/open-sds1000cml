// rawcapw.v — WIDE raw-pin recorder (auto-generated). 43 candidate balls, 3 banks.
module rawcapw (
    input  wire        clk,
    input  wire [42:0] probe,
    output wire [7:0]  adc_enc,
    output wire [3:0]  adc_ctl_hi,
    output wire [2:0]  adc_ctl_lo,
    input  wire        nCS1, input wire nOE, input wire nWE,
    input  wire [6:0]  sel,
    inout  wire [15:0] gpmc_d,
    output wire        gpmc_wait
);
    reg [2:0]  cs1_q=3'b111,oe_q=3'b111,we_q=3'b111;
    reg [6:0]  sel_q1=0,sel_q2=0; reg [15:0] d_q1=0,d_q2=0;
    always @(posedge clk) begin
        cs1_q<={cs1_q[1:0],nCS1}; oe_q<={oe_q[1:0],nOE}; we_q<={we_q[1:0],nWE};
        sel_q1<=sel; sel_q2<=sel_q1; d_q1<=gpmc_d; d_q2<=d_q1;
    end
    wire cs1_low=(cs1_q[2]==0); wire we_commit=(we_q[2]==0)&&(we_q[1]==1);
    wire [7:0] wr_sel={1'b0,sel_q2[6:2],2'b00}; wire [7:0] rd_sel={1'b0,sel[6:2],2'b00};
    reg [15:0] enc_div=3,decim=0; reg [1:0] bank=0; reg [8:0] rdaddr=0; reg arm=0;
    always @(posedge clk) begin
        arm<=0;
        if (we_commit&&cs1_low) case(wr_sel)
            8'h20: arm<=1; 8'h24: enc_div<=d_q2; 8'h28: decim<=d_q2;
            8'h2C: bank<=d_q2[1:0]; 8'h34: rdaddr<=d_q2[8:0]; default:;
        endcase
    end
    reg enc_clk=0,enc_prev=0; reg [15:0] dv=0;
    always @(posedge clk) begin
        enc_prev<=enc_clk;
        if (dv>=enc_div) begin dv<=0; enc_clk<=~enc_clk; end else dv<=dv+1;
    end
    wire enc_rise=enc_clk&~enc_prev;
    assign adc_enc={8{enc_clk}}; assign adc_ctl_hi=4'b1111; assign adc_ctl_lo=3'b000;
    reg [42:0] pr=0;
    wire [47:0] prpad = { 5'd0, pr };
    wire [15:0] slice = (bank==0)?prpad[15:0]:(bank==1)?prpad[31:16]:prpad[47:32];
    reg [15:0] buf_mem[0:255]; reg [8:0] wptr=0; reg full=0; reg [15:0] dcnt=0;
    always @(posedge clk) begin
        pr<=probe;
        if (arm) begin wptr<=0; full<=0; dcnt<=0; end
        else if (!full&&enc_rise) begin
            if (dcnt>=decim) begin dcnt<=0; buf_mem[wptr]<=slice;
                if (wptr==255) full<=1; else wptr<=wptr+1; end
            else dcnt<=dcnt+1;
        end
    end
    wire [15:0] rddata=buf_mem[rdaddr];
    reg [15:0] rdata;
    always @* case(rd_sel)
        8'h10: rdata=16'hADC2; 8'h24: rdata=enc_div; 8'h28: rdata=decim;
        8'h2C: rdata={14'd0,bank}; 8'h30: rdata={full,6'd0,wptr}; 8'h38: rdata=rddata;
        default: rdata=0;
    endcase
    wire read_active=(~nCS1)&(~nOE);
    assign gpmc_d=read_active?rdata:16'hzzzz; assign gpmc_wait=1'b1;
endmodule
